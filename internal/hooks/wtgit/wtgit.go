// Package wtgit holds the git-worktree probes used by every hook
// that branches on worktree state: prfinish, pushpending,
// wtmergemechanical. The five helpers were inlined byte-for-byte in
// each consumer; this package owns the canonical implementations so
// future fixes (a new merge-in-progress marker, an additional
// rebase-state path, a portable rev-parse fallback) land in one place.
package wtgit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorkingTreeClean reports whether `git status --porcelain` for
// worktree is empty. Any error (worktree gone, not a git dir, git
// missing) reads as "not clean" so hooks decline to act.
func WorkingTreeClean(worktree string) bool {
	out, err := exec.Command("git", "-C", worktree, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == ""
}

// WorkingTreeCleanForWorker reports whether a worker worktree is clean
// enough for lifecycle hooks. A task file diff that only records
// Spore-managed worker-state / worker-result frontmatter is treated as
// runtime metadata rather than payload dirtiness.
func WorkingTreeCleanForWorker(worktree, slug string) bool {
	out, err := exec.Command("git", "-C", worktree, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	status := string(out)
	if strings.TrimSpace(status) == "" {
		return true
	}
	for _, raw := range strings.Split(status, "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		path := statusPath(raw)
		if path == "tasks/"+slug+".md" && taskDiffIsWorkerRuntimeOnly(worktree, path) {
			continue
		}
		return false
	}
	return true
}

func taskDiffIsWorkerRuntimeOnly(worktree, path string) bool {
	out, err := exec.Command("git", "-C", worktree, "diff", "--no-ext-diff", "--", path).Output()
	if err != nil {
		return false
	}
	sawRuntimeLine := false
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			body := strings.TrimSpace(line[1:])
			if strings.HasPrefix(body, "worker-state:") || strings.HasPrefix(body, "worker-result:") {
				sawRuntimeLine = true
				continue
			}
			return false
		}
	}
	return sawRuntimeLine
}

func statusPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := strings.TrimSpace(line[3:])
	if i := strings.LastIndex(path, " -> "); i >= 0 {
		path = path[i+4:]
	}
	return filepath.ToSlash(path)
}

// MidMergeOrRebase reports whether the worktree sits in the middle
// of a merge or rebase: MERGE_HEAD, rebase-merge/, or rebase-apply/
// present under .git. Hooks that auto-commit or push refuse to act
// in this state.
func MidMergeOrRebase(worktree string) bool {
	gd, err := GitDir(worktree)
	if err != nil {
		return false
	}
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gd, name)); err == nil {
			return true
		}
	}
	return false
}

// GitDir resolves the absolute .git directory for worktree via
// `git rev-parse --git-dir`. Relative results (the common case in a
// worktree checkout, where rev-parse returns the linked path) are
// joined to worktree to absolutize.
func GitDir(worktree string) (string, error) {
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

// TopLevel returns the absolute path of the working-tree root for
// any directory inside a git checkout (`git rev-parse --show-toplevel`).
// Used by hooks that receive a cwd from the harness and need to find
// the worktree above it.
func TopLevel(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("empty cwd")
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// MainCheckoutFromWorktree resolves a linked worktree path to the
// main checkout that hosts its `.git/worktrees/<slug>/` administrative
// directory. Returns ("", false) when the input is itself the main
// checkout (its `.git` is a real directory, not the `gitdir: ...`
// pointer file linked worktrees carry).
func MainCheckoutFromWorktree(worktree string) (string, bool) {
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
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(worktree, gd)
	}
	parent := filepath.Dir(filepath.Dir(filepath.Dir(gd)))
	if parent == "" || parent == "/" {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		return real, true
	}
	return filepath.Clean(parent), true
}
