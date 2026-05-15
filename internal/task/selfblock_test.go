package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskFile(t *testing.T, dir, slug, status string) string {
	t.Helper()
	path := filepath.Join(dir, slug+".md")
	body := "---\nstatus: " + status + "\nslug: " + slug + "\ntitle: " + slug + "\n---\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write task file: %v", err)
	}
	return path
}

func TestBlockAutoFlipsActiveToBlocked(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "active")
	if err := BlockAuto(dir, "x", SelfBlockBlocker); err != nil {
		t.Fatalf("BlockAuto: %v", err)
	}
	if got := readStatus(t, path); got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "blocker: "+SelfBlockBlocker) {
		t.Errorf("missing blocker line: %s", raw)
	}
}

func TestBlockAutoSkipsInboxGate(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "active")
	inboxDir, err := InboxDir("x")
	if err != nil {
		t.Fatalf("InboxDir: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "999999.json"), []byte(`{"slug":"x","ts":"","msg":"unread"}`), 0o644); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	if err := BlockAuto(dir, "x", SelfBlockBlocker); err != nil {
		t.Fatalf("BlockAuto with unread inbox: %v", err)
	}
	if got := readStatus(t, path); got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
}

func TestBlockAutoRefusedFromCoordinatorSession(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindCoordinator)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "active")
	err := BlockAuto(dir, "x", SelfBlockBlocker)
	if err == nil {
		t.Fatal("BlockAuto from coordinator should refuse, got nil")
	}
	if !strings.Contains(err.Error(), "coordinator session is not authorized") {
		t.Errorf("error %q should mention coordinator gate", err)
	}
	if got := readStatus(t, path); got != "active" {
		t.Errorf("status should be unchanged, got %q", got)
	}
}

func TestSelfBlockOnCoordinatorTellHappyPath(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "active")
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "x"); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
	if got := readStatus(t, path); got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "blocker: "+SelfBlockBlocker) {
		t.Errorf("missing blocker line: %s", raw)
	}
}

func TestSelfBlockOnCoordinatorTellNoopWhenTargetIsNotCoordinator(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "active")
	if err := SelfBlockOnCoordinatorTell(dir, "some-other-slug", "x"); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
	if got := readStatus(t, path); got != "active" {
		t.Errorf("status = %q, want active (no flip)", got)
	}
}

func TestSelfBlockOnCoordinatorTellNoopWhenNoCallerSlug(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, ""); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
}

func TestSelfBlockOnCoordinatorTellNoopWhenCallerIsCoordinator(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, CoordinatorTellTarget); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
}

func TestSelfBlockOnCoordinatorTellTolerantWhenAlreadyBlocked(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "blocked")
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "x"); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell on already-blocked should be no-op, got: %v", err)
	}
	if got := readStatus(t, path); got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
}
