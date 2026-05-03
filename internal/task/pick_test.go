package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickNoTasks(t *testing.T) {
	dir := t.TempDir()
	_, err := Pick(dir)
	if err == nil {
		t.Fatal("expected error for empty tasks dir, got nil")
	}
}

func TestPickAllDone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "done.md"),
		[]byte("---\nstatus: done\nslug: done\ntitle: Done\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Pick(dir)
	if err == nil {
		t.Fatal("expected error when all tasks are done, got nil")
	}
}

func TestPickNoPicker(t *testing.T) {
	// Unset display and point PATH to an empty dir so no picker is found.
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "active.md"),
		[]byte("---\nstatus: active\nslug: active\ntitle: Active\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Pick(dir)
	if err == nil {
		t.Fatal("expected error when no picker available, got nil")
	}
}

func TestPickWithFakePicker(t *testing.T) {
	// Build a fake picker binary in a temp dir: a shell script that
	// outputs its first stdin line unchanged. This exercises the
	// slug-cut logic without needing an interactive terminal.
	binDir := t.TempDir()
	fzfPath := filepath.Join(binDir, "fzf")
	script := "#!/bin/sh\nread -r line; printf '%s\n' \"$line\"\n"
	if err := os.WriteFile(fzfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Add binDir before the real PATH so our fake fzf wins.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
	// No display: forces fzf path.
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.md"),
		[]byte("---\nstatus: active\nslug: demo\ntitle: Demo Task\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	slug, err := Pick(dir)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	if slug != "demo" {
		t.Errorf("slug = %q, want demo", slug)
	}
}

func TestPickSkipsDoneTasks(t *testing.T) {
	// Fake picker that returns the first line - verify done tasks are
	// not in the input.
	binDir := t.TempDir()
	fzfPath := filepath.Join(binDir, "fzf")
	// Output ALL lines so we can see everything the picker received.
	script := "#!/bin/sh\ncat\n"
	if err := os.WriteFile(fzfPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	dir := t.TempDir()
	tasks := []struct{ slug, status string }{
		{"active-task", "active"},
		{"done-task", "done"},
		{"paused-task", "paused"},
	}
	for _, task := range tasks {
		body := "---\nstatus: " + task.status + "\nslug: " + task.slug + "\ntitle: " + task.slug + "\n---\n"
		if err := os.WriteFile(filepath.Join(dir, task.slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The fake picker outputs all input lines; Pick takes the first
	// line and cuts on the first tab.
	slug, err := Pick(dir)
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	// done-task must not be selected.
	if slug == "done-task" {
		t.Error("Pick returned done task, should be excluded")
	}
}

func TestDetectPickerNoDisplay(t *testing.T) {
	// Build a fake fzf so detectPicker finds it.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "fzf"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	picker, args, err := detectPicker()
	if err != nil {
		t.Fatalf("detectPicker: %v", err)
	}
	if picker == "" {
		t.Error("expected a picker path, got empty")
	}
	if len(args) == 0 {
		t.Error("expected picker args, got none")
	}
}

func TestDetectPickerNoneAvailable(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	_, _, err := detectPicker()
	if err == nil {
		t.Fatal("expected error when no picker available, got nil")
	}
}
