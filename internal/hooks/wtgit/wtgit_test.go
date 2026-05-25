package wtgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkingTreeCleanForWorkerAllowsRuntimeTaskStateDiff(t *testing.T) {
	root := testRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "tasks", "demo.md")
	if err := os.WriteFile(task, []byte("---\nstatus: active\nslug: demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "tasks/demo.md")
	run(t, root, "git", "commit", "-q", "-m", "add task")

	if err := os.WriteFile(task, []byte("---\nstatus: active\nslug: demo\nworker-result: ready-to-merge\nworker-state: awaiting-operator\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !WorkingTreeCleanForWorker(root, "demo") {
		t.Fatal("runtime-only worker-state diff should be clean enough")
	}
}

func TestWorkingTreeCleanForWorkerRejectsPayloadTaskDiff(t *testing.T) {
	root := testRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(root, "tasks", "demo.md")
	if err := os.WriteFile(task, []byte("---\nstatus: active\nslug: demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "tasks/demo.md")
	run(t, root, "git", "commit", "-q", "-m", "add task")

	if err := os.WriteFile(task, []byte("---\nstatus: active\nslug: demo\nworker-state: awaiting-operator\n---\nchanged body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if WorkingTreeCleanForWorker(root, "demo") {
		t.Fatal("payload task diff must remain dirty")
	}
}

func TestWorkingTreeCleanForWorkerRejectsOtherDirtyPath(t *testing.T) {
	root := testRepo(t)
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if WorkingTreeCleanForWorker(root, "demo") {
		t.Fatal("non-task dirty path must remain dirty")
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README")
	run(t, root, "git", "commit", "-q", "-m", "seed")
	return root
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v in %s: %v: %s", name, args, dir, err, strings.TrimSpace(string(out)))
	}
}
