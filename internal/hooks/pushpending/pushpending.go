// Package pushpending implements M-finish-B of the worker ship-cycle
// contract (tasks/spore-worker-finish-contract.md section 5): a Stop
// hook that fires exit 2 when a worker idles after `wt merge` has
// fast-forwarded local main but `origin/main` is still behind.
//
// Decision boundary (all must hold; else exit 0):
//
//  1. UnpushedMainCommits(projectRoot) > 0  (local main ahead of origin/main).
//  2. git -C <worktree> status --porcelain is empty.
//  3. <worktree>'s git-dir contains no MERGE_HEAD, rebase-merge, or rebase-apply.
//  4. agentpane.Classify on the worker pane reports "idle".
//
// SPORE_TASK_SLUG drives the session lookup; without it the hook is a
// no-op (the firing process is not a known worker).
package pushpending

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpane"
	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/hooks/wtgit"
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

	slug, projectRoot, worktree, ok := wtgit.ResolveRoots(req.CWD, deps.LookupEnv)
	if !ok {
		return Result{}
	}

	unpushed, err := deps.UnpushedCount(projectRoot)
	if err != nil || unpushed == 0 {
		return Result{}
	}

	if !wtgit.IdleGate(projectRoot, worktree, slug, wtgit.GateDeps{
		LookupEnv:   deps.LookupEnv,
		Capture:     deps.Capture,
		SessionName: deps.SessionName,
	}) {
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
