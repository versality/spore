package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/testpath"
)

func TestClassifyWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	wt := filepath.Join(repo, ".worktrees", "x")
	branch := "wt/x"

	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeAbsent {
		t.Errorf("clean repo: state = %v, want worktreeAbsent", got)
	}

	runGit(t, repo, "worktree", "add", "-q", wt, "-b", branch)
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeOK {
		t.Errorf("after add: state = %v, want worktreeOK", got)
	}

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeStaleReg {
		t.Errorf("dir removed, reg intact: state = %v, want worktreeStaleReg", got)
	}

	runGit(t, repo, "worktree", "prune")

	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeDirNotReg {
		t.Errorf("plain dir at slot: state = %v, want worktreeDirNotReg", got)
	}
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(repo, ".worktrees", "other")
	runGit(t, repo, "worktree", "add", "-q", other, branch)
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeForeignReg {
		t.Errorf("branch checked out elsewhere (live): state = %v, want worktreeForeignReg", got)
	}
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeStaleReg {
		t.Errorf("branch checked out elsewhere (dir gone): state = %v, want worktreeStaleReg", got)
	}
	runGit(t, repo, "worktree", "prune")

	runGit(t, repo, "worktree", "add", "-q", wt, "-b", "wt/other")
	if got, _ := classifyWorktree(repo, wt, branch); got != worktreeWrongBranch {
		t.Errorf("dir on different branch: state = %v, want worktreeWrongBranch", got)
	}
}

func TestPrepareTaskBaselineCommitsOnlyCurrentTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "other.md"), []byte("other head\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tasks/other.md")
	runGit(t, repo, "commit", "-q", "-m", "seed tasks")

	if err := os.WriteFile(filepath.Join(tasksDir, "demo.md"), []byte("---\nslug: demo\n---\nbrief\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := filepath.Join(repo, ".worktrees", "demo")
	runGit(t, repo, "worktree", "add", "-q", "-b", "wt/demo", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "worker-note.txt"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "tasks", "other.md"), []byte("leaked other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "tasks", "leaked.md"), []byte("leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := prepareTaskBaseline(tasksDir, worktree, "demo"); err != nil {
		t.Fatalf("prepareTaskBaseline: %v", err)
	}
	if got := readFileString(t, filepath.Join(worktree, "tasks", "other.md")); got != "other head\n" {
		t.Fatalf("other task = %q, want HEAD content", got)
	}
	if _, err := os.Stat(filepath.Join(worktree, "tasks", "leaked.md")); !os.IsNotExist(err) {
		t.Fatalf("leaked task should be cleaned, stat err=%v", err)
	}
	if got := readFileString(t, filepath.Join(worktree, "tasks", "demo.md")); !strings.Contains(got, "brief") {
		t.Fatalf("current task not copied: %q", got)
	}
	if got := readFileString(t, filepath.Join(worktree, "worker-note.txt")); got != "keep me\n" {
		t.Fatalf("non-task file = %q, want preserved", got)
	}
	status := strings.TrimSpace(string(runGitOutput(t, worktree, "status", "--porcelain")))
	if status != "?? worker-note.txt" {
		t.Fatalf("worktree status = %q, want only non-task file preserved", status)
	}
	head := strings.TrimSpace(string(runGitOutput(t, worktree, "log", "-1", "--format=%s")))
	if head != "task: start demo" {
		t.Fatalf("HEAD subject = %q, want task baseline commit", head)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func runGitOutput(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, repo, err, out)
	}
	return out
}

func TestEnsureReusesExistingWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "reuse"
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: Reuse\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Ensure(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Ensure (first): %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()

	wt := filepath.Join(repo, ".worktrees", slug)
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree missing before second Ensure: %v", err)
	}

	if _, err := Ensure(tasksDir, slug, nil); err != nil {
		t.Fatalf("Ensure (resume): %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree missing after resume: %v", err)
	}
}

func TestEnsureRecoversStaleRegistration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
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
	slug := "stale"
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: Stale\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Ensure(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Ensure (first): %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	wt := filepath.Join(repo, ".worktrees", slug)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	if _, err := Ensure(tasksDir, slug, nil); err != nil {
		t.Fatalf("Ensure (after dir rm): %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree missing after stale-reg recovery: %v", err)
	}
}

func TestEnsureRefusesDirNotRegistered(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git"},
		FakeTools: map[string]string{
			"tmux":  "#!/bin/sh\nif [ \"$1\" = has-session ]; then exit 1; fi\nexit 0\n",
			"spore": "#!/bin/sh\nexit 0\n",
			"sleep": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

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
	slug := "plain-dir"
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: Plain\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", slug), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	_, err := Ensure(tasksDir, slug, nil)
	if err == nil {
		t.Fatal("Ensure on plain dir: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not a registered git worktree") {
		t.Errorf("unexpected error: %v", err)
	}
}
