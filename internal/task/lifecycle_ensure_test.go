package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsurePreservesActiveAndSpawnsSession covers the wt-go
// reconciler path: an already-active task with no live tmux session
// must respawn the session without flipping frontmatter (Start would
// refuse "already active"). Pre-respawn status was "active"; after
// Ensure it is still "active" and the tmux session exists.
func TestEnsurePreservesActiveAndSpawnsSession(t *testing.T) {
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
	slug := "active-no-tmux"
	taskPath := filepath.Join(tasksDir, slug+".md")
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: T\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Ensure(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after Ensure: status = %q, want active (status-preserving)", status)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", session).Run(); err != nil {
		t.Errorf("tmux has-session %q: %v", session, err)
	}

	again, err := Ensure(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Ensure (second): %v", err)
	}
	if again != session {
		t.Errorf("second Ensure returned %q, want %q", again, session)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after second Ensure: status = %q, want active", status)
	}
}

// TestEnsureRefusesDone locks in that a done task cannot be revived
// by Ensure; the reconciler must not respawn a worker the operator
// has already shipped.
func TestEnsureRefusesDone(t *testing.T) {
	tasksDir := t.TempDir()
	slug := "x"
	body := "---\nstatus: done\nslug: " + slug + "\ntitle: X\n---\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(tasksDir, slug, nil); err == nil {
		t.Fatal("Ensure on done task: want error, got nil")
	}
}

// TestEnsureHonorsSessionFrontmatter covers the wt-go session-naming
// contract: when a task carries `session:` in frontmatter (set by an
// external spawner like wt-go), Ensure must respawn under that exact
// name rather than the kernel's wt-style default. Without this the
// reconciler creates a second session and the operator's pinned name
// drifts.
func TestEnsureHonorsSessionFrontmatter(t *testing.T) {
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
	slug := "pinned"
	pinned := "wt-go-pinned-" + filepath.Base(t.TempDir())
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: P\nsession: " + pinned + "\n---\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Ensure(tasksDir, slug, nil)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if session != pinned {
		t.Errorf("Ensure returned %q, want %q (frontmatter session: must win)", session, pinned)
	}
	if err := exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", pinned).Run(); err != nil {
		t.Errorf("tmux has-session %q: %v", pinned, err)
	}
}

// TestEnsurePropagatesExtraEnv covers the wt-task env passthrough
// contract: --env KEY=VAL lands on tmux new-session as -e KEY=VAL,
// so the worker sees the var in its process environment. Asserted
// via `tmux show-environment -t <session> KEY`.
func TestEnsurePropagatesExtraEnv(t *testing.T) {
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
	slug := "env-prop"
	body := "---\nstatus: active\nslug: " + slug + "\ntitle: E\n---\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	session, err := Ensure(tasksDir, slug, []string{"WT_PROJECT=nix-config", "WT_CLAUDE_EFFORT=high"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	for _, want := range []struct{ key, val string }{
		{"WT_PROJECT", "nix-config"},
		{"WT_CLAUDE_EFFORT", "high"},
	} {
		out, err := exec.Command("tmux", "-L", testTmuxSocket, "show-environment", "-t", session, want.key).Output()
		if err != nil {
			t.Errorf("tmux show-environment %s: %v", want.key, err)
			continue
		}
		got := strings.TrimSpace(string(out))
		if got != want.key+"="+want.val {
			t.Errorf("session env: got %q, want %s=%s", got, want.key, want.val)
		}
	}
}
