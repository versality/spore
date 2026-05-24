package testagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCoordinatorRecordsRoleFile(t *testing.T) {
	dir := t.TempDir()
	role := filepath.Join(dir, "role.md")
	if err := os.WriteFile(role, []byte("coordinator role"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv("WT_SESSION_KIND", "coordinator")
	t.Setenv("SPORE_TASK_SLUG", "coordinator")
	t.Setenv("SPORE_COORDINATOR_ROLE", role)

	code := Run(context.Background(), Options{Provider: "fake-agent", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	event := findEvent(readEvents(t, logPath), "coordinator-contract")
	if event == nil {
		t.Fatal("coordinator-contract event missing")
	}
	if event.Fields["role_bytes"] != "16" {
		t.Fatalf("role_bytes = %q", event.Fields["role_bytes"])
	}
}

func TestRunCoordinatorWarnsWithoutRoleFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	t.Setenv(EnvMode, ModeOneTurn)
	t.Setenv(EnvEventLog, logPath)
	t.Setenv("WT_SESSION_KIND", "coordinator")
	t.Setenv("SPORE_TASK_SLUG", "coordinator")

	code := Run(context.Background(), Options{Provider: "fake-agent", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if findEvent(readEvents(t, logPath), "coordinator-contract-warning") == nil {
		t.Fatal("coordinator-contract-warning event missing")
	}
}
