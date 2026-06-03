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
	"path/filepath"

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

	slug, projectRoot, worktree, ok := wtgit.ResolveRoots(req.CWD, deps.LookupEnv)
	if !ok {
		return Result{}
	}

	branch := "wt/" + slug
	unmerged, err := task.UnmergedCommits(projectRoot, branch)
	if err != nil || unmerged == 0 {
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
