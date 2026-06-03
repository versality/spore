package wtgit

import (
	"path/filepath"

	"github.com/versality/spore/internal/agentpane"
)

// GateDeps carries the test seams the idle-worker gate needs. Each
// Stop hook (prfinish, pushpending, wtmergemechanical) already owns a
// Deps struct with these same fields; it forwards them here so the
// shared preamble runs against the hook's injected fakes.
type GateDeps struct {
	LookupEnv   func(string) (string, bool)
	Capture     agentpane.CaptureFunc
	SessionName func(projectRoot, slug string) string
}

// IdleWorkerGate runs the universal Stop-hook preamble shared by the
// worker ship-cycle hooks: it reads SPORE_TASK_SLUG, resolves the
// project root and worktree (SPORE_PROJECT_ROOT, else TopLevel +
// MainCheckoutFromWorktree off cwd), and confirms the worktree is
// clean, not mid-merge/rebase, and that the worker pane classifies as
// idle.
//
// ok is true only when every gate passes; on any miss the caller
// returns its empty Result. projectRoot and worktree are populated
// whenever a slug is present. Hooks that must run a distinguishing
// query before the clean/idle gates use ResolveRoots + IdleGate
// instead.
func IdleWorkerGate(cwd string, deps GateDeps) (projectRoot, worktree string, ok bool) {
	slug, projectRoot, worktree, ok := ResolveRoots(cwd, deps.LookupEnv)
	if !ok {
		return "", "", false
	}
	if !IdleGate(projectRoot, worktree, slug, deps) {
		return projectRoot, worktree, false
	}
	return projectRoot, worktree, true
}

// ResolveRoots runs the slug + project-root/worktree resolution half of
// the gate, for hooks that must run a distinguishing query against the
// resolved paths before applying the clean/idle gates. ok is false when
// SPORE_TASK_SLUG is unset or the cwd is not a git checkout.
func ResolveRoots(cwd string, lookupEnv func(string) (string, bool)) (slug, projectRoot, worktree string, ok bool) {
	slug, present := lookupEnv("SPORE_TASK_SLUG")
	if !present || slug == "" {
		return "", "", "", false
	}
	projectRoot, _ = lookupEnv("SPORE_PROJECT_ROOT")
	if projectRoot == "" {
		root, err := TopLevel(cwd)
		if err != nil {
			return "", "", "", false
		}
		if main, ok := MainCheckoutFromWorktree(root); ok {
			projectRoot = main
		} else {
			projectRoot = root
		}
	}
	worktree = cwd
	if worktree == "" {
		worktree = filepath.Join(projectRoot, ".worktrees", slug)
	}
	return slug, projectRoot, worktree, true
}

// IdleGate applies the clean / not-mid-merge / idle gates against an
// already-resolved worktree and project root. Hooks that ran their
// distinguishing query against the resolved paths call this after the
// query passes.
func IdleGate(projectRoot, worktree, slug string, deps GateDeps) bool {
	if !WorkingTreeClean(worktree) {
		return false
	}
	if MidMergeOrRebase(worktree) {
		return false
	}
	session := deps.SessionName(projectRoot, slug)
	if session == "" {
		return false
	}
	state, _ := agentpane.Classify(deps.Capture, session+":claude", "claude")
	return state == "idle"
}
