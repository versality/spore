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
