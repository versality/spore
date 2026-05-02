package task

import (
	"fmt"
	"strings"
)

// AutoCommitDrift stages and commits any uncommitted changes under
// tasksDir to the current branch. Idempotent: no-ops when the tree
// is clean. Returns nil when there is nothing to commit.
func AutoCommitDrift(tasksDir string) error {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}

	out, err := gitCmd(projectRoot, "status", "--porcelain", "--", tasksDir).Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}

	if o, err := gitCmd(projectRoot, "add", "--", tasksDir).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(o)))
	}
	if o, err := gitCmd(projectRoot, "commit", "-m", "tasks: auto-commit drift").CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(o)))
	}
	return nil
}
