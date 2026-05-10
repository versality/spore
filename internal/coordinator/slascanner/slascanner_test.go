package slascanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local)
}

func writeState(t *testing.T, body string) (Config, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write state.md: %v", err)
	}
	cfg := Config{
		StateDir:  dir,
		StateFile: path,
		Now:       fixedNow,
		TaskList: func() (string, error) {
			return "", nil
		},
	}
	return cfg.Defaults(), path
}

func TestScanMissingStateIsClean(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), StateFile: filepath.Join(t.TempDir(), "nope.md"), Now: fixedNow}.Defaults()
	res, err := Scan(cfg)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !res.MissingState {
		t.Fatalf("expected MissingState, got %+v", res)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %v", res.Findings)
	}
}

func TestScanCleanStateNoFindings(t *testing.T) {
	body := "# state\n\n## Notes\n\n## Active tasks\n- some-slug: foo bar (task: some-slug)\n"
	cfg, _ := writeState(t, body)
	res, err := Scan(cfg)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %v", res.Findings)
	}
}

func TestScanStaleEntryNoTimestamp(t *testing.T) {
	body := "## Notes\n- a thing with no slug and no ts\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	got := res.Findings[0].Line
	if !strings.HasPrefix(got, "stale: ") || !strings.Contains(got, "no timestamp") {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestScanStaleEntryWithOldTimestamp(t *testing.T) {
	body := "## Notes\n- 2026-05-09T08:00 ancient line, no slug\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	got := res.Findings[0].Line
	if !strings.HasPrefix(got, "stale: ") || !strings.Contains(got, "age ") {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestScanFreshEntryNotFlagged(t *testing.T) {
	// 30 minutes old, threshold is 2h.
	ts := fixedNow().Add(-30 * time.Minute).Format("2006-01-02T15:04")
	body := "## Notes\n- " + ts + " fresh line\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %v", res.Findings)
	}
}

func TestScanOrphanSlug(t *testing.T) {
	body := "## Notes\n- some thing (task: ghost-slug, see)\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	got := res.Findings[0]
	if got.Kind != "orphan" || !strings.Contains(got.Line, "ghost-slug not in fleet") {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestScanDoneSlugFromFleet(t *testing.T) {
	cfg, _ := writeState(t, "## Notes\n- thing (task: ship-it)\n")
	cfg.TaskList = func() (string, error) {
		return "slug status\nship-it done\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	if res.Findings[0].Kind != "done" || !strings.Contains(res.Findings[0].Line, "task ship-it, remove") {
		t.Fatalf("unexpected: %+v", res.Findings[0])
	}
}

func TestScanLiveSlugIsOk(t *testing.T) {
	cfg, _ := writeState(t, "## Notes\n- thing (task: live-one)\n")
	cfg.TaskList = func() (string, error) {
		return "slug status\nlive-one active\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %v", res.Findings)
	}
}

func TestScanTableRowDoneStrikethrough(t *testing.T) {
	body := "## Workboard\n| slug | note |\n| --- | --- |\n| ~~closed-slug~~ | did the thing |\n"
	cfg, _ := writeState(t, body)
	cfg.TaskList = func() (string, error) {
		return "slug status\nclosed-slug active\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	if res.Findings[0].Kind != "done" || !strings.Contains(res.Findings[0].Line, "closed-slug, remove") {
		t.Fatalf("unexpected: %+v", res.Findings[0])
	}
}

func TestScanTableRowFleetDoneAndOrphan(t *testing.T) {
	body := strings.Join([]string{
		"## Workboard",
		"| slug | note |",
		"| --- | --- |",
		"| done-slug | wrap up |",
		"| orphan-slug | ghost row |",
		"",
	}, "\n")
	cfg, _ := writeState(t, body)
	cfg.TaskList = func() (string, error) {
		return "slug status\ndone-slug done\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %v", res.Findings)
	}
	kinds := []string{res.Findings[0].Kind, res.Findings[1].Kind}
	want := []string{"done", "orphan"}
	for i := range kinds {
		if kinds[i] != want[i] {
			t.Fatalf("findings[%d].Kind = %q want %q (full: %+v)", i, kinds[i], want[i], res.Findings)
		}
	}
}

func TestScanIgnoresOperatorOwnedSections(t *testing.T) {
	body := strings.Join([]string{
		"## Active tasks",
		"- stale-no-slug-but-ignored",
		"## Operator ingress ledger",
		"- another stale line",
		"## Roadmap",
		"- yet another",
		"## directives for the operator",
		"- ignored too",
		"",
	}, "\n")
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings (all ignored), got %v", res.Findings)
	}
}

func TestScanRecentEventsRequiresDurableMarker(t *testing.T) {
	body := strings.Join([]string{
		"## Recent events",
		"- 2026-05-09T08:00 random chatter, no marker",
		"- 2026-05-09T08:00 operator directive: do the thing",
		"- 2026-05-09T08:00 operator decision: keep it",
		"- 2026-05-09T08:00 operator says we must keep it",
		"",
	}, "\n")
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	// Expect 3 stale entries (the durable lines), and the chatter
	// line dropped because it isn't durable.
	if len(res.Findings) != 3 {
		t.Fatalf("want 3 findings, got %v", res.Findings)
	}
	for _, f := range res.Findings {
		if f.Kind != "stale" {
			t.Fatalf("finding %+v not stale", f)
		}
	}
}

func TestScanTruncatesLongDisplayLines(t *testing.T) {
	long := "stale-line " + strings.Repeat("x", 200)
	body := "## Notes\n- " + long + "\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 {
		t.Fatalf("want 1 finding, got %v", res.Findings)
	}
	got := res.Findings[0].Line
	// "stale: " + 70-char display + " (no task, no timestamp)"
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker in: %q", got)
	}
	// Inner display must be exactly 70 bytes (67 + "...").
	const prefix = "stale: "
	const suffix = " (no task, no timestamp)"
	disp := got[len(prefix) : len(got)-len(suffix)]
	if len(disp) != 70 || !strings.HasSuffix(disp, "...") {
		t.Fatalf("display not truncated to 70 with `...`, got %q (len=%d)", disp, len(disp))
	}
}

func TestScanBulletSlugSyntax(t *testing.T) {
	body := "## Notes\n- bullet-slug: doing the thing\n"
	cfg, _ := writeState(t, body)
	cfg.TaskList = func() (string, error) {
		return "slug status\nbullet-slug done\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 || res.Findings[0].Kind != "done" {
		t.Fatalf("unexpected: %+v", res.Findings)
	}
}

func TestScanSectionLatestTSCarriesToOtherEntries(t *testing.T) {
	// First entry has a fresh ts; second entry has none. The second
	// must inherit the section's latest ts and therefore be fresh.
	freshTS := fixedNow().Add(-30 * time.Minute).Format("2006-01-02T15:04")
	body := "## Notes\n- " + freshTS + " first\n- second with no ts\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings (sec_latest carry), got %v", res.Findings)
	}
}

func TestScanFlushOnBlankLineEmitsBoth(t *testing.T) {
	body := "## Notes\n- 2026-05-09T08:00 first line\n\n- 2026-05-09T08:00 second line\n"
	cfg, _ := writeState(t, body)
	res, _ := Scan(cfg)
	if len(res.Findings) != 2 {
		t.Fatalf("want 2 findings, got %v", res.Findings)
	}
}

func TestParseTaskListSkipsHeader(t *testing.T) {
	in := "slug    status   tier\nfoo     active   soft\nbar     done     hard\n"
	got := parseTaskList(in)
	if got["foo"] != "active" || got["bar"] != "done" {
		t.Fatalf("unexpected map: %v", got)
	}
	if _, ok := got["slug"]; ok {
		t.Fatal("header row should be skipped")
	}
}

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/skytest")
	t.Setenv("SKYHELM_STATE_FILE", "/tmp/skytest/state.md")
	t.Setenv("SKYHELM_SLA_AGE_SECONDS", "1800")
	t.Setenv("WT_TASK_BIN", "/usr/local/bin/wt")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/skytest" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.StateFile != "/tmp/skytest/state.md" {
		t.Errorf("StateFile = %q", c.StateFile)
	}
	if c.AgeSeconds != 1800 {
		t.Errorf("AgeSeconds = %d", c.AgeSeconds)
	}
	if c.WtTaskBin != "/usr/local/bin/wt" {
		t.Errorf("WtTaskBin = %q", c.WtTaskBin)
	}
}

func TestVerboseTraceIncludesAllEntries(t *testing.T) {
	body := strings.Join([]string{
		"## Notes",
		"- thing (task: live)",
		"- thing (task: done-task)",
		"- thing (task: missing)",
		"- bare stale line",
		"",
	}, "\n")
	cfg, _ := writeState(t, body)
	cfg.Verbose = true
	cfg.TaskList = func() (string, error) {
		return "slug status\nlive active\ndone-task done\n", nil
	}
	res, _ := Scan(cfg)
	if len(res.Trace) != 4 {
		t.Fatalf("want 4 trace lines, got %v", res.Trace)
	}
	wantPrefix := []string{"ok", "done", "orphan", "stale"}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(strings.TrimLeft(res.Trace[i], " "), want) {
			t.Errorf("trace[%d] = %q want prefix %q", i, res.Trace[i], want)
		}
	}
}

func TestFormatFindingsJoinsAllLines(t *testing.T) {
	in := []Finding{
		{Kind: "done", Line: "done: a (task x, remove)"},
		{Kind: "stale", Line: "stale: b (no task, no timestamp)"},
	}
	got := FormatFindings(in)
	want := "done: a (task x, remove)\nstale: b (no task, no timestamp)\n"
	if got != want {
		t.Fatalf("FormatFindings = %q want %q", got, want)
	}
	if FormatFindings(nil) != "" {
		t.Fatal("expected empty for empty findings")
	}
}
