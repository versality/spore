// Subscription-mode primary signal: the OAuth /usage endpoint.
//
// The Anthropic console exposes a per-account rolling-window
// utilization surface at GET /api/oauth/usage (auth: Bearer
// access_token from ~/.claude/.credentials.json, anthropic-beta:
// oauth-2025-04-20). It counts every claude-code session against the
// operator's account regardless of host. Subscription mode prefers it
// over local transcript scraping so all consumers see the same picture.
//
// Response shape (only fields we consume):
//
//	{
//	  "five_hour": { "utilization": 5.0, "resets_at": "<RFC3339>" },
//	  "seven_day": { "utilization": 43.0, "resets_at": "<RFC3339>" }
//	}
//
// `utilization` is a percent (0-100); `resets_at` is the rolling-window
// reset clock, microsecond-precision UTC. Other fields (per-model and
// per-app sub-buckets) are ignored - the consumer only cares about the
// account-wide cap that drives 429s.
//
// Refresh strategy: read the current access token; if /usage returns
// 401 and a refresh token is present, refresh once and retry. The
// refreshed credentials are written back via mktemp+rename, the same
// dance claude-code itself uses. Any other HTTP/network failure
// short-circuits to the transcript fallback in Refresh.

package budget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	oauthClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthScope       = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	oauthBetaHeader  = "oauth-2025-04-20"
	usageHTTPTimeout = 8 * time.Second
	tokenHTTPTimeout = 15 * time.Second
)

// Overridable for tests.
var (
	oauthTokenURL = "https://platform.claude.com/v1/oauth/token"
	oauthUsageURL = "https://api.anthropic.com/api/oauth/usage"
)

type oauthBlock struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken,omitempty"`
	ExpiresAt        int64    `json:"expiresAt,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	SubscriptionType string   `json:"subscriptionType,omitempty"`
	RateLimitTier    string   `json:"rateLimitTier,omitempty"`
}

type credentialsFile struct {
	path  string
	raw   map[string]json.RawMessage
	oauth oauthBlock
}

func oauthCredsPath() string {
	if p := os.Getenv("AGENT_BUDGET_CREDS"); p != "" {
		return p
	}
	// Resolve like claude-code itself: an explicit CLAUDE_CONFIG_DIR wins,
	// then the XDG location claude-code migrated to, then the legacy
	// ~/.claude path. claude-code stopped refreshing the legacy file after
	// migrating to ~/.config/claude, so defaulting to it fed /usage an
	// expired token - which the endpoint answers with a 429, not a 401, so
	// the token-refresh path never fired and the snapshot stuck stale.
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".credentials.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	if xdg := filepath.Join(configHome, "claude", ".credentials.json"); fileExists(xdg) {
		return xdg
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func loadCredentials(path string) (*credentialsFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cf := &credentialsFile{path: path, raw: raw}
	if v, ok := raw["claudeAiOauth"]; ok {
		if err := json.Unmarshal(v, &cf.oauth); err != nil {
			return nil, fmt.Errorf("parse claudeAiOauth: %w", err)
		}
	}
	return cf, nil
}

// writeBack serialises cf back to disk via mktemp + rename, preserving
// every other top-level key (mcpOAuth, ...) verbatim. 0600.
func (cf *credentialsFile) writeBack() error {
	enc, err := json.Marshal(cf.oauth)
	if err != nil {
		return err
	}
	cf.raw["claudeAiOauth"] = enc
	out, err := json.MarshalIndent(cf.raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	dir := filepath.Dir(cf.path)
	tmp, err := os.CreateTemp(dir, ".credentials.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, cf.path)
}

type usageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type usageResponse struct {
	FiveHour usageWindow `json:"five_hour"`
	SevenDay usageWindow `json:"seven_day"`
}

type usageSnapshot struct {
	FetchedAt   time.Time   `json:"fetched_at"`
	LastAttempt time.Time   `json:"last_attempt,omitempty"`
	Short       usageWindow `json:"short"`
	Long        usageWindow `json:"long"`
	Stale       bool        `json:"stale,omitempty"`
	Tier        string      `json:"tier,omitempty"`
}

// normalizeTier maps a subscriptionType string to a routing tier.
func normalizeTier(subscriptionType string) string {
	switch strings.ToLower(subscriptionType) {
	case "max":
		return "max"
	case "pro":
		return "pro"
	case "team":
		return "team"
	case "free", "":
		return "free"
	default:
		return subscriptionType
	}
}

// ActiveTier reads the live OAuth credentials and prints the
// normalized tier (max, pro, team, free) on a single line. Callers
// (orchestrator spawn paths, token monitors) gate on this output.
func ActiveTier() error {
	tier, err := ActiveTierString()
	if err != nil {
		return err
	}
	fmt.Println(tier)
	return nil
}

// ActiveTierString returns the normalized tier of the live OAuth
// credentials. In-process callers (coordinator spawn tier-gate) use
// this instead of shelling out to ActiveTier.
func ActiveTierString() (string, error) {
	path := oauthCredsPath()
	if path == "" {
		return "", errors.New("oauth credentials path unresolved")
	}
	cf, err := loadCredentials(path)
	if err != nil {
		return "", err
	}
	tier := normalizeTier(cf.oauth.SubscriptionType)
	if tier == "" {
		return "", errors.New("subscriptionType missing from credentials")
	}
	return tier, nil
}

// fetchUsage hits /usage, refreshing the access token proactively when
// its recorded expiry has passed and once more on a 401, in each case
// only when a refresh token is available. Returns (nil, err) for any
// non-2xx outcome the caller cannot recover from; that's the signal to
// fall back to transcripts.
func fetchUsage(ctx context.Context, now time.Time) (*usageResponse, error) {
	path := oauthCredsPath()
	if path == "" {
		return nil, errors.New("oauth credentials path unresolved")
	}
	cf, err := loadCredentials(path)
	if err != nil {
		return nil, fmt.Errorf("load creds: %w", err)
	}
	if cf.oauth.AccessToken == "" {
		return nil, errors.New("no oauth access token in credentials")
	}

	// Refresh proactively when the recorded expiry has passed (or is about
	// to). An expired token answers /usage with HTTP 429, not 401, so the
	// 401-retry path below never fires for it; without this a consumer that
	// is not itself a claude-code session (the waybar chip, a token monitor)
	// stays stuck on the transcript fallback until some other session happens
	// to refresh this exact credentials file. Refreshing off expiresAt is the
	// same trigger claude-code uses, and keeps every budget consumer
	// self-sufficient across reboots and config-dir drift.
	if cf.oauth.RefreshToken != "" && tokenExpired(cf.oauth.ExpiresAt, now) {
		if rerr := refreshOAuthToken(ctx, cf, now); rerr != nil {
			return nil, fmt.Errorf("oauth proactive refresh: %w", rerr)
		}
	}

	body, code, err := callUsage(ctx, cf.oauth.AccessToken)
	if err != nil {
		return nil, err
	}
	if code == http.StatusUnauthorized && cf.oauth.RefreshToken != "" {
		if rerr := refreshOAuthToken(ctx, cf, now); rerr != nil {
			return nil, fmt.Errorf("oauth refresh after 401: %w", rerr)
		}
		body, code, err = callUsage(ctx, cf.oauth.AccessToken)
		if err != nil {
			return nil, err
		}
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("usage: HTTP %d", code)
	}
	var ur usageResponse
	if err := json.Unmarshal(body, &ur); err != nil {
		return nil, fmt.Errorf("usage decode: %w", err)
	}
	return &ur, nil
}

func callUsage(ctx context.Context, accessToken string) ([]byte, int, error) {
	c, cancel := context.WithTimeout(ctx, usageHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", oauthBetaHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("usage GET: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return b, resp.StatusCode, err
}

type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// callTokenRefresh exchanges a refresh token for a new access token.
func callTokenRefresh(ctx context.Context, refreshToken string) (*tokenRefreshResponse, error) {
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     oauthClientID,
		"scope":         oauthScope,
	})
	if err != nil {
		return nil, err
	}
	c, cancel := context.WithTimeout(ctx, tokenHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, oauthTokenURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth refresh POST: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("oauth refresh read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth refresh: HTTP %d", resp.StatusCode)
	}
	var out tokenRefreshResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("oauth refresh decode: %w", err)
	}
	if out.AccessToken == "" || out.ExpiresIn <= 0 {
		return nil, errors.New("oauth refresh: empty access_token or expires_in")
	}
	return &out, nil
}

// tokenRefreshSkew refreshes the access token slightly before its
// recorded expiry, so a /usage request in flight near the boundary still
// carries a valid token.
const tokenRefreshSkew = 60 * time.Second

// tokenExpired reports whether an access token with the given expiresAt
// (epoch millis) is at or past expiry, accounting for skew. A zero or
// absent expiresAt is treated as not-expired so callers fall through to
// the call-and-401-retry path rather than refreshing blindly.
func tokenExpired(expiresAtMillis int64, now time.Time) bool {
	if expiresAtMillis <= 0 {
		return false
	}
	return !now.Before(time.UnixMilli(expiresAtMillis).Add(-tokenRefreshSkew))
}

func refreshOAuthToken(ctx context.Context, cf *credentialsFile, now time.Time) error {
	if cf.oauth.RefreshToken == "" {
		return errors.New("no refresh token")
	}
	out, err := callTokenRefresh(ctx, cf.oauth.RefreshToken)
	if err != nil {
		return err
	}
	cf.oauth.AccessToken = out.AccessToken
	if out.RefreshToken != "" {
		cf.oauth.RefreshToken = out.RefreshToken
	}
	cf.oauth.ExpiresAt = now.UnixMilli() + out.ExpiresIn*1000
	if out.Scope != "" {
		cf.oauth.Scopes = strings.Fields(out.Scope)
	}
	return cf.writeBack()
}

// usageWindowState materialises one /usage window into the on-disk
// windowState shape that consumers already read. Frac is the
// utilization percent / 100; cost/cap are absent (the surface only
// reports utilization, not absolute USD), so they round-trip as zero.
func usageWindowState(uw usageWindow, dur time.Duration, stale bool) windowState {
	source := "usage"
	if stale {
		source = "usage-stale"
	}
	w := windowState{
		DurationSeconds: int(dur.Seconds()),
		Frac:            uw.Utilization / 100.0,
		Source:          source,
	}
	if w.Frac < 0 {
		w.Frac = 0
	}
	if uw.ResetsAt != "" {
		if t, err := parseUsageTimestamp(uw.ResetsAt); err == nil {
			w.ResetAt = &t
		}
	}
	return w
}

// parseUsageTimestamp accepts the microsecond-precision RFC3339 strings
// /usage emits (e.g. "2026-04-26T17:00:00.830978+00:00"). Go's
// time.Parse(time.RFC3339Nano) handles both microseconds and the
// ":00"-separated offset, so a single call is enough.
func parseUsageTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

const queryAutoRefreshAge = 30 * time.Minute

// queryNeedsRefresh returns true when snapshot data is absent or older
// than queryAutoRefreshAge. This lets query/summary serve fresh advice
// without requiring a prior explicit refresh call.
func queryNeedsRefresh(s *state, now time.Time) bool {
	if s.UsageSnapshot == nil {
		return true
	}
	return s.UsageSnapshot.Stale || now.Sub(s.UsageSnapshot.FetchedAt) >= queryAutoRefreshAge
}

// DebugUsage hits /usage once (bypassing the persisted-snapshot
// throttle) and prints the raw HTTP body alongside the parsed
// usageResponse. Used to triage reset_at semantics: when state.json's
// reset_at sits in the past while frac stays high, this surfaces
// whether Anthropic genuinely returns a stale resets_at or whether the
// parser is dropping a rollover.
func DebugUsage() error {
	now := time.Now().UTC()
	path := oauthCredsPath()
	if path == "" {
		return errors.New("oauth credentials path unresolved")
	}
	cf, err := loadCredentials(path)
	if err != nil {
		return fmt.Errorf("load creds: %w", err)
	}
	body, code, err := callUsage(context.Background(), cf.oauth.AccessToken)
	if err != nil {
		return err
	}
	if code == http.StatusUnauthorized && cf.oauth.RefreshToken != "" {
		if rerr := refreshOAuthToken(context.Background(), cf, now); rerr != nil {
			return fmt.Errorf("oauth refresh after 401: %w", rerr)
		}
		body, code, err = callUsage(context.Background(), cf.oauth.AccessToken)
		if err != nil {
			return err
		}
	}
	fmt.Printf("HTTP %d at %s\n", code, now.Format(time.RFC3339Nano))
	fmt.Println("--- raw body ---")
	fmt.Println(string(body))
	fmt.Println("--- parsed ---")
	var ur usageResponse
	if jerr := json.Unmarshal(body, &ur); jerr != nil {
		fmt.Printf("decode error: %v\n", jerr)
		return nil
	}
	out, _ := json.MarshalIndent(ur, "", "  ")
	fmt.Println(string(out))
	if t, terr := parseUsageTimestamp(ur.FiveHour.ResetsAt); terr == nil {
		fmt.Printf("five_hour resets_at -> %s (delta %s)\n", t.UTC(), t.Sub(now))
	} else {
		fmt.Printf("five_hour resets_at parse error: %v\n", terr)
	}
	if t, terr := parseUsageTimestamp(ur.SevenDay.ResetsAt); terr == nil {
		fmt.Printf("seven_day resets_at -> %s (delta %s)\n", t.UTC(), t.Sub(now))
	} else {
		fmt.Printf("seven_day resets_at parse error: %v\n", terr)
	}
	return nil
}
