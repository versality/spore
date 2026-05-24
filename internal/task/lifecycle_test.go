package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/evidence"
	"github.com/versality/spore/internal/task/frontmatter"
	"github.com/versality/spore/internal/testagent"
	"github.com/versality/spore/internal/testpath"
)

func TestLifecycleStartBlockDone(t *testing.T) {
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
	slug := "demo"
	body := "---\nstatus: draft\nslug: demo\ntitle: Demo\n---\nbody\n"
	taskPath := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Start(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	wantSession := projectEmoji(filepath.Base(repo)) + " " + filepath.Base(repo) + "/" + slug + " [opus]"
	if session != wantSession {
		t.Errorf("session = %q, want %q", session, wantSession)
	}

	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after Start: status = %q, want active", status)
	}

	if _, err := os.Stat(filepath.Join(repo, ".worktrees", slug)); err != nil {
		t.Errorf("worktree missing after Start: %v", err)
	}
	if !branchExists(repo, "wt/"+slug) {
		t.Errorf("branch wt/%s missing after Start", slug)
	}

	// The brief must be present inside the worktree. The source-branch
	// HEAD has no tasks/ dir at this point (init was an empty commit
	// and the brief is uncommitted), so without the in-kernel copy the
	// worker would spawn into a worktree with no prompt.
	briefInWt := filepath.Join(repo, ".worktrees", slug, "tasks", slug+".md")
	got, err := os.ReadFile(briefInWt)
	if err != nil {
		t.Errorf("brief missing in worktree: %v", err)
	} else if string(got) == "" {
		t.Errorf("brief in worktree is empty")
	}

	out, err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", session).CombinedOutput()
	if err != nil {
		t.Errorf("tmux has-session: %v: %s", err, out)
	}

	if err := Block(tasksDir, slug, "test:pause-equivalent"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if status := readStatus(t, taskPath); status != "blocked" {
		t.Errorf("after Block: status = %q, want blocked", status)
	}

	if err := Block(tasksDir, slug, "test:again"); err == nil {
		t.Error("Block from blocked should error, got nil")
	}

	if err := Done(tasksDir, slug, false); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("after Done: status = %q, want done", status)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", slug)); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed after Done, stat err = %v", err)
	}
	if branchExists(repo, "wt/"+slug) {
		t.Errorf("branch wt/%s should be removed after Done", slug)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", session).Run(); err == nil {
		t.Errorf("tmux session %q still alive after Done", session)
	}

	if err := Done(tasksDir, slug, false); err != nil {
		t.Errorf("Done on already-done task should be no-op, got %v", err)
	}
}

func TestStartResumesBlocked(t *testing.T) {
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
	slug := "demo"
	body := "---\nstatus: draft\nslug: demo\ntitle: Demo\n---\nbody\n"
	taskPath := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Start(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if err := Block(tasksDir, slug, "test:resume-target"); err != nil {
		t.Fatalf("Block: %v", err)
	}

	resumed, err := Start(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Start (resume from blocked): %v", err)
	}
	if resumed != session {
		t.Errorf("resumed session = %q, want %q", resumed, session)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after resume: status = %q, want active", status)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", resumed).Run(); err != nil {
		t.Errorf("tmux has-session after resume: %v", err)
	}

	if err := Done(tasksDir, slug, false); err != nil {
		t.Fatalf("Done: %v", err)
	}
}

func TestStartSpawnsWtStyleSessionForKnownProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	parent := t.TempDir()
	repo := filepath.Join(parent, "spore")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "known"
	body := "---\nstatus: draft\nslug: known\ntitle: Known\nagent: codex\neffort: high\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	session, err := Start(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	want := "\U0001F41D spore/known [codex-high]"
	if session != want {
		t.Fatalf("session = %q, want %q", session, want)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", want).Run(); err != nil {
		t.Fatalf("expected tmux session %q: %v", want, err)
	}
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "show-environment", "-t", want, "WT_SESSION_KIND").Output()
	if err != nil {
		t.Fatalf("tmux show-environment: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "WT_SESSION_KIND=worker" {
		t.Errorf("WT_SESSION_KIND env on worker session = %q, want %q", got, "WT_SESSION_KIND=worker")
	}
}

func TestStartRendersCodexHooksIntoWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	parent := t.TempDir()
	repo := filepath.Join(parent, "spore")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	configsDir := filepath.Join(repo, "configs", "codex")
	if err := os.MkdirAll(configsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `{"events":{"Stop":[{"command":"coord-only","kinds":["coordinator"]},{"command":"worker-only","kinds":["worker"]}]}}`
	if err := os.WriteFile(filepath.Join(configsDir, "hooks-config.json"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "codexrender"
	body := "---\nstatus: draft\nslug: codexrender\ntitle: T\nagent: codex\neffort: high\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	session, err := Start(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	worktree := filepath.Join(repo, ".worktrees", slug)
	out, err := os.ReadFile(filepath.Join(worktree, ".codex/hooks.json"))
	if err != nil {
		t.Fatalf("worker spawn did not render .codex/hooks.json: %v", err)
	}
	body2 := string(out)
	if !strings.Contains(body2, "worker-only") {
		t.Errorf("rendered .codex/hooks.json missing worker-only: %s", body2)
	}
	if strings.Contains(body2, "coord-only") {
		t.Errorf("rendered .codex/hooks.json leaked coordinator binding into worker render: %s", body2)
	}
}

func TestDoneKillsFrontmatterSession(t *testing.T) {
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
	slug := "demo"
	// Pretend an external spawner registered a custom session name in
	// the brief. The kernel-computed name "spore/<project>/demo"
	// would never match; only the frontmatter value should.
	customSession := "worker-demo-" + filepath.Base(t.TempDir())
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nsession: " + customSession + "\n---\nbody\n"
	taskPath := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Spawn a tmux session under the custom name; no kernel-named
	// session is created. Done must still tear it down.
	if out, err := exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", customSession, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session %q: %v: %s", customSession, err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", customSession).Run()
	})

	if err := Done(tasksDir, slug, false); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("after Done: status = %q, want done", status)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", customSession).Run(); err == nil {
		t.Errorf("custom tmux session %q still alive after Done", customSession)
	}
}

func TestReapKillsFrontmatterSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	repo := t.TempDir()
	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "x"
	customSession := "reap-test-" + filepath.Base(t.TempDir())
	body := "---\nstatus: done\nslug: x\ntitle: X\nsession: " + customSession + "\n---\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", customSession, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", customSession).Run()
	})

	if err := Reap(tasksDir, repo, slug); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", customSession).Run(); err == nil {
		t.Errorf("custom tmux session %q still alive after Reap", customSession)
	}
}

func TestStartRefusesActive(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(tasksDir, "x", nil); err == nil {
		t.Fatal("Start on active task should error, got nil")
	}
}

func TestStartRefusesDone(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: done\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(tasksDir, "x", nil); err == nil {
		t.Fatal("Start on done task should error, got nil")
	}
}

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
		FakeTools: map[string]string{
			"tmux":  "#!/bin/sh\nexit 1\n",
			"spore": "#!/bin/sh\nexit 0\n",
		},
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
		FakeTools: map[string]string{
			"tmux":  "#!/bin/sh\nexit 1\n",
			"spore": "#!/bin/sh\nexit 0\n",
		},
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
		FakeTools: map[string]string{
			"spore": "#!/bin/sh\nexit 0\n",
		},
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

func TestWorkerAgentCommandCodexUsesEffortPolicy(t *testing.T) {
	t.Setenv("SPORE_AGENT_BINARY", "")
	m := frontmatter.Meta{
		Agent: "codex",
		Extra: map[string]string{
			"effort": "very-high",
			"model":  "gpt-5.5",
		},
	}
	got, err := workerAgentCommand(m)
	if err != nil {
		t.Fatalf("workerAgentCommand: %v", err)
	}
	want := "codex --dangerously-bypass-approvals-and-sandbox --no-alt-screen --disable apps -m gpt-5.5 -c 'model_reasoning_effort=\"xhigh\"'"
	if got != want {
		t.Errorf("command = %q want %q", got, want)
	}
}

func TestWorkerAgentCommandClaudeUsesEffortPolicy(t *testing.T) {
	t.Setenv("SPORE_AGENT_BINARY", "")
	m := frontmatter.Meta{
		Agent: "claude",
		Extra: map[string]string{
			"effort": "high",
		},
	}
	got, err := workerAgentCommand(m)
	if err != nil {
		t.Fatalf("workerAgentCommand: %v", err)
	}
	want := "claude --dangerously-skip-permissions --effort high"
	if got != want {
		t.Errorf("command = %q want %q", got, want)
	}
}

func TestWorkerAgentCommandOverrideWins(t *testing.T) {
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	got, err := workerAgentCommand(frontmatter.Meta{Agent: "codex"})
	if err != nil {
		t.Fatalf("workerAgentCommand: %v", err)
	}
	if got != "sleep 30" {
		t.Errorf("command = %q want override", got)
	}
}

func TestBlockRequiresActive(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Block(tasksDir, "x", "test:reason"); err == nil {
		t.Fatal("Block on draft task should error, got nil")
	}
}

func TestDoneRefusesBogusEvidence(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nevidence_required: [commit]\n---\n" +
		"## Evidence\n- commit: hello world not a sha\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_EVIDENCE_WARN_ONLY", "0")
	// Force the gate out of the soak window so the verdict hard-fails
	// regardless of clock drift in CI.
	origStart := evidence.ContractStart
	evidence.ContractStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() { evidence.ContractStart = origStart })

	if err := Done(tasksDir, "x", false); err == nil {
		t.Fatal("Done with bogus evidence should error, got nil")
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("status flipped despite refusal: got %q want active", status)
	}
}

func TestDoneAllowsRealImpl(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nevidence_required: [commit, file]\n---\n" +
		"## Evidence\n- commit: a1b2c3d4 shipped it\n- file: internal/x.go added X\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_EVIDENCE_WARN_ONLY", "0")
	origStart := evidence.ContractStart
	evidence.ContractStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() { evidence.ContractStart = origStart })

	if err := Done(tasksDir, "x", false); err != nil {
		t.Fatalf("Done with real-impl evidence: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done", status)
	}
}

func TestDoneWarnOnlyAllowsBlockedVerdict(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nevidence_required: [commit]\n---\n" +
		"## Evidence\n- commit:\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_EVIDENCE_WARN_ONLY", "1")
	origStart := evidence.ContractStart
	evidence.ContractStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() { evidence.ContractStart = origStart })

	if err := Done(tasksDir, "x", false); err != nil {
		t.Fatalf("Done in warn-only mode should pass, got %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("status = %q want done (warn-only)", status)
	}
}

func TestDoneRefusesUnreadInbox(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Chdir(filepath.Dir(tasksDir))

	inbox := filepath.Join(state, "spore", filepath.Base(filepath.Dir(tasksDir)), "x", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Done(tasksDir, "x", false)
	if err == nil {
		t.Fatal("Done should refuse with unread inbox, got nil")
	}
	if !strings.Contains(err.Error(), "unread inbox") {
		t.Errorf("error %q should mention 'unread inbox'", err)
	}
	if readStatus(t, taskPath) != "active" {
		t.Error("status should remain active")
	}
}

func TestDoneForceBypassesInbox(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Chdir(filepath.Dir(tasksDir))

	inbox := filepath.Join(state, "spore", filepath.Base(filepath.Dir(tasksDir)), "x", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done --force should bypass inbox gate: %v", err)
	}
	if readStatus(t, taskPath) != "done" {
		t.Error("status should be done")
	}
}

func TestBlockRefusesUnreadInbox(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Chdir(filepath.Dir(tasksDir))

	inbox := filepath.Join(state, "spore", filepath.Base(filepath.Dir(tasksDir)), "x", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Block(tasksDir, "x", "operator: waiting on something")
	if err == nil {
		t.Fatal("Block should refuse with unread inbox, got nil")
	}
	if !strings.Contains(err.Error(), "unread inbox") {
		t.Errorf("error %q should mention 'unread inbox'", err)
	}
}

func TestDoneRefusesUnmergedCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, repo, "checkout", "-q", "-b", "wt/x")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feature")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Done(tasksDir, "x", false)
	if err == nil {
		t.Fatal("Done should refuse with unmerged commits, got nil")
	}
	if !strings.Contains(err.Error(), "unmerged commit") {
		t.Errorf("error %q should mention 'unmerged commit'", err)
	}
}

func TestDoneForceBypassesUnmergedCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	repo := t.TempDir()
	t.Chdir(repo)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, repo, "checkout", "-q", "-b", "wt/x")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "feature")
	runGit(t, repo, "checkout", "-q", "main")

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "x.md"), []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done --force should bypass unmerged gate: %v", err)
	}
	if readStatus(t, filepath.Join(tasksDir, "x.md")) != "done" {
		t.Error("status should be done")
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func configureOrigin(t *testing.T, repo string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, t.TempDir(), "init", "--bare", "-q", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-q", "-u", "origin", "main")
	return remote
}

func readStatus(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m.Status
}
