package testagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunCompleteWithEvidenceWritesEvidence(t *testing.T) {
	dir := t.TempDir()
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
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeEvidence)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "fake-agent", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".wt", "fake-agent-evidence.md")); err != nil {
		t.Fatalf("evidence file: %v", err)
	}
	if findEvent(readEvents(t, logPath), "evidence") == nil {
		t.Fatal("evidence event missing")
	}
}

func TestRunCommitChangeCommitsDeterministicChange(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "Test Agent")
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
	logPath := filepath.Join(dir, "events.jsonl")
	t.Setenv(EnvMode, ModeCommitChange)
	t.Setenv(EnvEventLog, logPath)

	code := Run(context.Background(), Options{Provider: "fake-agent", Now: fixedNow})
	if code != 0 {
		t.Fatalf("Run exit = %d, want 0", code)
	}
	out := runGit(t, dir, "log", "--oneline", "-1")
	if out == "" {
		t.Fatal("git log is empty")
	}
	if findEvent(readEvents(t, logPath), "git-commit") == nil {
		t.Fatal("git-commit event missing")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
