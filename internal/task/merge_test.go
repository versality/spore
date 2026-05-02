package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMergeNoBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Merge(tasksDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing branch, got nil")
	}
}

func TestMergeFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	// Create the wt/<slug> branch with a commit ahead of main.
	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo work")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// The branch should be deleted after merge.
	if branchExists(repo, "wt/demo") {
		t.Error("wt/demo branch still exists after Merge")
	}

	// main should include the feature commit.
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	if countLines(string(out)) < 2 {
		t.Errorf("expected at least 2 commits on main after merge, got:\n%s", out)
	}
}

func TestMergeRefusesNonFFOnDivergedBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	// Create diverging branch.
	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	// Add a commit to main so the two branches diverge.
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "main: extra")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := Merge(tasksDir, "demo")
	if err == nil {
		t.Fatal("expected error for non-FF merge, got nil")
	}
}

func TestMergeRemovesWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	// Create a worktree directory so we can test that Merge removes it.
	worktree := filepath.Join(repo, ".worktrees", "demo")
	runGit(t, repo, "worktree", "add", worktree, "wt/demo")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// Worktree should be gone.
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree %q still exists after Merge", worktree)
	}
}

func TestMergeNoWorktreeStillSucceeds(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	runGit(t, repo, "checkout", "-q", "-b", "wt/demo")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feat: demo")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// No worktree exists - Merge should still succeed (cleanup is best-effort).
	if err := Merge(tasksDir, "demo"); err != nil {
		t.Fatalf("Merge without worktree: %v", err)
	}
}
