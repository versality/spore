package fleet

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// reapEnv carries the injection seams for tests; production uses
// defaultReapEnv().
type reapEnv struct {
	projectsFile  string
	currentRoot   func() (string, error)
	gitRunner     func(dir string, args ...string) (string, error)
	tmuxRunner    reapTmuxRunner
	listWorktrees func(mainRoot string) ([]string, error)
}

type reapTmuxRunner interface {
	listSessions() (string, error)
	hasSession(name string) bool
	killSession(name string) error
}

type defaultReapTmux struct{}

func (defaultReapTmux) listSessions() (string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (defaultReapTmux) hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func (defaultReapTmux) killSession(name string) error {
	return exec.Command("tmux", "kill-session", "-t", name).Run()
}

func defaultReapEnv() reapEnv {
	return reapEnv{
		projectsFile: projectsFilePath(),
		currentRoot:  func() (string, error) { return os.Getwd() },
		gitRunner:    defaultReapGit,
		tmuxRunner:   defaultReapTmux{},
		listWorktrees: func(mainRoot string) ([]string, error) {
			return listWorktreesAt(defaultReapGit, mainRoot)
		},
	}
}

func defaultReapGit(dir string, args ...string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	full := append([]string{"-c", "safe.directory=" + abs, "-C", dir}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	return string(out), err
}

type reapCounts struct {
	killedSessions, removedWorktrees, deletedBranches int
}

func (c reapCounts) empty() bool {
	return c.killedSessions == 0 && c.removedWorktrees == 0 && c.deletedBranches == 0
}

// Reap walks each configured project (projects file) and tears down
// worktrees + tmux sessions whose task is done, parked-and-published,
// or orphan-on-main. Mirrors wt-task fleet reap. When forcePublished
// is true, active tasks whose wt/<slug> is contained in origin/main
// and whose origin/main tasks/<slug>.md is done/superseded are also
// reaped (catches the post-merge cleanup window).
func Reap(forcePublished bool, stdout, stderr io.Writer) (int, error) {
	return runReap(forcePublished, stdout, stderr, defaultReapEnv())
}

func runReap(forcePublished bool, stdout, stderr io.Writer, e reapEnv) (int, error) {
	roots, err := reapRoots(e)
	if err != nil {
		fmt.Fprintf(stderr, "spore: %v\n", err)
		return 1, err
	}
	var total reapCounts
	for _, root := range roots {
		c, rc := reapOne(root, forcePublished, stdout, stderr, e)
		total.killedSessions += c.killedSessions
		total.removedWorktrees += c.removedWorktrees
		total.deletedBranches += c.deletedBranches
		if rc != 0 {
			return rc, nil
		}
	}
	if total.empty() {
		fmt.Fprintln(stdout, "fleet reap: nothing to do")
		return 0, nil
	}
	fmt.Fprintf(stdout, "fleet reap: killed=%d removed=%d branches=%d\n",
		total.killedSessions, total.removedWorktrees, total.deletedBranches)
	return 0, nil
}

func reapRoots(e reapEnv) ([]string, error) {
	projects, err := readProjects(e.projectsFile)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		root, err := e.currentRoot()
		if err != nil {
			return nil, err
		}
		return []string{root}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range projects {
		if !dirExists(p) {
			continue
		}
		key := realpathOrClean(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		root, err := e.currentRoot()
		if err != nil {
			return nil, err
		}
		return []string{root}, nil
	}
	return out, nil
}

func reapOne(mainRoot string, forcePublished bool, stdout, stderr io.Writer, e reapEnv) (reapCounts, int) {
	project, err := task.ProjectName(mainRoot)
	if err != nil || project == "" {
		project = filepath.Base(mainRoot)
	}

	worktrees, err := e.listWorktrees(mainRoot)
	if err != nil {
		fmt.Fprintf(stderr, "spore: %v\n", err)
		return reapCounts{}, 1
	}

	var counts reapCounts

	for _, wtDir := range worktrees {
		slug := filepath.Base(wtDir)
		file := filepath.Join(mainRoot, "tasks", slug+".md")

		var meta frontmatter.Meta
		var hasFile bool
		if raw, rerr := os.ReadFile(file); rerr == nil {
			m, _, perr := frontmatter.Parse(raw)
			if perr == nil {
				meta = m
				hasFile = true
			}
		}
		status := meta.Status

		reapStatus := status
		publishedReapable := status == task.StatusActive && forcePublished
		if publishedReapable && publishedClosed(mainRoot, slug, e) {
			reapStatus = "published"
		}

		switch reapStatus {
		case task.StatusActive:
			// Live work; leave alone. The active-duplicate-session
			// repair owned by spore Reconcile covers the remaining
			// invariant.
			continue

		case task.StatusBlocked:
			session := sessionForReap(mainRoot, slug, meta)
			if session != "" && e.tmuxRunner.hasSession(session) {
				if e.tmuxRunner.killSession(session) == nil {
					fmt.Fprintf(stdout, "[spore] reap: killed session %s (%s blocked)\n", session, slug)
					counts.killedSessions++
				}
			}

		default:
			// done, "published", or "" (orphan: file missing on main).
			displayStatus := reapStatus
			if displayStatus == "" {
				displayStatus = "orphan"
			}

			// Sentinel: file-missing + wt/<slug> has unlanded commits
			// not contained in HEAD nor origin/main is the wt-merge
			// unblock window. Tearing down here destroys live work.
			if reapStatus == "" && !hasFile && branchHasUnlandedWork(mainRoot, slug, e) {
				fmt.Fprintf(stderr, "spore: reap: %s: tasks/%s.md missing on main but wt/%s has unlanded commits; preserving worktree+session+branch\n", slug, slug, slug)
				continue
			}

			session := sessionForReap(mainRoot, slug, meta)
			if session != "" && e.tmuxRunner.hasSession(session) {
				if e.tmuxRunner.killSession(session) == nil {
					fmt.Fprintf(stdout, "[spore] reap: killed session %s (%s %s)\n", session, slug, displayStatus)
					counts.killedSessions++
				}
			}
			if dirExists(wtDir) {
				if _, gerr := e.gitRunner(mainRoot, "worktree", "remove", "--force", wtDir); gerr == nil {
					fmt.Fprintf(stdout, "[spore] reap: removed worktree %s (%s %s)\n", wtDir, slug, displayStatus)
					counts.removedWorktrees++
				} else {
					fmt.Fprintf(stderr, "spore: reap: could not remove worktree %s\n", wtDir)
				}
			}
			branch := "wt/" + slug
			if _, gerr := e.gitRunner(mainRoot, "rev-parse", "--verify", "--quiet", branch); gerr == nil {
				if _, derr := e.gitRunner(mainRoot, "branch", "-d", branch); derr == nil {
					fmt.Fprintf(stdout, "[spore] reap: deleted branch %s\n", branch)
					counts.deletedBranches++
				} else if branchContainedInPublicMain(mainRoot, slug, e) {
					if _, ferr := e.gitRunner(mainRoot, "branch", "-D", branch); ferr == nil {
						fmt.Fprintf(stdout, "[spore] reap: deleted branch %s (contained in origin/main)\n", branch)
						counts.deletedBranches++
					} else {
						fmt.Fprintf(stderr, "spore: reap: branch %s contained in origin/main but delete failed; keeping\n", branch)
					}
				} else {
					fmt.Fprintf(stderr, "spore: reap: branch %s not merged into main; keeping\n", branch)
				}
			}
		}
	}

	// Pass 2: orphan tmux sessions whose worktree is gone. Routes
	// every shape (current wt-emoji, legacy spore/<project>/<slug>)
	// through ParseSession; only worker kinds are candidates - the
	// coordinator session has no worktree on purpose.
	if sessions, lerr := e.tmuxRunner.listSessions(); lerr == nil {
		for _, line := range strings.Split(strings.TrimRight(sessions, "\n"), "\n") {
			if line == "" {
				continue
			}
			p, ok := task.MatchProject(line, project)
			if !ok || p.Kind != task.SessionKindWorker || p.Slug == "" {
				continue
			}
			if dirExists(filepath.Join(mainRoot, ".worktrees", p.Slug)) {
				continue
			}
			if e.tmuxRunner.killSession(line) == nil {
				fmt.Fprintf(stdout, "[spore] reap: killed orphan session %s\n", line)
				counts.killedSessions++
			}
		}
	}

	// Drop stale worktree registry rows. Cheap, idempotent.
	_, _ = e.gitRunner(mainRoot, "worktree", "prune")

	return counts, 0
}

// sessionForReap picks the session to kill: frontmatter `session:` wins,
// otherwise fall back to the kernel-computed wt-style session name.
func sessionForReap(projectRoot, slug string, m frontmatter.Meta) string {
	if m.Session != "" {
		return m.Session
	}
	return task.TaskTmuxSession(filepath.Join(projectRoot, "tasks"), projectRoot, slug)
}

// publishedClosed reports whether wt/<slug> is contained in origin/main
// and the corresponding tasks/<slug>.md on origin/main is done or
// superseded.
func publishedClosed(mainRoot, slug string, e reapEnv) bool {
	if !branchContainedInPublicMain(mainRoot, slug, e) {
		return false
	}
	out, err := e.gitRunner(mainRoot, "show", "refs/remotes/origin/main:tasks/"+slug+".md")
	if err != nil {
		// File missing on origin/main is the strongest signal that the
		// task was published-then-removed; treat as closed.
		return true
	}
	m, _, perr := frontmatter.Parse([]byte(out))
	if perr != nil {
		return false
	}
	return m.Status == task.StatusDone || m.Status == "superseded"
}

func branchContainedInPublicMain(mainRoot, slug string, e reapEnv) bool {
	if _, err := e.gitRunner(mainRoot, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main"); err != nil {
		return false
	}
	_, err := e.gitRunner(mainRoot, "merge-base", "--is-ancestor", "wt/"+slug, "refs/remotes/origin/main")
	return err == nil
}

// branchHasUnlandedWork: wt/<slug> exists and is contained in NEITHER
// HEAD nor origin/main. Used as the file-missing sentinel.
func branchHasUnlandedWork(mainRoot, slug string, e reapEnv) bool {
	branch := "wt/" + slug
	if _, err := e.gitRunner(mainRoot, "rev-parse", "--verify", "--quiet", branch); err != nil {
		return false
	}
	if _, err := e.gitRunner(mainRoot, "merge-base", "--is-ancestor", branch, "HEAD"); err == nil {
		return false
	}
	if branchContainedInPublicMain(mainRoot, slug, e) {
		return false
	}
	return true
}

func listWorktreesAt(git func(dir string, args ...string) (string, error), mainRoot string) ([]string, error) {
	out, err := git(mainRoot, "worktree", "list", "--porcelain")
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, err
	}
	prefix := mainRoot + "/.worktrees/"
	var out2 []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimPrefix(line, "worktree ")
		if strings.HasPrefix(path, prefix) {
			out2 = append(out2, path)
		}
	}
	return out2, nil
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
