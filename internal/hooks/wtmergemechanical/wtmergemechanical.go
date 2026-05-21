// Package wtmergemechanical implements M1 of the worker-lifecycle FSM
// (docs/todo/worker-lifecycle-fsm.md section 9): a Claude Stop-hook
// that fires exit 2 with a merge nudge when a worker idles on its
// wt/<slug> branch with shipped-but-unmerged commits and a clean
// working tree.
//
// Decision boundary (all must hold; else exit 0):
//
//  1. unmerged := task.UnmergedCommits(projectRoot, "wt/<slug>") > 0
//  2. git -C <worktree> status --porcelain is empty
//  3. <worktree>'s git-dir contains no MERGE_HEAD, rebase-merge, or rebase-apply
//  4. agentpane.Classify on the worker pane reports "idle"
//
// SPORE_TASK_SLUG drives the branch + session lookup; without it the
// hook is a no-op (the firing process is not a known worker).
package wtmergemechanical

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
)

// Result is the hook's verdict the caller should propagate.
type Result struct {
	ExitCode int
	Stderr   string
}

// Deps are the test seams. Nil fields fall back to real implementations.
type Deps struct {
	// LookupEnv defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Capture defaults to agentpane.RealCapture.
	Capture agentpane.CaptureFunc
	// SessionName resolves slug -> tmux session. Defaults to
	// task.TaskTmuxSession against <projectRoot>/tasks.
	SessionName func(projectRoot, slug string) string
}

// Run evaluates the decision boundary and returns the Result.
func Run(req hooks.Request, deps Deps) Result {
	deps = deps.withDefaults()

	slug, ok := deps.LookupEnv("SPORE_TASK_SLUG")
	if !ok || slug == "" {
		return Result{}
	}

	projectRoot, _ := deps.LookupEnv("SPORE_PROJECT_ROOT")
	if projectRoot == "" {
		// Fallback: derive from the worktree (req.CWD) via git.
		root, err := topLevel(req.CWD)
		if err != nil {
			return Result{}
		}
		// req.CWD points at the worktree; the project root we want for
		// UnmergedCommits is the main checkout. Walk up: a worktree's
		// .git file lists `gitdir: <main>/.git/worktrees/<slug>`.
		if main, ok := mainCheckoutFromWorktree(root); ok {
			projectRoot = main
		} else {
			projectRoot = root
		}
	}
	worktree := req.CWD
	if worktree == "" {
		// Last-resort: assume the worker is running inside its worktree.
		worktree = filepath.Join(projectRoot, ".worktrees", slug)
	}

	branch := "wt/" + slug
	unmerged, err := task.UnmergedCommits(projectRoot, branch)
	if err != nil || unmerged == 0 {
		return Result{}
	}

	if !workingTreeClean(worktree) {
		return Result{}
	}

	if midMergeOrRebase(worktree) {
		return Result{}
	}

	session := deps.SessionName(projectRoot, slug)
	if session == "" {
		return Result{}
	}
	state, _ := agentpane.Classify(deps.Capture, session+":claude", "claude")
	if state != "idle" {
		return Result{}
	}

	msg := fmt.Sprintf(
		"Branch %s has %d unmerged commit(s), a clean tree, and you have stopped. Run `wt merge` to ship the unit; or, if the unit is not done, write the next step and continue.\n",
		branch, unmerged)
	return Result{ExitCode: 2, Stderr: msg}
}

func (d Deps) withDefaults() Deps {
	if d.LookupEnv == nil {
		d.LookupEnv = os.LookupEnv
	}
	if d.Capture == nil {
		d.Capture = agentpane.RealCapture
	}
	if d.SessionName == nil {
		d.SessionName = func(projectRoot, slug string) string {
			return task.TaskTmuxSession(filepath.Join(projectRoot, "tasks"), projectRoot, slug)
		}
	}
	return d
}

func workingTreeClean(worktree string) bool {
	cmd := exec.Command("git", "-C", worktree, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == ""
}

func midMergeOrRebase(worktree string) bool {
	gitDir, err := gitDir(worktree)
	if err != nil {
		return false
	}
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true
		}
	}
	return false
}

func gitDir(worktree string) (string, error) {
	out, err := exec.Command("git", "-C", worktree, "rev-parse", "--git-dir").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	return dir, nil
}

func topLevel(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("empty cwd")
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// mainCheckoutFromWorktree resolves a worktree path to its main
// checkout by parsing the worktree's `.git` pointer file. Returns
// ("", false) when the input is the main checkout (a real .git dir).
func mainCheckoutFromWorktree(worktree string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(b))
	const prefix = "gitdir: "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	parent := filepath.Dir(filepath.Dir(filepath.Dir(gitDir)))
	if parent == "" || parent == "/" {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		return real, true
	}
	return filepath.Clean(parent), true
}
