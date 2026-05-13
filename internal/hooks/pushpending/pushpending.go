// Package pushpending implements M-finish-B of the rower ship-cycle
// contract (tasks/spore-rower-finish-contract.md section 5): a Stop
// hook that fires exit 2 when a rower idles after `wt merge` has
// fast-forwarded local main but `origin/main` is still behind.
//
// Decision boundary (all must hold; else exit 0):
//
//  1. UnpushedMainCommits(projectRoot) > 0  (local main ahead of origin/main).
//  2. git -C <worktree> status --porcelain is empty.
//  3. <worktree>'s git-dir contains no MERGE_HEAD, rebase-merge, or rebase-apply.
//  4. agentpane.Classify on the rower pane reports "idle".
//
// SPORE_TASK_SLUG drives the session lookup; without it the hook is a
// no-op (the firing process is not a known rower).
package pushpending

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
	LookupEnv   func(string) (string, bool)
	Capture     agentpane.CaptureFunc
	SessionName func(projectRoot, slug string) string
	// UnpushedCount counts commits on local main not reachable from
	// origin/main. Defaults to the real `git rev-list` query.
	UnpushedCount func(projectRoot string) (int, error)
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
		root, err := topLevel(req.CWD)
		if err != nil {
			return Result{}
		}
		if main, ok := mainCheckoutFromWorktree(root); ok {
			projectRoot = main
		} else {
			projectRoot = root
		}
	}
	worktree := req.CWD
	if worktree == "" {
		worktree = filepath.Join(projectRoot, ".worktrees", slug)
	}

	unpushed, err := deps.UnpushedCount(projectRoot)
	if err != nil || unpushed == 0 {
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
		"Local main is %d commit(s) ahead of origin/main and you have stopped. Run `git push` (or `wt ship` once it lands) to push the unit.\n",
		unpushed)
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
	if d.UnpushedCount == nil {
		d.UnpushedCount = UnpushedMainCommits
	}
	return d
}

// UnpushedMainCommits returns the count of commits on local `main`
// (or `master` fallback) that are not reachable from `origin/main`.
// Returns 0 when origin/main is missing (e.g. a repo with no remote
// yet); a missing remote ref is "nothing to push" by convention.
func UnpushedMainCommits(projectRoot string) (int, error) {
	mainRef := "main"
	if exec.Command("git", "-C", projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/main").Run() != nil {
		if exec.Command("git", "-C", projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/master").Run() == nil {
			mainRef = "master"
		} else {
			return 0, nil
		}
	}
	remoteRef := "refs/remotes/origin/" + mainRef
	if exec.Command("git", "-C", projectRoot, "show-ref", "--verify", "--quiet", remoteRef).Run() != nil {
		return 0, nil
	}
	out, err := exec.Command("git", "-C", projectRoot, "rev-list", "--count", "origin/"+mainRef+".."+mainRef).Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("unexpected rev-list --count output: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
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
	gd := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	parent := filepath.Dir(filepath.Dir(filepath.Dir(gd)))
	if parent == "" || parent == "/" {
		return "", false
	}
	return parent, true
}
