package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureRun wraps captureStdout (declared in shrink_cmd_test.go) so
// each call records the exit code alongside the stdout payload.
func captureRun(t *testing.T, args []string) (int, string) {
	t.Helper()
	read := captureStdout(t)
	code := runAccount(args)
	return code, read()
}

func setupAccountClaudeEnv(t *testing.T) (storeDir, livePath string) {
	t.Helper()
	root := t.TempDir()
	storeDir = filepath.Join(root, "claude-accounts")
	livePath = filepath.Join(root, ".credentials.json")
	t.Setenv("AGENT_BUDGET_ACCOUNTS_DIR", storeDir)
	t.Setenv("AGENT_BUDGET_CREDS", livePath)
	t.Setenv("AGENT_BUDGET_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("XDG_STATE_HOME", "")
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "at-x",
			"refreshToken":     "rt-x",
			"expiresAt":        time.Now().Add(time.Hour).UnixMilli(),
			"subscriptionType": "max",
		},
		"mcpOAuth": map[string]any{"keep": true},
	}
	b, _ := json.MarshalIndent(body, "", "  ")
	if err := os.WriteFile(livePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return storeDir, livePath
}

func TestAccountUsageNoArgs(t *testing.T) {
	if got := runAccount(nil); got != 2 {
		t.Errorf("no args exit: got %d want 2", got)
	}
}

func TestAccountUnknownSub(t *testing.T) {
	if got := runAccount([]string{"flarp"}); got != 2 {
		t.Errorf("unknown sub exit: got %d want 2", got)
	}
}

func TestAccountHelp(t *testing.T) {
	for _, h := range []string{"-h", "--help", "help"} {
		if got := runAccount([]string{h}); got != 0 {
			t.Errorf("help exit: got %d want 0", got)
		}
	}
}

func TestAccountSaveRequiresDriver(t *testing.T) {
	if got := runAccount([]string{"save", "alpha"}); got != 2 {
		t.Errorf("save without driver: got %d want 2", got)
	}
}

func TestAccountSaveRequiresID(t *testing.T) {
	if got := runAccount([]string{"save", "--driver", "claude"}); got != 2 {
		t.Errorf("save without id: got %d want 2", got)
	}
}

func TestAccountSaveAndList(t *testing.T) {
	setupAccountClaudeEnv(t)
	if got := runAccount([]string{"save", "--driver", "claude", "alpha"}); got != 0 {
		t.Errorf("save: got %d want 0", got)
	}
	code, out := captureRun(t, []string{"list", "--driver", "claude"})
	if code != 0 {
		t.Errorf("list: got %d want 0", code)
	}
	if !strings.Contains(out, `"id": "alpha"`) {
		t.Errorf("list output missing alpha: %q", out)
	}
}

func TestAccountSwitchNoSuchAccountExit4(t *testing.T) {
	setupAccountClaudeEnv(t)
	if got := runAccount([]string{"switch", "--driver", "claude", "--to", "ghost"}); got != 4 {
		t.Errorf("switch ghost: got %d want 4", got)
	}
}

func TestAccountSwitchSuccess(t *testing.T) {
	setupAccountClaudeEnv(t)
	if got := runAccount([]string{"save", "--driver", "claude", "alpha"}); got != 0 {
		t.Fatalf("save: %d", got)
	}
	if got := runAccount([]string{"switch", "--driver", "claude", "--to", "alpha", "--reason", "test"}); got != 0 {
		t.Errorf("switch: got %d want 0", got)
	}
	code, out := captureRun(t, []string{"active", "--driver", "claude"})
	if code != 0 {
		t.Errorf("active: got %d want 0", code)
	}
	if strings.TrimSpace(out) != "alpha" {
		t.Errorf("active output: got %q want alpha", out)
	}
}

func TestAccountAutoAllRationExit3(t *testing.T) {
	setupAccountClaudeEnv(t)
	storeDir, livePath := setupAccountClaudeEnv(t)
	_ = storeDir
	if got := runAccount([]string{"save", "--driver", "claude", "a1"}); got != 0 {
		t.Fatal(got)
	}
	// Refresh live for second account before second save.
	body := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      "at-y",
			"refreshToken":     "rt-y",
			"expiresAt":        time.Now().Add(time.Hour).UnixMilli(),
			"subscriptionType": "max",
		},
	}
	b, _ := json.MarshalIndent(body, "", "  ")
	if err := os.WriteFile(livePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runAccount([]string{"save", "--driver", "claude", "a2"}); got != 0 {
		t.Fatal(got)
	}
	stateDir := os.Getenv("AGENT_BUDGET_STATE_DIR")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"account_snapshots": map[string]any{
			"a1": map[string]any{"short": map[string]any{"utilization": 95.0}, "long": map[string]any{"utilization": 50.0}},
			"a2": map[string]any{"short": map[string]any{"utilization": 50.0}, "long": map[string]any{"utilization": 95.0}},
		},
	}
	sb, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), sb, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out := captureRun(t, []string{"auto", "--driver", "claude"})
	if code != 3 {
		t.Errorf("auto all-ration: got %d want 3", code)
	}
	if !strings.Contains(out, `"all-ration"`) {
		t.Errorf("auto stdout: got %q want all-ration body", out)
	}
}

func TestAccountAutoBadDriver(t *testing.T) {
	if got := runAccount([]string{"auto", "--driver", "weird"}); got != 2 {
		t.Errorf("bad driver: got %d want 2", got)
	}
}
