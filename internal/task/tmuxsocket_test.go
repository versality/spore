package task

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// testTmuxSocket is the -L socket name every tmux invocation in this
// package's _test.go files passes. The literal name `default` matches
// the basename tmux would otherwise pick when -L is omitted, so test
// calls reach the same socket file production code (lifecycle.go,
// merge.go) lands on. Per-test-process isolation comes from the
// TMUX_TMPDIR override below: each test process gets its own temp
// dir, so `<tmpdir>/tmux-<UID>/default` is unique to this process and
// invisible to the operator's host tmux server. The lint that
// enforces -L scopes to *_test.go only, so production stays unchanged.
const testTmuxSocket = "default"

func TestMain(m *testing.M) {
	tmpdir, err := os.MkdirTemp("", "spore-tmux-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: mkdtemp:", err)
		os.Exit(2)
	}
	if err := os.Setenv("TMUX_TMPDIR", tmpdir); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: setenv:", err)
		os.Exit(2)
	}
	// Unset TMUX so child tmux invocations do not attach to the
	// operator's running server (which would land sessions on the
	// host's socket regardless of TMUX_TMPDIR).
	_ = os.Unsetenv("TMUX")
	_ = os.Unsetenv("TMUX_PANE")
	// Keepalive session: tmux's default behavior is to exit the
	// server when the last session ends. With a per-process server,
	// each test's cleanup can drop the session count to zero,
	// triggering server shutdown right before the next test spawns
	// its own session. The race surfaces as "server exited
	// unexpectedly". A long-lived dummy keeps the server up for the
	// duration of the test run.
	_ = exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", "keepalive", "sleep 86400").Run()
	code := m.Run()
	_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-server").Run()
	_ = os.RemoveAll(tmpdir)
	os.Exit(code)
}
