package testagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunProcessesInboxMessages(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(inbox, "001.json")
	if err := os.WriteFile(msg, []byte(`{"body":"wake"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv("SPORE_TASK_INBOX", inbox)

	code := Run(context.Background(), Options{Provider: "fake-agent", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(msg + ".processed"); err != nil {
		t.Fatalf("processed inbox message: %v", err)
	}
	events := readEvents(t, logPath)
	for _, typ := range []string{"inbox-seen", "inbox-drained", "wake-processed"} {
		if findEvent(events, typ) == nil {
			t.Fatalf("%s event missing: %s", typ, eventTypes(events))
		}
	}
}
