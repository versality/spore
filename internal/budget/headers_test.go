package budget

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilterRateLimitHeaders(t *testing.T) {
	in := map[string]string{
		"Anthropic-Ratelimit-Tokens-Remaining": "1000",
		"anthropic-ratelimit-requests-limit":   "50",
		"Anthropic-Priority-Input-Tokens":      "tier-2",
		"x-request-id":                         "leak-me-not",
		"content-type":                         "application/json",
	}
	out := filterRateLimitHeaders(in)
	if _, ok := out["x-request-id"]; ok {
		t.Errorf("filter leaked x-request-id")
	}
	if _, ok := out["content-type"]; ok {
		t.Errorf("filter leaked content-type")
	}
	if got := out["anthropic-ratelimit-tokens-remaining"]; got != "1000" {
		t.Errorf("filter dropped tokens-remaining or did not lowercase: %q", got)
	}
	if got := out["anthropic-priority-input-tokens"]; got != "tier-2" {
		t.Errorf("filter dropped anthropic-priority-*: %q", got)
	}
}

func TestAppendHeaderLineRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)
	err := appendHeaderLine(&headerLine{Identity: "runner-a", Headers: map[string]string{"x-request-id": "junk"}})
	if err == nil {
		t.Fatal("expected error when no anthropic-* headers present")
	}
}

func TestAppendHeaderLineRejectsMissingIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)
	err := appendHeaderLine(&headerLine{Headers: map[string]string{
		"anthropic-ratelimit-tokens-remaining": "1",
		"anthropic-ratelimit-tokens-limit":     "10",
	}})
	if err == nil {
		t.Fatal("expected error when identity missing")
	}
}

func TestAppendAndReadLatest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)

	older := time.Now().UTC().Add(-2 * time.Minute)
	newer := time.Now().UTC().Add(-10 * time.Second)
	if err := appendHeaderLine(&headerLine{
		Timestamp: older,
		Identity:  "runner-a",
		Model:     "claude-sonnet-4-6",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "100",
			"anthropic-ratelimit-tokens-limit":     "200",
		},
	}); err != nil {
		t.Fatalf("append1: %v", err)
	}
	if err := appendHeaderLine(&headerLine{
		Timestamp: newer,
		Identity:  "runner-b",
		Model:     "claude-haiku-4-5",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "8000",
			"anthropic-ratelimit-tokens-limit":     "10000",
		},
	}); err != nil {
		t.Fatalf("append2: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, apiHeadersFilename))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("spool mode: got %o want 600", fi.Mode().Perm())
	}

	hl, err := readLatestHeaderLine("")
	if err != nil {
		t.Fatalf("readLatest: %v", err)
	}
	if hl == nil || hl.Identity != "runner-b" {
		t.Fatalf("readLatest: got %+v want runner-b line", hl)
	}

	hl, err = readLatestHeaderLine("runner-a")
	if err != nil {
		t.Fatalf("readLatest filter: %v", err)
	}
	if hl == nil || hl.Identity != "runner-a" {
		t.Fatalf("readLatest filter: got %+v want runner-a line", hl)
	}
}

func TestReadLatestMissingSpool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)
	hl, err := readLatestHeaderLine("")
	if err != nil {
		t.Fatalf("readLatest: %v", err)
	}
	if hl != nil {
		t.Errorf("expected nil for missing spool, got %+v", hl)
	}
}

func TestParseRateLimitReadingPicksWorstBucket(t *testing.T) {
	r, ok := parseRateLimitReading(map[string]string{
		"anthropic-ratelimit-input-tokens-remaining":  "9000",
		"anthropic-ratelimit-input-tokens-limit":      "10000",
		"anthropic-ratelimit-output-tokens-remaining": "100",
		"anthropic-ratelimit-output-tokens-limit":     "10000",
		"anthropic-ratelimit-output-tokens-reset":     "2026-04-26T11:00:00Z",
	})
	if !ok {
		t.Fatal("parseRateLimitReading: no reading")
	}
	wantFrac := 1.0 - 100.0/10000.0
	if math.Abs(r.Frac-wantFrac) > 1e-9 {
		t.Errorf("frac: got %.4f want %.4f", r.Frac, wantFrac)
	}
	if r.Bucket != "output-tokens" {
		t.Errorf("bucket: got %q want output-tokens", r.Bucket)
	}
	if r.ResetAt == nil {
		t.Error("expected ResetAt populated for chosen bucket")
	}
}

func TestParseRateLimitReadingNoUsableHeaders(t *testing.T) {
	if _, ok := parseRateLimitReading(map[string]string{}); ok {
		t.Error("expected no reading from empty headers")
	}
	if _, ok := parseRateLimitReading(map[string]string{
		"anthropic-ratelimit-tokens-remaining": "1",
	}); ok {
		t.Error("expected no reading when only -remaining present")
	}
}

func TestResolveModeExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)

	t.Setenv("AGENT_BUDGET_MODE", "api")
	mode, err := resolveMode(time.Now())
	if err != nil || mode != "api" {
		t.Errorf("explicit api: got %q (err=%v)", mode, err)
	}

	t.Setenv("AGENT_BUDGET_MODE", "subscription")
	mode, err = resolveMode(time.Now())
	if err != nil || mode != "subscription" {
		t.Errorf("explicit subscription: got %q (err=%v)", mode, err)
	}

	t.Setenv("AGENT_BUDGET_MODE", "garbage")
	if _, err := resolveMode(time.Now()); err == nil {
		t.Error("expected error for invalid AGENT_BUDGET_MODE")
	}
}

func TestResolveModeAutoDetect(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)
	t.Setenv("AGENT_BUDGET_MODE", "")

	now := time.Now().UTC()
	mode, err := resolveMode(now)
	if err != nil || mode != "subscription" {
		t.Errorf("no spool: got %q (err=%v) want subscription", mode, err)
	}

	if err := appendHeaderLine(&headerLine{
		Timestamp: now.Add(-1 * time.Hour),
		Identity:  "runner-a",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "1",
			"anthropic-ratelimit-tokens-limit":     "10",
		},
	}); err != nil {
		t.Fatalf("append old: %v", err)
	}
	mode, err = resolveMode(now)
	if err != nil || mode != "subscription" {
		t.Errorf("stale spool: got %q want subscription (only recent lines flip)", mode)
	}

	if err := appendHeaderLine(&headerLine{
		Timestamp: now.Add(-30 * time.Second),
		Identity:  "runner-a",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "5",
			"anthropic-ratelimit-tokens-limit":     "10",
		},
	}); err != nil {
		t.Fatalf("append fresh: %v", err)
	}
	mode, err = resolveMode(now)
	if err != nil || mode != "api" {
		t.Errorf("fresh spool: got %q want api", mode)
	}
}

func TestRefreshAPIModeMatchesHeaderRemaining(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateDir)
	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_MODE", "api")
	t.Setenv("AGENT_BUDGET_IDENTITY", "runner-a")

	now := time.Now().UTC()
	reset := now.Add(45 * time.Second).UTC().Truncate(time.Second)
	if err := appendHeaderLine(&headerLine{
		Timestamp: now.Add(-5 * time.Second),
		Identity:  "runner-a",
		Model:     "claude-haiku-4-5",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining":        "8000",
			"anthropic-ratelimit-tokens-limit":            "10000",
			"anthropic-ratelimit-tokens-reset":            reset.Format(time.RFC3339),
			"anthropic-ratelimit-input-tokens-remaining":  "9000",
			"anthropic-ratelimit-input-tokens-limit":      "10000",
			"anthropic-ratelimit-output-tokens-remaining": "9500",
			"anthropic-ratelimit-output-tokens-limit":     "10000",
			"anthropic-ratelimit-requests-remaining":      "100",
			"anthropic-ratelimit-requests-limit":          "100",
		},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Mode != "api" {
		t.Errorf("mode: got %q want api", s.Mode)
	}
	if s.Short.Source != "api-headers" {
		t.Errorf("short.source: got %q want api-headers", s.Short.Source)
	}
	wantFrac := 1.0 - 8000.0/10000.0
	if math.Abs(s.Short.Frac-wantFrac) > 1e-9 {
		t.Errorf("short.frac: got %.4f want %.4f", s.Short.Frac, wantFrac)
	}
	if s.Short.TokensRemaining == nil || *s.Short.TokensRemaining != 8000 {
		t.Errorf("tokens_remaining: %+v want 8000", s.Short.TokensRemaining)
	}
	if s.Short.TokensLimit == nil || *s.Short.TokensLimit != 10000 {
		t.Errorf("tokens_limit: %+v want 10000", s.Short.TokensLimit)
	}
	if s.Short.TokensBucket != "tokens" {
		t.Errorf("tokens_bucket: got %q want tokens (worst bucket)", s.Short.TokensBucket)
	}
	if s.Short.ResetAt == nil || !s.Short.ResetAt.Equal(reset) {
		t.Errorf("reset_at: got %+v want %v", s.Short.ResetAt, reset)
	}
	if s.Long.Source != "transcript-est" {
		t.Errorf("long.source: got %q want transcript-est", s.Long.Source)
	}
}

func TestCaptureFromStdin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_STATE_DIR", dir)

	payload, _ := json.Marshal(headerLine{
		Identity: "runner-a",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "100",
			"anthropic-ratelimit-tokens-limit":     "200",
			"x-request-id":                         "should-be-filtered",
		},
	})

	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		_, _ = io.Copy(w, bytes.NewReader(payload))
		w.Close()
	}()
	defer func() { os.Stdin = origStdin }()

	if err := Capture(); err != nil {
		t.Fatalf("capture: %v", err)
	}

	hl, err := readLatestHeaderLine("")
	if err != nil {
		t.Fatalf("readLatest: %v", err)
	}
	if hl == nil || hl.Identity != "runner-a" {
		t.Fatalf("readLatest: %+v", hl)
	}
	if _, leaked := hl.Headers["x-request-id"]; leaked {
		t.Error("capture leaked non-anthropic header into spool")
	}
}
