package tokenmonitor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsCoordinator(t *testing.T) {
	cases := []struct {
		inbox    string
		stateDir string
		want     bool
	}{
		{"/state/coord", "/state/coord", true},
		{"/state/coord/myproj/inbox", "/state/coord", true},
		{"/state/wt/slug/inbox", "/state/coord", false},
		{"", "/state/coord", false},
	}
	for _, tc := range cases {
		cfg := Config{Inbox: tc.inbox, StateDir: tc.stateDir}
		if got := cfg.IsCoordinator(); got != tc.want {
			t.Errorf("IsCoordinator(%q, %q) = %v, want %v", tc.inbox, tc.stateDir, got, tc.want)
		}
	}
}

func TestCheckSkipsNonCoordinator(t *testing.T) {
	cfg := Config{
		Inbox:    "/some/other/path",
		StateDir: "/state/coord",
	}
	result := Check(cfg, HookPayload{})
	if result.Level != "skip" {
		t.Errorf("expected skip, got %s", result.Level)
	}
}

func TestCheckHardCap(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcript")
	os.MkdirAll(transcriptDir, 0o700)
	f := filepath.Join(transcriptDir, "session.jsonl")

	line := `{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":195000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	os.WriteFile(f, []byte(line+"\n"), 0o644)

	stateDir := filepath.Join(dir, "state")
	cfg := Config{
		SoftCap:  150000,
		HardCap:  190000,
		StateDir: stateDir,
		Inbox:    stateDir,
	}

	result := Check(cfg, HookPayload{SessionID: "test", TranscriptPath: f})
	if result.Level != "hard" {
		t.Errorf("expected hard, got %s", result.Level)
	}
	if !result.ShouldFire {
		t.Error("expected ShouldFire = true")
	}
	if result.Ctx != 195000 {
		t.Errorf("Ctx = %d, want 195000", result.Ctx)
	}
}

func TestCheckSoftCap(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcript")
	os.MkdirAll(transcriptDir, 0o700)
	f := filepath.Join(transcriptDir, "session.jsonl")

	line := `{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":160000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	os.WriteFile(f, []byte(line+"\n"), 0o644)

	stateDir := filepath.Join(dir, "state")
	cfg := Config{
		SoftCap:  150000,
		HardCap:  190000,
		StateDir: stateDir,
		Inbox:    stateDir,
	}

	result := Check(cfg, HookPayload{SessionID: "test-soft", TranscriptPath: f})
	if result.Level != "soft" {
		t.Errorf("expected soft, got %s", result.Level)
	}
	if !result.ShouldFire {
		t.Error("expected ShouldFire = true on first soft crossing")
	}

	result2 := Check(cfg, HookPayload{SessionID: "test-soft", TranscriptPath: f})
	if result2.Level != "ok" {
		t.Errorf("expected ok on second check (soft marker exists), got %s", result2.Level)
	}
	if result2.ShouldFire {
		t.Error("expected ShouldFire = false on second soft check")
	}
}

func TestCheckOk(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "transcript")
	os.MkdirAll(transcriptDir, 0o700)
	f := filepath.Join(transcriptDir, "session.jsonl")

	line := `{"type":"assistant","message":{"role":"assistant","content":[],"usage":{"input_tokens":50000,"output_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	os.WriteFile(f, []byte(line+"\n"), 0o644)

	stateDir := filepath.Join(dir, "state")
	cfg := Config{
		SoftCap:  150000,
		HardCap:  190000,
		StateDir: stateDir,
		Inbox:    stateDir,
	}

	result := Check(cfg, HookPayload{SessionID: "test-ok", TranscriptPath: f})
	if result.Level != "ok" {
		t.Errorf("expected ok, got %s", result.Level)
	}
}

func TestAppendLedger(t *testing.T) {
	dir := t.TempDir()
	ledgerFile := filepath.Join(dir, "ledger.jsonl")
	cfg := Config{
		StateDir:   dir,
		LedgerFile: ledgerFile,
	}
	cfg = cfg.Defaults()
	appendLedger(cfg, "sess1", 100000, false, false)

	content, err := os.ReadFile(ledgerFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Error("expected ledger content")
	}
}
