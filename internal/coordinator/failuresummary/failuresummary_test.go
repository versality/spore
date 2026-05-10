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
	loc := time.FixedZone("EEST", 3*60*60)
	return time.Date(2026, 5, 10, 12, 0, 0, 0, loc)
}

// recentEpoch returns an epoch inside the default 24h window relative
// to fixedNow.
func recentEpoch() int64 {
	return fixedNow().Unix() - 60
}

// staleEpoch returns an epoch outside the default 24h window.
func staleEpoch() int64 {
	return fixedNow().Unix() - 86400 - 600
}

// recentTS returns an ISO-8601 ts string inside the default window.
func recentTS() string {
	return fixedNow().Add(-60 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
}

// staleTS returns an ISO-8601 ts string outside the default window.
func staleTS() string {
	return fixedNow().Add(-25 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
}

func newCfg(t *testing.T) (Config, string) {
	t.Helper()
	st := t.TempDir()
	wt := t.TempDir()
	return Config{
		StateDir: st,
		WtState:  wt,
		Now:      fixedNow,
		FleetStatus: func() (string, error) {
			return "", nil
		},
		ProjectRoots: func() []string { return nil },
	}.Defaults(), st
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
	cfg, _ := newCfg(t)
	s := Summarize(cfg)
	if len(s.Actions) != 0 {
		t.Fatalf("expected no actions, got %v", s.Actions)
	}
	if s.Counts.Wraps != 0 || s.Counts.Respawns != 0 ||
		s.Counts.WakeErrors != 0 || s.Counts.PaneDeaths != 0 ||
		s.Counts.StuckRowers != 0 {
		t.Fatalf("expected zero counts, got %+v", s.Counts)
	}
	out := s.Format(false)
	if !strings.Contains(out, "[skyhelm-failure-summary] window=24h") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, "actionable: (none)") {
		t.Fatalf("missing clean marker: %s", out)
	}
	if !strings.Contains(out, "counts: wraps=0 respawns=0 wake-errors=0 stuck-rowers=0 pane-deaths=0") {
		t.Fatalf("unexpected counts line: %s", out)
	}
}

func TestSummarizeWrapsTierAndTopSlugs(t *testing.T) {
	cfg, _ := newCfg(t)
	rec := recentEpoch()
	stale := staleEpoch()
	writeJSONL(t, filepath.Join(cfg.WtState, "rower-voluntary-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d,"tier":"soft","slug":"alpha"}`, rec),
		fmt.Sprintf(`{"epoch":%d,"tier":"soft","slug":"alpha"}`, rec),
		fmt.Sprintf(`{"epoch":%d,"tier":"hard","slug":"beta"}`, rec),
		fmt.Sprintf(`{"epoch":%d,"slug":"gamma"}`, rec),
		fmt.Sprintf(`{"epoch":%d,"tier":"hard","slug":"alpha"}`, stale),
	})
	s := Summarize(cfg)
	if s.Counts.Wraps != 4 {
		t.Fatalf("wraps = %d, want 4 (stale dropped)", s.Counts.Wraps)
	}
	if s.Counts.TierBreakdown["soft"] != 2 ||
		s.Counts.TierBreakdown["hard"] != 1 ||
		s.Counts.TierBreakdown["unknown"] != 1 {
		t.Fatalf("tier breakdown = %v", s.Counts.TierBreakdown)
	}
	if len(s.Counts.TopSlugs) != 1 || s.Counts.TopSlugs[0].Slug != "alpha" || s.Counts.TopSlugs[0].Count != 2 {
		t.Fatalf("top slugs = %v (only slugs with >=2 inside window qualify)", s.Counts.TopSlugs)
	}
	out := s.Format(false)
	if !strings.Contains(out, "(hard:1 soft:2 unknown:1)") {
		t.Fatalf("tier render missing/wrong: %s", out)
	}
	if !strings.Contains(out, "top: alpha(2)") {
		t.Fatalf("top render missing: %s", out)
	}
}

func TestSummarizeRespawnsAndPaneDeaths(t *testing.T) {
	cfg, _ := newCfg(t)
	writeJSONL(t, filepath.Join(cfg.StateDir, "respawn-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d}`, recentEpoch()),
		fmt.Sprintf(`{"epoch":%d}`, staleEpoch()),
	})
	writeJSONL(t, filepath.Join(cfg.WtState, "rower-pane-death.jsonl"), []string{
		fmt.Sprintf(`{"ts":"%s","event":"rower-pane-death"}`, recentTS()),
		fmt.Sprintf(`{"ts":"%s","event":"other"}`, recentTS()),
		fmt.Sprintf(`{"ts":"%s","event":"rower-pane-death"}`, staleTS()),
	})
	s := Summarize(cfg)
	if s.Counts.Respawns != 2 {
		t.Fatalf("respawns = %d", s.Counts.Respawns)
	}
	if s.Counts.PaneDeaths != 1 {
		t.Fatalf("pane-deaths = %d", s.Counts.PaneDeaths)
	}
}

func TestSummarizeWakeErrorsActionable(t *testing.T) {
	cfg, _ := newCfg(t)
	rec := recentTS()
	writeJSONL(t, filepath.Join(cfg.StateDir, "codex-inbox-watcher.jsonl"), []string{
		fmt.Sprintf(`{"ts":"%s","status":"wake-error","source":"alpha"}`, rec),
		fmt.Sprintf(`{"ts":"%s","status":"wake-error","source":"alpha"}`, rec),
		fmt.Sprintf(`{"ts":"%s","status":"wake-error","source":"alpha"}`, rec),
		fmt.Sprintf(`{"ts":"%s","status":"wake-error","source":"beta"}`, rec),
		fmt.Sprintf(`{"ts":"%s","status":"ok","source":"gamma"}`, rec),
	})
	s := Summarize(cfg)
	if s.Counts.WakeErrors != 4 {
		t.Fatalf("wake-errors = %d", s.Counts.WakeErrors)
	}
	found := false
	for _, a := range s.Actions {
		if strings.Contains(a, "codex inbox wake-errors recur for: alpha(3)") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected wake-error action with alpha(3); actions=%v", s.Actions)
	}
}

func TestSummarizeStuckRowerStatusDrift(t *testing.T) {
	cfg, _ := newCfg(t)
	writeJSONL(t, filepath.Join(cfg.StateDir, "rower-watch.json"), []string{
		`{"slug":"foo","status":"active","agent":"claude","idle_secs":3600}`,
		`{"slug":"bar","status":"active","agent":"claude","stuck":true,"idle_secs":60}`,
		`{"slug":"baz","status":"paused","agent":"claude","idle_secs":9999}`,
	})
	s := Summarize(cfg)
	if s.Counts.StuckRowers != 3 {
		t.Fatalf("stuck-rowers = %d", s.Counts.StuckRowers)
	}
	var foo, bar bool
	for _, a := range s.Actions {
		if strings.Contains(a, "rower foo status=active idle_secs=3600") &&
			strings.Contains(a, "wt task pause foo") {
			foo = true
		}
		if strings.Contains(a, "rower bar stuck status=active") &&
			strings.Contains(a, "wt task tell bar") {
			bar = true
		}
	}
	if !foo {
		t.Fatalf("missing drift action for foo: %v", s.Actions)
	}
	if !bar {
		t.Fatalf("missing stuck action for bar: %v", s.Actions)
	}
}

func TestSummarizeBlockedWithoutTrigger(t *testing.T) {
	cfg, _ := newCfg(t)
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	mustWrite := func(name, body string) {
		if err := os.WriteFile(filepath.Join(tasks, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustWrite("orphan.md", "---\nslug: orphan\nstatus: blocked\n---\n")
	mustWrite("with-trigger.md", "---\nslug: with-trigger\nstatus: blocked\ntrigger: foo-done\n---\n")
	mustWrite("active.md", "---\nslug: active\nstatus: active\n---\n")
	cfg.ProjectRoots = func() []string { return []string{root} }

	s := Summarize(cfg)
	if len(s.Actions) != 1 {
		t.Fatalf("expected 1 action, got %v", s.Actions)
	}
	if !strings.Contains(s.Actions[0], "task orphan blocked without trigger/needs/scheduler") {
		t.Fatalf("unexpected action: %s", s.Actions[0])
	}
	if !strings.Contains(s.Actions[0], "repo="+root) {
		t.Fatalf("missing repo annotation: %s", s.Actions[0])
	}
}

func TestSummarizeActiveLiveBelowFloor(t *testing.T) {
	cfg, _ := newCfg(t)
	cfg.Floor = 6
	cfg.FleetStatus = func() (string, error) {
		return "tier=worker active-live=4 idle=2\n", nil
	}
	s := Summarize(cfg)
	found := false
	for _, a := range s.Actions {
		if strings.Contains(a, "active-live=4 below floor=6") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected floor action; got %v", s.Actions)
	}
}

func TestSummarizeActiveLiveAtOrAboveFloorNoAction(t *testing.T) {
	cfg, _ := newCfg(t)
	cfg.Floor = 6
	cfg.FleetStatus = func() (string, error) {
		return "active-live=6\n", nil
	}
	s := Summarize(cfg)
	for _, a := range s.Actions {
		if strings.Contains(a, "below floor") {
			t.Fatalf("unexpected floor action: %s", a)
		}
	}
}

func TestSummarizeCombinedOutputAndExit2Shape(t *testing.T) {
	cfg, _ := newCfg(t)
	rec := recentEpoch()
	writeJSONL(t, filepath.Join(cfg.WtState, "rower-voluntary-events.jsonl"), []string{
		fmt.Sprintf(`{"epoch":%d,"tier":"soft","slug":"alpha"}`, rec),
	})
	writeJSONL(t, filepath.Join(cfg.StateDir, "rower-watch.json"), []string{
		`{"slug":"foo","status":"active","agent":"claude","idle_secs":3600}`,
	})
	s := Summarize(cfg)
	if len(s.Actions) == 0 {
		t.Fatal("expected actions to fire (would map to exit 2)")
	}
	out := s.Format(false)
	if !strings.Contains(out, "actionable:\n") {
		t.Fatalf("missing actionable header: %s", out)
	}
	if !strings.Contains(out, "  - rower foo status=active") {
		t.Fatalf("missing action line: %s", out)
	}
}

func TestSummarizeQuietSuppressesHeaderAndCounts(t *testing.T) {
	cfg, _ := newCfg(t)
	out := Summarize(cfg).Format(true)
	if out != "" {
		t.Fatalf("quiet+clean expected empty, got %q", out)
	}
	writeJSONL(t, filepath.Join(cfg.StateDir, "rower-watch.json"), []string{
		`{"slug":"foo","status":"active","agent":"claude","idle_secs":3600}`,
	})
	out = Summarize(cfg).Format(true)
	if strings.Contains(out, "[skyhelm-failure-summary]") {
		t.Fatalf("quiet should drop header: %s", out)
	}
	if strings.Contains(out, "actionable:") {
		t.Fatalf("quiet should drop actionable header: %s", out)
	}
	if !strings.Contains(out, "  - rower foo") {
		t.Fatalf("action line missing: %s", out)
	}
}

func TestParseTSMatchesJqQuirk(t *testing.T) {
	// The bash uses `sub("\\+.*";"Z") | fromdateiso8601`, which only
	// strips the `+offset` form (treating it as UTC). Negative
	// offsets are parsed natively by fromdateiso8601 and resolve to
	// real UTC.
	utcNoon := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).Unix()
	cases := []struct {
		in   string
		want int64
	}{
		{"2026-05-10T12:00:00Z", utcNoon},
		{"2026-05-10T12:00:00+03:00", utcNoon},
		{"2026-05-10T12:00:00-04:30", time.Date(2026, 5, 10, 16, 30, 0, 0, time.UTC).Unix()},
	}
	for _, c := range cases {
		got, ok := parseTS(c.in)
		if !ok || got != c.want {
			t.Errorf("parseTS(%q) = (%d,%v) want (%d,true)", c.in, got, ok, c.want)
		}
	}
	if _, ok := parseTS("not-a-ts"); ok {
		t.Errorf("expected parseTS to fail on garbage")
	}
}

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/sk")
	t.Setenv("WT_STATE", "/tmp/wt")
	t.Setenv("WT_CFG", "/tmp/cfg")
	t.Setenv("WT_TASK_BIN", "wt-task-test")
	t.Setenv("SKYHELM_FAILURE_WINDOW_SECS", "3600")
	t.Setenv("SKYHELM_FAILURE_STUCK_SECS", "60")
	t.Setenv("WT_FLEET_FLOOR", "9")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/sk" {
		t.Errorf("StateDir=%q", c.StateDir)
	}
	if c.WtState != "/tmp/wt" {
		t.Errorf("WtState=%q", c.WtState)
	}
	if c.WtCfg != "/tmp/cfg" {
		t.Errorf("WtCfg=%q", c.WtCfg)
	}
	if c.WtTaskBin != "wt-task-test" {
		t.Errorf("WtTaskBin=%q", c.WtTaskBin)
	}
	if c.WindowSecs != 3600 {
		t.Errorf("WindowSecs=%d", c.WindowSecs)
	}
	if c.StuckSecs != 60 {
		t.Errorf("StuckSecs=%d", c.StuckSecs)
	}
	if c.Floor != 9 {
		t.Errorf("Floor=%d", c.Floor)
	}
}

func TestReadFrontmatterReadsFirstBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.md")
	body := "---\nslug: x\nstatus: blocked\ntrigger: foo\n---\n\nbody text with key: ignored\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	fm := readFrontmatter(p)
	if fm["slug"] != "x" || fm["status"] != "blocked" || fm["trigger"] != "foo" {
		t.Fatalf("frontmatter = %v", fm)
	}
	if _, ok := fm["body"]; ok {
		t.Fatalf("post-fence content leaked into map: %v", fm)
	}
}

func TestDiscoverProjectRootsViaProjectsFile(t *testing.T) {
	wtCfg := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	other := t.TempDir() // no tasks/ subdir, should be skipped.
	body := root + "\n" + other + "\n# comment line\n\n"
	if err := os.WriteFile(filepath.Join(wtCfg, "projects"), []byte(body), 0o600); err != nil {
		t.Fatalf("write projects: %v", err)
	}
	got := discoverProjectRoots(wtCfg)
	if len(got) != 1 || got[0] != root {
		t.Fatalf("discoverProjectRoots = %v want [%s]", got, root)
	}
}
