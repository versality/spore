package spawn

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/versality/spore/internal/fleet"
)

// safeBuf is a goroutine-safe bytes.Buffer wrapper. Tests read the
// stderr buffer from the test goroutine while Run writes from its
// own; bare bytes.Buffer would race.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

const testTmuxSocket = "default"

func TestMain(m *testing.M) {
	tmpdir, err := os.MkdirTemp("", "spore-spawn-tmux-")
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

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
}

func TestResolveDriverPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_PROVIDER", "")
	t.Setenv("SPORE_COORDINATOR_AGENT", "")
	t.Setenv("SPORE_AGENT_BINARY", "")

	if got := ResolveDriver(dir); got != "claude" {
		t.Errorf("default driver = %q, want claude", got)
	}

	t.Setenv("SPORE_AGENT_BINARY", "sh -c 'sleep 30'")
	if got := ResolveDriver(dir); got != "sh" {
		t.Errorf("SPORE_AGENT_BINARY override: got %q, want sh", got)
	}

	t.Setenv("SPORE_COORDINATOR_AGENT", "/nix/store/abc/bin/codex")
	if got := ResolveDriver(dir); got != "codex" {
		t.Errorf("SPORE_COORDINATOR_AGENT: got %q, want codex", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte("[coordinator]\ndriver = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveDriver(dir); got != "claude" {
		t.Errorf("spore.toml driver: got %q, want claude", got)
	}

	t.Setenv("SPORE_COORDINATOR_PROVIDER", "codex")
	if got := ResolveDriver(dir); got != "codex" {
		t.Errorf("SPORE_COORDINATOR_PROVIDER: got %q, want codex", got)
	}
}

func TestEnforceTier(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		tier   string
		lerr   error
		want   string
	}{
		{"claude-max-ok", "claude", "max", nil, ""},
		{"claude-pro-refused", "claude", "pro", nil, "requires max"},
		{"claude-free-refused", "claude", "free", nil, "requires max"},
		{"claude-lookup-fail", "claude", "", errors.New("creds missing"), "tier check failed"},
		{"codex-bypass", "codex", "free", nil, ""},
		{"codex-bypass-error", "codex", "", errors.New("creds missing"), ""},
		{"unknown-bypass", "exotic", "free", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lookup := func() (string, error) { return c.tier, c.lerr }
			err := enforceTier(c.driver, lookup)
			if c.want == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error containing %q, got nil", c.want)
				return
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q missing %q", err.Error(), c.want)
			}
		})
	}
}

func TestWaitForDeathReturnsImmediatelyWhenSessionMissing(t *testing.T) {
	requireTmux(t)
	done := make(chan error, 1)
	go func() { done <- WaitForDeath("spore-spawn-test-no-such-session", 50) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForDeath on missing session: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("WaitForDeath blocked on a missing session")
	}
}

func TestWaitForDeathUnblocksOnKill(t *testing.T) {
	requireTmux(t)
	session := fmt.Sprintf("spore-spawn-death-%d", os.Getpid())
	mustTmux(t, "new-session", "-d", "-s", session, "sleep", "60")
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run() })

	done := make(chan error, 1)
	go func() { done <- WaitForDeath(session, 51) }()
	if !waitForSessionClosedHook(t, 51, 2*time.Second) {
		t.Fatalf("WaitForDeath did not install session-closed[51] hook")
	}
	mustTmux(t, "kill-session", "-t", session)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForDeath returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("WaitForDeath did not unblock after kill-session")
	}
}

func TestRunRefusesNonMaxClaude(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), []byte("[coordinator]\ndriver = \"claude\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf safeBuf
	err := Run(Options{
		ProjectRoot: dir,
		Stderr:      &buf,
		TierLookup:  func() (string, error) { return "pro", nil },
		Signals:     []os.Signal{},
	})
	if err == nil {
		t.Fatal("Run with non-max claude tier returned nil error")
	}
	if !strings.Contains(err.Error(), "requires max") {
		t.Errorf("error %q missing 'requires max'", err.Error())
	}
}

func TestRunSpawnsAdoptsAndReturnsOnSessionDeath(t *testing.T) {
	requireTmux(t)
	dir, cleanup := setupProject(t, "codex")
	defer cleanup()

	var buf safeBuf
	doneCh := make(chan error, 1)
	tierCalled := false
	go func() {
		doneCh <- Run(Options{
			ProjectRoot: dir,
			Stderr:      &buf,
			TierLookup:  func() (string, error) { tierCalled = true; return "", errors.New("must not be called") },
			HookSlot:    52,
			Signals:     []os.Signal{},
		})
	}()
	if !waitForSpawnMarker(&buf, 3*time.Second) {
		t.Fatalf("spawn marker not written: %s", buf.String())
	}
	if tierCalled {
		t.Errorf("tier lookup invoked for codex driver")
	}
	killCoordinator(dir)
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Run: %v (stderr=%s)", err, buf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not return after session death (stderr=%s)", buf.String())
	}
	if !strings.Contains(buf.String(), "spawned") {
		t.Errorf("stderr missing 'spawned' marker: %s", buf.String())
	}
}

func TestRunSignalShutdownKillsSession(t *testing.T) {
	requireTmux(t)
	dir, cleanup := setupProject(t, "codex")
	defer cleanup()

	var buf safeBuf
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- Run(Options{
			ProjectRoot: dir,
			Stderr:      &buf,
			TierLookup:  func() (string, error) { return "max", nil },
			HookSlot:    53,
			Signals:     []os.Signal{syscall.SIGUSR1},
		})
	}()
	if !waitForSpawnMarker(&buf, 3*time.Second) {
		t.Fatalf("spawn marker not written: %s", buf.String())
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatalf("kill self with SIGUSR1: %v", err)
	}
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Run after signal: %v (stderr=%s)", err, buf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not return after signal (stderr=%s)", buf.String())
	}
	if coordinatorAlive(dir) {
		t.Errorf("coordinator session still alive after signal shutdown")
	}
}

func TestRunAdoptsExistingSession(t *testing.T) {
	requireTmux(t)
	dir, cleanup := setupProject(t, "codex")
	defer cleanup()

	if _, _, err := fleet.EnsureCoordinator(dir); err != nil {
		t.Fatalf("pre-spawn EnsureCoordinator: %v", err)
	}
	session := fleet.CoordinatorSessionName(dir)
	created1, err := sessionCreated(session)
	if err != nil {
		t.Fatalf("sessionCreated #1: %v", err)
	}

	var buf safeBuf
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- Run(Options{
			ProjectRoot: dir,
			Stderr:      &buf,
			TierLookup:  func() (string, error) { return "max", nil },
			HookSlot:    54,
			Signals:     []os.Signal{},
		})
	}()
	if !waitForSpawnMarker(&buf, 3*time.Second) {
		t.Fatalf("adopt marker not written: %s", buf.String())
	}
	created2, err := sessionCreated(session)
	if err != nil {
		t.Fatalf("sessionCreated #2: %v", err)
	}
	if created1 != created2 {
		t.Errorf("session respawned (created %q -> %q); expected adoption", created1, created2)
	}
	killCoordinator(dir)
	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not return after adopted-session death")
	}
	if !strings.Contains(buf.String(), "adopted") {
		t.Errorf("stderr missing 'adopted' marker: %s", buf.String())
	}
}

func setupProject(t *testing.T, driver string) (string, func()) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(fmt.Sprintf("[coordinator]\ndriver = %q\n", driver)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SPORE_COORDINATOR_AGENT", "sh -c 'sleep 60'")
	cleanup := func() { killCoordinator(root) }
	return root, cleanup
}

// waitForSessionClosedHook polls `tmux show-hooks -g` until the
// session-closed hook for the given slot is registered. WaitForDeath
// installs that hook synchronously before blocking on tmux wait-for;
// once it shows up we know any subsequent kill-session will trigger
// the hook, not race ahead of it.
func waitForSessionClosedHook(t *testing.T, slot int, timeout time.Duration) bool {
	t.Helper()
	needle := fmt.Sprintf("session-closed[%d]", slot)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("tmux", "-L", testTmuxSocket, "show-hooks", "-g").Output()
		if strings.Contains(string(out), needle) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitForSpawnMarker blocks until the spawn stderr buffer contains the
// "spawned" line, which Run writes only after EnsureCoordinator's
// 150ms post-spawn settle window has cleared. Tests that need to
// externally kill the session must wait on this marker first to avoid
// racing the settle check.
func waitForSpawnMarker(buf *safeBuf, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "[coordinator-spawn] spawned") ||
			strings.Contains(buf.String(), "[coordinator-spawn] adopted") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func coordinatorAlive(projectRoot string) bool {
	session := fleet.CoordinatorSessionName(projectRoot)
	return exec.Command("tmux", "-L", testTmuxSocket, "has-session", "-t", session).Run() == nil
}

func killCoordinator(projectRoot string) error {
	session := fleet.CoordinatorSessionName(projectRoot)
	return exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
}

func sessionCreated(name string) (string, error) {
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "display-message", "-p", "-t", name, "#{session_created}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func mustTmux(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("tmux", "-L", testTmuxSocket)
	cmd.Args = append(cmd.Args, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
}
