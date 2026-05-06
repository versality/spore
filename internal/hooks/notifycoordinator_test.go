package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifyCoordinator_WritesPoke(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	slug := "myproject"
	if err := NotifyCoordinator(slug); err != nil {
		t.Fatalf("NotifyCoordinator: %v", err)
	}

	inbox := filepath.Join(state, slug, "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected 1 json file, got %d: %v", len(jsonFiles), jsonFiles)
	}

	b, err := os.ReadFile(filepath.Join(inbox, jsonFiles[0]))
	if err != nil {
		t.Fatal(err)
	}
	var ev tellEvent
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatalf("unmarshal poke: %v", err)
	}
	if ev.Ts == "" {
		t.Error("ts is empty")
	}
	if ev.Source != "notification" {
		t.Errorf("source=%q, want notification", ev.Source)
	}
	if ev.Body != "poke" {
		t.Errorf("body=%q, want poke", ev.Body)
	}
}

func TestNotifyCoordinator_AtomicWrite(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	if err := NotifyCoordinator("proj"); err != nil {
		t.Fatal(err)
	}

	tmpDir := filepath.Join(state, "proj", "inbox", ".tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("leftover in .tmp: %s", e.Name())
		}
	}
}

func TestNotifyCoordinator_CreatesInboxDirs(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	if err := NotifyCoordinator("fresh"); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"", ".tmp", "read"} {
		p := filepath.Join(state, "fresh", "inbox", sub)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing dir %s: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", sub)
		}
	}
}

func TestNotifyCoordinatorEnv_NoProjectIsNoop(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("WT_PROJECT", "")
	t.Setenv("SPORE_TASK_INBOX", "")

	if err := NotifyCoordinatorEnv(); err != nil {
		t.Fatalf("NotifyCoordinatorEnv: %v", err)
	}
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Errorf("expected no project dirs created, got %d", len(entries))
	}
}

func TestNotifyCoordinatorEnv_PokesProjectInbox(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("WT_PROJECT", "myproject")
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(t.TempDir(), "rower-slug", "inbox"))

	if err := NotifyCoordinatorEnv(); err != nil {
		t.Fatalf("NotifyCoordinatorEnv: %v", err)
	}
	inbox := filepath.Join(state, "myproject", "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected 1 json file, got %d", len(jsonFiles))
	}
}

func TestNotifyCoordinatorEnv_SelfPokeIsNoop(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("WT_PROJECT", "myproject")
	inbox := filepath.Join(state, "myproject", "inbox")
	t.Setenv("SPORE_TASK_INBOX", inbox)

	if err := NotifyCoordinatorEnv(); err != nil {
		t.Fatalf("NotifyCoordinatorEnv: %v", err)
	}
	if _, err := os.Stat(inbox); !os.IsNotExist(err) {
		t.Errorf("expected inbox not created (self-poke skipped), got err=%v", err)
	}
}
