package testagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCodexExecutesRuntimeHooks(t *testing.T) {
	dir := t.TempDir()
	hooksDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	hooks := `{"hooks":{"SessionStart":[{"hooks":[{"command":"printf session >> ` + outPath + `","timeout":10}]}],"PreToolUse":[{"hooks":[{"command":"printf pre >> ` + outPath + `","timeout":10}]}],"Stop":[{"hooks":[{"command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "sessionprestop" {
		t.Fatalf("hook output = %q", body)
	}
	events := readEvents(t, logPath)
	if countEvents(events, "hook") != 3 {
		t.Fatalf("hook count = %d, want 3: %s", countEvents(events, "hook"), eventTypes(events))
	}
}

func TestRunCodexInvalidHooksRecordsParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "hooks.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if findEvent(readEvents(t, logPath), "hook-parse-error") == nil {
		t.Fatal("hook-parse-error event missing")
	}
}

func TestRunClaudeExecutesStopHooks(t *testing.T) {
	dir := t.TempDir()
	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "hooks.out")
	settings := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"printf stop >> ` + outPath + `","timeout":10}]}]}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "stop" {
		t.Fatalf("hook output = %q", body)
	}
	if countEvents(readEvents(t, logPath), "hook") != 1 {
		t.Fatal("hook event missing")
	}
}

func TestRunClaudeInvalidSettingsRecordsParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.local.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if findEvent(readEvents(t, logPath), "hook-parse-error") == nil {
		t.Fatal("hook-parse-error event missing")
	}
}

func countEvents(events []Event, typ string) int {
	var n int
	for _, event := range events {
		if event.Type == typ {
			n++
		}
	}
	return n
}
