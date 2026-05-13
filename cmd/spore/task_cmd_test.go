package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTaskMergeForceMergeRedRequiresReason(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"flag with no follow-up", []string{"demo", "--force-merge-red"}},
		{"flag followed by empty string", []string{"demo", "--force-merge-red", ""}},
		{"flag with empty equals", []string{"demo", "--force-merge-red="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runTaskMerge(tc.args)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "requires a <reason>") {
				t.Errorf("error = %q should mention requires a <reason>", err)
			}
		})
	}
}

func TestRunTaskMergeUnknownFlag(t *testing.T) {
	err := runTaskMerge([]string{"demo", "--bogus"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("error = %q should mention unknown flag", err)
	}
}

func TestRunTaskMergeMissingSlug(t *testing.T) {
	err := runTaskMerge(nil)
	if err == nil {
		t.Fatal("expected error for missing slug, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error = %q should be a usage hint", err)
	}
}

func TestRunTaskStatusCommandsWarnAndFlip(t *testing.T) {
	cases := []struct {
		name       string
		run        func([]string) error
		wantStatus string
		wantWarn   string
	}{
		{"pause", runTaskPause, "paused", "status: pause is deprecated, use park\n"},
		{"park", runTaskPark, "parked", "status: park is deprecated, use park\n"},
		{"block", runTaskBlock, "blocked", "status: block is deprecated, use park\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if err := os.Mkdir("tasks", 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("tasks", "demo.md")
			if err := os.WriteFile(path, []byte("---\nstatus: active\nslug: demo\ntitle: Demo\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			errOut := captureTaskStderr(t, func() {
				if err := tc.run([]string{"demo"}); err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
			})
			if errOut != tc.wantWarn {
				t.Errorf("stderr = %q, want %q", errOut, tc.wantWarn)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "status: "+tc.wantStatus+"\n") {
				t.Errorf("task status not flipped to %s:\n%s", tc.wantStatus, raw)
			}
		})
	}
}

func TestRunTaskNewPriorityFlag(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := runTaskNew([]string{"--no-edit", "--priority", "high", "Demo Task"}); err != nil {
		t.Fatalf("runTaskNew: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join("tasks", "demo-task.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "priority: high\n") {
		t.Errorf("priority line missing:\n%s", raw)
	}
}

func TestRunTaskNewPriorityDefault(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := runTaskNew([]string{"--no-edit", "Demo Task"}); err != nil {
		t.Fatalf("runTaskNew: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join("tasks", "demo-task.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "priority: medium\n") {
		t.Errorf("default priority not medium:\n%s", raw)
	}
}

func TestRunTaskNewPriorityRejectsBogus(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	err := runTaskNew([]string{"--no-edit", "--priority", "urgent", "Demo"})
	if err == nil {
		t.Fatal("expected error for bogus priority, got nil")
	}
	if !strings.Contains(err.Error(), "critical|high|medium|low") {
		t.Errorf("error %q should list valid values", err)
	}
}

func captureTaskStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
