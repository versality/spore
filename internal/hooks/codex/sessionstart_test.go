package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStart_Coordinator(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("ROLE BRIEF\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := SessionStartConfig{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		BriefPath:           briefPath,
		Now:                 func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
	}
	res, err := SessionStart(cfg, strings.NewReader(`{"session_id":"sess-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected output, got skipped")
	}

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	if out.HookSpecificOutput.AdditionalContext != "ROLE BRIEF\n" {
		t.Errorf("additionalContext = %q", out.HookSpecificOutput.AdditionalContext)
	}

	ledger := filepath.Join(stateDir, "codex-context-monitor.jsonl")
	body, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if !strings.Contains(string(body), `"session_id":"sess-1"`) {
		t.Errorf("ledger missing session_id: %s", body)
	}
	if !strings.Contains(string(body), `"event":"session-start"`) {
		t.Errorf("ledger missing event: %s", body)
	}
}

func TestSessionStart_NotCoordinator(t *testing.T) {
	dir := t.TempDir()
	cfg := SessionStartConfig{
		Inbox:               filepath.Join(dir, "elsewhere"),
		CoordinatorStateDir: filepath.Join(dir, "state"),
		BriefPath:           filepath.Join(dir, "brief.md"),
	}
	res, err := SessionStart(cfg, strings.NewReader(`{"session_id":"sess-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip on non-coordinator inbox")
	}
}

func TestSessionStart_NoInbox(t *testing.T) {
	cfg := SessionStartConfig{
		CoordinatorStateDir: "/state",
		BriefPath:           "/brief.md",
	}
	res, _ := SessionStart(cfg, strings.NewReader(`{}`))
	if !res.Skipped {
		t.Fatalf("expected skip on empty inbox")
	}
}

func TestSessionStart_BriefMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := SessionStartConfig{
		Inbox:               dir,
		CoordinatorStateDir: dir,
		BriefPath:           filepath.Join(dir, "missing.md"),
	}
	res, err := SessionStart(cfg, strings.NewReader(`{"session_id":"sess-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip when brief is missing")
	}
}

func TestSessionStart_DefaultUnknownSessionID(t *testing.T) {
	dir := t.TempDir()
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte("X"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := SessionStartConfig{
		Inbox:               dir,
		CoordinatorStateDir: dir,
		BriefPath:           briefPath,
	}
	res, err := SessionStart(cfg, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected output")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "codex-context-monitor.jsonl"))
	if !strings.Contains(string(body), `"session_id":"unknown"`) {
		t.Errorf("expected unknown sid, got: %s", body)
	}
}

func TestSessionStart_InboxUnderSubdir(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	subInbox := filepath.Join(stateDir, "proj1")
	if err := os.MkdirAll(subInbox, 0o700); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(dir, "brief.md")
	os.WriteFile(briefPath, []byte("X"), 0o600)
	cfg := SessionStartConfig{
		Inbox:               subInbox,
		CoordinatorStateDir: stateDir,
		BriefPath:           briefPath,
	}
	res, err := SessionStart(cfg, strings.NewReader(`{"session_id":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected emit when inbox is under state dir")
	}
}
