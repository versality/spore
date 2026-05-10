package spawn

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTmux records every Run/Output and lets the test drive WaitFor
// completion. liveSessions toggles HasSession behaviour; Output
// returns whatever the test queues for `list-sessions`.
type fakeTmux struct {
	mu sync.Mutex

	calls         [][]string
	listSessions  string
	hasSessions   map[string]bool
	waitDone      chan struct{}
	waitStarted   chan struct{}
	newSessionErr error
}

func newFakeTmux() *fakeTmux {
	return &fakeTmux{
		hasSessions: map[string]bool{},
		waitDone:    make(chan struct{}),
		waitStarted: make(chan struct{}, 1),
	}
}

func (f *fakeTmux) Run(args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) >= 3 && args[0] == "has-session" && args[1] == "-t" {
		if f.hasSessions[args[2]] {
			return nil
		}
		return errors.New("no session")
	}
	if len(args) >= 1 && args[0] == "new-session" {
		if f.newSessionErr != nil {
			return f.newSessionErr
		}
		// Mark session live for subsequent has-session.
		for i, a := range args {
			if a == "-s" && i+1 < len(args) {
				f.hasSessions[args[i+1]] = true
			}
		}
	}
	if len(args) >= 1 && args[0] == "kill-session" {
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				delete(f.hasSessions, args[i+1])
			}
		}
	}
	return nil
}

func (f *fakeTmux) Output(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) >= 1 && args[0] == "list-sessions" {
		return f.listSessions, nil
	}
	return "", nil
}

func (f *fakeTmux) WaitFor(ctx context.Context, channel string) error {
	select {
	case f.waitStarted <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil
	case <-f.waitDone:
		return nil
	}
}

func (f *fakeTmux) callsCopy() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = append([]string(nil), c...)
	}
	return out
}

func (f *fakeTmux) ranArgs(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if len(c) < len(prefix) {
			continue
		}
		ok := true
		for i, want := range prefix {
			if c[i] != want {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func baseCfg(t *testing.T, ft *fakeTmux, projectsBody string) Config {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	briefPath := filepath.Join(dir, "brief.md")
	projectsPath := filepath.Join(dir, "projects")
	projectPath := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	writeFile(t, briefPath, "brief")
	body := projectsBody
	if body == "" {
		body = projectPath + "\n"
	}
	writeFile(t, projectsPath, body)
	cfg := Config{
		StateDir:     stateDir,
		Brief:        briefPath,
		ProjectsFile: projectsPath,
		Tmux:         ft,
		TierLookup:   func() (string, error) { return "max", nil },
		Stderr:       io.Discard,
	}
	return cfg
}

func TestRunPreflightMissingBrief(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.Brief = filepath.Join(t.TempDir(), "missing.md")
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "brief missing") {
		t.Fatalf("got %v", err)
	}
}

func TestRunPreflightNoProject(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "# only comments\n\n")
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "no usable project") {
		t.Fatalf("got %v", err)
	}
}

func TestRunPreflightTierBlock(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.TierLookup = func() (string, error) { return "pro", nil }
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "requires max") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "tier=pro") {
		t.Fatalf("expected tier=pro in error, got %v", err)
	}
}

func TestRunPreflightTierUnknownOnLookupErr(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.TierLookup = func() (string, error) { return "", errors.New("boom") }
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "tier=unknown") {
		t.Fatalf("got %v", err)
	}
}

func TestRunPreflightCodexEffortMustBeHigh(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "codex"
	cfg.CodexEffort = "medium"
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "must be 'high'") {
		t.Fatalf("got %v", err)
	}
}

func TestRunPreflightUnknownDriver(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "bogus"
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown skyhelm driver") {
		t.Fatalf("got %v", err)
	}
}

func TestRunSpawnsWhenNoSessionAlive(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "claude"

	go func() {
		<-ft.waitStarted
		// Simulate session-closed firing the channel.
		close(ft.waitDone)
	}()

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !ft.ranArgs("new-session", "-d", "-s", "skyhelm_claude_high") {
		t.Fatalf("missing new-session call. calls=%v", ft.callsCopy())
	}
	// Hook installed and unwound on clean exit.
	if !ft.ranArgs("set-hook", "-g", "session-closed[99]") {
		t.Fatalf("missing set-hook -g. calls=%v", ft.callsCopy())
	}
	if !ft.ranArgs("set-hook", "-gu", "session-closed[99]") {
		t.Fatalf("missing set-hook -gu. calls=%v", ft.callsCopy())
	}
	// Session marker persisted.
	body, err := os.ReadFile(filepath.Join(cfg.StateDir, "session"))
	if err != nil {
		t.Fatalf("read session marker: %v", err)
	}
	if strings.TrimSpace(string(body)) != "skyhelm_claude_high" {
		t.Fatalf("session marker: %q", body)
	}
	// Inbox dirs created under state/<projectname>/inbox.
	calls := ft.callsCopy()
	var spawned []string
	for _, c := range calls {
		if len(c) >= 1 && c[0] == "new-session" {
			spawned = c
		}
	}
	wantEnv := []string{
		"SKYBOT_INBOX=",
		"SPORE_TASK_INBOX=",
		"WT_PROJECT=",
		"SKYHELM_DRIVER=claude",
		"SKYHELM_SESSION=skyhelm_claude_high",
		"SKYHELM_CODEX_EFFORT=high",
		"SKYHELM_REPO_ROOT=",
		"SPORE_COORDINATOR_STATE_DIR=",
	}
	for _, want := range wantEnv {
		found := false
		for _, a := range spawned {
			if strings.HasPrefix(a, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("new-session missing env prefix %q in %v", want, spawned)
		}
	}
}

func TestRunCodexDriverNeedsHighEffort(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "codex"
	cfg.CodexEffort = "very-high"
	err := Run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "must be 'high'") {
		t.Fatalf("got %v", err)
	}
}

func TestRunWaitsWhenSessionAlreadyAlive(t *testing.T) {
	ft := newFakeTmux()
	ft.hasSessions["skyhelm_claude_high"] = true
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "claude"

	go func() {
		<-ft.waitStarted
		close(ft.waitDone)
	}()

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ft.ranArgs("new-session") {
		t.Fatalf("should not double-spawn when alive. calls=%v", ft.callsCopy())
	}
}

func TestRunAdoptsSurvivorSession(t *testing.T) {
	ft := newFakeTmux()
	ft.listSessions = "other\nskyhelm_claude_max\n"
	ft.hasSessions["skyhelm_claude_max"] = true
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "claude"
	cfg.ClaudeEffort = "high" // mismatched on purpose

	go func() {
		<-ft.waitStarted
		close(ft.waitDone)
	}()

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(cfg.StateDir, "session"))
	if strings.TrimSpace(string(body)) != "skyhelm_claude_max" {
		t.Fatalf("session marker: want survivor adoption skyhelm_claude_max, got %q", body)
	}
}

func TestRunSIGTERMKillsSessionAndExitsCleanly(t *testing.T) {
	ft := newFakeTmux()
	cfg := baseCfg(t, ft, "")
	cfg.SkyhelmDriver = "claude"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()

	select {
	case <-ft.waitStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitFor never started")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after SIGTERM: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !ft.ranArgs("kill-session", "-t", "skyhelm_claude_high") {
		t.Fatalf("missing kill-session. calls=%v", ft.callsCopy())
	}
	if !ft.ranArgs("set-hook", "-gu", "session-closed[99]") {
		t.Fatalf("missing hook unwind. calls=%v", ft.callsCopy())
	}
}

func TestSessionNameDriverEffort(t *testing.T) {
	cases := []struct {
		driver string
		claude string
		codex  string
		want   string
	}{
		{"claude", "", "", "skyhelm_claude_high"},
		{"claude", "max", "", "skyhelm_claude_max"},
		{"codex", "", "high", "skyhelm_codex_high"},
	}
	for _, c := range cases {
		got := SessionName(c.driver, Config{ClaudeEffort: c.claude, CodexEffort: c.codex})
		if got != c.want {
			t.Errorf("SessionName(%q,..) = %q want %q", c.driver, got, c.want)
		}
	}
}

func TestFirstProjectStripsCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects")
	writeFile(t, path, "# header\n\n   \n  /home/x/foo  # comment\n/home/x/bar\n")
	got, err := firstProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/x/foo" {
		t.Errorf("firstProject = %q", got)
	}
}
