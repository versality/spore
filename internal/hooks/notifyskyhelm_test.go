package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifySkyhelm_WritesPoke(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SKYHELM_STATE_DIR", state)

	slug := "myproject"
	if err := NotifySkyhelm(slug); err != nil {
		t.Fatalf("NotifySkyhelm: %v", err)
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

func TestNotifySkyhelm_AtomicWrite(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SKYHELM_STATE_DIR", state)

	if err := NotifySkyhelm("proj"); err != nil {
		t.Fatal(err)
	}

	// .tmp should be empty (file was renamed out).
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

func TestNotifySkyhelm_CreatesInboxDirs(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SKYHELM_STATE_DIR", state)

	if err := NotifySkyhelm("fresh"); err != nil {
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
