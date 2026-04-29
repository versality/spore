package lints

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// newTestRepo creates a temp git repo at t.TempDir(), writes the
// given files (path => contents), commits everything, and returns
// the repo root.
func newTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()

	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "commit.gpgsign", "false")

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(files[p]), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "test fixture")
	return root
}

func TestListFiles_FiltersByExt(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"a.go":   "package a\n",
		"b.md":   "doc\n",
		"sub/c.sh": "echo hi\n",
	})
	got, err := listFiles(root, sourceExts)
	if err != nil {
		t.Fatalf("listFiles: %v", err)
	}
	want := []string{"a.go", "sub/c.sh"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}
