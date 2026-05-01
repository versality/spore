package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Merge fast-forward merges the wt/<slug> branch into main, then
// cleans up the worktree and branch. The task file's status is
// flipped to done as part of the merge. Refuses if the merge would
// not be a fast-forward.
func Merge(tasksDir, slug string) error {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}
	branch := "wt/" + slug
	if !branchExists(projectRoot, branch) {
		return fmt.Errorf("branch %s does not exist", branch)
	}

	// Fast-forward main to include wt/<slug>.
	out, err := gitCmd(projectRoot, "merge", "--ff-only", branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge --ff-only: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// Cleanup: remove worktree, delete branch, kill tmux session.
	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	if _, statErr := os.Stat(worktree); statErr == nil {
		_ = gitCmd(projectRoot, "worktree", "remove", "--force", worktree).Run()
	}
	_ = gitCmd(projectRoot, "branch", "-d", branch).Run()

	session := tmuxSessionName(projectRoot, slug)
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()

	return nil
}
