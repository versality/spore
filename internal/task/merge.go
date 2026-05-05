package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// Merge fast-forward merges the wt/<slug> branch into main, then
// cleans up the worktree and branch. The task file's status is
// flipped to done as part of the merge. Refuses if the merge would
// not be a fast-forward or if the landed main cannot be pushed to
// origin.
func Merge(tasksDir, slug string) error {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}
	branch := "wt/" + slug
	if !branchExists(projectRoot, branch) {
		return fmt.Errorf("branch %s does not exist", branch)
	}
	if err := requireMainCheckout(projectRoot); err != nil {
		return err
	}

	// Fast-forward main to include wt/<slug>.
	out, err := gitCmd(projectRoot, "merge", "--ff-only", branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("merge --ff-only: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if err := closeMergedTask(tasksDir, slug); err != nil {
		return err
	}
	if err := pushAndVerifyMain(projectRoot); err != nil {
		return err
	}

	worktree := filepath.Join(projectRoot, ".worktrees", slug)
	if _, statErr := os.Stat(worktree); statErr == nil {
		_ = gitCmd(projectRoot, "worktree", "remove", "--force", worktree).Run()
	}
	_ = gitCmd(projectRoot, "branch", "-d", branch).Run()

	session := taskTmuxSession(tasksDir, projectRoot, slug)
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()

	return nil
}

func requireMainCheckout(projectRoot string) error {
	out, err := gitCmd(projectRoot, "branch", "--show-current").Output()
	if err != nil {
		return fmt.Errorf("git branch --show-current: %w", err)
	}
	current := strings.TrimSpace(string(out))
	if current != "main" {
		if current == "" {
			current = "detached HEAD"
		}
		return fmt.Errorf("merge must run from the main checkout; current branch is %q", current)
	}
	return nil
}

func pushAndVerifyMain(projectRoot string) error {
	out, err := gitCmd(projectRoot, "push", "origin", "main:main").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push origin main:main: %w: %s", err, strings.TrimSpace(string(out)))
	}

	localOut, err := gitCmd(projectRoot, "rev-parse", "main").Output()
	if err != nil {
		return fmt.Errorf("git rev-parse main: %w", err)
	}
	local := strings.TrimSpace(string(localOut))

	remoteOut, err := gitCmd(projectRoot, "ls-remote", "origin", "refs/heads/main").Output()
	if err != nil {
		return fmt.Errorf("git ls-remote origin refs/heads/main: %w", err)
	}
	fields := strings.Fields(string(remoteOut))
	if len(fields) == 0 {
		return fmt.Errorf("post-push verification failed: origin refs/heads/main not found")
	}
	if fields[0] != local {
		return fmt.Errorf("post-push verification failed: origin/main=%s, local main=%s", fields[0], local)
	}
	return nil
}

func closeMergedTask(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m, body, err := frontmatter.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if m.Status == "done" {
		return nil
	}
	if err := inboxGate(slug); err != nil {
		return err
	}
	if err := evidenceGate(slug, m, body, os.Stderr); err != nil {
		return err
	}

	m.Status = "done"
	if err := os.WriteFile(path, frontmatter.Write(m, body), 0o644); err != nil {
		return err
	}

	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(projectRoot, path)
	if err != nil {
		rel = path
	}
	if out, err := gitCmd(projectRoot, "add", "--", rel).CombinedOutput(); err != nil {
		return fmt.Errorf("git add task close: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := gitCmd(projectRoot, "commit", "-m", fmt.Sprintf("tasks/%s: status -> done", slug), "--", rel).CombinedOutput(); err != nil {
		if strings.Contains(string(out), "nothing to commit") {
			return nil
		}
		return fmt.Errorf("git commit task close: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
