package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAutoCommitDriftCleanTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "existing.md"), []byte("---\nstatus: active\nslug: existing\ntitle: Existing\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")

	// Clean tree - nothing to commit, should be a no-op.
	if err := AutoCommitDrift(tasksDir); err != nil {
		t.Fatalf("AutoCommitDrift on clean tree: %v", err)
	}

	// Verify no extra commit was made.
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := countLines(string(out))
	if lines != 1 {
		t.Errorf("expected 1 commit, got %d", lines)
	}
}

func TestAutoCommitDriftUncommittedChanges(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(tasksDir, "new.md"), []byte("---\nstatus: active\nslug: new\ntitle: New\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AutoCommitDrift(tasksDir); err != nil {
		t.Fatalf("AutoCommitDrift: %v", err)
	}

	// Should have added a commit.
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := countLines(string(out))
	if lines != 2 {
		t.Errorf("expected 2 commits, got %d:\n%s", lines, out)
	}
}

func TestAutoCommitDriftModifiedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: task\ntitle: Task\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")

	// Modify the task file.
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: task\ntitle: Task\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AutoCommitDrift(tasksDir); err != nil {
		t.Fatalf("AutoCommitDrift: %v", err)
	}

	// Verify there are 2 commits now.
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := countLines(string(out))
	if lines != 2 {
		t.Errorf("expected 2 commits, got %d:\n%s", lines, out)
	}
}

func TestAutoCommitDriftNotAGitRepo(t *testing.T) {
	// tasks dir inside a non-git directory - projectRootFromTasksDir just
	// returns the parent, git status will fail.
	parent := t.TempDir()
	tasksDir := filepath.Join(parent, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := AutoCommitDrift(tasksDir)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
}

func TestAutoCommitHappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(t.TempDir(), "merge.lock")
	if err := AutoCommit(AutoCommitOptions{Repo: repo, Lock: lock}); err != nil {
		t.Fatalf("AutoCommit: %v", err)
	}
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").Output()
	if err != nil {
		t.Fatal(err)
	}
	if countLines(string(out)) != 2 {
		t.Errorf("expected 2 commits, got:\n%s", out)
	}
}

func TestAutoCommitNoTasksDirNoop(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	lock := filepath.Join(t.TempDir(), "merge.lock")
	if err := AutoCommit(AutoCommitOptions{Repo: repo, Lock: lock}); err != nil {
		t.Fatalf("AutoCommit: %v", err)
	}
}

func TestAutoCommitMissingRepo(t *testing.T) {
	err := AutoCommit(AutoCommitOptions{Repo: ""})
	if err == nil {
		t.Fatal("expected error for empty repo")
	}
}

func TestAutoCommitNotAGitRepo(t *testing.T) {
	dir := t.TempDir()
	err := AutoCommit(AutoCommitOptions{Repo: dir, Lock: filepath.Join(t.TempDir(), "l")})
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
}

func TestAutoCommitRefusesStagedNonTasks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	if err := os.MkdirAll(filepath.Join(repo, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tasks", "t.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "other.txt")

	err := AutoCommit(AutoCommitOptions{Repo: repo, Lock: filepath.Join(t.TempDir(), "l")})
	var staged *AutoCommitStagedNonTasksError
	if !errors.As(err, &staged) {
		t.Fatalf("want AutoCommitStagedNonTasksError, got %v", err)
	}
	if len(staged.Paths) != 1 || staged.Paths[0] != "other.txt" {
		t.Errorf("Paths = %v, want [other.txt]", staged.Paths)
	}
}

func TestAutoCommitLockContentionReturnsErr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	lock := filepath.Join(t.TempDir(), "merge.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("seed flock: %v", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	// Use waitSeconds=0 indirectly: AutoCommit uses 30s, too long for a unit test.
	// Call flockWait directly to assert the timeout path.
	f2, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	err = flockWait(int(f2.Fd()), 0)
	if !errors.Is(err, ErrAutoCommitLocked) {
		t.Fatalf("want ErrAutoCommitLocked, got %v", err)
	}
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	if len(s) > 0 && s[len(s)-1] != '\n' {
		n++
	}
	return n
}
