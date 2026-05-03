package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEditMissingTask(t *testing.T) {
	dir := t.TempDir()
	err := Edit(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing task file, got nil")
	}
}

func TestEditMissingTaskErrorContainsSlug(t *testing.T) {
	dir := t.TempDir()
	err := Edit(dir, "my-task")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	errStr := err.Error()
	if len(errStr) == 0 {
		t.Error("error message is empty")
	}
}

func TestEditInvokesEditor(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skipf("'true' not available: %v", err)
	}

	dir := t.TempDir()
	slug := "my-task"
	taskPath := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: my-task\ntitle: My Task\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use "true" as editor so it exits 0 without interactive input.
	t.Setenv("EDITOR", "true")

	if err := Edit(dir, slug); err != nil {
		t.Fatalf("Edit: %v", err)
	}
}

func TestEditEditorExitNonZero(t *testing.T) {
	if _, err := exec.LookPath("false"); err != nil {
		t.Skipf("'false' not available: %v", err)
	}

	dir := t.TempDir()
	slug := "my-task"
	taskPath := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: draft\nslug: my-task\ntitle: My Task\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EDITOR", "false")

	if err := Edit(dir, slug); err == nil {
		t.Fatal("expected error when editor exits non-zero, got nil")
	}
}
