package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/testagent"
	"github.com/versality/spore/internal/testpath"
)

func TestStartMissingSelectedAgentLeavesDraft(t *testing.T) {
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
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: x\ntitle: X\nagent: codex\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git"},
		FakeTools: map[string]string{"tmux": "#!/bin/sh\nexit 1\n", "spore": "#!/bin/sh\nexit 0\n"},
	})
	t.Setenv("PATH", h.BinDir)
	t.Setenv("SPORE_AGENT_BINARY", "")

	_, err := Start(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "missing-worker-agent:codex") {
		t.Fatalf("Start err = %v, want missing codex preflight", err)
	}
	if status := readStatus(t, taskPath); status != "draft" {
		t.Fatalf("status = %q, want draft", status)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "x")); !os.IsNotExist(err) {
		t.Fatalf("worktree stat err = %v, want not exist", err)
	}
}

func TestEnsureMissingSelectedAgentDoesNotCreateWorktree(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: active\nslug: x\ntitle: X\nagent: codex\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git"},
		FakeTools: map[string]string{"tmux": "#!/bin/sh\nexit 1\n", "spore": "#!/bin/sh\nexit 0\n"},
	})
	t.Setenv("PATH", h.BinDir)
	t.Setenv("SPORE_AGENT_BINARY", "")

	_, err := Ensure(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "missing-worker-agent:codex") {
		t.Fatalf("Ensure err = %v, want missing codex preflight", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "x")); !os.IsNotExist(err) {
		t.Fatalf("worktree stat err = %v, want not exist", err)
	}
}

func TestStartDetectsAgentExitDuringSettle(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: draft\nslug: x\ntitle: X\nagent: codex\neffort: high\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testagent.InstallPathHarness(t, testagent.PathOptions{
		IncludeCodex: true,
		RealTools:    []string{"git", "tmux"},
		FakeTools:    map[string]string{"spore": "#!/bin/sh\nexit 0\n"},
	})
	t.Setenv("PATH", h.PATH)
	t.Setenv("SPORE_AGENT_BINARY", "")
	t.Setenv(testagent.EnvMode, testagent.ModeExitNonzero)
	t.Setenv(testagent.EnvEventLog, filepath.Join(t.TempDir(), "events.jsonl"))

	_, err := Start(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "died on spawn") && !strings.Contains(err.Error(), "dead pane") {
		t.Fatalf("Start err = %v, want settle failure", err)
	}
}

func TestEnsureExistingDeadPaneIsNotHealthy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	repo := t.TempDir()
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	session := "dead-pane-test-" + strings.ReplaceAll(filepath.Base(repo), "-", "")
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: active\nslug: x\ntitle: X\nsession: "+session+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", session, "sh", "-c", "read line").CombinedOutput()
	if err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})
	if out, err := exec.Command("tmux", "-L", testTmuxSocket, "set-option", "-t", session, "remain-on-exit", "on").CombinedOutput(); err != nil {
		t.Fatalf("tmux remain-on-exit: %v: %s", err, out)
	}
	if out, err := exec.Command("tmux", "-L", testTmuxSocket, "send-keys", "-t", session, "done", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("tmux send-keys: %v: %s", err, out)
	}
	time.Sleep(workerSpawnSettleDelay)

	_, err = Ensure(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "dead pane") {
		t.Fatalf("Ensure err = %v, want dead pane", err)
	}
}
