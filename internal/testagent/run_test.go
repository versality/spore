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

func TestRunWorkerLaunchContractRecordsBrief(t *testing.T) {
	dir := t.TempDir()
	wtDir := filepath.Join(dir, ".wt")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(wtDir, "initial-prompt")
	if err := os.WriteFile(briefPath, []byte("brief body"), 0o644); err != nil {
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
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, ModeWorkThenExit)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv("WT_SESSION_KIND", "worker")
	t.Setenv("SPORE_TASK_SLUG", "smoke")
	t.Setenv("SPORE_PROJECT_ROOT", filepath.Dir(dir))
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(dir, "inbox"))
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", filepath.Join(dir, "state"))
	t.Setenv("WT_PROJECT", "spore")
	t.Setenv("SPORE_BRIEF_FILE", briefPath)

	code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	events := readEvents(t, logPath)
	contract := findEvent(events, "launch-contract")
	if contract == nil {
		t.Fatalf("launch-contract event missing: %s", eventTypes(events))
	}
	if contract.Fields["initial_prompt_exists"] != "true" {
		t.Fatalf("initial_prompt_exists = %q", contract.Fields["initial_prompt_exists"])
	}
	if contract.Fields["brief_bytes"] != "10" {
		t.Fatalf("brief_bytes = %q", contract.Fields["brief_bytes"])
	}
}

func TestRunWorkerLaunchContractErrorsWithoutSlug(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, ModeWorkThenExit)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv("WT_SESSION_KIND", "worker")

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	events := readEvents(t, logPath)
	if findEvent(events, "launch-contract-error") == nil {
		t.Fatalf("launch-contract-error event missing: %s", eventTypes(events))
	}
	if findEvent(events, "launch-contract-warning") == nil {
		t.Fatalf("launch-contract-warning event missing: %s", eventTypes(events))
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

func TestRunProgressKeepsWritingUntilCanceled(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, ModeProgress)
	t.Setenv(EnvEventLog, logPath)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(250 * time.Millisecond)
		cancel()
	}()

	code := Run(ctx, Options{Provider: "codex", Now: time.Now})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	events := readEvents(t, logPath)
	if findEvent(events, "progress") == nil {
		t.Fatalf("progress event missing: %s", eventTypes(events))
	}
	if findEvent(events, "stop") == nil {
		t.Fatalf("stop event missing: %s", eventTypes(events))
	}
}

func TestRunWaitForFileStopsOnSentinel(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	exitPath := filepath.Join(dir, "exit")
	t.Setenv(EnvMode, ModeWaitForFile)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvExitFile, exitPath)
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := os.WriteFile(exitPath, []byte("go"), 0o644); err != nil {
			panic(err)
		}
	}()

	code := Run(context.Background(), Options{Provider: "claude", Now: time.Now})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	events := readEvents(t, logPath)
	if findEvent(events, "progress") == nil {
		t.Fatalf("progress event missing: %s", eventTypes(events))
	}
}

func TestRunOneTurnExitsZero(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if got := eventTypes(readEvents(t, logPath)); got != "start,ready,progress,stop" {
		t.Fatalf("events = %s", got)
	}
}

func TestRunImmediateExitModes(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want int
	}{
		{mode: ModeExitZero, want: 0},
		{mode: ModeExitNonzero, want: 127},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "events.jsonl")
			t.Setenv(EnvMode, tc.mode)
			t.Setenv(EnvEventLog, logPath)

			code := Run(context.Background(), Options{Provider: "codex", Now: fixedNow})
			if code != tc.want {
				t.Fatalf("Run exit = %d, want %d", code, tc.want)
			}
			if len(readEvents(t, logPath)) < 2 {
				t.Fatal("event log too short")
			}
		})
	}
}

func TestRunCrashAfterReadyWritesReadyThenFails(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv(EnvMode, ModeCrashReady)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvReadyFile, readyPath)

	code := Run(context.Background(), Options{Provider: "claude", Now: fixedNow})
	if code == 0 {
		t.Fatal("Run exit = 0, want non-zero")
	}
	if _, err := os.Stat(readyPath); err != nil {
		t.Fatalf("ready file: %v", err)
	}
	if got := eventTypes(readEvents(t, logPath)); got != "start,ready,error" {
		t.Fatalf("events = %s", got)
	}
}

func TestRunHangBeforeReadyNeverTouchesReady(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "events.jsonl")
	readyPath := filepath.Join(dir, "ready")
	t.Setenv(EnvMode, ModeHangReady)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv(EnvReadyFile, readyPath)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	code := Run(ctx, Options{Provider: "codex", Now: time.Now})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(readyPath); !os.IsNotExist(err) {
		t.Fatalf("ready file err = %v, want not exist", err)
	}
	if got := eventTypes(readEvents(t, logPath)); got != "start,hang,stop" {
		t.Fatalf("events = %s", got)
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

func findEvent(events []Event, typ string) *Event {
	for i := range events {
		if events[i].Type == typ {
			return &events[i]
		}
	}
	return nil
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
}
