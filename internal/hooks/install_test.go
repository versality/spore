package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func TestInstall_WritesHooksAndConfigsCoreHooksPath(t *testing.T) {
	root := newGitRepo(t)
	dir, err := Install(root, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, name := range []string{"commit-msg", "pre-commit"} {
		hookPath := filepath.Join(dir, name)
		st, err := os.Stat(hookPath)
		if err != nil {
			t.Fatalf("stat hook %s: %v", name, err)
		}
		if st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("hook %s not executable: %s", name, st.Mode())
		}
		body, err := os.ReadFile(hookPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(body), "#!") {
			t.Fatalf("hook %s body missing shebang: %q", name, string(body))
		}
	}

	out, err := exec.Command("git", "-C", root, "config", "core.hooksPath").Output()
	if err != nil {
		t.Fatalf("git config: %v", err)
	}
	got := strings.TrimSpace(string(out))
	if got != dir {
		t.Fatalf("core.hooksPath: got %q want %q", got, dir)
	}
}

func TestInstall_RepeatConvergesNotAccumulates(t *testing.T) {
	root := newGitRepo(t)
	dir1, err := Install(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir2, err := Install(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dir1 != dir2 {
		t.Fatalf("repeat install changed dir: %q vs %q", dir1, dir2)
	}
}

func TestInstall_RejectsHookWithoutShebang(t *testing.T) {
	root := newGitRepo(t)
	_, err := Install(root, []GitHook{{Name: "pre-commit", Body: "no shebang here\n"}})
	if err == nil || !strings.Contains(err.Error(), "shebang") {
		t.Fatalf("expected shebang error, got %v", err)
	}
}

func TestCommitMsg_BlocksEmDash(t *testing.T) {
	root := t.TempDir()
	clean := filepath.Join(root, "clean")
	dirty := filepath.Join(root, "dirty")
	if err := os.WriteFile(clean, []byte("plain message\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirty, []byte("with \u2014 here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitMsg(clean); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if err := CommitMsg(dirty); err == nil {
		t.Fatalf("dirty: expected error, got nil")
	}
}

func TestPreCommitChecksStagedGoFiles(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	root := newGitRepo(t)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main(){println(\"x\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "main.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	err := PreCommit(root)
	if err == nil {
		t.Fatal("PreCommit should reject unformatted staged Go")
	}
	if !strings.Contains(err.Error(), "gofmt needed") {
		t.Fatalf("PreCommit error = %q, want gofmt needed", err)
	}
}

func TestPreCommitAllowsFormattedStagedGoFiles(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	root := newGitRepo(t)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"x\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "main.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := PreCommit(root); err != nil {
		t.Fatalf("PreCommit: %v", err)
	}
}

func TestPreCommitChecksStagedBlobNotWorkingTree(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	root := newGitRepo(t)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main(){println(\"x\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "main.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"x\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := PreCommit(root)
	if err == nil {
		t.Fatal("PreCommit should reject unformatted staged blob")
	}
	if !strings.Contains(err.Error(), "main.go") {
		t.Fatalf("PreCommit error = %q, want staged path", err)
	}
}

func TestPreCommitIgnoresUnstagedWorkingTreeFormatting(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	root := newGitRepo(t)
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"x\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "main.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := os.WriteFile(path, []byte("package main\nfunc main(){println(\"x\")}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PreCommit(root); err != nil {
		t.Fatalf("PreCommit: %v", err)
	}
}
