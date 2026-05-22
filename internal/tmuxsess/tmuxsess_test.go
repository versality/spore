package tmuxsess

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"testing"
)

// TestMain isolates the tmux server to a per-process socket so the
// tests never touch the operator's live tmux. The pattern mirrors
// internal/task/tmuxsocket_test.go: set TMUX_TMPDIR to a tempdir AND
// unset TMUX / TMUX_PANE so child tmux invocations do not inherit
// the operator's socket path. A keepalive session keeps the server
// from shutting down between tests.
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("tmux"); err != nil {
		os.Exit(m.Run())
	}
	tmpdir, err := os.MkdirTemp("", "spore-tmuxsess-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: mkdtemp:", err)
		os.Exit(2)
	}
	if err := os.Setenv("TMUX_TMPDIR", tmpdir); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: setenv:", err)
		os.Exit(2)
	}
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	_ = exec.Command("tmux", "new-session", "-d", "-s", "keepalive", "sleep 86400").Run()
	code := m.Run()
	_ = exec.Command("tmux", "kill-server").Run()
	_ = os.RemoveAll(tmpdir)
	os.Exit(code)
}

func spawn(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("tmux", "new-session", "-d", "-s", name, "sleep", "60")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spawn %s: %v\n%s", name, err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	})
}

func skipNoTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not in PATH")
	}
}

func TestHas(t *testing.T) {
	skipNoTmux(t)
	if Has("not-a-session-name-xyz") {
		t.Fatalf("Has(missing) should be false")
	}
	spawn(t, "ts-has-alpha")
	if !Has("ts-has-alpha") {
		t.Fatalf("Has(ts-has-alpha) should be true after spawn")
	}
}

func TestList(t *testing.T) {
	skipNoTmux(t)
	spawn(t, "ts-list-alpha")
	spawn(t, "ts-list-beta")
	got, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	have := map[string]bool{}
	for _, n := range got {
		have[n] = true
	}
	want := []string{"ts-list-alpha", "ts-list-beta"}
	sort.Strings(want)
	for _, n := range want {
		if !have[n] {
			t.Fatalf("expected %s in %v", n, got)
		}
	}
}

func TestKill(t *testing.T) {
	skipNoTmux(t)
	spawn(t, "ts-kill-alpha")
	Kill("ts-kill-alpha")
	if Has("ts-kill-alpha") {
		t.Fatalf("Has should be false after Kill")
	}
	Kill("ts-kill-missing-noop")
}

func TestKillErr(t *testing.T) {
	skipNoTmux(t)
	spawn(t, "ts-killerr-alpha")
	if err := KillErr("ts-killerr-alpha"); err != nil {
		t.Fatalf("KillErr: %v", err)
	}
	if err := KillErr("ts-killerr-alpha"); err == nil {
		t.Fatalf("KillErr on missing should error")
	}
}
