package tokenmonitor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain isolates this package's tmux server inside a per-binary
// TMUX_TMPDIR so the driver-kill integration test cannot reach (or
// be reached by) the operator's interactive tmux sessions.
func TestMain(m *testing.M) {
	tmpdir, err := os.MkdirTemp("", "spore-tokenmonitor-tmux-")
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
	_ = exec.Command("tmux", "-L", testTmuxSocket, "new-session", "-d", "-s", "keepalive", "sleep 86400").Run()
	code := m.Run()
	_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-server").Run()
	_ = os.RemoveAll(tmpdir)
	os.Exit(code)
}

const testTmuxSocket = "default"

// TestDriverOnlyKillLeavesPeersIntact spins up three remain-on-exit
// tmux sessions, runs the wrap-fire driver-kill against one of them,
// and asserts the target session survives with a dead pane and intact
// scrollback while the peers stay alive and untouched. Pins the
// blast-radius guarantee the wrap-restart path must preserve: a single
// over-threshold worker tears down its own driver, never the fleet.
func TestDriverOnlyKillLeavesPeersIntact(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	if _, err := exec.LookPath("pkill"); err != nil {
		t.Skipf("pkill not available: %v", err)
	}

	sessions := []string{"rover-a", "rover-b", "rover-c"}
	const marker = "ROVER-SCROLLBACK-MARKER"
	for _, s := range sessions {
		mustTmux(t, "new-session", "-d", "-s", s, "-n", "agent",
			"sh", "-c", fmt.Sprintf("echo %s; sleep 600", marker))
		mustTmux(t, "set-option", "-t", s, "remain-on-exit", "on")
		t.Cleanup(func() {
			_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", s).Run()
		})
	}
	// Wait for the panes to settle and the marker line to land in the
	// scrollback. tmux new-session returns before the shell has
	// printed anything; capturing immediately would race the echo.
	waitForPaneText(t, sessions[0], marker)
	waitForPaneText(t, sessions[1], marker)
	waitForPaneText(t, sessions[2], marker)

	target := sessions[0]

	// Resolve the target pane's tty the same way DriverKillCommand
	// does when the agent runs it from inside its own pane, then
	// signal every process on that tty. This is the production kill
	// path; running it from outside the pane just makes the test
	// deterministic about who pulls the trigger.
	ttyOut, err := tmuxOutput("display-message", "-p", "-t", target, "#{pane_tty}")
	if err != nil {
		t.Fatalf("display-message pane_tty: %v", err)
	}
	tty := strings.TrimPrefix(strings.TrimSpace(ttyOut), "/dev/")
	if tty == "" {
		t.Fatalf("empty pane_tty for %s", target)
	}
	if err := exec.Command("pkill", "-TERM", "-t", tty).Run(); err != nil {
		// pkill returns 1 when nothing matched; the pane shell has
		// already exited in that case, which is the outcome we want
		// anyway. Anything else is a test setup bug worth surfacing.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("pkill: %v", err)
		}
	}

	// Wait for the pane to flip to dead. remain-on-exit holds the
	// pane on screen so #{pane_dead} stays observable instead of the
	// session vanishing.
	if !waitForPaneDead(t, target) {
		t.Fatalf("target pane never went dead for %s", target)
	}

	if !tmuxHasSession(target) {
		t.Errorf("target session %s was destroyed; driver kill must preserve the session", target)
	}
	for _, peer := range sessions[1:] {
		if !tmuxHasSession(peer) {
			t.Errorf("peer session %s was destroyed; driver kill must not touch peers", peer)
		}
		if paneDead(t, peer) {
			t.Errorf("peer session %s pane went dead; driver kill must not signal peer processes", peer)
		}
	}

	// Scrollback survives: the marker the shell printed before the
	// kill is still in the pane's capture buffer.
	capture, err := tmuxOutput("capture-pane", "-p", "-S", "-1000", "-t", target+":agent")
	if err != nil {
		t.Fatalf("capture-pane after kill: %v", err)
	}
	if !strings.Contains(capture, marker) {
		t.Errorf("target pane lost its scrollback after driver kill; capture:\n%s", capture)
	}
}

func mustTmux(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("tmux", "-L", testTmuxSocket)
	cmd.Args = append(cmd.Args, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
}

func tmuxOutput(args ...string) (string, error) {
	cmd := exec.Command("tmux", "-L", testTmuxSocket)
	cmd.Args = append(cmd.Args, args...)
	out, err := cmd.Output()
	return string(out), err
}

func tmuxHasSession(name string) bool {
	return exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", name).Run() == nil
}

func paneDead(t *testing.T, session string) bool {
	t.Helper()
	out, err := tmuxOutput("display-message", "-p", "-t", session, "#{pane_dead}")
	if err != nil {
		t.Fatalf("display-message pane_dead: %v", err)
	}
	return strings.TrimSpace(out) == "1"
}

func waitForPaneText(t *testing.T, session, needle string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := tmuxOutput("capture-pane", "-p", "-t", session+":agent")
		if err == nil && strings.Contains(out, needle) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %s never showed %q", session, needle)
}

func waitForPaneDead(t *testing.T, session string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if paneDead(t, session) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
