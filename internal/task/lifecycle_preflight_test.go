package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/hooks/settings"
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

func TestStartCodexUntrustedProjectDoesNotCreateWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.MkdirAll(filepath.Join(repo, "configs", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "configs", "codex", "hooks-config.json"), []byte(`{"events":{"Stop":[{"command":"spore hooks codex stop","timeout":30}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".codex", "hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"spore hooks codex stop","timeout":30}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
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
		FakeTools: map[string]string{"codex": "#!/bin/sh\nexit 0\n", "tmux": "#!/bin/sh\nexit 1\n", "spore": "#!/bin/sh\nexit 0\n"},
	})
	t.Setenv("PATH", h.BinDir)

	_, err := Start(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "codex-project-untrusted:codex") {
		t.Fatalf("Start err = %v, want codex trust preflight", err)
	}
	if status := readStatus(t, taskPath); status != "draft" {
		t.Fatalf("status = %q, want draft", status)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "x")); !os.IsNotExist(err) {
		t.Fatalf("worktree stat err = %v, want not exist", err)
	}
}

func TestStartUsesFleetDefaultAgentForUnpinnedTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "spore.toml"), []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "configs", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "configs", "codex", "hooks-config.json"), []byte(`{"events":{"Stop":[{"command":"spore hooks codex stop","timeout":30}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCodexReady(t, repo)
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: draft\nslug: x\ntitle: X\neffort: high\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmuxState := filepath.Join(t.TempDir(), "tmux-session")
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git"},
		FakeTools: map[string]string{
			"codex": "#!/bin/sh\nexit 0\n",
			"spore": "#!/bin/sh\nexit 0\n",
			"tmux":  "#!/bin/sh\ncase \"$1\" in\n  has-session) test -f " + shellQuote(tmuxState) + " ;;\n  new-session) echo ok > " + shellQuote(tmuxState) + " ;;\n  set-option) exit 0 ;;\n  list-panes) echo 0 ;;\n  kill-session) exit 0 ;;\n  *) exit 0 ;;\nesac\n",
		},
	})
	t.Setenv("PATH", h.BinDir)
	t.Setenv("SPORE_AGENT_BINARY", "")

	_, err := Start(tasksDir, "x", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "x", ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("worktree codex hooks should not be written: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(repo, ".codex", "hooks.json")); err != nil {
		t.Fatalf("codex hooks missing: %v", err)
	} else if !strings.Contains(string(body), "spore hooks codex stop") {
		t.Fatalf("codex hooks missing stop adapter:\n%s", body)
	}
}

func writeCodexReady(t *testing.T, repo string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[projects."+strconv.Quote(filepath.Clean(repo))+"]\ntrust_level = \"trusted\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, ok, err := settings.RenderCodex(filepath.Join(repo, "configs", "codex", "hooks-config.json"), SessionKindCoordinator)
	if err != nil || !ok {
		t.Fatalf("render codex hooks: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".codex", "hooks.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStartMissingFleetDefaultAgentDoesNotCreateWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "spore.toml"), []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: x\ntitle: X\n---\nbody\n"), 0o644); err != nil {
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

func TestStartExplicitAgentOverridesFleetDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	repo := t.TempDir()
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "spore.toml"), []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: draft\nslug: x\ntitle: X\nagent: claude\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git"},
		FakeTools: map[string]string{"tmux": "#!/bin/sh\nexit 1\n", "spore": "#!/bin/sh\nexit 0\n"},
	})
	t.Setenv("PATH", h.BinDir)
	t.Setenv("SPORE_AGENT_BINARY", "")

	_, err := Start(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "missing-worker-agent:claude") {
		t.Fatalf("Start err = %v, want missing claude preflight", err)
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
	if status := readStatus(t, filepath.Join(tasksDir, "x.md")); status != "draft" {
		t.Fatalf("status = %q, want draft after failed spawn", status)
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
	waitForDeadPane(t, session)

	_, err = Ensure(tasksDir, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "dead pane") {
		t.Fatalf("Ensure err = %v, want dead pane", err)
	}
}

func waitForDeadPane(t *testing.T, session string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		dead, err := sessionHasDeadPane(session)
		if err != nil {
			lastErr = err
		} else if dead {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("dead pane did not appear: %v", lastErr)
	}
	t.Fatal("dead pane did not appear before timeout")
}
