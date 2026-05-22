package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunLaunchDryRun shims bwrap onto PATH, points worktree+home at
// temp dirs, and drives runLaunch through `-shell -dry-run`. The
// happy path: preparePolicy resolves, bwrapArgs renders, and
// buildLaunchCmd prints the assembled launch line to stdout.
func TestRunLaunchDryRun(t *testing.T) {
	shimDir := t.TempDir()
	bwrapPath := filepath.Join(shimDir, "bwrap")
	if err := os.WriteFile(bwrapPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bwrap shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	wt := t.TempDir()
	home := t.TempDir()

	// runLaunch prints to os.Stdout; pipe it to a buffer so the
	// assertion doesn't bleed into go test output.
	origOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() {
		os.Stdout = origOut
	}()

	runLaunch([]string{
		"-dry-run",
		"-shell",
		"-worktree", wt,
		"-home", home,
	})

	_ = w.Close()
	out := <-done

	if !strings.Contains(out, bwrapPath) {
		t.Errorf("launch cmd does not reference bwrap shim path %q:\n%s", bwrapPath, out)
	}
	if !strings.Contains(out, "--bind "+wt+" "+wt) {
		t.Errorf("launch cmd missing worktree bind for %q:\n%s", wt, out)
	}
	if !strings.Contains(out, "--tmpfs "+home) {
		t.Errorf("launch cmd missing home tmpfs for %q:\n%s", home, out)
	}
	if !strings.Contains(out, "--unshare-net") {
		t.Errorf("launch cmd missing --unshare-net (no-allow default):\n%s", out)
	}
	if !strings.Contains(out, " bash") {
		t.Errorf("launch cmd missing bash terminator (shell mode):\n%s", out)
	}
}
