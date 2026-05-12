package fleet

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeTmux struct {
	sessions map[string]bool
	panesOut string
	captures map[string]string
}

func (f *fakeTmux) listPanes() (string, error) { return f.panesOut, nil }
func (f *fakeTmux) capturePane(target string) (string, error) {
	if f.captures == nil {
		return "", nil
	}
	return f.captures[target], nil
}
func (f *fakeTmux) hasSession(name string) bool { return f.sessions[name] }

type setup struct {
	tmp        string
	state      string
	projects   []string
	projConfig string
}

func newSetup(t *testing.T, projects map[string]map[string]string) setup {
	t.Helper()
	tmp := t.TempDir()
	var projectNames []string
	for projName := range projects {
		projectNames = append(projectNames, projName)
	}
	sort.Strings(projectNames)
	var projectDirs []string
	for _, projName := range projectNames {
		root := filepath.Join(tmp, "p", projName)
		tasksDir := filepath.Join(root, "tasks")
		if err := os.MkdirAll(tasksDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for slug, body := range projects[projName] {
			if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		projectDirs = append(projectDirs, root)
	}
	cfg := filepath.Join(tmp, "cfg")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := filepath.Join(cfg, "projects")
	if err := os.WriteFile(projConfig, []byte(strings.Join(projectDirs, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(tmp, "state")
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("WT_ROWER_WAKE_PENDING_TTL", "")
	return setup{tmp: tmp, state: state, projects: projectDirs, projConfig: projConfig}
}

func newLivenessEnv(s setup, hostname string, tmux tmuxRunner) livenessEnv {
	return livenessEnv{
		hostname:     func() string { return hostname },
		now:          func() time.Time { return time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC) },
		projectsFile: s.projConfig,
		tmuxRunner:   tmux,
	}
}

// taskAgentSession mirrors wt-go's helper with a `slug:` line so
// task.List populates Meta.Slug.
func taskAgentSession(slug, status, host, agent, session string) string {
	body := "---\nslug: " + slug + "\nstatus: " + status + "\n"
	if host != "" {
		body += "host: " + host + "\n"
	}
	if agent != "" {
		body += "agent: " + agent + "\n"
	}
	if session != "" {
		body += "session: " + session + "\n"
	}
	body += "---\nbody\n"
	return body
}

// claudePaneCapture builds a captured-pane string that mimics the
// Claude Code TUI: optional `body`, the input box separators wrapping
// `prompt`, then the mode bar.
func claudePaneCapture(body, prompt string) string {
	separator := strings.Repeat("─", 60)
	mode := "  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)         8000 tokens"
	parts := []string{}
	if body != "" {
		parts = append(parts, body, "")
	}
	parts = append(parts, separator, prompt, separator, mode)
	return strings.Join(parts, "\n")
}

// writeUnread drops a sentinel json into the inbox for slug under
// the spore-side state path for projectRoot.
func writeUnread(t *testing.T, projectRoot, slug string) {
	t.Helper()
	state := os.Getenv("XDG_STATE_HOME")
	inboxDir := filepath.Join(state, "spore", filepath.Base(projectRoot), slug, "inbox")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "pending.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFleetStatusReportsDeadPane(t *testing.T) {
	session := "rower project/dead-pane [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"dead-pane": taskAgentSession("dead-pane", "active", "thishost", "codex", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t1\t1\tcodex\tskytower\n",
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=0 active-dead=1",
		"dead: dead-pane (codex, session=" + session + ", pane dead status=1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusDistinguishesCodexIdleAndRunning(t *testing.T) {
	idleSession := "rower project/codex-idle [codex-medium]"
	runningSession := "rower project/codex-running [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-idle":    taskAgentSession("codex-idle", "active", "thishost", "codex", idleSession),
			"codex-running": taskAgentSession("codex-running", "active", "thishost", "codex", runningSession),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{idleSession: true, runningSession: true},
		panesOut: strings.Join([]string{
			idleSession + "\tcodex\t0\t\tcodex-raw\tproject",
			runningSession + "\tcodex\t0\t\tcodex-raw\tproject",
			"",
		}, "\n"),
		captures: map[string]string{
			idleSession + ":codex":    "• Ran true\n\n› Improve documentation in @filename\n\n  gpt-5.5 medium · " + filepath.Join(s.tmp, "project"),
			runningSession + ":codex": "• Working (1m 04s • esc to interrupt)\n\n› queued input\n\n  gpt-5.5 high · " + filepath.Join(s.tmp, "project"),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=2 active-dead=0",
		"codex-idle: codex-idle",
		"codex-running: codex-running",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusReportsIdleCodexUnreadInbox(t *testing.T) {
	session := "rower project/codex-idle-unread [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-idle-unread": taskAgentSession("codex-idle-unread", "active", "thishost", "codex", session),
		},
	})
	writeUnread(t, s.projects[0], "codex-idle-unread")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "• Ran true\n\n› Improve documentation in @filename",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=1 idle-unread=1",
		"codex-idle-unread: codex-idle-unread (session=" + session + ", unread-inbox=1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusDoesNotCountBlockedSessionActiveLive(t *testing.T) {
	session := "rower project/blocked-task [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"blocked-task": taskAgentSession("blocked-task", "blocked", "thishost", "codex", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "• Ran true\n\n› waiting on operator\n",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "fleet status: active-live=0 active-dead=0") {
		t.Errorf("stdout=%q", got)
	}
	if strings.Contains(got, "blocked-task") {
		t.Errorf("blocked task appeared in active runtime details: %q", got)
	}
}

func TestFleetStatusReportsCodexInterruptedPaneAsDead(t *testing.T) {
	session := "rower project/codex-interrupted [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-interrupted": taskAgentSession("codex-interrupted", "active", "thishost", "codex", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "■ Conversation interrupted - tell the model what to do differently.\n\n› Explain this codebase",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=0 active-dead=1",
		"dead: codex-interrupted (codex, session=" + session + ", codex conversation interrupted)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusReportsInterruptedClaudePaneAsIdle(t *testing.T) {
	session := "rower project/claude-interrupted [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-interrupted": taskAgentSession("claude-interrupted", "active", "thishost", "claude", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("Interrupted · What should Claude do instead?", "❯ "),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=1 idle-unread=0",
		"claude-idle: claude-interrupted (session=" + session + ")",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusReportsClaudeAtEmptyPromptAsIdle(t *testing.T) {
	session := "rower project/claude-idle [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-idle": taskAgentSession("claude-idle", "active", "thishost", "claude", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("● Read(/some/path)\n  ⎿  Read 12 lines", "❯ "),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=1 idle-unread=0",
		"claude-idle: claude-idle (session=" + session + ")",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

// TestFleetStatusReportsClaudeAtPastedBriefAsIdle pins the new
// fix: a Claude pane with a multi-line brief pasted into the input
// box (mode line present, no spinner, prompt starts with `❯ ---`)
// gets classified idle, not running. This is the false-positive the
// operator hit before the lift.
func TestFleetStatusReportsClaudeAtPastedBriefAsIdle(t *testing.T) {
	session := "rower project/claude-pasted [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-pasted": taskAgentSession("claude-pasted", "active", "thishost", "claude", session),
		},
	})
	briefPrompt := "❯ ---\n  slug: rower-claim\n  title: claim some thing\n  status: active\n  ---\n\n  Hello, please do the thing described above."
	capture := strings.Join([]string{
		"  ctx 33k / 120k (27%) unknown",
		"",
		strings.Repeat("─", 60),
		briefPrompt,
		strings.Repeat("─", 60),
		"  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)         8000 tokens",
	}, "\n")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": capture,
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=1",
		"claude-idle: claude-pasted (session=" + session + ")",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusReportsClaudeWithSpinnerAsRunning(t *testing.T) {
	session := "rower project/claude-running [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-running": taskAgentSession("claude-running", "active", "thishost", "claude", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("✶ Cogitating… (53s · ↓ 2.2k tokens · thought for 4s)", "❯ "),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "fleet status: active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=0") {
		t.Errorf("stdout=%q", got)
	}
	if strings.Contains(got, "claude-idle") {
		t.Errorf("running rower wrongly flagged idle: %s", got)
	}
}

func TestFleetStatusReportsClaudeRunningToolAsRunning(t *testing.T) {
	session := "rower project/claude-tool [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-tool": taskAgentSession("claude-tool", "active", "thishost", "claude", session),
		},
	})
	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("✶ Bashing… (1s · ↓ 0 tokens · esc to interrupt)", "❯ "),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	if got := stdout.String(); strings.Contains(got, "claude-idle") {
		t.Errorf("running tool wrongly flagged idle: %s", got)
	}
}

func TestFleetStatusFlagsClaudeIdleWithUnreadInbox(t *testing.T) {
	session := "rower project/claude-idle-unread [claude-opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-idle-unread": taskAgentSession("claude-idle-unread", "active", "thishost", "claude", session),
		},
	})
	writeUnread(t, s.projects[0], "claude-idle-unread")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("● Read(/x)\n  ⎿  Read 1 line", "❯ "),
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"fleet status: active-live=1 active-dead=0 active-zombie=0 active-unknown=0 duplicates=0 active-idle=1 idle-unread=1",
		"claude-idle-unread: claude-idle-unread (session=" + session + ", unread-inbox=1)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusSuppressesFreshWakePending(t *testing.T) {
	session := "rower project/codex-pending [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-pending": taskAgentSession("codex-pending", "active", "thishost", "codex", session),
		},
	})
	writeUnread(t, s.projects[0], "codex-pending")
	markWakePending(s.projects[0], "codex-pending")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "• Ran true\n\n› Improve documentation in @filename",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, newLivenessEnv(s, "thishost", tmux))
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"idle-unread=0",
		"codex-idle-wake-pending: codex-pending",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusReportsExpiredWakePendingAsStuck(t *testing.T) {
	session := "rower project/codex-expired [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-expired": taskAgentSession("codex-expired", "active", "thishost", "codex", session),
		},
	})
	writeUnread(t, s.projects[0], "codex-expired")
	markWakePending(s.projects[0], "codex-expired")
	e := newLivenessEnv(s, "thishost", &fakeTmux{})
	wpPath, err := wakePendingPath(s.projects[0], "codex-expired")
	if err != nil {
		t.Fatal(err)
	}
	old := e.now().Add(-6 * time.Minute)
	if err := os.Chtimes(wpPath, old, old); err != nil {
		t.Fatal(err)
	}

	e.tmuxRunner = &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "• Ran true\n\n› Improve documentation in @filename",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"idle-unread=0",
		"idle-wake-stuck=1",
		"codex-idle-wake-stuck: codex-expired",
		"wake-pending-age=",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q\n%s", want, got)
		}
	}
}

func TestFleetStatusWakePendingTTLOverride(t *testing.T) {
	session := "rower project/codex-expired [codex-high]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"codex-expired": taskAgentSession("codex-expired", "active", "thishost", "codex", session),
		},
	})
	t.Setenv("WT_ROWER_WAKE_PENDING_TTL", "60")
	writeUnread(t, s.projects[0], "codex-expired")
	markWakePending(s.projects[0], "codex-expired")
	e := newLivenessEnv(s, "thishost", &fakeTmux{})
	wpPath, err := wakePendingPath(s.projects[0], "codex-expired")
	if err != nil {
		t.Fatal(err)
	}
	old := e.now().Add(-2 * time.Minute)
	if err := os.Chtimes(wpPath, old, old); err != nil {
		t.Fatal(err)
	}

	e.tmuxRunner = &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tcodex\t0\t\tcodex-raw\tproject\n",
		captures: map[string]string{
			session + ":codex": "• Ran true\n\n› Improve documentation in @filename",
		},
	}

	var stdout, stderr bytes.Buffer
	rc, err := runStatus(&stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	if got := stdout.String(); !strings.Contains(got, "codex-idle-wake-stuck: codex-expired") {
		t.Fatalf("stdout missing stuck line\n%s", got)
	}
}

var _ io.Writer = (*bytes.Buffer)(nil)
