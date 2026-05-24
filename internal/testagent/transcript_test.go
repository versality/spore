package testagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesCodexTranscript(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "codex.jsonl")
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvTranscript, transcript)
	t.Setenv(EnvTokenTotal, "4242")

	code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"total_tokens":4242`) {
		t.Fatalf("transcript = %s", body)
	}
	if findEvent(readEvents(t, logPath), "transcript") == nil {
		t.Fatal("transcript event missing")
	}
}

func TestRunWritesClaudeTranscript(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "claude.jsonl")
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvTranscript, transcript)
	t.Setenv(EnvTokenTotal, "120")

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"input_tokens":60`) || !strings.Contains(string(body), `"output_tokens":60`) {
		t.Fatalf("transcript = %s", body)
	}
}
