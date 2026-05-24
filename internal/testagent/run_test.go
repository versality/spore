package testagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunWorkThenExitRecordsLaunchAndProgress(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv(EnvMode, ModeWorkThenExit)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvReadyFile, readyPath)
	t.Setenv(EnvTurnLimit, "2")
	t.Setenv("SPORE_TASK_SLUG", "smoke")
	var stdout bytes.Buffer

	code := Run(context.Background(), Options{
		Provider: "codex",
		Argv:     []string{"codex", "--", "brief"},
		Stdout:   &stdout,
		Now:      fixedNow,
	})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("ready file: %v", err)
	}
	events := readEvents(t, logPath)
	if got := eventTypes(events); got != "start,ready,progress,progress,stop" {
		t.Fatalf("events = %s", got)
	}
	if events[0].Provider != "codex" || events[0].Mode != ModeWorkThenExit {
		t.Fatalf("start event provider/mode = %q/%q", events[0].Provider, events[0].Mode)
	}
	if events[0].Env["SPORE_TASK_SLUG"] != "smoke" {
		t.Fatalf("start env SPORE_TASK_SLUG = %q", events[0].Env["SPORE_TASK_SLUG"])
	}
	if stdout.String() == "" {
		t.Fatal("stdout is empty, want progress output")
	}
}

func TestRunUnknownModeRecordsError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, "wat")
	t.Setenv(EnvEventLog, logPath)
	var stderr bytes.Buffer

	code := Run(context.Background(), Options{Provider: "claude", Stderr: &stderr, Now: fixedNow})
	if code == 0 {
		t.Fatal("Run exit = 0, want non-zero")
	}
	events := readEvents(t, logPath)
	if got := eventTypes(events); got != "start,error" {
		t.Fatalf("events = %s", got)
	}
	if events[1].Error == "" {
		t.Fatal("error event has empty error")
	}
	if stderr.String() == "" {
		t.Fatal("stderr is empty")
	}
}

func TestRunMissingEventLogFailsClearly(t *testing.T) {
	t.Setenv(EnvMode, ModeWorkThenExit)
	var stderr bytes.Buffer

	code := Run(context.Background(), Options{Provider: "codex", Stderr: &stderr, Now: fixedNow})
	if code == 0 {
		t.Fatal("Run exit = 0, want non-zero")
	}
	if !bytes.Contains(stderr.Bytes(), []byte(EnvEventLog)) {
		t.Fatalf("stderr = %q, want %s", stderr.String(), EnvEventLog)
	}
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func eventTypes(events []Event) string {
	var out string
	for i, e := range events {
		if i > 0 {
			out += ","
		}
		out += e.Type
	}
	return out
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
}
