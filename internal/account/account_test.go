package account

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeEnv wires per-driver env to a TempDir-scoped layout for the
// duration of the test. Returns (storeDir, livePath, stateDir).
func claudeEnv(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	storeDir := filepath.Join(root, "claude-accounts")
	stateDir := filepath.Join(root, "agent-budget")
	livePath := filepath.Join(root, ".credentials.json")
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", storeDir)
	t.Setenv("AGENT_BUDGET_CREDS", livePath)
	t.Setenv("AGENT_BUDGET_STATE_DIR", stateDir)
	t.Setenv("XDG_STATE_HOME", "")
	return storeDir, livePath, stateDir
}

func codexEnv(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	storeDir := filepath.Join(root, "codex-accounts")
	livePath := filepath.Join(root, "auth.json")
	t.Setenv("SPORE_CODEX_ACCOUNTS_DIR", storeDir)
	t.Setenv("SPORE_CODEX_CREDS", livePath)
	return storeDir, livePath
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeLive(t *testing.T, path string, accessToken string) {
	t.Helper()
	writeJSON(t, path, map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      accessToken,
			"refreshToken":     "rt-" + accessToken,
			"expiresAt":        time.Now().Add(2 * time.Hour).UnixMilli(),
			"subscriptionType": "max",
		},
		"mcpOAuth": map[string]any{"preserved": true, "marker": accessToken},
	})
}

func writeCodexLive(t *testing.T, path, accessToken string) {
	t.Helper()
	writeJSON(t, path, map[string]any{
		"auth_mode": "ChatGPT",
		"tokens": map[string]any{
			"id_token":      "id-" + accessToken,
			"access_token":  accessToken,
			"refresh_token": "rt-" + accessToken,
			"account_id":    "acct-" + accessToken,
		},
		"last_refresh": "2026-05-09T12:00:00Z",
		"side_extra":   "preserved",
	})
}

func writeStateSnapshots(t *testing.T, stateDir string, snaps map[string]map[string]any) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"account_snapshots": snaps}
	writeJSON(t, filepath.Join(stateDir, "state.json"), body)
}

func snap(shortPct, longPct float64, stale bool) map[string]any {
	return map[string]any{
		"fetched_at": time.Now().UTC(),
		"short":      map[string]any{"utilization": shortPct, "resets_at": time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)},
		"long":       map[string]any{"utilization": longPct, "resets_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339Nano)},
		"stale":      stale,
		"tier":       "max",
	}
}

// readClaudeLive parses the live creds file and returns the raw map so
// tests can inspect both the claudeAiOauth block and adjacent keys.
func readClaudeLive(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestListEmptyStore(t *testing.T) {
	claudeEnv(t)
	rows, err := List(DriverClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty store: got %d rows, want 0", len(rows))
	}
}

func TestSaveSwitchListSingleAccount(t *testing.T) {
	storeDir, livePath, _ := claudeEnv(t)

	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatalf("save alpha: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storeDir, "alpha.json")); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	rows, err := List(DriverClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "alpha" {
		t.Fatalf("rows: got %+v", rows)
	}
	if rows[0].Active {
		t.Errorf("no .active yet, want active=false")
	}

	if err := Switch(DriverClaude, "alpha", "manual"); err != nil {
		t.Fatalf("switch alpha: %v", err)
	}
	got, err := Active(DriverClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha" {
		t.Errorf("active: got %q want alpha", got)
	}
}

func TestSwitchPreservesAdjacentTopLevelKeysClaude(t *testing.T) {
	storeDir, livePath, _ := claudeEnv(t)

	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "beta")
	if err := Save(DriverClaude, "beta"); err != nil {
		t.Fatal(err)
	}

	// live now has beta as oauth + mcpOAuth.marker = "beta". Switching
	// to alpha must restore alpha's accessToken AND keep mcpOAuth from
	// the current live file (the user's MCP setup is independent of
	// which Claude account is logged in).
	if err := Switch(DriverClaude, "alpha", ""); err != nil {
		t.Fatal(err)
	}

	live := readClaudeLive(t, livePath)
	var oauth struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(live["claudeAiOauth"], &oauth); err != nil {
		t.Fatal(err)
	}
	if oauth.AccessToken != "alpha" {
		t.Errorf("accessToken: got %q want alpha", oauth.AccessToken)
	}
	var mcp struct {
		Preserved bool   `json:"preserved"`
		Marker    string `json:"marker"`
	}
	if err := json.Unmarshal(live["mcpOAuth"], &mcp); err != nil {
		t.Fatal(err)
	}
	if !mcp.Preserved || mcp.Marker != "beta" {
		t.Errorf("mcpOAuth not preserved: %+v", mcp)
	}

	// switches.jsonl should hold one entry (the alpha switch). The
	// initial save calls don't touch the ledger.
	b, err := os.ReadFile(filepath.Join(storeDir, "switches.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("ledger lines: got %d (%q)", len(lines), b)
	}
}

func TestSwitchPreservesAdjacentTopLevelKeysCodex(t *testing.T) {
	storeDir, livePath := codexEnv(t)

	writeCodexLive(t, livePath, "one")
	if err := Save(DriverCodex, "one"); err != nil {
		t.Fatal(err)
	}
	writeCodexLive(t, livePath, "two")
	if err := Save(DriverCodex, "two"); err != nil {
		t.Fatal(err)
	}

	// Mutate side_extra in live to a recognisable value before the
	// switch so we know it round-trips through the merge.
	cur := readClaudeLive(t, livePath)
	cur["side_extra"] = json.RawMessage(`"keep-me"`)
	out, _ := json.MarshalIndent(cur, "", "  ")
	if err := os.WriteFile(livePath, out, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Switch(DriverCodex, "one", ""); err != nil {
		t.Fatal(err)
	}
	live := readClaudeLive(t, livePath)
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(live["tokens"], &tokens); err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "one" {
		t.Errorf("access_token: got %q want one", tokens.AccessToken)
	}
	var sideExtra string
	if err := json.Unmarshal(live["side_extra"], &sideExtra); err != nil {
		t.Fatal(err)
	}
	if sideExtra != "keep-me" {
		t.Errorf("side_extra: got %q want keep-me", sideExtra)
	}
	_ = storeDir
}

func TestSwitchNoSuchAccount(t *testing.T) {
	_, livePath, _ := claudeEnv(t)
	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	err := Switch(DriverClaude, "ghost", "")
	if !errors.Is(err, ErrNoSuchAccount) {
		t.Fatalf("want ErrNoSuchAccount, got %v", err)
	}
}

func TestSwitchIdempotentOnAlreadyActive(t *testing.T) {
	storeDir, livePath, _ := claudeEnv(t)

	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverClaude, "alpha", "first"); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverClaude, "alpha", "second"); err != nil {
		t.Fatal(err)
	}
	got, err := Active(DriverClaude)
	if err != nil || got != "alpha" {
		t.Fatalf("active: got %q err %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(storeDir, "switches.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("ledger lines: want 2 (one per switch call), got %d (%q)", len(lines), b)
	}
}

func TestAtomicWriteSurvivesPreexistingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Force a target unlink between writes so atomicWrite has to MkdirAll
	// and recreate; the file content must end up as the new bytes, not a
	// half-written intermediate.
	for i := 0; i < 5; i++ {
		_ = os.Remove(target)
		if err := atomicWrite(target, []byte(`{"new":true}`), 0o600); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		b, err := os.ReadFile(target)
		if err != nil || string(b) != `{"new":true}` {
			t.Fatalf("post-write %d: got %q err %v", i, b, err)
		}
		fi, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode: got %o want 0600", fi.Mode().Perm())
		}
	}

	// No leftover .store.* tempfiles in dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".store.") {
			t.Errorf("leftover tempfile: %s", e.Name())
		}
	}
}

func TestAutoPicksFirstNonRationedAlphabetical(t *testing.T) {
	_, livePath, stateDir := claudeEnv(t)

	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "beta")
	if err := Save(DriverClaude, "beta"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "gamma")
	if err := Save(DriverClaude, "gamma"); err != nil {
		t.Fatal(err)
	}

	// alpha rationed, beta stale, gamma healthy. Picker must skip
	// alpha (over threshold) and beta (stale) and land on gamma.
	writeStateSnapshots(t, stateDir, map[string]map[string]any{
		"alpha": snap(95, 50, false),
		"beta":  snap(10, 10, true),
		"gamma": snap(20, 30, false),
	})

	picked, err := Auto(DriverClaude, "test")
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if picked != "gamma" {
		t.Errorf("picked: got %q want gamma", picked)
	}
	got, _ := Active(DriverClaude)
	if got != "gamma" {
		t.Errorf("active after auto: got %q want gamma", got)
	}
}

func TestAutoReturnsAllRationedWhenAllOverCap(t *testing.T) {
	_, livePath, stateDir := claudeEnv(t)

	writeClaudeLive(t, livePath, "a1")
	if err := Save(DriverClaude, "a1"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "a2")
	if err := Save(DriverClaude, "a2"); err != nil {
		t.Fatal(err)
	}
	writeStateSnapshots(t, stateDir, map[string]map[string]any{
		"a1": snap(95, 80, false),
		"a2": snap(50, 92, false),
	})

	_, err := Auto(DriverClaude, "test")
	if !errors.Is(err, ErrAllRationed) {
		t.Fatalf("want ErrAllRationed, got %v", err)
	}
}

func TestAutoNoOpSingleAccount(t *testing.T) {
	_, livePath, _ := claudeEnv(t)
	writeClaudeLive(t, livePath, "only")
	if err := Save(DriverClaude, "only"); err != nil {
		t.Fatal(err)
	}
	picked, err := Auto(DriverClaude, "test")
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if picked != "" {
		t.Errorf("single-account auto must no-op, got picked=%q", picked)
	}
	got, _ := Active(DriverClaude)
	if got != "" {
		t.Errorf("active should remain empty, got %q", got)
	}
}

func TestAutoNoOpWhenAlreadyOnHealthyPick(t *testing.T) {
	_, livePath, stateDir := claudeEnv(t)

	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "beta")
	if err := Save(DriverClaude, "beta"); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverClaude, "alpha", "seed"); err != nil {
		t.Fatal(err)
	}
	writeStateSnapshots(t, stateDir, map[string]map[string]any{
		"alpha": snap(20, 10, false),
		"beta":  snap(20, 10, false),
	})
	picked, err := Auto(DriverClaude, "")
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if picked != "" {
		t.Errorf("auto should no-op when already on healthy first pick, got %q", picked)
	}
}

func TestAutoCodexDegradedRespectsRationedUntil(t *testing.T) {
	storeDir, livePath := codexEnv(t)

	writeCodexLive(t, livePath, "one")
	if err := Save(DriverCodex, "one"); err != nil {
		t.Fatal(err)
	}
	writeCodexLive(t, livePath, "two")
	if err := Save(DriverCodex, "two"); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverCodex, "one", "seed"); err != nil {
		t.Fatal(err)
	}

	// No marker -> active stays as-is, picker no-ops.
	picked, err := Auto(DriverCodex, "")
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if picked != "" {
		t.Errorf("auto with no markers should no-op, got %q", picked)
	}

	// Mark active rationed -> picker walks alphabetical and picks two.
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(storeDir, "one.rationed-until"), []byte(future+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	picked, err = Auto(DriverCodex, "rate-limit")
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if picked != "two" {
		t.Errorf("picked: got %q want two", picked)
	}

	// Both rationed -> ErrAllRationed.
	if err := os.WriteFile(filepath.Join(storeDir, "two.rationed-until"), []byte(future+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverCodex, "two", "seed"); err != nil {
		t.Fatal(err)
	}
	_, err = Auto(DriverCodex, "")
	if !errors.Is(err, ErrAllRationed) {
		t.Fatalf("want ErrAllRationed, got %v", err)
	}
}

func TestSaveRejectsBadID(t *testing.T) {
	_, livePath, _ := claudeEnv(t)
	writeClaudeLive(t, livePath, "x")
	for _, bad := range []string{"", ".", "..", ".hidden", "a/b", "x\\y"} {
		if err := Save(DriverClaude, bad); err == nil {
			t.Errorf("Save(%q) should reject", bad)
		}
	}
}

func TestSwitchRejectsBadID(t *testing.T) {
	claudeEnv(t)
	for _, bad := range []string{"", ".", "..", ".hidden", "a/b"} {
		if err := Switch(DriverClaude, bad, ""); err == nil {
			t.Errorf("Switch(%q) should reject", bad)
		}
	}
}

func TestSaveMissingLiveCreds(t *testing.T) {
	claudeEnv(t)
	if err := Save(DriverClaude, "alpha"); err == nil {
		t.Fatal("save with no live creds should error")
	}
}

func TestStoreDirUnknownDriver(t *testing.T) {
	if _, err := StoreDir("nope"); err == nil {
		t.Fatal("unknown driver should error")
	}
}

func TestListReportsActiveAndStale(t *testing.T) {
	_, livePath, stateDir := claudeEnv(t)
	writeClaudeLive(t, livePath, "alpha")
	if err := Save(DriverClaude, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeClaudeLive(t, livePath, "beta")
	if err := Save(DriverClaude, "beta"); err != nil {
		t.Fatal(err)
	}
	if err := Switch(DriverClaude, "beta", ""); err != nil {
		t.Fatal(err)
	}
	writeStateSnapshots(t, stateDir, map[string]map[string]any{
		"beta": snap(40, 30, false),
		// alpha has no snapshot -> stale=true in row.
	})
	rows, err := List(DriverClaude)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d want 2", len(rows))
	}
	for _, r := range rows {
		switch r.ID {
		case "alpha":
			if r.Active || !r.Stale {
				t.Errorf("alpha row: %+v", r)
			}
		case "beta":
			if !r.Active || r.Stale {
				t.Errorf("beta row: %+v", r)
			}
			if r.ShortFrac != 0.4 || r.LongFrac != 0.3 {
				t.Errorf("beta fracs: %+v", r)
			}
		}
	}
}
