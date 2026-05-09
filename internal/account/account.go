// Package account manages a per-driver multi-account credential store
// with switch and auto-pick primitives. It is the produce side of the
// account-stacking flow whose consume side already lives in
// internal/budget (per-account /usage aggregation).
//
// Drivers:
//
//	claude   live creds at ~/.claude/.credentials.json (claudeAiOauth
//	         block); store at ~/.local/state/claude-accounts/<id>.json.
//	codex    live creds at ~/.codex/auth.json (auth_mode + tokens +
//	         last_refresh top-level keys); store at
//	         ~/.local/state/codex-accounts/<id>.json.
//
// Switch preserves every other top-level key in the live file (e.g.
// claude's mcpOAuth block) so the multi-account layer never silently
// drops adjacent state.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DriverClaude = "claude"
	DriverCodex  = "codex"

	rationFracThreshold = 0.9
)

// ErrAllRationed is returned by Auto when no candidate qualifies.
// Callers (CLI, spawn paths) render the operator notification.
var ErrAllRationed = errors.New("all accounts rationed")

// ErrNoSuchAccount is returned when an explicit Switch target is not
// present in the per-driver store.
var ErrNoSuchAccount = errors.New("no such account")

// Row is one entry of the picker / waybar surface returned by List.
// ResetAt is the earliest of short.reset_at and long.reset_at when both
// are known; nil otherwise.
type Row struct {
	ID        string     `json:"id"`
	Tier      string     `json:"tier,omitempty"`
	ShortFrac float64    `json:"short_frac"`
	LongFrac  float64    `json:"long_frac"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
	Active    bool       `json:"active"`
	Stale     bool       `json:"stale,omitempty"`
}

// StoreDir resolves the per-driver store directory, honoring the
// per-driver env override.
func StoreDir(driver string) (string, error) {
	switch driver {
	case DriverClaude:
		if d := os.Getenv("AGENT_BUDGET_ACCOUNTS_DIR"); d != "" {
			return d, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "claude-accounts"), nil
	case DriverCodex:
		if d := os.Getenv("SPORE_CODEX_ACCOUNTS_DIR"); d != "" {
			return d, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "codex-accounts"), nil
	default:
		return "", fmt.Errorf("unknown driver %q", driver)
	}
}

// LiveCredsPath resolves the live credentials file for the driver.
func LiveCredsPath(driver string) (string, error) {
	switch driver {
	case DriverClaude:
		if p := os.Getenv("AGENT_BUDGET_CREDS"); p != "" {
			return p, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", ".credentials.json"), nil
	case DriverCodex:
		if p := os.Getenv("SPORE_CODEX_CREDS"); p != "" {
			return p, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".codex", "auth.json"), nil
	default:
		return "", fmt.Errorf("unknown driver %q", driver)
	}
}

// Save snapshots the live credentials file's per-driver oauth block
// into <storeDir>/<id>.json. Replaces any existing snapshot for id.
func Save(driver, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	storeDir, err := StoreDir(driver)
	if err != nil {
		return err
	}
	livePath, err := LiveCredsPath(driver)
	if err != nil {
		return err
	}
	live, err := os.ReadFile(livePath)
	if err != nil {
		return fmt.Errorf("read live creds: %w", err)
	}
	snap, err := extractSnapshot(driver, live)
	if err != nil {
		return err
	}
	return atomicWrite(snapshotPath(storeDir, id), snap, storeFileMode)
}

// Switch writes the snapshot for id into the live credentials file,
// preserving every other top-level key. Updates .active and appends a
// switches.jsonl entry. Idempotent: when id is already active the live
// file is rewritten (still valid creds) but no .active mutation
// happens; the ledger entry is appended either way.
func Switch(driver, id, reason string) error {
	if err := validateID(id); err != nil {
		return err
	}
	storeDir, err := StoreDir(driver)
	if err != nil {
		return err
	}
	livePath, err := LiveCredsPath(driver)
	if err != nil {
		return err
	}

	snapBytes, err := os.ReadFile(snapshotPath(storeDir, id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s/%s", ErrNoSuchAccount, driver, id)
		}
		return fmt.Errorf("read snapshot: %w", err)
	}

	live, lerr := os.ReadFile(livePath)
	if lerr != nil && !errors.Is(lerr, fs.ErrNotExist) {
		return fmt.Errorf("read live creds: %w", lerr)
	}

	merged, err := mergeIntoLive(driver, live, snapBytes)
	if err != nil {
		return err
	}
	if err := atomicWrite(livePath, merged, storeFileMode); err != nil {
		return fmt.Errorf("write live creds: %w", err)
	}

	from, _ := readActive(storeDir)
	if from != id {
		if err := writeActive(storeDir, id); err != nil {
			return fmt.Errorf("update .active: %w", err)
		}
	}
	return appendLedger(storeDir, ledgerEntry{
		Timestamp: time.Now().UTC(),
		Driver:    driver,
		From:      from,
		To:        id,
		Reason:    reason,
	})
}

// Auto picks the first non-stale account whose short and long frac are
// both below the ration threshold (0.9), in alphabetical id order, and
// switches to it.
//
// Returns:
//
//	(picked, nil)         a switch happened.
//	("", nil)             no-op (zero or one account, or current pick
//	                      already active, or codex-degraded with no
//	                      active marker present).
//	("", ErrAllRationed)  N>=2 accounts but no candidate qualifies.
func Auto(driver, reason string) (string, error) {
	storeDir, err := StoreDir(driver)
	if err != nil {
		return "", err
	}
	ids, err := listSnapshotIDs(storeDir)
	if err != nil {
		return "", err
	}
	if len(ids) < 2 {
		return "", nil
	}
	active, _ := readActive(storeDir)

	picked, err := pickCandidate(driver, storeDir, ids, active)
	if err != nil {
		return "", err
	}
	if picked == "" {
		return "", ErrAllRationed
	}
	if picked == active {
		return "", nil
	}
	if err := Switch(driver, picked, reason); err != nil {
		return "", err
	}
	return picked, nil
}

// Active returns the currently active account id. Empty when the store
// is empty or running in single-account legacy mode (no .active file).
func Active(driver string) (string, error) {
	storeDir, err := StoreDir(driver)
	if err != nil {
		return "", err
	}
	return readActive(storeDir)
}

// List returns one row per account in alphabetical id order.
// Snapshot data is best-effort: when state.json does not have an entry
// for an account the row carries zeroed fracs and stale=true.
func List(driver string) ([]Row, error) {
	storeDir, err := StoreDir(driver)
	if err != nil {
		return nil, err
	}
	ids, err := listSnapshotIDs(storeDir)
	if err != nil {
		return nil, err
	}
	active, _ := readActive(storeDir)

	snaps := loadAccountSnapshots(driver)
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		row := Row{ID: id, Active: id == active}
		if snap, ok := snaps[id]; ok {
			row.Tier = snap.Tier
			row.ShortFrac = snap.Short.Utilization / 100.0
			row.LongFrac = snap.Long.Utilization / 100.0
			row.Stale = snap.Stale
			row.ResetAt = earliestReset(snap.Short.ResetsAt, snap.Long.ResetsAt)
		} else {
			row.Stale = true
		}
		out = append(out, row)
	}
	return out, nil
}

// validateID rejects empty / path-traversal-ish ids that would let a
// caller escape the store dir or stomp .active / switches.jsonl.
func validateID(id string) error {
	if id == "" {
		return errors.New("account id is empty")
	}
	if strings.ContainsAny(id, "/\\") || id == "." || id == ".." || strings.HasPrefix(id, ".") {
		return fmt.Errorf("invalid account id %q", id)
	}
	return nil
}

// extractSnapshot turns the live credentials file bytes into the
// per-driver snapshot bytes we persist under <id>.json.
func extractSnapshot(driver string, live []byte) ([]byte, error) {
	switch driver {
	case DriverClaude:
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(live, &raw); err != nil {
			return nil, fmt.Errorf("parse live creds: %w", err)
		}
		block, ok := raw["claudeAiOauth"]
		if !ok || len(block) == 0 {
			return nil, errors.New("live creds missing claudeAiOauth block")
		}
		return canonicalJSON(block)
	case DriverCodex:
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(live, &raw); err != nil {
			return nil, fmt.Errorf("parse live creds: %w", err)
		}
		out := map[string]json.RawMessage{}
		for _, k := range codexSnapshotKeys {
			if v, ok := raw[k]; ok {
				out[k] = v
			}
		}
		if _, ok := out["tokens"]; !ok {
			return nil, errors.New("live creds missing tokens block")
		}
		return canonicalJSON(out)
	default:
		return nil, fmt.Errorf("unknown driver %q", driver)
	}
}

// codexSnapshotKeys is the set of top-level keys spore reads from
// ~/.codex/auth.json into <id>.json. Adjacent keys (if codex grows
// any) round-trip through Switch unchanged.
var codexSnapshotKeys = []string{"auth_mode", "tokens", "last_refresh"}

// mergeIntoLive splices the snapshot back into the live file,
// preserving every other top-level key. live may be nil (fresh install
// with no live file yet); we then synthesise a minimal envelope from
// the snapshot.
func mergeIntoLive(driver string, live, snap []byte) ([]byte, error) {
	liveMap := map[string]json.RawMessage{}
	if len(live) > 0 {
		if err := json.Unmarshal(live, &liveMap); err != nil {
			return nil, fmt.Errorf("parse live creds: %w", err)
		}
	}
	switch driver {
	case DriverClaude:
		liveMap["claudeAiOauth"] = json.RawMessage(snap)
	case DriverCodex:
		var snapMap map[string]json.RawMessage
		if err := json.Unmarshal(snap, &snapMap); err != nil {
			return nil, fmt.Errorf("parse snapshot: %w", err)
		}
		for k, v := range snapMap {
			liveMap[k] = v
		}
	default:
		return nil, fmt.Errorf("unknown driver %q", driver)
	}
	return canonicalJSON(liveMap)
}

// canonicalJSON marshals v with sorted keys and a trailing newline.
// Sorted by virtue of map[string]json.RawMessage going through
// json.Marshal which sorts top-level keys.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// pickCandidate dispatches to the driver-specific picker. Returns the
// chosen id, or "" when no candidate qualifies (caller maps that to
// ErrAllRationed). The candidate set is pre-sorted alphabetically.
func pickCandidate(driver, storeDir string, ids []string, active string) (string, error) {
	switch driver {
	case DriverClaude:
		return pickClaude(ids), nil
	case DriverCodex:
		return pickCodex(storeDir, ids, active)
	default:
		return "", fmt.Errorf("unknown driver %q", driver)
	}
}

// pickClaude reads state.json snapshots and returns the first id where
// short and long frac are both below the ration threshold. Stale
// snapshots are skipped (we cannot trust their frac). Missing snapshot
// for an id is also a skip - we cannot prove the account is healthy.
func pickClaude(ids []string) string {
	snaps := loadAccountSnapshots(DriverClaude)
	for _, id := range ids {
		snap, ok := snaps[id]
		if !ok {
			continue
		}
		if snap.Stale {
			continue
		}
		shortFrac := snap.Short.Utilization / 100.0
		longFrac := snap.Long.Utilization / 100.0
		if shortFrac < rationFracThreshold && longFrac < rationFracThreshold {
			return id
		}
	}
	return ""
}

// pickCodex implements the degraded codex picker: only switch when the
// active account has a future-dated <id>.rationed-until marker. Picks
// the first id whose marker is absent or already in the past. Returns
// active (= no-op trigger) when active is unrationed.
func pickCodex(storeDir string, ids []string, active string) (string, error) {
	now := time.Now().UTC()

	if active != "" {
		until, err := readRationedUntil(storeDir, active)
		if err != nil {
			return "", err
		}
		if until.IsZero() || !until.After(now) {
			return active, nil
		}
	}

	for _, id := range ids {
		until, err := readRationedUntil(storeDir, id)
		if err != nil {
			return "", err
		}
		if until.IsZero() || !until.After(now) {
			return id, nil
		}
	}
	return "", nil
}

// stateAccountSnapshot mirrors internal/budget's usageSnapshot for the
// fields the picker / list reads. Re-declared locally to avoid an
// account -> budget package dependency.
type stateAccountSnapshot struct {
	FetchedAt time.Time `json:"fetched_at"`
	Short     struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"short"`
	Long struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    string  `json:"resets_at"`
	} `json:"long"`
	Stale bool   `json:"stale,omitempty"`
	Tier  string `json:"tier,omitempty"`
}

type stateView struct {
	AccountSnapshots map[string]*stateAccountSnapshot `json:"account_snapshots,omitempty"`
}

// loadAccountSnapshots reads $AGENT_BUDGET_STATE_DIR/state.json and
// returns the per-account snapshot map. Codex has no /usage signal
// upstream so this is claude-only - codex callers receive an empty map
// (and pickCodex never consults it). Errors degrade to an empty map so
// a missing budget run does not break the picker; Auto then falls
// through to ErrAllRationed which the caller already handles.
func loadAccountSnapshots(driver string) map[string]*stateAccountSnapshot {
	if driver != DriverClaude {
		return map[string]*stateAccountSnapshot{}
	}
	p, err := stateJSONPath()
	if err != nil {
		return map[string]*stateAccountSnapshot{}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return map[string]*stateAccountSnapshot{}
	}
	var s stateView
	if err := json.Unmarshal(b, &s); err != nil {
		return map[string]*stateAccountSnapshot{}
	}
	if s.AccountSnapshots == nil {
		return map[string]*stateAccountSnapshot{}
	}
	return s.AccountSnapshots
}

// stateJSONPath mirrors internal/budget.stateDir() so account and
// budget see the same on-disk file without sharing code.
func stateJSONPath() (string, error) {
	if d := os.Getenv("AGENT_BUDGET_STATE_DIR"); d != "" {
		return filepath.Join(d, "state.json"), nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "agent-budget", "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agent-budget", "state.json"), nil
}

// earliestReset returns the earlier of two RFC3339 timestamps; nil
// when both are empty or unparseable. Microsecond-precision strings
// (the /usage shape) round-trip through time.RFC3339Nano.
func earliestReset(shortRA, longRA string) *time.Time {
	a, aok := parseReset(shortRA)
	b, bok := parseReset(longRA)
	switch {
	case aok && bok:
		if a.Before(b) {
			return &a
		}
		return &b
	case aok:
		return &a
	case bok:
		return &b
	}
	return nil
}

func parseReset(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// SortRows sorts rows by id (stable) for callers that want a
// deterministic ordering after editing the slice.
func SortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
}
