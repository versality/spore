package failuresummary

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
}

func recentEpoch() int64 { return fixedNow().Unix() - 60 }
func staleEpoch() int64  { return fixedNow().Unix() - 86400 - 600 }

func recentTS() string {
	return fixedNow().Add(-60 * time.Second).UTC().Format(time.RFC3339)
}

func staleTS() string {
	return fixedNow().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
}

func newCfg(t *testing.T) (Config, string, string) {
	t.Helper()
	coord := t.TempDir()
	worker := t.TempDir()
	cfg := Config{
		CoordinatorStateDir: coord,
		WorkerStateDir:      worker,
		Now:                 fixedNow,
		FleetStatus:         func() (string, error) { return "fleet status: active-live=9\n", nil },
	}.Defaults()
	return cfg, coord, worker
}

func writeJSONL(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSummarizeEmptyStateIsClean(t *testing.T) {
	cfg, _, _ := newCfg(t)
	s := Summarize(cfg)
	if len(s.Actions) != 0 {
		t.Fatalf("expected no actions, got %v", s.Actions)
	}
	if s.Counts.Wraps != 0 || s.Counts.Respawns != 0 ||
		s.Counts.WakeErrors != 0 || s.Counts.PaneDeaths != 0 ||
		s.Counts.StuckWorkers != 0 {
		t.Fatalf("expected zero counts, got %+v", s.Counts)
	}
	out := s.Format(false)
	if !strings.Contains(out, "[spore-failure-summary] window=24h") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, "actionable: (none)") {
		t.Fatalf("missing clean marker: %s", out)
	}
	if !strings.Contains(out, "dark-signals: stuck-workers pane-deaths") {
		t.Fatalf("missing dark-signals line: %s", out)
	}
}

func TestSummarizeCountsWrapsByTierAndSlug(t *testing.T) {
	cfg, _, worker := newCfg(t)
	writeJSONL(t, filepath.Join(worker, "worker-voluntary-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d,"tier":"light","slug":"bar"}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, staleEpoch()),
	})
	s := Summarize(cfg)
	if s.Counts.Wraps != 3 {
		t.Fatalf("wraps=%d, want 3 (stale row excluded)", s.Counts.Wraps)
	}
	if got := s.Counts.TierBreakdown["heavy"]; got != 2 {
		t.Fatalf("heavy tier=%d, want 2", got)
	}
	if got := s.Counts.TierBreakdown["light"]; got != 1 {
		t.Fatalf("light tier=%d, want 1", got)
	}
	if len(s.Counts.TopSlugs) != 1 || s.Counts.TopSlugs[0].Slug != "foo" || s.Counts.TopSlugs[0].Count != 2 {
		t.Fatalf("top-slugs=%+v, want foo(2) only (bar has only 1, below minCount)", s.Counts.TopSlugs)
	}
}

func TestSummarizeRespawnsAndWakeErrors(t *testing.T) {
	cfg, coord, _ := newCfg(t)
	writeJSONL(t, filepath.Join(coord, "respawn-events.jsonl"), []string{
		fmt.Sprintf(`{"ts":%q}`, recentTS()),
		fmt.Sprintf(`{"ts":%q}`, recentTS()),
		fmt.Sprintf(`{"ts":%q}`, staleTS()),
	})
	writeJSONL(t, filepath.Join(coord, "codex-inbox-watcher.jsonl"), []string{
		fmt.Sprintf(`{"ts":%q,"status":"wake-error","source":"alpha"}`, recentTS()),
		fmt.Sprintf(`{"ts":%q,"status":"wake-error","source":"alpha"}`, recentTS()),
		fmt.Sprintf(`{"ts":%q,"status":"wake-error","source":"alpha"}`, recentTS()),
		fmt.Sprintf(`{"ts":%q,"status":"ok","source":"alpha"}`, recentTS()),
	})
	s := Summarize(cfg)
	if s.Counts.Respawns != 2 {
		t.Fatalf("respawns=%d, want 2", s.Counts.Respawns)
	}
	if s.Counts.WakeErrors != 3 {
		t.Fatalf("wake-errors=%d, want 3 (status=ok row excluded)", s.Counts.WakeErrors)
	}
	if len(s.Actions) == 0 {
		t.Fatalf("expected a wake-error action, got none")
	}
	wantPrefix := "codex inbox wake-errors recur for: alpha(3)"
	if !strings.HasPrefix(s.Actions[len(s.Actions)-1], wantPrefix) {
		t.Fatalf("wake-error action=%q, want prefix %q", s.Actions[0], wantPrefix)
	}
}

func TestSummarizeFloorAction(t *testing.T) {
	cfg, _, _ := newCfg(t)
	cfg.Floor = 6
	cfg.FleetStatus = func() (string, error) {
		return "fleet status: active-live=2 active-dead=0\n", nil
	}
	s := Summarize(cfg)
	if len(s.Actions) != 1 {
		t.Fatalf("actions=%v, want 1", s.Actions)
	}
	if !strings.Contains(s.Actions[0], "active-live=2 below floor=6") {
		t.Fatalf("floor action=%q", s.Actions[0])
	}
	if !strings.Contains(s.Actions[0], "spore task new --draft") {
		t.Fatalf("floor action must use spore CLI, got %q", s.Actions[0])
	}
}

func TestSummarizeFleetProbeFailureDoesNotTrigger(t *testing.T) {
	cfg, _, _ := newCfg(t)
	cfg.Floor = 6
	cfg.FleetStatus = func() (string, error) { return "", fmt.Errorf("tmux unreachable") }
	s := Summarize(cfg)
	for _, a := range s.Actions {
		if strings.Contains(a, "below floor") {
			t.Fatalf("unreachable fleet must not trigger floor action, got %q", a)
		}
	}
}

func TestSummarizeMalformedJSONLSkipsLine(t *testing.T) {
	cfg, _, worker := newCfg(t)
	writeJSONL(t, filepath.Join(worker, "worker-voluntary-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
		`{"epoch": not-json`,
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
	})
	s := Summarize(cfg)
	if s.Counts.Wraps != 2 {
		t.Fatalf("wraps=%d, want 2 (malformed row skipped)", s.Counts.Wraps)
	}
}

func TestFormatQuietOnlyEmitsActions(t *testing.T) {
	cfg, _, _ := newCfg(t)
	cfg.Floor = 6
	cfg.FleetStatus = func() (string, error) {
		return "fleet status: active-live=0\n", nil
	}
	s := Summarize(cfg)
	out := s.Format(true)
	if strings.Contains(out, "[spore-failure-summary]") {
		t.Fatalf("quiet must drop the header: %s", out)
	}
	if strings.Contains(out, "dark-signals:") {
		t.Fatalf("quiet must drop dark-signals: %s", out)
	}
	if !strings.Contains(out, "active-live=0 below floor=6") {
		t.Fatalf("quiet must still emit action lines: %s", out)
	}
}

func TestFormatHeaderShape(t *testing.T) {
	cfg, coord, worker := newCfg(t)
	writeJSONL(t, filepath.Join(worker, "worker-voluntary-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d,"tier":"heavy","slug":"foo"}`, recentEpoch()),
	})
	writeJSONL(t, filepath.Join(coord, "respawn-events.jsonl"), []string{
		fmt.Sprintf(`{"ts":%q}`, recentTS()),
	})
	s := Summarize(cfg)
	out := s.Format(false)
	wantSubs := []string{
		"[spore-failure-summary] window=24h state_dir=" + coord,
		"counts: wraps=2 (heavy:2) top: foo(2) respawns=1 wake-errors=0 stuck-workers=0 pane-deaths=0",
		"dark-signals: stuck-workers pane-deaths (no producer yet)",
		"actionable: (none)",
	}
	for _, sub := range wantSubs {
		if !strings.Contains(out, sub) {
			t.Fatalf("missing %q in output:\n%s", sub, out)
		}
	}
}

func TestDefaultsReadsEnv(t *testing.T) {
	t.Setenv(envWindowSecs, "3600")
	t.Setenv(envFloor, "12")
	cfg := Config{}.Defaults()
	if cfg.WindowSecs != 3600 {
		t.Fatalf("WindowSecs=%d, want 3600", cfg.WindowSecs)
	}
	if cfg.Floor != 12 {
		t.Fatalf("Floor=%d, want 12", cfg.Floor)
	}
}

func TestDefaultsRejectsBadEnv(t *testing.T) {
	t.Setenv(envWindowSecs, "not-a-number")
	cfg := Config{}.Defaults()
	if cfg.WindowSecs != DefaultWindowSecs {
		t.Fatalf("bad env should fall back to default, got %d", cfg.WindowSecs)
	}
}
