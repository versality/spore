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
	hookPath := filepath.Join(dir, "commit-msg")
	st, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("hook not executable: %s", st.Mode())
	}
	body, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "#!") {
		t.Fatalf("hook body missing shebang: %q", string(body))
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
