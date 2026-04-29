package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

	if err := Done(tasksDir, slug); err != nil {
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

	if err := Done(tasksDir, slug); err != nil {
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

	if err := Done(tasksDir, slug); err != nil {
		t.Fatalf("Done: %v", err)
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
