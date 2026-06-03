package main

import (
	"os"
	"os/exec"
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

func TestRunTaskBlockWritesBlockerAndUnblockClears(t *testing.T) {
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
	if err := runTaskBlock([]string{"demo", "--blocker", "scheduler:foo"}); err != nil {
		t.Fatalf("block: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "status: blocked") || !strings.Contains(string(raw), "blocker: scheduler:foo") {
		t.Errorf("after block, file lacks expected lines:\n%s", raw)
	}
	if err := runTaskUnblock([]string{"demo"}); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "status: active") || strings.Contains(string(raw), "blocker:") {
		t.Errorf("after unblock, file unexpected:\n%s", raw)
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

func TestResolveTasksDirFromLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	root := gitInitProject(t)
	if err := os.Mkdir(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(root, ".worktrees", "demo")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "-b", "wt/demo", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}

	t.Chdir(worktree)
	got := resolveTasksDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realRoot, "tasks")
	if got != want {
		t.Fatalf("resolveTasksDir from worktree = %q, want main tasks %q", got, want)
	}
}
