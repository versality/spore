package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockFlipsActiveToBlocked(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Block(tasksDir, "x", "scheduler:test-trigger"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if status := readStatus(t, taskPath); status != "blocked" {
		t.Errorf("after Block: status = %q, want blocked", status)
	}
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "blocker: scheduler:test-trigger") {
		t.Errorf("after Block: file missing blocker line:\n%s", raw)
	}
}

func TestBlockRefusedFromCoordinatorSession(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindCoordinator)
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Block(tasksDir, "x", "anything")
	if err == nil {
		t.Fatal("Block should refuse from coordinator session, got nil")
	}
	if !strings.Contains(err.Error(), "coordinator session is not authorized") {
		t.Errorf("error %q should mention coordinator gate", err)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("status should be unchanged after refused block, got %q", status)
	}
}

func TestBlockAllowedFromWorkerSession(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: active\nslug: x\ntitle: X\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Block(tasksDir, "x", "worker: own slug"); err != nil {
		t.Fatalf("Block from worker session: %v", err)
	}
	if status := readStatus(t, taskPath); status != "blocked" {
		t.Errorf("after Block: status = %q, want blocked", status)
	}
}

func TestUnblockFlipsBlockedToActiveAndClearsBlocker(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	if err := os.WriteFile(taskPath, []byte("---\nstatus: blocked\nslug: x\ntitle: X\nblocker: scheduler:foo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Unblock(tasksDir, "x"); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	if status := readStatus(t, taskPath); status != "active" {
		t.Errorf("after Unblock: status = %q, want active", status)
	}
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "blocker:") {
		t.Errorf("after Unblock: file still has blocker line:\n%s", raw)
	}
}
