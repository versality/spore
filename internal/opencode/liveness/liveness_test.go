package liveness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestProbe_MidStreamGraceCountsAsOk(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	stats := SessionStats{
		LatestSession: "sess",
		AnyMs:         (now.Unix() - 30) * 1000, // 30s ago
		AsstMs:        (now.Unix() - 1200) * 1000,
	}
	rs := Probe(now, Config{}, "alpha", stats, 0)
	if rs.Stuck {
		t.Fatalf("worker in mid-stream grace must not be stuck: %+v", rs)
	}
}

func TestProbe_StuckRequiresIdleAboveThresholdAndZeroCommits(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	stuckIdle := SessionStats{
		AnyMs:  (now.Unix() - 1200) * 1000,
		AsstMs: (now.Unix() - 1200) * 1000,
	}

	// idle > 600s, 0 commits ahead -> stuck
	rs := Probe(now, Config{}, "alpha", stuckIdle, 0)
	if !rs.Stuck {
		t.Errorf("expected stuck verdict, got %+v", rs)
	}
	if rs.IdleSeconds != 1200 {
		t.Errorf("IdleSeconds = %d, want 1200", rs.IdleSeconds)
	}

	// idle > 600s but commits ahead -> ok
	rs = Probe(now, Config{}, "alpha", stuckIdle, 3)
	if rs.Stuck {
		t.Errorf("commits-ahead must clear stuckness: %+v", rs)
	}

	// idle just under threshold -> ok
	freshIdle := SessionStats{
		AnyMs:  (now.Unix() - 500) * 1000,
		AsstMs: (now.Unix() - 500) * 1000,
	}
	rs = Probe(now, Config{}, "alpha", freshIdle, 0)
	if rs.Stuck {
		t.Errorf("idle below threshold must not be stuck: %+v", rs)
	}
}

func TestProbe_NoAssistantTurnIsStuck(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	stats := SessionStats{} // no rows
	rs := Probe(now, Config{}, "alpha", stats, 0)
	if !rs.Stuck {
		t.Errorf("worker with no assistant turn ever must be stuck")
	}
	if rs.Session != "(none)" {
		t.Errorf("Session = %q, want (none)", rs.Session)
	}
}

func TestProbe_CustomThresholds(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	stats := SessionStats{
		AnyMs:  (now.Unix() - 200) * 1000,
		AsstMs: (now.Unix() - 200) * 1000,
	}
	cfg := Config{GraceSeconds: 10, StuckSeconds: 100}
	rs := Probe(now, cfg, "alpha", stats, 0)
	if !rs.Stuck {
		t.Errorf("expected stuck under custom 100s threshold")
	}
}

type fakeDB struct {
	available bool
	rows      map[string]SessionStats
}

func (f fakeDB) Available() bool                       { return f.available }
func (f fakeDB) Stats(wt string) (SessionStats, error) { return f.rows[wt], nil }

type fakeGit struct{ ahead map[string]int }

func (f fakeGit) CommitsAhead(slug string) int { return f.ahead[slug] }

func writeWorker(t *testing.T, root, slug string, status string) {
	t.Helper()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nstatus: " + status + "\nslug: " + slug +
		"\ntitle: t\ncreated: 2026-01-01T00:00:00Z\nproject: spore\nagent: opencode\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasks, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wtDir := filepath.Join(root, ".worktrees", slug, ".wt")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, "agent"), []byte("opencode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRun_DBMissingShortCircuits(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	rep, err := Run(now, Config{}, t.TempDir(), fakeDB{available: false}, fakeGit{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Note != "db-missing" || !rep.DBAbsent {
		t.Errorf("rep = %+v", rep)
	}
}

func TestRun_AggregatesPerSlug(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	root := t.TempDir()
	writeWorker(t, root, "stuck1", "active")
	writeWorker(t, root, "ok1", "active")
	writeWorker(t, root, "ahead1", "active")

	wtFor := func(slug string) string { return filepath.Join(root, ".worktrees", slug) }

	db := fakeDB{
		available: true,
		rows: map[string]SessionStats{
			wtFor("stuck1"): {
				LatestSession: "s-stuck", SessionCount: 1, MessagesInLatest: 4,
				AnyMs:  (now.Unix() - 1200) * 1000,
				AsstMs: (now.Unix() - 1200) * 1000,
			},
			wtFor("ok1"): {
				LatestSession: "s-ok", SessionCount: 2, MessagesInLatest: 7,
				AnyMs:  (now.Unix() - 30) * 1000,
				AsstMs: (now.Unix() - 30) * 1000,
			},
			wtFor("ahead1"): {
				LatestSession: "s-ahead", SessionCount: 1, MessagesInLatest: 1,
				AnyMs:  (now.Unix() - 1200) * 1000,
				AsstMs: (now.Unix() - 1200) * 1000,
			},
		},
	}
	git := fakeGit{ahead: map[string]int{"ahead1": 2}}

	rep, err := Run(now, Config{}, root, db, git)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 3 {
		t.Errorf("Total = %d, want 3", rep.Total)
	}
	if rep.OkCount != 2 {
		t.Errorf("OkCount = %d, want 2", rep.OkCount)
	}
	if len(rep.Stuck) != 1 || rep.Stuck[0].Slug != "stuck1" {
		t.Errorf("Stuck = %+v", rep.Stuck)
	}
}

func TestFormatJSON_RoundTripsStuckList(t *testing.T) {
	rep := Report{
		Total: 2, OkCount: 1,
		Stuck: []WorkerStatus{{Slug: "alpha", IdleSeconds: 1200, Reason: "r", Session: "s", SessionsTotal: 1, MessagesInLatest: 4}},
	}
	var buf bytes.Buffer
	if err := FormatJSON(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Stuck   []map[string]any `json:"stuck"`
		OkCount int              `json:"ok_count"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", buf.String(), err)
	}
	if out.Total != 2 || out.OkCount != 1 || len(out.Stuck) != 1 {
		t.Errorf("decoded = %+v", out)
	}
	if got := out.Stuck[0]["slug"]; got != "alpha" {
		t.Errorf("slug = %v", got)
	}
}

func TestFormatJSON_DBMissingNote(t *testing.T) {
	var buf bytes.Buffer
	if err := FormatJSON(&buf, Report{Note: "db-missing"}); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["note"] != "db-missing" {
		t.Errorf("note missing: %v", raw)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[int64]string{
		5:    "5s",
		65:   "1m05s",
		3700: "1h01m",
	}
	for in, want := range cases {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestParseStatsTSV(t *testing.T) {
	in := []byte("latest_session\tsession_count\tlatest_msgs\tany_ms\tasst_ms\n" +
		"abc\t3\t11\t1234567890\t1234567000\n")
	got, err := parseStatsTSV(in)
	if err != nil {
		t.Fatal(err)
	}
	want := SessionStats{LatestSession: "abc", SessionCount: 3, MessagesInLatest: 11, AnyMs: 1234567890, AsstMs: 1234567000}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSqlLit_QuotesEmbeddedQuote(t *testing.T) {
	if got := sqlLit("a'b"); got != "'a''b'" {
		t.Errorf("sqlLit = %q", got)
	}
}
