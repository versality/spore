package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/fleet"
)

// captureFn runs fn with stdout/stderr piped to in-memory buffers and
// returns the exit code plus captured streams. Mirrors captureRoleBrief
// but accepts an arbitrary closure so each lifecycle subcommand can use
// the same shape.
func captureFn(t *testing.T, fn func() int) (code int, stdout, stderr string) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	done := make(chan [2]string, 1)
	go func() {
		var ob, eb bytes.Buffer
		_, _ = io.Copy(&ob, outR)
		_, _ = io.Copy(&eb, errR)
		done <- [2]string{ob.String(), eb.String()}
	}()

	code = fn()
	outW.Close()
	errW.Close()
	got := <-done
	return code, got[0], got[1]
}

func requireToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
}

// gitInitProject creates a project dir with a unique basename derived
// from the test name, runs `git init -b main` plus an empty commit so
// task helpers that resolve git toplevel succeed.
func gitInitProject(t *testing.T) string {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return root
}

// chdirToRoot temporarily switches the test process into root so the
// CLI handlers (which use os.Getwd) operate on the test project.
func chdirToRoot(t *testing.T, root string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir %s: %v", root, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestCoordinatorStartStopStatus(t *testing.T) {
	requireToolchain(t)

	root := gitInitProject(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")
	chdirToRoot(t, root)

	session := fleet.CoordinatorSessionName(root)
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	code, out, errOut := captureFn(t, func() int { return runCoordinatorStatus(nil) })
	if code != 3 {
		t.Fatalf("status before start: exit=%d (want 3 for down); stdout=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "down") {
		t.Errorf("status stdout missing down: %q", out)
	}

	code, out, errOut = captureFn(t, func() int { return runCoordinatorStart(nil) })
	if code != 0 {
		t.Fatalf("start: exit=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "spawned") || !strings.Contains(out, session) {
		t.Errorf("start stdout missing spawn line: %q", out)
	}
	if !fleet.CoordinatorAlive(root) {
		t.Fatalf("expected session %q alive after start", session)
	}

	code, out, errOut = captureFn(t, func() int { return runCoordinatorStart(nil) })
	if code != 0 {
		t.Fatalf("start (idempotent): exit=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "already running") {
		t.Errorf("idempotent start stdout missing 'already running': %q", out)
	}

	code, out, errOut = captureFn(t, func() int { return runCoordinatorStatus(nil) })
	if code != 0 {
		t.Fatalf("status while up: exit=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "up") {
		t.Errorf("status stdout missing up: %q", out)
	}

	code, out, errOut = captureFn(t, func() int { return runCoordinatorStop(nil) })
	if code != 0 {
		t.Fatalf("stop: exit=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "killed") {
		t.Errorf("stop stdout missing 'killed': %q", out)
	}
	if fleet.CoordinatorAlive(root) {
		t.Errorf("session %q still alive after stop", session)
	}

	code, out, _ = captureFn(t, func() int { return runCoordinatorStop(nil) })
	if code != 0 {
		t.Fatalf("stop (idempotent): exit=%d", code)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("idempotent stop stdout missing 'not running': %q", out)
	}
}

// TestCoordinatorStartReportsDeadAgent guards the post-spawn settle
// check in EnsureCoordinator: when the agent binary fails to exec,
// tmux still registers the session momentarily, then tears it down.
// The CLI must surface that as a non-zero exit instead of printing
// "spawned" while no session exists.
func TestCoordinatorStartReportsDeadAgent(t *testing.T) {
	requireToolchain(t)

	root := gitInitProject(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SPORE_COORDINATOR_AGENT", "spore-no-such-binary-zzz")
	chdirToRoot(t, root)

	session := fleet.CoordinatorSessionName(root)
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	code, out, errOut := captureFn(t, func() int { return runCoordinatorStart(nil) })
	if code == 0 {
		t.Fatalf("start with missing agent: exit=0 (want non-zero); stdout=%q stderr=%q", out, errOut)
	}
	if !strings.Contains(errOut, "died on spawn") {
		t.Errorf("stderr missing 'died on spawn': %q", errOut)
	}
	if fleet.CoordinatorAlive(root) {
		t.Errorf("session %q must not be alive when agent failed to exec", session)
	}
}

func TestCoordinatorStatusShowsTOML(t *testing.T) {
	requireToolchain(t)

	root := gitInitProject(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	chdirToRoot(t, root)

	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[coordinator]\ndriver = \"claude\"\nmodel = \"opus\"\nbrief = \"docs/helm.md\"\n"),
		0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := captureFn(t, func() int { return runCoordinatorStatus(nil) })
	if code != 3 {
		t.Fatalf("expected 3 (down), got %d; stdout=%q", code, out)
	}
	for _, want := range []string{"driver: claude", "model:  opus", "brief:  docs/helm.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}
