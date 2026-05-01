package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeCredentials(t *testing.T, path string, accessToken, refreshToken string, expiresAt int64) {
	t.Helper()
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  accessToken,
			"refreshToken": refreshToken,
			"expiresAt":    expiresAt,
			"scopes":       []string{"user:profile"},
		},
		"mcpOAuth": map[string]any{"preserved": true},
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func swapURLs(t *testing.T, usage, token string) {
	t.Helper()
	prevU, prevT := oauthUsageURL, oauthTokenURL
	oauthUsageURL = usage
	oauthTokenURL = token
	t.Cleanup(func() {
		oauthUsageURL = prevU
		oauthTokenURL = prevT
	})
}

func TestParseUsageTimestampMicroseconds(t *testing.T) {
	t.Parallel()
	in := "2026-04-26T17:00:00.830978+00:00"
	got, err := parseUsageTimestamp(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 4, 26, 17, 0, 0, 830978000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("parseUsageTimestamp: got %v want %v", got, want)
	}
}

func TestUsageWindowStateMapping(t *testing.T) {
	t.Parallel()
	uw := usageWindow{Utilization: 42.5, ResetsAt: "2026-04-26T20:00:00+00:00"}
	w := usageWindowState(uw, longWindow, false)
	if math.Abs(w.Frac-0.425) > 1e-9 {
		t.Errorf("frac: got %.4f want 0.425", w.Frac)
	}
	if w.Source != "usage" {
		t.Errorf("source: got %q want usage", w.Source)
	}
	if w.ResetAt == nil || w.ResetAt.IsZero() {
		t.Errorf("ResetAt empty: %+v", w.ResetAt)
	}
	if w.DurationSeconds != int(longWindow.Seconds()) {
		t.Errorf("duration: got %d want %d", w.DurationSeconds, int(longWindow.Seconds()))
	}

	stale := usageWindowState(uw, shortWindow, true)
	if stale.Source != "usage-stale" {
		t.Errorf("stale source: got %q want usage-stale", stale.Source)
	}
}

func TestFetchUsageHappyPath(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, ".credentials.json")
	future := time.Now().Add(2 * time.Hour).UnixMilli()
	writeCredentials(t, creds, "at-fresh", "rt-fresh", future)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer at-fresh" {
			t.Errorf("auth header: %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != oauthBetaHeader {
			t.Errorf("anthropic-beta: %q", got)
		}
		_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":7,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":42,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")
	t.Setenv("AGENT_BUDGET_CREDS", creds)

	ur, err := fetchUsage(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if math.Abs(ur.FiveHour.Utilization-7) > 1e-9 {
		t.Errorf("five_hour.utilization: %v", ur.FiveHour.Utilization)
	}
	if ur.SevenDay.ResetsAt == "" {
		t.Errorf("seven_day.resets_at missing")
	}
}

func TestFetchUsageRefreshOn401(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, ".credentials.json")
	expired := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeCredentials(t, creds, "at-stale", "rt-good", expired)

	var usageCalls int32
	usage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&usageCalls, 1)
		auth := r.Header.Get("Authorization")
		if n == 1 {
			if auth != "Bearer at-stale" {
				t.Errorf("first call auth: %q", auth)
			}
			http.Error(w, "expired", http.StatusUnauthorized)
			return
		}
		if auth != "Bearer at-refreshed" {
			t.Errorf("second call auth: %q", auth)
		}
		_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":11,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":33,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`)
	}))
	defer usage.Close()

	var tokenCalls int32
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["grant_type"] != "refresh_token" || req["refresh_token"] != "rt-good" {
			t.Errorf("unexpected refresh body: %v", req)
		}
		_, _ = fmt.Fprintln(w, `{"access_token":"at-refreshed","refresh_token":"rt-rolled","expires_in":3600,"scope":"user:profile user:inference"}`)
	}))
	defer token.Close()

	swapURLs(t, usage.URL, token.URL)
	t.Setenv("AGENT_BUDGET_CREDS", creds)

	ur, err := fetchUsage(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if math.Abs(ur.FiveHour.Utilization-11) > 1e-9 {
		t.Errorf("five_hour.utilization: %v", ur.FiveHour.Utilization)
	}
	if got := atomic.LoadInt32(&usageCalls); got != 2 {
		t.Errorf("usage call count: got %d want 2", got)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 1 {
		t.Errorf("token call count: got %d want 1", got)
	}

	cf, err := loadCredentials(creds)
	if err != nil {
		t.Fatalf("reload creds: %v", err)
	}
	if cf.oauth.AccessToken != "at-refreshed" {
		t.Errorf("access token not persisted: %q", cf.oauth.AccessToken)
	}
	if cf.oauth.RefreshToken != "rt-rolled" {
		t.Errorf("refresh token not persisted: %q", cf.oauth.RefreshToken)
	}
	if _, ok := cf.raw["mcpOAuth"]; !ok {
		t.Error("write-back lost mcpOAuth")
	}

	fi, err := os.Stat(creds)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("creds mode: got %o want 600", fi.Mode().Perm())
	}
}

func TestFetchUsageNoCreds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BUDGET_CREDS", filepath.Join(dir, "missing.json"))
	if _, err := fetchUsage(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error for missing creds")
	}
}

func TestRefreshSubscriptionPrefersUsage(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	projects := filepath.Join(dir, "projects", "myproj")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	transcript := `{"type":"assistant","requestId":"req_a","timestamp":"` + recent + `","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":1000}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, "session.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":82,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":40,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Mode != "subscription" {
		t.Errorf("mode: got %q want subscription", s.Mode)
	}
	if s.Short.Source != "usage" {
		t.Errorf("short.source: got %q want usage", s.Short.Source)
	}
	if math.Abs(s.Short.Frac-0.82) > 1e-9 {
		t.Errorf("short.frac: got %.4f want 0.82", s.Short.Frac)
	}
	if s.Long.Source != "usage" {
		t.Errorf("long.source: got %q want usage", s.Long.Source)
	}
	if math.Abs(s.Long.Frac-0.40) > 1e-9 {
		t.Errorf("long.frac: got %.4f want 0.40", s.Long.Frac)
	}
	if s.UsageSnapshot == nil {
		t.Fatal("usage_snapshot not persisted")
	}
	if s.Advice != "tighten" {
		t.Errorf("advice: got %q want tighten (short=82%%)", s.Advice)
	}
}

func TestRefreshSubscriptionFallsBackToTranscript(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	projects := filepath.Join(dir, "projects", "myproj")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339Nano)
	transcript := `{"type":"assistant","requestId":"req_a","timestamp":"` + recent + `","message":{"id":"msg_a","model":"claude-opus-4-7","usage":{"input_tokens":1000,"output_tokens":1000}}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, "session.jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Mode != "subscription" {
		t.Errorf("mode: got %q want subscription", s.Mode)
	}
	if s.Short.Source != "transcript" {
		t.Errorf("short.source: got %q want transcript fallback", s.Short.Source)
	}
	if s.Short.MessageCount != 1 {
		t.Errorf("transcript not aggregated: count=%d", s.Short.MessageCount)
	}
	if s.UsageSnapshot != nil {
		t.Errorf("usage_snapshot should not be set on cold-start failure: %+v", s.UsageSnapshot)
	}
}

func TestRefreshKeepsStaleSnapshotWhenUsageDown(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":50,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":25,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`)
			return
		}
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))
	t.Setenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC", "0")

	if err := Refresh(); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := Refresh(); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.UsageSnapshot == nil {
		t.Fatal("usage_snapshot dropped after second-call failure")
	}
	if !s.UsageSnapshot.Stale {
		t.Errorf("expected stale=true after failure: %+v", s.UsageSnapshot)
	}
	if s.Short.Source != "usage-stale" {
		t.Errorf("short.source: got %q want usage-stale", s.Short.Source)
	}
	if math.Abs(s.Short.Frac-0.50) > 1e-9 {
		t.Errorf("short.frac: got %.4f want 0.50 (cached)", s.Short.Frac)
	}
}

func TestRefreshThrottlesUsageHits(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":12,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":34,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))

	if err := Refresh(); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := Refresh(); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("usage hits within freshness gate: got %d want 1", got)
	}

	t.Setenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC", "0")
	if err := Refresh(); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("usage hits with gate=0: got %d want 2", got)
	}
}

func TestNormalizeTier(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"max", "max"},
		{"Max", "max"},
		{"pro", "pro"},
		{"PRO", "pro"},
		{"team", "team"},
		{"free", "free"},
		{"", "free"},
		{"enterprise", "enterprise"},
	}
	for _, c := range cases {
		if got := normalizeTier(c.in); got != c.want {
			t.Errorf("normalizeTier(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestActiveTier(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, ".credentials.json")
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "at-test",
			"subscriptionType": "max",
		},
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(creds, b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BUDGET_CREDS", creds)

	// Capture stdout.
	r, w, _ := os.Pipe()
	origOut := os.Stdout
	os.Stdout = w
	aerr := ActiveTier()
	w.Close()
	os.Stdout = origOut

	if aerr != nil {
		t.Fatalf("ActiveTier: %v", aerr)
	}
	out := make([]byte, 64)
	n, _ := r.Read(out)
	got := string(out[:n])
	if got != "max\n" {
		t.Errorf("ActiveTier output: got %q want %q", got, "max\n")
	}
}

func TestRefreshAPIModeSkipsUsage(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")

	calls := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "should not be called", http.StatusTeapot)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))
	t.Setenv("AGENT_BUDGET_MODE", "api")
	t.Setenv("AGENT_BUDGET_IDENTITY", "runner-a")

	now := time.Now().UTC()
	if err := appendHeaderLine(&headerLine{
		Timestamp: now.Add(-5 * time.Second),
		Identity:  "runner-a",
		Headers: map[string]string{
			"anthropic-ratelimit-tokens-remaining": "9000",
			"anthropic-ratelimit-tokens-limit":     "10000",
		},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("usage server should not be hit in api mode, got %d calls", got)
	}
}

func writeAccountFile(t *testing.T, dir, name, accessToken, refreshToken string) {
	t.Helper()
	writeAccountFileWithTier(t, dir, name, accessToken, refreshToken, "max")
}

func writeAccountFileWithTier(t *testing.T, dir, name, accessToken, refreshToken, tier string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"accessToken":      accessToken,
		"refreshToken":     refreshToken,
		"expiresAt":        time.Now().Add(2 * time.Hour).UnixMilli(),
		"subscriptionType": tier,
		"rateLimitTier":    "default",
		"scopes":           []string{"user:profile"},
	}
	b, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshMultiAccountAggregation(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	accountsD := filepath.Join(dir, "accounts")

	writeAccountFile(t, accountsD, "primary", "at-primary", "rt-primary")
	writeAccountFile(t, accountsD, "secondary", "at-secondary", "rt-secondary")

	usageByToken := map[string]string{
		"at-primary":   `{"five_hour":{"utilization":80,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":60,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`,
		"at-secondary": `{"five_hour":{"utilization":20,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":40,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if resp, ok := usageByToken[auth]; ok {
			_, _ = fmt.Fprintln(w, resp)
		} else {
			http.Error(w, "unknown token", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", accountsD)
	t.Setenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC", "0")

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	computeAggregates(s, time.Now().UTC())

	if len(s.AccountSnapshots) != 2 {
		t.Fatalf("account_snapshots len: got %d want 2", len(s.AccountSnapshots))
	}
	if math.Abs(s.AccountSnapshots["primary"].Short.Utilization-80) > 1e-9 {
		t.Errorf("primary short: got %v want 80", s.AccountSnapshots["primary"].Short.Utilization)
	}
	if math.Abs(s.AccountSnapshots["secondary"].Short.Utilization-20) > 1e-9 {
		t.Errorf("secondary short: got %v want 20", s.AccountSnapshots["secondary"].Short.Utilization)
	}

	if math.Abs(s.Short.Frac-0.50) > 1e-9 {
		t.Errorf("aggregate short.frac: got %.4f want 0.50", s.Short.Frac)
	}
	if math.Abs(s.Long.Frac-0.50) > 1e-9 {
		t.Errorf("aggregate long.frac: got %.4f want 0.50", s.Long.Frac)
	}
	if s.Short.Source != "usage-aggregate" {
		t.Errorf("short.source: got %q want usage-aggregate", s.Short.Source)
	}
	if s.Advice != "ok" {
		t.Errorf("advice: got %q want ok (avg short=50%%)", s.Advice)
	}
}

func TestRefreshPopulatesTier(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	accountsD := filepath.Join(dir, "accounts")

	writeAccountFileWithTier(t, accountsD, "primary", "at-primary", "rt-primary", "max")
	writeAccountFileWithTier(t, accountsD, "secondary", "at-secondary", "rt-secondary", "team")

	usageByToken := map[string]string{
		"at-primary":   `{"five_hour":{"utilization":10,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":5,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`,
		"at-secondary": `{"five_hour":{"utilization":20,"resets_at":"2026-04-26T17:00:00.000000+00:00"},"seven_day":{"utilization":10,"resets_at":"2026-04-26T20:00:00.000000+00:00"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if resp, ok := usageByToken[auth]; ok {
			_, _ = fmt.Fprintln(w, resp)
		} else {
			http.Error(w, "unknown", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", accountsD)
	t.Setenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC", "0")

	if err := Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	s, err := loadState()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if s.AccountSnapshots["primary"].Tier != "max" {
		t.Errorf("primary tier: got %q want max", s.AccountSnapshots["primary"].Tier)
	}
	if s.AccountSnapshots["secondary"].Tier != "team" {
		t.Errorf("secondary tier: got %q want team", s.AccountSnapshots["secondary"].Tier)
	}
}

func TestTier(t *testing.T) {
	dir := t.TempDir()
	accountsD := filepath.Join(dir, "accounts")

	writeAccountFileWithTier(t, accountsD, "primary", "at-1", "rt-1", "max")
	writeAccountFileWithTier(t, accountsD, "secondary", "at-2", "rt-2", "team")

	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", accountsD)

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = pw
	terr := Tier()
	pw.Close()
	os.Stdout = origStdout

	if terr != nil {
		t.Fatalf("Tier: %v", terr)
	}

	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, rerr := pr.Read(b)
		if n > 0 {
			buf.Write(b[:n])
		}
		if rerr != nil {
			break
		}
	}
	pr.Close()

	var tiers map[string]string
	if err := json.Unmarshal([]byte(buf.String()), &tiers); err != nil {
		t.Fatalf("parse tier output: %v (raw: %q)", err, buf.String())
	}
	if tiers["primary"] != "max" {
		t.Errorf("primary: got %q want max", tiers["primary"])
	}
	if tiers["secondary"] != "team" {
		t.Errorf("secondary: got %q want team", tiers["secondary"])
	}
}

func TestTierMissingDir(t *testing.T) {
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(t.TempDir(), "nonexistent"))

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = pw
	terr := Tier()
	pw.Close()
	os.Stdout = origStdout
	pr.Close()

	if terr != nil {
		t.Fatalf("Tier with missing dir: %v", terr)
	}
}

func TestQueryAutoRefreshesStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	stateD := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateD, 0o700); err != nil {
		t.Fatal(err)
	}

	staleAt := time.Now().UTC().Add(-(queryAutoRefreshAge + time.Minute))
	initial := &state{
		Cache: map[string]*fileEntry{},
		UsageSnapshot: &usageSnapshot{
			FetchedAt: staleAt,
			Short:     usageWindow{Utilization: 100.0, ResetsAt: "2099-01-01T00:00:00.000000+00:00"},
			Long:      usageWindow{Utilization: 100.0, ResetsAt: "2099-01-01T00:00:00.000000+00:00"},
		},
	}
	b, err := json.MarshalIndent(initial, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateD, "state.json"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = fmt.Fprintln(w, `{"five_hour":{"utilization":50,"resets_at":"2099-01-01T00:00:00.000000+00:00"},"seven_day":{"utilization":25,"resets_at":"2099-01-01T00:00:00.000000+00:00"}}`)
	}))
	defer srv.Close()
	swapURLs(t, srv.URL, srv.URL+"/token-unused")

	creds := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, creds, "at-fresh", "rt-fresh", time.Now().Add(2*time.Hour).UnixMilli())

	t.Setenv("AGENT_BUDGET_PROJECTS", filepath.Join(dir, "projects-empty"))
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateD)
	t.Setenv("AGENT_BUDGET_CREDS", creds)
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", filepath.Join(dir, "no-accounts"))

	pr, pw, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	origStdout := os.Stdout
	os.Stdout = pw
	qErr := Query()
	pw.Close()
	os.Stdout = origStdout
	pr.Close()

	if qErr != nil {
		t.Fatalf("Query: %v", qErr)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("usage server calls: got %d want 1 (stale snapshot should trigger re-fetch)", got)
	}

	s, lerr := loadState()
	if lerr != nil {
		t.Fatalf("load state: %v", lerr)
	}
	if s.UsageSnapshot == nil {
		t.Fatal("usage_snapshot missing after query refresh")
	}
	if math.Abs(s.UsageSnapshot.Short.Utilization-50.0) > 1e-9 {
		t.Errorf("short utilization after refresh: got %.1f want 50.0", s.UsageSnapshot.Short.Utilization)
	}
	if time.Since(s.UsageSnapshot.FetchedAt) > 5*time.Second {
		t.Errorf("FetchedAt not updated: %v", s.UsageSnapshot.FetchedAt)
	}
}
