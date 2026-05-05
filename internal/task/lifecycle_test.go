package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/evidence"
	"github.com/versality/spore/internal/task/frontmatter"
)

func TestLifecycleStartPauseDone(t *testing.T) {
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

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	wantSuffix := "/" + slug
	if !strings.HasSuffix(session, wantSuffix) {
		t.Errorf("session %q missing suffix %q", session, wantSuffix)
	}
	if !strings.HasPrefix(session, "spore/") {
		t.Errorf("session %q missing prefix \"spore/\"", session)
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

	out, err := exec.Command("tmux", "has-session", "-t", session).CombinedOutput()
	if err != nil {
		t.Errorf("tmux has-session: %v: %s", err, out)
	}

	if err := Pause(tasksDir, slug); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if status := readStatus(t, taskPath); status != "paused" {
		t.Errorf("after Pause: status = %q, want paused", status)
	}

	if err := Pause(tasksDir, slug); err == nil {
		t.Error("Pause from paused should error, got nil")
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
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err == nil {
		t.Errorf("tmux session %q still alive after Done", session)
	}

	if err := Done(tasksDir, slug, false); err != nil {
		t.Errorf("Done on already-done task should be no-op, got %v", err)
	}
}

func TestStartResumesPaused(t *testing.T) {
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

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	if err := Pause(tasksDir, slug); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	resumed, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start (resume from paused): %v", err)
	}
	if resumed != session {
		t.Errorf("resumed session = %q, want %q", resumed, session)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after resume: status = %q, want active", status)
	}
	if err := exec.Command("tmux", "has-session", "-t", resumed).Run(); err != nil {
		t.Errorf("tmux has-session after resume: %v", err)
	}

	if err := Done(tasksDir, slug, false); err != nil {
		t.Fatalf("Done: %v", err)
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
	customSession := "rower-demo-" + filepath.Base(t.TempDir())
	body := "---\nstatus: active\nslug: demo\ntitle: Demo\nsession: " + customSession + "\n---\nbody\n"
	taskPath := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Spawn a tmux session under the custom name; no kernel-named
	// session is created. Done must still tear it down.
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", customSession, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session %q: %v: %s", customSession, err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", customSession).Run()
	})

	if err := Done(tasksDir, slug, false); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if status := readStatus(t, taskPath); status != "done" {
		t.Errorf("after Done: status = %q, want done", status)
	}
	if err := exec.Command("tmux", "has-session", "-t", customSession).Run(); err == nil {
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

	if out, err := exec.Command("tmux", "new-session", "-d", "-s", customSession, "sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", customSession).Run()
	})

	if err := Reap(tasksDir, repo, slug); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if err := exec.Command("tmux", "has-session", "-t", customSession).Run(); err == nil {
		t.Errorf("custom tmux session %q still alive after Reap", customSession)
	}
}

func TestStartRefusesActive(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(tasksDir, "x"); err == nil {
		t.Fatal("Start on active task should error, got nil")
	}
}

func TestStartRefusesDone(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: done\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(tasksDir, "x"); err == nil {
		t.Fatal("Start on done task should error, got nil")
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

func TestPauseRequiresActive(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Pause(tasksDir, "x"); err == nil {
		t.Fatal("Pause on draft task should error, got nil")
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

func TestPauseRefusesUnreadInbox(t *testing.T) {
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

	err := Pause(tasksDir, "x")
	if err == nil {
		t.Fatal("Pause should refuse with unread inbox, got nil")
	}
	if !strings.Contains(err.Error(), "unread inbox") {
		t.Errorf("error %q should mention 'unread inbox'", err)
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

	err := Block(tasksDir, "x")
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

func TestBlockFlipsActiveToBlocked(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Block(tasksDir, "x"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if status := readStatus(t, taskPath); status != "blocked" {
		t.Errorf("after Block: status = %q, want blocked", status)
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
