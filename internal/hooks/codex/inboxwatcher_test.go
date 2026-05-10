package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTmux struct {
	mu          sync.Mutex
	hasSession  bool
	pane        string
	respawnArgs [][]string
	resolveCmds []string
}

func (f *fakeTmux) HasSession(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasSession
}

func (f *fakeTmux) ResolvePane(session string, paneCmds []string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCmds = append([]string(nil), paneCmds...)
	if f.pane == "" {
		return "", false
	}
	return f.pane, true
}

func (f *fakeTmux) RespawnPane(paneID string, argv []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respawnArgs = append(f.respawnArgs, append([]string{paneID}, argv...))
	return nil
}

func writeInboxFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunInboxWatcher_WakesOnNewFile(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	inbox := filepath.Join(stateDir, "proj1", "inbox")
	writeInboxFile(t, inbox, "100.json", `{"source":"agent","body":"hi"}`)

	tm := &fakeTmux{hasSession: true, pane: "%5"}
	cfg := &InboxWatcherConfig{
		StateDir:    stateDir,
		Projects:    []ProjectInbox{{Name: "proj1", Path: inbox}},
		SessionName: "skyhelm",
		PaneCmds:    []string{"codex-raw"},
		WakeArgv:    []string{"wake-cmd"},
		Driver:      "codex",
		Once:        true,
		Tmux:        tm,
		Sleep:       func(time.Duration) {},
		Now:         func() time.Time { return refTime },
	}

	if err := RunInboxWatcher(context.Background(), cfg); err != nil {
		t.Fatalf("watcher: %v", err)
	}

	if len(tm.respawnArgs) != 1 {
		t.Fatalf("respawn calls = %d, want 1", len(tm.respawnArgs))
	}
	if tm.respawnArgs[0][0] != "%5" {
		t.Errorf("pane id = %q", tm.respawnArgs[0][0])
	}
	if tm.respawnArgs[0][1] != "wake-cmd" {
		t.Errorf("argv = %v", tm.respawnArgs[0])
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-inbox-watcher.jsonl"))
	if !strings.Contains(string(body), `"status":"wake-sent"`) {
		t.Errorf("ledger missing wake-sent: %s", body)
	}
	if !strings.Contains(string(body), `"source":"agent"`) {
		t.Errorf("ledger missing source: %s", body)
	}
	state, _ := os.ReadFile(filepath.Join(stateDir, "proj1", "codex-inbox-watcher.last"))
	if strings.TrimSpace(string(state)) != "100.json" {
		t.Errorf("last marker = %q", state)
	}
}

func TestRunInboxWatcher_NoCodexDriver_Idles(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	inbox := filepath.Join(stateDir, "proj1", "inbox")
	writeInboxFile(t, inbox, "100.json", `{"source":"x"}`)

	tm := &fakeTmux{hasSession: true, pane: "%5"}
	cfg := &InboxWatcherConfig{
		StateDir:    stateDir,
		Projects:    []ProjectInbox{{Name: "proj1", Path: inbox}},
		SessionName: "skyhelm",
		PaneCmds:    []string{"codex-raw"},
		WakeArgv:    []string{"wake-cmd"},
		Driver:      "claude",
		Once:        true,
		Tmux:        tm,
		Sleep: func(d time.Duration) {
			tm.mu.Lock()
			tm.hasSession = false
			tm.mu.Unlock()
		},
		Now: func() time.Time { return refTime },
	}
	if err := RunInboxWatcher(context.Background(), cfg); err != nil {
		t.Fatalf("watcher: %v", err)
	}
	if len(tm.respawnArgs) != 0 {
		t.Errorf("respawn should not have been called for claude driver")
	}
}

func TestRunInboxWatcher_RecordOnlyMode(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	inbox := filepath.Join(stateDir, "proj1", "inbox")
	writeInboxFile(t, inbox, "100.json", `{"source":"x"}`)

	tm := &fakeTmux{hasSession: true, pane: "%5"}
	cfg := &InboxWatcherConfig{
		StateDir:    stateDir,
		Projects:    []ProjectInbox{{Name: "proj1", Path: inbox}},
		SessionName: "skyhelm",
		PaneCmds:    []string{"codex-raw"},
		WakeArgv:    []string{"wake"},
		WakeMode:    WakeModeRecordOnly,
		Driver:      "codex",
		Once:        true,
		Tmux:        tm,
		Sleep:       func(time.Duration) {},
		Now:         func() time.Time { return refTime },
	}
	_ = RunInboxWatcher(context.Background(), cfg)
	if len(tm.respawnArgs) != 0 {
		t.Errorf("respawn should not run in record-only mode")
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "codex-inbox-watcher.jsonl"))
	if !strings.Contains(string(body), `"status":"recorded-only"`) {
		t.Errorf("missing recorded-only event: %s", body)
	}
}

func TestRunInboxWatcher_SessionGoneNoLoop(t *testing.T) {
	tm := &fakeTmux{hasSession: false}
	cfg := &InboxWatcherConfig{
		StateDir:    t.TempDir(),
		Projects:    []ProjectInbox{{Name: "p", Path: t.TempDir()}},
		SessionName: "skyhelm",
		PaneCmds:    []string{"x"},
		WakeArgv:    []string{"wake"},
		Driver:      "codex",
		StartupWait: 500 * time.Millisecond,
		Once:        true,
		Tmux:        tm,
		Sleep:       func(time.Duration) {},
		Now:         func() time.Time { return refTime },
	}
	err := RunInboxWatcher(context.Background(), cfg)
	if err == nil {
		t.Errorf("expected error when session never appears")
	}
}

func TestObserveInbox_NewestAndCount(t *testing.T) {
	dir := t.TempDir()
	writeInboxFile(t, dir, "100.json", `{"source":"a"}`)
	writeInboxFile(t, dir, "200.json", `{"source":"b"}`)
	writeInboxFile(t, dir, "300.json", `{"source":"c"}`)
	obs := observeInbox(dir)
	if obs.Newest != "300.json" {
		t.Errorf("newest = %q", obs.Newest)
	}
	if obs.UnreadCount != 3 {
		t.Errorf("count = %d", obs.UnreadCount)
	}
	if obs.Source != "c" {
		t.Errorf("source = %q", obs.Source)
	}
}

func TestObserveInbox_Empty(t *testing.T) {
	dir := t.TempDir()
	obs := observeInbox(dir)
	if obs.Newest != "" || obs.UnreadCount != 0 {
		t.Errorf("expected empty obs, got %+v", obs)
	}
}
