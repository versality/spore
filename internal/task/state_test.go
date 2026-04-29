package task

import (
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
