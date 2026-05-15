package evictor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
)

// fakeProbe is the test seam for Run. Returns the inputs the caller
// staged for each slug; missing slugs default to the zero value
// (which the predicate treats as "do not evict").
type fakeProbe struct {
	bySlug map[string]Inputs
	calls  []string
}

func (p *fakeProbe) SlugInputs(projectRoot, tasksDir, slug string, now time.Time) Inputs {
	p.calls = append(p.calls, slug)
	if p.bySlug == nil {
		return Inputs{}
	}
	return p.bySlug[slug]
}

func writeTaskFile(t *testing.T, tasksDir, slug, status string) {
	t.Helper()
	body := fmt.Sprintf("---\nstatus: %s\nslug: %s\ntitle: %s\n---\n", status, slug, slug)
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write task %s: %v", slug, err)
	}
}

func readStatus(t *testing.T, tasksDir, slug string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatalf("read task %s: %v", slug, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "status:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "status:"))
		}
	}
	t.Fatalf("status not found in %s", slug)
	return ""
}

func readBlocker(t *testing.T, tasksDir, slug string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatalf("read task %s: %v", slug, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "blocker:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "blocker:"))
		}
	}
	return ""
}

// TestRunFlipsIdleSlugAndLeavesBusyOnesAlone is the integration test
// the brief asks for: drives Run with a fake probe, real
// task.BlockAuto, and asserts the frontmatter actually flips on
// disk (proving the wiring works end to end).
func TestRunFlipsIdleSlugAndLeavesBusyOnesAlone(t *testing.T) {
	t.Setenv(task.SessionKindEnv, task.SessionKindWorker)
	t.Setenv(KillSwitchEnv, "")

	tasksDir := t.TempDir()
	writeTaskFile(t, tasksDir, "idle", "active")
	writeTaskFile(t, tasksDir, "busy", "active")
	writeTaskFile(t, tasksDir, "draining", "active")
	writeTaskFile(t, tasksDir, "draft", "draft")

	now := time.Date(2026, 5, 15, 18, 0, 0, 0, time.UTC)
	threshold := 10 * time.Minute

	probe := &fakeProbe{bySlug: map[string]Inputs{
		"idle": {
			SessionPresent:  true,
			Idle:            20 * time.Minute,
			IdleKnown:       true,
			UnreadInbox:     0,
			LastCommitKnown: false,
		},
		"busy": {
			SessionPresent:  true,
			Idle:            1 * time.Minute,
			IdleKnown:       true,
			UnreadInbox:     0,
			LastCommitKnown: false,
		},
		"draining": {
			SessionPresent: true,
			Idle:           20 * time.Minute,
			IdleKnown:      true,
			UnreadInbox:    3,
		},
	}}

	rep, err := Run(Config{
		TasksDir:    tasksDir,
		ProjectRoot: filepath.Dir(tasksDir),
		Threshold:   threshold,
		Now:         func() time.Time { return now },
		Probe:       probe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantSlugs := map[string]bool{"busy": true, "draining": true, "idle": true}
	for _, s := range rep.Slugs {
		if !wantSlugs[s] {
			t.Errorf("unexpected slug in report: %q", s)
		}
		delete(wantSlugs, s)
	}
	if len(wantSlugs) != 0 {
		t.Errorf("missing slugs in report: %v", wantSlugs)
	}

	if got := readStatus(t, tasksDir, "idle"); got != "blocked" {
		t.Errorf("idle: status = %q, want blocked", got)
	}
	if got := readBlocker(t, tasksDir, "idle"); got != BlockerKey {
		t.Errorf("idle: blocker = %q, want %s", got, BlockerKey)
	}

	for _, slug := range []string{"busy", "draining", "draft"} {
		if got := readStatus(t, tasksDir, slug); got != initialStatus(slug) {
			t.Errorf("%s: status = %q, want unchanged", slug, got)
		}
	}

	if probe.calls[0] == "" {
		t.Fatalf("probe was not invoked")
	}
}

func initialStatus(slug string) string {
	if slug == "draft" {
		return "draft"
	}
	return "active"
}

// TestRunDisabledShortCircuits proves the kill-switch suppresses the
// sweep without touching frontmatter.
func TestRunDisabledShortCircuits(t *testing.T) {
	t.Setenv(KillSwitchEnv, "off")

	tasksDir := t.TempDir()
	writeTaskFile(t, tasksDir, "idle", "active")

	probe := &fakeProbe{bySlug: map[string]Inputs{
		"idle": {SessionPresent: true, Idle: 1 * time.Hour, IdleKnown: true},
	}}

	rep, err := Run(Config{
		TasksDir:    tasksDir,
		ProjectRoot: filepath.Dir(tasksDir),
		Threshold:   10 * time.Minute,
		Now:         func() time.Time { return time.Now() },
		Probe:       probe,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Disabled {
		t.Fatalf("expected Disabled=true")
	}
	if got := readStatus(t, tasksDir, "idle"); got != "active" {
		t.Errorf("kill-switch: status = %q, want active", got)
	}
	if len(probe.calls) != 0 {
		t.Errorf("probe should not be called when disabled, got %v", probe.calls)
	}
}

// TestRunDryRunDoesNotFlip lets the operator preview the verdict
// without changing on-disk state.
func TestRunDryRunDoesNotFlip(t *testing.T) {
	t.Setenv(task.SessionKindEnv, task.SessionKindWorker)
	t.Setenv(KillSwitchEnv, "")

	tasksDir := t.TempDir()
	writeTaskFile(t, tasksDir, "idle", "active")

	now := time.Date(2026, 5, 15, 18, 0, 0, 0, time.UTC)
	probe := &fakeProbe{bySlug: map[string]Inputs{
		"idle": {SessionPresent: true, Idle: 1 * time.Hour, IdleKnown: true},
	}}

	rep, err := Run(Config{
		TasksDir:    tasksDir,
		ProjectRoot: filepath.Dir(tasksDir),
		Threshold:   10 * time.Minute,
		Now:         func() time.Time { return now },
		Probe:       probe,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Decisions) != 1 || !rep.Decisions[0].Evicted {
		t.Fatalf("expected dry-run verdict Evicted=true, got %+v", rep.Decisions)
	}
	if got := readStatus(t, tasksDir, "idle"); got != "active" {
		t.Errorf("dry-run: status = %q, want active", got)
	}
}

// TestRunIsolatesPerSlugErrors proves one slug's flip failure does
// not abort the sweep.
func TestRunIsolatesPerSlugErrors(t *testing.T) {
	t.Setenv(task.SessionKindEnv, task.SessionKindWorker)
	t.Setenv(KillSwitchEnv, "")

	tasksDir := t.TempDir()
	writeTaskFile(t, tasksDir, "first", "active")
	writeTaskFile(t, tasksDir, "second", "active")

	now := time.Date(2026, 5, 15, 18, 0, 0, 0, time.UTC)
	probe := &fakeProbe{bySlug: map[string]Inputs{
		"first":  {SessionPresent: true, Idle: 1 * time.Hour, IdleKnown: true},
		"second": {SessionPresent: true, Idle: 1 * time.Hour, IdleKnown: true},
	}}
	calls := 0
	rep, err := Run(Config{
		TasksDir:    tasksDir,
		ProjectRoot: filepath.Dir(tasksDir),
		Threshold:   10 * time.Minute,
		Now:         func() time.Time { return now },
		Probe:       probe,
		Block: func(td, slug, blocker string) error {
			calls++
			if slug == "first" {
				return fmt.Errorf("synthetic flip failure")
			}
			return task.BlockAuto(td, slug, blocker)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("Block called %d times, want 2", calls)
	}
	if rep.Decisions[0].Err == nil {
		t.Fatalf("first slug should have recorded error, got nil")
	}
	if rep.Decisions[1].Err != nil {
		t.Fatalf("second slug error = %v, want nil", rep.Decisions[1].Err)
	}
	if got := readStatus(t, tasksDir, "second"); got != "blocked" {
		t.Errorf("second: status = %q, want blocked", got)
	}
}
