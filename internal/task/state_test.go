package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDirXDG(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-spore-test")

	got, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join("/tmp/xdg-spore-test", "spore", filepath.Base(dir))
	if got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
}

func TestStateDirHomeFallback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/tmp/home-spore-test")

	got, err := StateDir()
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	want := filepath.Join("/tmp/home-spore-test", ".local", "state", "spore", filepath.Base(dir))
	if got != want {
		t.Errorf("StateDir = %q, want %q", got, want)
	}
}

func TestStateDirNoHomeNoXDG(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")

	if _, err := StateDir(); err == nil {
		t.Fatal("StateDir: expected error when both HOME and XDG_STATE_HOME are empty, got nil")
	}
}

func TestCountUnreadInbox(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	n, _, err := CountUnreadInbox("foo")
	if err != nil {
		t.Fatalf("CountUnreadInbox (no dir): %v", err)
	}
	if n != 0 {
		t.Errorf("empty inbox = %d, want 0", n)
	}

	inbox, _ := InboxDir("foo")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "2.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o755); err != nil {
		t.Fatal(err)
	}

	n, _, err = CountUnreadInbox("foo")
	if err != nil {
		t.Fatalf("CountUnreadInbox: %v", err)
	}
	if n != 2 {
		t.Errorf("unread = %d, want 2", n)
	}
}

func TestInboxDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-spore-test")

	got, err := InboxDir("foo")
	if err != nil {
		t.Fatalf("InboxDir: %v", err)
	}
	want := filepath.Join("/tmp/xdg-spore-test", "spore", filepath.Base(dir), "foo", "inbox")
	if got != want {
		t.Errorf("InboxDir = %q, want %q", got, want)
	}
}

func TestInboxDirForProjectUsesProjectRootNotWorkerCwd(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	worker := filepath.Join(root, ".worktrees", "alpha")
	if err := os.MkdirAll(worker, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(worker)
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-spore-test")

	got, err := InboxDirForProject(root, "alpha")
	if err != nil {
		t.Fatalf("InboxDirForProject: %v", err)
	}
	want := filepath.Join("/tmp/xdg-spore-test", "spore", "project", "alpha", "inbox")
	if got != want {
		t.Errorf("InboxDirForProject = %q, want %q", got, want)
	}
}

func TestCoordinatorInboxDirForProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-spore-test")
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", "")

	got, err := CoordinatorInboxDirForProject(root)
	if err != nil {
		t.Fatalf("CoordinatorInboxDirForProject: %v", err)
	}
	want := filepath.Join("/tmp/xdg-spore-test", "spore", "coordinator", "project", "inbox")
	if got != want {
		t.Errorf("CoordinatorInboxDirForProject = %q, want %q", got, want)
	}
}
