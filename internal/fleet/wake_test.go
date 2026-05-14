package fleet

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnsurer records (projectRoot, slug) tuples for each ensureSlug
// call so tests can assert which workers got nudged.
type fakeEnsurer struct {
	calls   []string
	fail    map[string]bool
	failErr error
}

func (f *fakeEnsurer) ensure(projectRoot, slug string) error {
	f.calls = append(f.calls, filepath.Base(projectRoot)+"/"+slug)
	if f.fail[slug] {
		if f.failErr == nil {
			return errors.New("ensure failed")
		}
		return f.failErr
	}
	return nil
}

// fakeMarker records mark-pending calls; tests use it to assert that
// only the successfully-woken slugs have their wake-pending marker set.
type fakeMarker struct {
	marks []string
}

func (f *fakeMarker) mark(projectRoot, slug string) {
	f.marks = append(f.marks, filepath.Base(projectRoot)+"/"+slug)
}

func TestWakeRespawnsIdleUnreadWorkers(t *testing.T) {
	session := "worker project/claude-idle [opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-idle": taskAgentSession("claude-idle", "active", "thishost", "claude", session),
		},
	})
	writeUnread(t, s.projects[0], "claude-idle")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("● Read(/x)\n  ⎿  Read 1 line", "❯ "),
		},
	}
	ens := &fakeEnsurer{}
	mark := &fakeMarker{}
	e := wakeEnv{
		livenessEnv: newLivenessEnv(s, "thishost", tmux),
		scan:        scanActiveRuntimes,
		ensureSlug:  ens.ensure,
		mark:        mark.mark,
	}

	var stdout, stderr bytes.Buffer
	rc, err := runWake("", &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runWake: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	if len(ens.calls) != 1 || ens.calls[0] != "project/claude-idle" {
		t.Fatalf("ensure calls=%v", ens.calls)
	}
	if len(mark.marks) != 1 || mark.marks[0] != "project/claude-idle" {
		t.Fatalf("marks=%v", mark.marks)
	}
	if !strings.Contains(stdout.String(), "woken=1 pending=0") {
		t.Errorf("stdout=%q", stdout.String())
	}
}

func TestWakeSkipsWhenWakePendingFresh(t *testing.T) {
	session := "worker project/claude-idle [opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-idle": taskAgentSession("claude-idle", "active", "thishost", "claude", session),
		},
	})
	writeUnread(t, s.projects[0], "claude-idle")

	// Drop a fresh wake-pending marker so the wake call should be
	// suppressed.
	markWakePending(s.projects[0], "claude-idle")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("● Read(/x)\n  ⎿  Read 1 line", "❯ "),
		},
	}
	ens := &fakeEnsurer{}
	mark := &fakeMarker{}
	e := wakeEnv{
		livenessEnv: newLivenessEnv(s, "thishost", tmux),
		scan:        scanActiveRuntimes,
		ensureSlug:  ens.ensure,
		mark:        mark.mark,
	}

	var stdout, stderr bytes.Buffer
	rc, err := runWake("", &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runWake: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	if len(ens.calls) != 0 {
		t.Fatalf("expected no wakes, got %v", ens.calls)
	}
	if !strings.Contains(stdout.String(), "woken=0 pending=1") {
		t.Errorf("stdout=%q", stdout.String())
	}
}

func TestWakeFiltersBySlug(t *testing.T) {
	sessionA := "worker project/slug-a [opus]"
	sessionB := "worker project/slug-b [opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"slug-a": taskAgentSession("slug-a", "active", "thishost", "claude", sessionA),
			"slug-b": taskAgentSession("slug-b", "active", "thishost", "claude", sessionB),
		},
	})
	writeUnread(t, s.projects[0], "slug-a")
	writeUnread(t, s.projects[0], "slug-b")

	tmux := &fakeTmux{
		sessions: map[string]bool{sessionA: true, sessionB: true},
		panesOut: strings.Join([]string{
			sessionA + "\tclaude\t0\t\tclaude\tproject",
			sessionB + "\tclaude\t0\t\tclaude\tproject",
			"",
		}, "\n"),
		captures: map[string]string{
			sessionA + ":claude": claudePaneCapture("", "❯ "),
			sessionB + ":claude": claudePaneCapture("", "❯ "),
		},
	}
	ens := &fakeEnsurer{}
	mark := &fakeMarker{}
	e := wakeEnv{
		livenessEnv: newLivenessEnv(s, "thishost", tmux),
		scan:        scanActiveRuntimes,
		ensureSlug:  ens.ensure,
		mark:        mark.mark,
	}

	var stdout, stderr bytes.Buffer
	rc, err := runWake("slug-a", &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runWake: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if len(ens.calls) != 1 || ens.calls[0] != "project/slug-a" {
		t.Fatalf("expected only slug-a, got %v", ens.calls)
	}
}

func TestWakeReportsFailureExit2(t *testing.T) {
	session := "worker project/claude-idle [opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-idle": taskAgentSession("claude-idle", "active", "thishost", "claude", session),
		},
	})
	writeUnread(t, s.projects[0], "claude-idle")

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			session + ":claude": claudePaneCapture("", "❯ "),
		},
	}
	ens := &fakeEnsurer{fail: map[string]bool{"claude-idle": true}}
	mark := &fakeMarker{}
	e := wakeEnv{
		livenessEnv: newLivenessEnv(s, "thishost", tmux),
		scan:        scanActiveRuntimes,
		ensureSlug:  ens.ensure,
		mark:        mark.mark,
	}

	var stdout, stderr bytes.Buffer
	rc, err := runWake("", &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runWake: %v", err)
	}
	if rc != 2 {
		t.Fatalf("rc=%d stderr=%s stdout=%s", rc, stderr.String(), stdout.String())
	}
	if len(mark.marks) != 0 {
		t.Fatalf("failed wake must not mark pending, got %v", mark.marks)
	}
}

func TestWakeSlugWithoutIdleUnreadEmitsNoOpMessage(t *testing.T) {
	session := "worker project/claude-busy [opus]"
	s := newSetup(t, map[string]map[string]string{
		"project": {
			"claude-busy": taskAgentSession("claude-busy", "active", "thishost", "claude", session),
		},
	})

	tmux := &fakeTmux{
		sessions: map[string]bool{session: true},
		panesOut: session + "\tclaude\t0\t\tclaude\tproject\n",
		captures: map[string]string{
			// Pane shows the agent mid-turn, not idle.
			session + ":claude": "● Working...",
		},
	}
	ens := &fakeEnsurer{}
	mark := &fakeMarker{}
	e := wakeEnv{
		livenessEnv: newLivenessEnv(s, "thishost", tmux),
		scan:        scanActiveRuntimes,
		ensureSlug:  ens.ensure,
		mark:        mark.mark,
	}

	var stdout, stderr bytes.Buffer
	rc, err := runWake("claude-busy", &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runWake: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout.String(), "claude-busy no idle unread worker") {
		t.Errorf("stdout=%q", stdout.String())
	}
}
