package workerstopforceclosing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTask(t *testing.T, worktree, slug, status string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(worktree, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nslug: " + slug + "\nstatus: " + status + "\n---\n\nbody\n"
	path := filepath.Join(worktree, "tasks", slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedState(t *testing.T, dir, slug string, s state) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, slug+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func baseCfg(t *testing.T, slug, status, head string) (Config, string, string) {
	t.Helper()
	wt := t.TempDir()
	state := t.TempDir()
	writeTask(t, wt, slug, status)
	return Config{
		Slug:     slug,
		Worktree: wt,
		StateDir: state,
		Now:      func() time.Time { return time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC) },
		Head:     func(string) string { return head },
	}, wt, state
}

func TestRun_FirstTurn_NoFire(t *testing.T) {
	cfg, _, _ := baseCfg(t, "demo", "active", "deadbeef")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 || res.Reason != "first-turn" {
		t.Fatalf("got %+v, want first-turn exit 0", res)
	}
}

func TestRun_IdleSecondTurn_Fires(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "active", "deadbeef")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "deadbeef",
		LastStatus: "active",
	})
	cfg.ToolUsesSince = func(string, time.Time) []string { return nil }
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 2 {
		t.Fatalf("want exit 2, got %+v", res)
	}
	if !strings.Contains(res.Stderr, "wt/demo") {
		t.Fatalf("stderr missing branch: %q", res.Stderr)
	}
}

func TestRun_HeadAdvanced_NoFire(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "active", "newsha")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "oldsha",
		LastStatus: "active",
	})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 || res.Reason != "head-advanced" {
		t.Fatalf("got %+v, want head-advanced exit 0", res)
	}
}

func TestRun_StatusFlipped_NoFire(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "done", "deadbeef")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "deadbeef",
		LastStatus: "active",
	})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	// Status != active short-circuits before status-flipped, so we expect not-active.
	if res.ExitCode != 0 || res.Reason != "not-active" {
		t.Fatalf("got %+v, want not-active exit 0", res)
	}
}

func TestRun_BlockedStatus_NoFire(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "blocked", "deadbeef")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "deadbeef",
		LastStatus: "active",
	})
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("got %+v, want exit 0 on blocked", res)
	}
}

func TestRun_EditInTranscript_NoFire(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "active", "deadbeef")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "deadbeef",
		LastStatus: "active",
	})
	cfg.TranscriptPath = "/fake.jsonl"
	cfg.ToolUsesSince = func(p string, since time.Time) []string {
		return []string{"Bash", "Edit"}
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 0 || res.Reason != "edit-in-transcript" {
		t.Fatalf("got %+v, want edit-in-transcript exit 0", res)
	}
}

func TestRun_ReadOnlyToolsOnly_Fires(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "active", "deadbeef")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 59, 0, 0, time.UTC),
		LastHead:   "deadbeef",
		LastStatus: "active",
	})
	cfg.TranscriptPath = "/fake.jsonl"
	cfg.ToolUsesSince = func(p string, since time.Time) []string {
		return []string{"Read", "Bash", "Grep"}
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.ExitCode != 2 {
		t.Fatalf("want exit 2 with only read-only tools, got %+v", res)
	}
}

func TestRun_StateUpdatedOnFire(t *testing.T) {
	cfg, _, stateDir := baseCfg(t, "demo", "active", "newsha")
	seedState(t, stateDir, "demo", state{
		LastStopAt: time.Date(2026, 5, 14, 11, 0, 0, 0, time.UTC),
		LastHead:   "newsha",
		LastStatus: "active",
	})
	cfg.ToolUsesSince = func(string, time.Time) []string { return nil }
	if _, err := Run(cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if !s.LastStopAt.Equal(time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("LastStopAt not updated: %v", s.LastStopAt)
	}
	if s.LastHead != "newsha" || s.LastStatus != "active" {
		t.Fatalf("state not updated: %+v", s)
	}
}

func TestRun_MissingTask_Noop(t *testing.T) {
	cfg := Config{
		Slug:     "ghost",
		Worktree: t.TempDir(),
		StateDir: t.TempDir(),
		Now:      func() time.Time { return time.Now() },
		Head:     func(string) string { return "x" },
	}
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Reason != "missing-task" {
		t.Fatalf("got %+v, want missing-task exit 0", res)
	}
}

func TestScanClaudeToolUses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	lines := []string{
		`{"timestamp":"2026-05-14T11:00:00Z","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"timestamp":"2026-05-14T11:30:00Z","message":{"content":[{"type":"tool_use","name":"Edit"}]}}`,
		`{"timestamp":"2026-05-14T11:50:00Z","message":{"content":[{"type":"text","text":"hi"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 5, 14, 11, 15, 0, 0, time.UTC)
	got := ScanClaudeToolUses(path, since)
	if len(got) != 1 || got[0] != "Edit" {
		t.Fatalf("got %v, want [Edit]", got)
	}
}
