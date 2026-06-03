package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

// shimBwrap puts a no-op bwrap on PATH and points HOME at an override-
// free dir so maybeSandboxWrap's LookPath + user-config load are
// hermetic (mirrors cmd/spore-sandbox/launch_test.go).
func shimBwrap(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	return home
}

func writeSandboxToml(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMaybeSandboxWrapEnabled(t *testing.T) {
	shimBwrap(t)
	root := t.TempDir()
	writeSandboxToml(t, root, "[sandbox]\nenabled = true\nallow_hosts = [\"api.anthropic.com\"]\n")
	wt := filepath.Join(root, ".worktrees", "slug")

	got, err := maybeSandboxWrap(root, wt, frontmatter.Meta{Agent: "claude"}, "claude --dangerously-skip-permissions")
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	for _, want := range []string{
		"spore-sandbox --exec",
		"-worktree " + wt,
		"-rw " + filepath.Join(root, ".git"),
		"-target claude",
		"-- claude --dangerously-skip-permissions",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped cmd missing %q:\n%s", want, got)
		}
	}
}

func TestMaybeSandboxWrapSkips(t *testing.T) {
	shimBwrap(t)
	const bare = "claude --dangerously-skip-permissions"

	disabled := t.TempDir() // no spore.toml -> sandbox off
	if got, err := maybeSandboxWrap(disabled, disabled, frontmatter.Meta{Agent: "claude"}, bare); err != nil || got != bare {
		t.Fatalf("disabled project should pass through: got %q err %v", got, err)
	}

	enabled := t.TempDir()
	writeSandboxToml(t, enabled, "[sandbox]\nenabled = true\n")

	// per-task opt-out
	optOut := frontmatter.Meta{Agent: "claude", Extra: map[string]string{"sandbox": "false"}}
	if got, err := maybeSandboxWrap(enabled, enabled, optOut, bare); err != nil || got != bare {
		t.Fatalf("sandbox:false should pass through: got %q err %v", got, err)
	}

	// custom agent has no registered target
	custom := frontmatter.Meta{Agent: "mybin"}
	if got, err := maybeSandboxWrap(enabled, enabled, custom, "mybin"); err != nil || got != "mybin" {
		t.Fatalf("custom agent should pass through: got %q err %v", got, err)
	}
}

func TestMaybeSandboxWrapEnabledMissingBwrap(t *testing.T) {
	// Empty PATH so bwrap cannot be found; enabled project must error
	// rather than silently run unsandboxed.
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	writeSandboxToml(t, root, "[sandbox]\nenabled = true\n")
	if _, err := maybeSandboxWrap(root, root, frontmatter.Meta{Agent: "claude"}, "claude"); err == nil {
		t.Fatal("expected error when sandbox enabled but bwrap absent")
	}
}
