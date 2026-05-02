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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
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
	FetchedAt time.Time   `json:"fetched_at"`
	Short     usageWindow `json:"short"`
	Long      usageWindow `json:"long"`
	Stale     bool        `json:"stale,omitempty"`
	Tier      string      `json:"tier,omitempty"`
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
	path := oauthCredsPath()
	if path == "" {
		return errors.New("oauth credentials path unresolved")
	}
	cf, err := loadCredentials(path)
	if err != nil {
		return err
	}
	tier := normalizeTier(cf.oauth.SubscriptionType)
	if tier == "" {
		return errors.New("subscriptionType missing from credentials")
	}
	fmt.Println(tier)
	return nil
}

// fetchUsage hits /usage, refreshing the access token once on a 401 if
// a refresh token is available. Returns (nil, err) for any non-2xx
// outcome the caller cannot recover from; that's the signal to fall
// back to transcripts.
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
// Shared by refreshOAuthToken (credentials file path) and
// fetchUsageForFile (account store path).
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

// accountsStoreDir returns the directory where per-account OAuth
// snapshots are stored (~/.local/state/claude-accounts). Override with
// AGENT_BUDGET_ACCOUNTS_DIR for tests or non-standard layouts.
func accountsStoreDir() (string, error) {
	if d := os.Getenv("AGENT_BUDGET_ACCOUNTS_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "claude-accounts"), nil
}

func hasAccountFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}

// writeOAuthBlockToFile persists an updated oauthBlock back to a store
// file (flat format, without the claudeAiOauth wrapper). Uses
// mktemp + rename for atomicity; mode 0600.
func writeOAuthBlockToFile(path string, block oauthBlock) error {
	b, err := json.MarshalIndent(block, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".store.*")
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
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, path)
}

type accountUsageResult struct {
	Usage *usageResponse
	Tier  string
}

// fetchUsageForFile hits /usage using the OAuth token stored in a
// per-account store file (flat oauthBlock JSON). On 401, refreshes
// once and writes the new token back to the same file.
func fetchUsageForFile(ctx context.Context, path string, now time.Time) (*accountUsageResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var block oauthBlock
	if err := json.Unmarshal(b, &block); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	if block.AccessToken == "" {
		return nil, fmt.Errorf("%s: no access_token", filepath.Base(path))
	}

	body, code, err := callUsage(ctx, block.AccessToken)
	if err != nil {
		return nil, err
	}
	if code == http.StatusUnauthorized && block.RefreshToken != "" {
		out, rerr := callTokenRefresh(ctx, block.RefreshToken)
		if rerr != nil {
			return nil, fmt.Errorf("oauth refresh for %s: %w", filepath.Base(path), rerr)
		}
		block.AccessToken = out.AccessToken
		if out.RefreshToken != "" {
			block.RefreshToken = out.RefreshToken
		}
		block.ExpiresAt = now.UnixMilli() + out.ExpiresIn*1000
		if out.Scope != "" {
			block.Scopes = strings.Fields(out.Scope)
		}
		if werr := writeOAuthBlockToFile(path, block); werr != nil {
			fmt.Fprintf(os.Stderr, "spore budget: refresh writeback for %s: %v\n", filepath.Base(path), werr)
		}
		body, code, err = callUsage(ctx, block.AccessToken)
		if err != nil {
			return nil, err
		}
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("usage for %s: HTTP %d", filepath.Base(path), code)
	}
	var ur usageResponse
	if err := json.Unmarshal(body, &ur); err != nil {
		return nil, fmt.Errorf("usage decode for %s: %w", filepath.Base(path), err)
	}
	return &accountUsageResult{
		Usage: &ur,
		Tier:  normalizeTier(block.SubscriptionType),
	}, nil
}

// readAccountTier reads the subscriptionType from a store file without
// hitting the network.
func readAccountTier(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var block oauthBlock
	if err := json.Unmarshal(b, &block); err != nil {
		return "", fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return normalizeTier(block.SubscriptionType), nil
}

// refreshAllAccountSnapshots fetches /usage for every *.json file in
// storeDir and updates s.AccountSnapshots. Per-account freshness gate
// mirrors the single-account usageMinInterval. Snapshots for accounts
// no longer in the store are removed.
func refreshAllAccountSnapshots(s *state, now time.Time, storeDir string) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "spore budget: read accounts dir: %v\n", err)
		}
		return
	}

	minInterval := usageMinInterval
	if v := os.Getenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			minInterval = time.Duration(n) * time.Second
		}
	}

	if s.AccountSnapshots == nil {
		s.AccountSnapshots = make(map[string]*usageSnapshot)
	}

	liveNames := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		liveNames[name] = true

		existing := s.AccountSnapshots[name]
		if existing != nil && !existing.Stale && now.Sub(existing.FetchedAt) < minInterval {
			continue
		}

		path := filepath.Join(storeDir, e.Name())
		result, ferr := fetchUsageForFile(context.Background(), path, now)
		if ferr != nil {
			fmt.Fprintf(os.Stderr, "spore budget: /usage for %s unavailable (%v)\n", name, ferr)
			if existing != nil {
				existing.Stale = true
			}
			continue
		}
		s.AccountSnapshots[name] = &usageSnapshot{
			FetchedAt: now,
			Short:     result.Usage.FiveHour,
			Long:      result.Usage.SevenDay,
			Tier:      result.Tier,
		}
	}

	for name := range s.AccountSnapshots {
		if !liveNames[name] {
			delete(s.AccountSnapshots, name)
		}
	}
}

// aggregateAccountSnapshots averages per-account fracs across all
// snapshots. Mean frac = total used / total capacity (equal-cap
// assumption). ResetAt is the earliest per-window reset time.
func aggregateAccountSnapshots(snaps map[string]*usageSnapshot) (windowState, windowState) {
	var shortSum, longSum float64
	var shortResetAt, longResetAt *time.Time
	anyStale := false
	n := 0

	for _, snap := range snaps {
		if snap.Stale {
			anyStale = true
		}
		shortSum += snap.Short.Utilization / 100.0
		longSum += snap.Long.Utilization / 100.0

		if snap.Short.ResetsAt != "" {
			if t, err := parseUsageTimestamp(snap.Short.ResetsAt); err == nil {
				if shortResetAt == nil || t.Before(*shortResetAt) {
					tc := t
					shortResetAt = &tc
				}
			}
		}
		if snap.Long.ResetsAt != "" {
			if t, err := parseUsageTimestamp(snap.Long.ResetsAt); err == nil {
				if longResetAt == nil || t.Before(*longResetAt) {
					tc := t
					longResetAt = &tc
				}
			}
		}
		n++
	}

	if n == 0 {
		return windowState{}, windowState{}
	}

	source := "usage-aggregate"
	if anyStale {
		source = "usage-aggregate-stale"
	}

	return windowState{
		DurationSeconds: int(shortWindow.Seconds()),
		Frac:            shortSum / float64(n),
		Source:          source,
		ResetAt:         shortResetAt,
	}, windowState{
		DurationSeconds: int(longWindow.Seconds()),
		Frac:            longSum / float64(n),
		Source:          source,
		ResetAt:         longResetAt,
	}
}

const queryAutoRefreshAge = 30 * time.Minute

// queryNeedsRefresh returns true when subscription-mode snapshot data
// is absent or older than queryAutoRefreshAge. This lets query/summary
// serve fresh advice without requiring a prior explicit refresh call.
func queryNeedsRefresh(s *state, now time.Time) bool {
	mode, err := resolveMode(now)
	if err != nil || mode != "subscription" {
		return false
	}
	if len(s.AccountSnapshots) > 0 {
		for _, snap := range s.AccountSnapshots {
			if snap.Stale || now.Sub(snap.FetchedAt) >= queryAutoRefreshAge {
				return true
			}
		}
		return false
	}
	if s.UsageSnapshot == nil {
		return true
	}
	return s.UsageSnapshot.Stale || now.Sub(s.UsageSnapshot.FetchedAt) >= queryAutoRefreshAge
}

// Tier prints a JSON map of account names to their tiers. Reads the
// account store files without hitting the network.
func Tier() error {
	storeDir, err := accountsStoreDir()
	if err != nil {
		return fmt.Errorf("account store: %w", err)
	}

	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Println("{}")
			return nil
		}
		return fmt.Errorf("read accounts dir: %w", err)
	}

	tiers := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		tier, terr := readAccountTier(filepath.Join(storeDir, e.Name()))
		if terr != nil {
			fmt.Fprintf(os.Stderr, "spore budget: tier for %s: %v\n", name, terr)
			continue
		}
		tiers[name] = tier
	}

	b, err := json.MarshalIndent(tiers, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
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
