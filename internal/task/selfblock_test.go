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
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "I'm stuck", "x"); err != nil {
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
	if err := SelfBlockOnCoordinatorTell(dir, "some-other-slug", "hi", "x"); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
	if got := readStatus(t, path); got != "active" {
		t.Errorf("status = %q, want active (no flip)", got)
	}
}

func TestSelfBlockOnCoordinatorTellNoopWhenNoCallerSlug(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "hi", ""); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
}

func TestSelfBlockOnCoordinatorTellNoopWhenCallerIsCoordinator(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "hi", CoordinatorTellTarget); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
	}
}

func TestSelfBlockOnCoordinatorTellTolerantWhenAlreadyBlocked(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	dir := t.TempDir()
	path := writeTaskFile(t, dir, "x", "blocked")
	if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, "hi", "x"); err != nil {
		t.Fatalf("SelfBlockOnCoordinatorTell on already-blocked should be no-op, got: %v", err)
	}
	if got := readStatus(t, path); got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
}

func TestSelfBlockOnCoordinatorTellBodyExempt(t *testing.T) {
	t.Setenv(SessionKindEnv, SessionKindWorker)
	cases := []struct {
		name string
		body string
	}{
		{"plan ready basic", "plan ready: my-slug"},
		{"plan ready leading space", "  plan ready: my-slug"},
		{"plan ready no slug", "plan ready:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeTaskFile(t, dir, "x", "active")
			if err := SelfBlockOnCoordinatorTell(dir, CoordinatorTellTarget, c.body, "x"); err != nil {
				t.Fatalf("SelfBlockOnCoordinatorTell: %v", err)
			}
			if got := readStatus(t, path); got != "active" {
				t.Errorf("body %q: status = %q, want active (exempt)", c.body, got)
			}
		})
	}
}

func TestBodyTriggersAutoBlock(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"I'm stuck", true},
		{"", true},
		{"question about X", true},
		{"plan-ready misspelled", true},
		{"plan ready: foo", false},
		{"plan ready:", false},
		{" plan ready: foo", false},
		{"\tplan ready: foo", false},
	}
	for _, c := range cases {
		if got := bodyTriggersAutoBlock(c.body); got != c.want {
			t.Errorf("bodyTriggersAutoBlock(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}
