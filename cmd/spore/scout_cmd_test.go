package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScoutAppendsJSONLAndExitsZeroOnDirtyRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "rule.md"), "this has an em\u2014dash inside\n")
	gitInit(t, root)

	ledger := filepath.Join(t.TempDir(), "scout-findings.jsonl")
	var code int
	_, stderr := captureStdoutStderr(t, func() {
		code = runScout([]string{"--root", root, "--state-file", ledger})
	})
	if code != 0 {
		t.Fatalf("scout exit: want 0, got %d\nstderr: %s", code, stderr)
	}

	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("expected at least one ledger row, got empty file")
	}

	var row jsonFinding
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("first row not valid JSON: %v (%q)", err, lines[0])
	}
	if row.Lint == "" || row.Fingerprint == "" {
		t.Fatalf("row missing lint/fingerprint: %+v", row)
	}
	if !strings.Contains(stderr, "appended findings to") {
		t.Fatalf("stderr summary missing, got %q", stderr)
	}
}

func TestRunScoutAppendsAcrossRuns(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "rule.md"), "em\u2014dash here\n")
	gitInit(t, root)

	ledger := filepath.Join(t.TempDir(), "scout-findings.jsonl")
	for i := 0; i < 2; i++ {
		captureStdoutStderr(t, func() {
			if code := runScout([]string{"--root", root, "--state-file", ledger}); code != 0 {
				t.Fatalf("scout exit on run %d: %d", i, code)
			}
		})
	}
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 rows after two runs, got %d:\n%s", len(lines), data)
	}
}

func TestRunScoutCleanRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "readme.md"), "no issues here\n")
	gitInit(t, root)

	ledger := filepath.Join(t.TempDir(), "scout-findings.jsonl")
	var code int
	_, stderr := captureStdoutStderr(t, func() {
		code = runScout([]string{"--root", root, "--state-file", ledger})
	})
	if code != 0 {
		t.Fatalf("expected exit 0 on clean repo, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "clean") {
		t.Fatalf("expected 'clean' summary, got %q", stderr)
	}
}

func TestRunScoutRejectsPositional(t *testing.T) {
	var code int
	captureStdoutStderr(t, func() {
		code = runScout([]string{"unexpected"})
	})
	if code != 2 {
		t.Fatalf("expected usage exit 2, got %d", code)
	}
}

func TestRunScoutMintEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustWrite(t, filepath.Join(repo, "rule.md"), "this has an em\u2014dash inside\n")
	gitInit(t, repo)

	state := t.TempDir()
	ledger := filepath.Join(state, "scout-findings.jsonl")
	captureStdoutStderr(t, func() {
		if code := runScout([]string{"--root", repo, "--state-file", ledger}); code != 0 {
			t.Fatalf("scout scan failed: %d", code)
		}
	})

	tasksDir := filepath.Join(state, "tasks")
	mintedPath := filepath.Join(state, "minted.tsv")
	falseposPath := filepath.Join(state, "falsepos.tsv")
	var code int
	stdout, stderr := captureStdoutStderr(t, func() {
		code = runScout([]string{
			"mint-healers",
			"--ledger", ledger,
			"--tasks-dir", tasksDir,
			"--minted", mintedPath,
			"--falsepos", falseposPath,
		})
	})
	if code != 0 {
		t.Fatalf("mint exit: %d (stderr: %s)", code, stderr)
	}
	slugs := strings.Fields(stdout)
	if len(slugs) == 0 {
		t.Fatalf("expected at least one minted slug on stdout, got %q", stdout)
	}
	for _, slug := range slugs {
		path := filepath.Join(tasksDir, slug+".md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("brief missing %s: %v", path, err)
		}
		if !strings.Contains(string(body), "source: scout") {
			t.Fatalf("brief missing source key: %s", body)
		}
	}

	// Re-running mint should be a no-op via minted.tsv.
	stdout2, _ := captureStdoutStderr(t, func() {
		if code := runScout([]string{
			"mint-healers",
			"--ledger", ledger,
			"--tasks-dir", tasksDir,
			"--minted", mintedPath,
			"--falsepos", falseposPath,
		}); code != 0 {
			t.Fatalf("second mint should succeed")
		}
	})
	if strings.TrimSpace(stdout2) != "" {
		t.Fatalf("second mint should print no slugs (already minted), got %q", stdout2)
	}
}
