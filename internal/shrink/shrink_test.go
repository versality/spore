package shrink

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Two harness/*.sh, one nested.
	mustWrite(t, filepath.Join(root, "harness", "a.sh"), "echo a\necho b\n")
	mustWrite(t, filepath.Join(root, "harness", "sub", "b.sh"), "echo c\necho d\necho e\n")
	// A non-sh file in harness/ that should not count.
	mustWrite(t, filepath.Join(root, "harness", "README.md"), "ignore me\n")
	// One wt-go .go file.
	mustWrite(t, filepath.Join(root, "nix", "packages", "wt-go", "main.go"), "package main\n\nfunc main() {}\n")
	// Hook configs: one command in settings, two in hooks-config, none in extras.
	mustWrite(t, filepath.Join(root, "configs", "claude", "settings.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"command":"/run/x"}]}]}}`)
	mustWrite(t, filepath.Join(root, "configs", "claude", "settings-extras.json"), `{"effortLevel":"medium"}`)
	mustWrite(t, filepath.Join(root, "configs", "claude", "hooks-config.json"),
		`{"events":{"Stop":[{"hooks":[{"command":"/run/y"},{"command":"/run/z"}]}]}}`)
	return root
}

func TestProbeMatchesFakeRepo(t *testing.T) {
	root := fakeRepo(t)
	wtState := t.TempDir()
	mustWrite(t, filepath.Join(wtState, "events.jsonl"), "")
	mustWrite(t, filepath.Join(wtState, "rower-wrap-count", "slug-a"), "1\n")

	frozen := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	snap, err := Probe(Options{
		RepoRoot:   root,
		WtStateDir: wtState,
		Now:        func() time.Time { return frozen },
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if snap.Repo != root {
		t.Errorf("Repo = %q, want %q", snap.Repo, root)
	}
	if snap.BashFiles != 2 {
		t.Errorf("BashFiles = %d, want 2", snap.BashFiles)
	}
	if snap.BashLoc != 5 {
		t.Errorf("BashLoc = %d, want 5", snap.BashLoc)
	}
	if snap.WtgoLoc != 3 {
		t.Errorf("WtgoLoc = %d, want 3", snap.WtgoLoc)
	}
	if snap.WtStateFiles != 2 {
		t.Errorf("WtStateFiles = %d, want 2", snap.WtStateFiles)
	}
	if snap.HookCount != 3 {
		t.Errorf("HookCount = %d, want 3", snap.HookCount)
	}
	if !snap.Ts.Equal(frozen) {
		t.Errorf("Ts = %s, want %s", snap.Ts, frozen)
	}
}

func TestProbeOptionalPathsMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "harness", "a.sh"), "echo a\n")
	snap, err := Probe(Options{
		RepoRoot:   root,
		WtStateDir: filepath.Join(root, "does-not-exist"),
	})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if snap.BashFiles != 1 || snap.BashLoc != 1 {
		t.Errorf("bash mismatch: %+v", snap)
	}
	if snap.WtgoLoc != 0 {
		t.Errorf("WtgoLoc = %d, want 0", snap.WtgoLoc)
	}
	if snap.WtStateFiles != 0 {
		t.Errorf("WtStateFiles = %d, want 0", snap.WtStateFiles)
	}
	if snap.HookCount != 0 {
		t.Errorf("HookCount = %d, want 0", snap.HookCount)
	}
}

func TestProbeMissingHarnessIsError(t *testing.T) {
	root := t.TempDir()
	if _, err := Probe(Options{RepoRoot: root}); err == nil {
		t.Fatal("want error when harness/ is missing")
	}
}

func TestProbeRequiresRepoRoot(t *testing.T) {
	if _, err := Probe(Options{}); err == nil {
		t.Fatal("want error when RepoRoot is empty")
	}
}

func TestLineCountNoTrailingNewline(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "f.sh")
	mustWrite(t, tmp, "a\nb")
	n, err := lineCount(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("lineCount = %d, want 2", n)
	}
}

func TestCountHooksHandlesMissingFiles(t *testing.T) {
	root := t.TempDir()
	n, err := countHooks(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("countHooks empty repo = %d, want 0", n)
	}
}
