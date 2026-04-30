package budget

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCostForUsageOpus(t *testing.T) {
	got, total := costForUsage("claude-opus-4-7", &usageBlock{
		InputTokens:              1_000_000,
		OutputTokens:             1_000_000,
		CacheReadInputTokens:     1_000_000,
		CacheCreationInputTokens: 1_000_000,
	})
	want := 5.0 + 25.0 + 0.50 + 6.25
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("opus cost: got %.6f want %.6f", got, want)
	}
	if total != 4_000_000 {
		t.Errorf("total tokens: got %d want %d", total, 4_000_000)
	}
}

func TestCostForUsageUnknownFallsBackToSonnet(t *testing.T) {
	got, _ := costForUsage("claude-newmodel-zzz", &usageBlock{InputTokens: 1_000_000})
	if math.Abs(got-3.0) > 1e-9 {
		t.Errorf("unknown model fallback: got %.6f want 3.0", got)
	}
}

func TestPricingTableLoaded(t *testing.T) {
	if pricingErr != nil {
		t.Fatalf("pricing.toml parse error: %v", pricingErr)
	}
	if _, ok := pricing["claude-opus-4-7"]; !ok {
		t.Errorf("pricing table missing claude-opus-4-7")
	}
	if fallbackPricing.InputPerMTok != 3.0 {
		t.Errorf("fallback InputPerMTok: got %.4f want 3.0", fallbackPricing.InputPerMTok)
	}
}

func TestAdviceBands(t *testing.T) {
	cases := []struct {
		short, long float64
		want        string
	}{
		{0, 0, "ok"},
		{0.79, 0.79, "ok"},
		{0.80, 0, "tighten"},
		{0.89, 0.79, "tighten"},
		{0.90, 0, "ration"},
		{0, 0.80, "ration"},
		{1.5, 0.1, "ration"},
	}
	for _, c := range cases {
		if got := adviceFor(c.short, c.long); got != c.want {
			t.Errorf("adviceFor(%.2f, %.2f) = %q, want %q", c.short, c.long, got, c.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0m"},
		{30 * time.Second, "0m"},
		{45 * time.Minute, "45m"},
		{5 * time.Hour, "5h"},
		{1*time.Hour + 47*time.Minute, "1h47m"},
		{4 * 24 * time.Hour, "4d"},
		{(7*24 + 3) * time.Hour, "7d3h"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRefreshAndAggregate(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "myproj")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	old := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339Nano)
	tooOld := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	transcript := `{"type":"permission-mode"}
{"type":"assistant","requestId":"req_a","timestamp":"` + recent + `","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":1000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":1000}}}
{"type":"assistant","requestId":"req_a","timestamp":"` + recent + `","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":1000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":1000}}}
{"type":"assistant","requestId":"req_b","timestamp":"` + old + `","message":{"id":"msg_b","model":"claude-haiku-4-5","usage":{"input_tokens":2000,"output_tokens":2000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"assistant","requestId":"req_c","timestamp":"` + tooOld + `","message":{"id":"msg_c","model":"claude-opus-4-7","usage":{"input_tokens":99999999,"output_tokens":0}}}
`
	if err := os.WriteFile(filepath.Join(projects, "session.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("AGENT_BUDGET_CREDS", filepath.Join(dir, "no-such-credentials.json"))

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	computeAggregates(s, time.Now().UTC())

	if s.Short.MessageCount != 1 {
		t.Errorf("short msg count: got %d want 1 (only req_a within 5h)", s.Short.MessageCount)
	}
	wantShort := (1000.0*5 + 1000.0*25 + 1000.0*6.25 + 1000.0*0.50) / 1e6
	if math.Abs(s.Short.CostUSD-wantShort) > 1e-9 {
		t.Errorf("short cost: got %.6f want %.6f", s.Short.CostUSD, wantShort)
	}

	if s.Long.MessageCount != 2 {
		t.Errorf("long msg count: got %d want 2 (req_a + req_b within 7d, req_c excluded)", s.Long.MessageCount)
	}
	wantLong := wantShort + (2000.0*1+2000.0*5)/1e6
	if math.Abs(s.Long.CostUSD-wantLong) > 1e-9 {
		t.Errorf("long cost: got %.6f want %.6f", s.Long.CostUSD, wantLong)
	}

	statePath := filepath.Join(dir, "state", "state.json")
	fi, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode: got %o want 600", fi.Mode().Perm())
	}

	if err := Refresh(); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	s2, err := loadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Cache) != 1 {
		t.Errorf("expected one cached file, got %d", len(s2.Cache))
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)
	now := time.Now().UTC().Truncate(time.Microsecond)
	reset := now.Add(2 * time.Hour)
	rem := int64(8000)
	lim := int64(10000)
	want := &state{
		Mode:      "api",
		UpdatedAt: now,
		Short: windowState{
			DurationSeconds: int(shortWindow.Seconds()),
			Frac:            0.42,
			ResetAt:         &reset,
			Source:          "api-headers",
			TokensRemaining: &rem,
			TokensLimit:     &lim,
			TokensBucket:    "tokens",
		},
		Long: windowState{
			DurationSeconds: int(longWindow.Seconds()),
			CostUSD:         12.50,
			CapUSD:          2000.0,
			Frac:            0.00625,
			MessageCount:    3,
			Source:          "transcript-est",
		},
		Advice: "ok",
		Cache: map[string]*fileEntry{
			"/tmp/session.jsonl": {SizeBytes: 1024, MtimeNs: now.UnixNano(), Messages: nil},
		},
		UsageSnapshot: &usageSnapshot{
			FetchedAt: now,
			Short:     usageWindow{Utilization: 42.0, ResetsAt: reset.Format(time.RFC3339Nano)},
			Long:      usageWindow{Utilization: 12.5, ResetsAt: reset.Format(time.RFC3339Nano)},
		},
	}
	if err := writeState(want); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	got, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got.Mode != want.Mode || got.Advice != want.Advice {
		t.Errorf("mode/advice mismatch: got %+v", got)
	}
	if got.Short.Source != "api-headers" || got.Short.TokensBucket != "tokens" {
		t.Errorf("short window mismatch: got %+v", got.Short)
	}
	if got.Short.TokensRemaining == nil || *got.Short.TokensRemaining != 8000 {
		t.Errorf("tokens_remaining round-trip: %+v", got.Short.TokensRemaining)
	}
	if got.UsageSnapshot == nil || got.UsageSnapshot.Short.Utilization != 42.0 {
		t.Errorf("usage_snapshot round-trip: %+v", got.UsageSnapshot)
	}
	if math.Abs(got.Long.CostUSD-12.50) > 1e-9 {
		t.Errorf("long cost round-trip: %.6f want 12.50", got.Long.CostUSD)
	}

	if err := writeState(got); err != nil {
		t.Fatalf("re-writeState: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	got2, err := loadState()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeState(got2); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("state.json bytes drifted across read+write cycle\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestBandFor(t *testing.T) {
	cases := []struct {
		frac, tighten, ration float64
		want                  string
	}{
		{0, 0.7, 0.9, "ok"},
		{0.69, 0.7, 0.9, "ok"},
		{0.70, 0.7, 0.9, "tighten"},
		{0.89, 0.7, 0.9, "tighten"},
		{0.90, 0.7, 0.9, "ration"},
		{1.5, 0.7, 0.9, "ration"},
	}
	for _, c := range cases {
		if got := bandFor(c.frac, c.tighten, c.ration); got != c.want {
			t.Errorf("bandFor(%.2f, %.2f, %.2f) = %q want %q", c.frac, c.tighten, c.ration, got, c.want)
		}
	}
}

func TestUpdateMarkersTransitions(t *testing.T) {
	dir := t.TempDir()
	must := func(name string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected marker %s to exist: %v", name, err)
		}
	}
	mustAbsent := func(name string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("expected marker %s to be absent", name)
		}
	}

	fresh, err := updateMarkers(dir, "short", "tighten")
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Errorf("first tighten: want fresh=true")
	}
	must("short-tighten")
	mustAbsent("short-ration")

	fresh, _ = updateMarkers(dir, "short", "tighten")
	if fresh {
		t.Errorf("repeat tighten: want fresh=false")
	}

	fresh, _ = updateMarkers(dir, "short", "ration")
	if !fresh {
		t.Errorf("crossing into ration: want fresh=true")
	}
	must("short-tighten")
	must("short-ration")

	fresh, _ = updateMarkers(dir, "short", "ration")
	if fresh {
		t.Errorf("repeat ration: want fresh=false")
	}

	fresh, _ = updateMarkers(dir, "short", "tighten")
	if fresh {
		t.Errorf("ration -> tighten dip: want fresh=false (tighten marker held)")
	}
	must("short-tighten")
	mustAbsent("short-ration")

	fresh, _ = updateMarkers(dir, "short", "ok")
	if fresh {
		t.Errorf("drop to ok must not be fresh")
	}
	mustAbsent("short-tighten")
	mustAbsent("short-ration")

	fresh, _ = updateMarkers(dir, "short", "tighten")
	if !fresh {
		t.Errorf("re-cross after ok reset: want fresh=true")
	}
}

func TestReminderTextBindingHints(t *testing.T) {
	now := time.Now().UTC()
	short := windowState{Frac: 0.82, OldestEventAt: ptrTime(now.Add(-3*time.Hour - 48*time.Minute))}
	long := windowState{Frac: 0.18, OldestEventAt: ptrTime(now.Add(-1 * 24 * time.Hour))}
	s := &state{Short: short, Long: long}

	got := reminderTextFor(s, "tighten")
	if !strings.HasPrefix(got, "AGENT BUDGET (tighten):") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "short=82%") {
		t.Errorf("missing short pct: %q", got)
	}
	if !strings.Contains(got, "(resets in") {
		t.Errorf("expected reset hint on binding short window: %q", got)
	}
	if strings.Count(got, "(resets in") != 1 {
		t.Errorf("expected exactly one reset hint (binding only), got: %q", got)
	}
	if !strings.Contains(got, "long=18%") {
		t.Errorf("missing long pct: %q", got)
	}
	if !strings.Contains(got, "runner") {
		t.Errorf("missing tighten advice tail (runner mention): %q", got)
	}

	rationShort := windowState{Frac: 0.92, OldestEventAt: ptrTime(now.Add(-4*time.Hour - 22*time.Minute))}
	rationLong := windowState{Frac: 0.22, OldestEventAt: ptrTime(now.Add(-2 * 24 * time.Hour))}
	r := &state{Short: rationShort, Long: rationLong}
	gotR := reminderTextFor(r, "ration")
	if !strings.Contains(gotR, "AGENT BUDGET (ration):") {
		t.Errorf("missing ration header: %q", gotR)
	}
	if !strings.Contains(gotR, "Stop spawning runners") {
		t.Errorf("missing ration advice tail: %q", gotR)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestStopHookFiresOnceThenSilent(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "myproj")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	transcript := `{"type":"assistant","requestId":"req_a","timestamp":"` + recent + `","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":1000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":1000}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, "session.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("AGENT_BUDGET_CREDS", filepath.Join(dir, "no-such-credentials.json"))
	// Synthetic transcript above costs ~$0.037; pick a tiny short cap so any
	// spend trips at least tighten without needing realistic token counts.
	t.Setenv("AGENT_BUDGET_SHORT_CAP", "0.045")
	t.Setenv("AGENT_BUDGET_LONG_CAP", "10000")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	rc := StopHook()
	w.Close()
	os.Stderr = origStderr

	if rc != 2 {
		t.Errorf("first stop-hook: rc=%d want 2 (fresh tighten crossing)", rc)
	}
	stderr, _ := readAll(r)
	if !strings.Contains(stderr, "AGENT BUDGET") {
		t.Errorf("expected reminder on stderr, got %q", stderr)
	}

	if rc2 := StopHook(); rc2 != 0 {
		t.Errorf("second stop-hook: rc=%d want 0 (idempotent while held)", rc2)
	}

	markersAt := filepath.Join(dir, "state", "markers")
	matches, _ := filepath.Glob(filepath.Join(markersAt, "*"))
	if len(matches) == 0 {
		t.Fatalf("expected at least one marker after fresh crossing")
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
	if rc3 := StopHook(); rc3 != 2 {
		t.Errorf("after marker reset: rc=%d want 2 (re-armed)", rc3)
	}
}

func TestStopHookUnderCapExitsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "projects-empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("AGENT_BUDGET_CREDS", filepath.Join(dir, "no-such-credentials.json"))
	t.Setenv("AGENT_BUDGET_SHORT_CAP", "1000")
	t.Setenv("AGENT_BUDGET_LONG_CAP", "10000")

	if rc := StopHook(); rc != 0 {
		t.Errorf("under-cap stop-hook: rc=%d want 0", rc)
	}
}

func readAll(r *os.File) (string, error) {
	defer r.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return b.String(), nil
}

