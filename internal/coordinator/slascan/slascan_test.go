package slascan

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
		Lister:    func() (map[string]string, error) { return map[string]string{}, nil },
	}
	return cfg.Defaults(), path
}

func TestScanMissingStateIsClean(t *testing.T) {
	cfg := Config{
		StateDir:  t.TempDir(),
		StateFile: filepath.Join(t.TempDir(), "nope.md"),
		Now:       fixedNow,
		Lister:    func() (map[string]string, error) { return map[string]string{}, nil },
	}.Defaults()
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
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"ship-it": "done"}, nil
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
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"live-one": "active"}, nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %v", res.Findings)
	}
}

func TestScanTableRowDoneStrikethrough(t *testing.T) {
	body := "## Workboard\n| slug | note |\n| --- | --- |\n| ~~closed-slug~~ | did the thing |\n"
	cfg, _ := writeState(t, body)
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"closed-slug": "active"}, nil
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
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"done-slug": "done"}, nil
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
		"## Recently done",
		"- finished thing",
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
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncation marker in: %q", got)
	}
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
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"bullet-slug": "done"}, nil
	}
	res, _ := Scan(cfg)
	if len(res.Findings) != 1 || res.Findings[0].Kind != "done" {
		t.Fatalf("unexpected: %+v", res.Findings)
	}
}

func TestScanSectionLatestTSCarriesToOtherEntries(t *testing.T) {
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

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", "/tmp/sporetest")
	t.Setenv("SPORE_COORDINATOR_STATE_FILE", "/tmp/sporetest/state.md")
	t.Setenv("SPORE_SLA_AGE_SECONDS", "1800")
	t.Setenv("SPORE_TASKS_DIR", "/tmp/sporetest/tasks")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/sporetest" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.StateFile != "/tmp/sporetest/state.md" {
		t.Errorf("StateFile = %q", c.StateFile)
	}
	if c.AgeSeconds != 1800 {
		t.Errorf("AgeSeconds = %d", c.AgeSeconds)
	}
	if c.TasksDir != "/tmp/sporetest/tasks" {
		t.Errorf("TasksDir = %q", c.TasksDir)
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
	cfg.Lister = func() (map[string]string, error) {
		return map[string]string{"live": "active", "done-task": "done"}, nil
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

func TestListerFromTasksDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "live-task.md"), []byte("---\nstatus: active\nslug: live-task\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("write task: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shipped.md"), []byte("---\nstatus: done\nslug: shipped\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("write task: %v", err)
	}
	got, err := listTasks(dir)
	if err != nil {
		t.Fatalf("listTasks: %v", err)
	}
	if got["live-task"] != "active" || got["shipped"] != "done" {
		t.Fatalf("unexpected map: %v", got)
	}
}

func TestListerEmptyDirIsEmptyMap(t *testing.T) {
	got, err := listTasks("")
	if err != nil {
		t.Fatalf("listTasks(\"\"): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}
