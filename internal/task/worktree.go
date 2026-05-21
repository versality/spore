package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// worktreeState classifies the live state of the (worktree path, branch)
// pair against `git worktree list --porcelain`. ensureSession routes on
// it to stay idempotent across worker restarts and operator-side cleanups.
type worktreeState int

const (
	// Dir + git registration both exist at our path and the registration
	// is on our branch. Reuse, skip add.
	worktreeOK worktreeState = iota
	// Dir absent and no git registration mentions our path or our
	// branch. Fresh add.
	worktreeAbsent
	// Branch is registered at our exact path but the dir is gone. Prune
	// then re-add. This is the production trigger for stuck workers.
	worktreeStaleReg
	// Branch is registered at a different live path. Real conflict;
	// surface to the operator.
	worktreeForeignReg
	// Dir exists at our path but git doesn't know about it as a
	// worktree. Either a plain dir collided with our slot or the
	// worktree's .git pointer is corrupt. Bail.
	worktreeDirNotReg
	// Dir + registration both at our path but the registration is on a
	// different branch. Bail; we won't silently retarget.
	worktreeWrongBranch
)

type worktreeEntry struct {
	path   string
	branch string
}

func listWorktrees(projectRoot string) ([]worktreeEntry, error) {
	out, err := gitCmd(projectRoot, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return entries, nil
}

func canonicalWorktreePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func classifyWorktree(projectRoot, worktree, branch string) (worktreeState, error) {
	absWT := canonicalWorktreePath(worktree)
	entries, err := listWorktrees(projectRoot)
	if err != nil {
		return 0, err
	}
	var atPath *worktreeEntry
	var onBranch *worktreeEntry
	for i := range entries {
		e := &entries[i]
		abs := canonicalWorktreePath(e.path)
		if abs == absWT {
			atPath = e
		}
		if e.branch == branch {
			onBranch = e
		}
	}
	_, statErr := os.Stat(worktree)
	dirExists := statErr == nil
	switch {
	case atPath != nil && atPath.branch == branch && dirExists:
		return worktreeOK, nil
	case atPath != nil && atPath.branch != branch && dirExists:
		return worktreeWrongBranch, nil
	case atPath != nil && !dirExists:
		// Registered at our path, dir gone. Branch may be ours or not;
		// either way prune clears it and we re-add fresh.
		return worktreeStaleReg, nil
	case atPath == nil && dirExists:
		return worktreeDirNotReg, nil
	case onBranch != nil:
		// Branch is live at a different path. If that dir is also gone,
		// prune reclaims it; otherwise it's a real conflict.
		if _, err := os.Stat(onBranch.path); os.IsNotExist(err) {
			return worktreeStaleReg, nil
		}
		return worktreeForeignReg, nil
	}
	return worktreeAbsent, nil
}

func worktreeConflictError(state worktreeState, worktree, branch, projectRoot string) error {
	switch state {
	case worktreeDirNotReg:
		return fmt.Errorf("worktree path %s exists but is not a registered git worktree; remove or recover it before resuming", worktree)
	case worktreeWrongBranch:
		entries, _ := listWorktrees(projectRoot)
		other := branch
		absW := canonicalWorktreePath(worktree)
		for _, e := range entries {
			if canonicalWorktreePath(e.path) == absW {
				other = e.branch
				break
			}
		}
		return fmt.Errorf("worktree %s is checked out on branch %s, want %s", worktree, other, branch)
	case worktreeForeignReg:
		entries, _ := listWorktrees(projectRoot)
		var atPath string
		for _, e := range entries {
			if e.branch == branch {
				atPath = e.path
				break
			}
		}
		return fmt.Errorf("branch %s is checked out at %s, cannot also check out at %s", branch, atPath, worktree)
	}
	return fmt.Errorf("worktree %s: unexpected state %d", worktree, state)
}
