package stopwatchdog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupCfg(t *testing.T, paneOut string) Config {
	t.Helper()
	state := t.TempDir()
	inbox := filepath.Join(state, "wt-test", "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, ".tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Config{
		Inbox:      inbox,
		WorkerDir:  filepath.Dir(inbox),
		Agent:      "claude",
		Wait:       5 * time.Second,
		Now:        func() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) },
		Sleep:      func(time.Duration) {},
		TmuxTarget: func() (string, error) { return "wt-test:claude", nil },
		Capture:    func(string) (string, error) { return paneOut, nil },
	}
}

func TestTick_IdlePaneLeavesNoFootprint(t *testing.T) {
	cfg := setupCfg(t, "● Read(/x)\n  ⎿  Read 12 lines\n────────────\n❯ \n────────────\n  -- INSERT --\n")
	if err := Tick(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkerDir, "worker-stop-errors.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no worker-stop-errors.jsonl on idle, got %v", err)
	}
	entries, _ := os.ReadDir(cfg.Inbox)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("unexpected tell dropped on idle pane: %s", e.Name())
		}
	}
}

func TestTick_StopChainHungLogsAndForceReleases(t *testing.T) {
	cfg := setupCfg(t, "● Bash(go test ./...)\n  ⎿  ok\n\n• Running Stop hook: Checking fleet state\n")
	// Override the agentpane classifier-relevant pane so it returns
	// "running" (no mode line, no stop-hook bullet recognition - use
	// raw "starting up" which classifies as running per the existing
	// fallback).
	cfg.Capture = func(string) (string, error) { return "starting up\n", nil }
	if err := Tick(cfg); err != nil {
		t.Fatal(err)
	}

	errPath := filepath.Join(cfg.WorkerDir, "worker-stop-errors.jsonl")
	b, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatalf("expected worker-stop-errors.jsonl: %v", err)
	}
	var row struct {
		Kind  string `json:"kind"`
		Slug  string `json:"slug"`
		Wait  string `json:"wait"`
		State string `json:"state"`
		Agent string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &row); err != nil {
		t.Fatalf("unmarshal jsonl: %v", err)
	}
	if row.Kind != "slow-stop-chain" {
		t.Errorf("kind = %q", row.Kind)
	}
	if row.Slug != "wt-test" {
		t.Errorf("slug = %q", row.Slug)
	}
	if row.State != "running" {
		t.Errorf("state = %q", row.State)
	}
	if row.Wait != "5s" {
		t.Errorf("wait = %q", row.Wait)
	}
	if row.Agent != "claude" {
		t.Errorf("agent = %q", row.Agent)
	}

	entries, err := os.ReadDir(cfg.Inbox)
	if err != nil {
		t.Fatal(err)
	}
	var tell string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			tell = e.Name()
		}
	}
	if tell == "" {
		t.Fatal("expected force-release tell in inbox")
	}
	body, err := os.ReadFile(filepath.Join(cfg.Inbox, tell))
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]string
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatal(err)
	}
	if ev["source"] != "stop-watchdog" {
		t.Errorf("source = %q", ev["source"])
	}
	if !strings.Contains(ev["body"], "did not release to idle TUI") {
		t.Errorf("body = %q", ev["body"])
	}
}

func TestTick_NoInboxIsNoop(t *testing.T) {
	cfg := setupCfg(t, "starting up\n")
	cfg.Inbox = ""
	if err := Tick(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestTick_TmuxUnavailableSkipsClassify(t *testing.T) {
	cfg := setupCfg(t, "starting up\n")
	cfg.TmuxTarget = func() (string, error) { return "", nil }
	if err := Tick(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkerDir, "worker-stop-errors.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no jsonl when tmux unavailable, got %v", err)
	}
}

func TestTick_DeadPaneIsNoop(t *testing.T) {
	cfg := setupCfg(t, "■ Conversation interrupted - tell the model what to do differently.\n")
	cfg.Agent = "codex"
	if err := Tick(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg.WorkerDir, "worker-stop-errors.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no jsonl when pane is dead, got %v", err)
	}
}
