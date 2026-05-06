package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit creates a temp repo with the given files, commits them, and
// returns the repo root.
func gitInit(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mustGit("init", "-q", "-b", "main")
	mustGit("config", "commit.gpgsign", "false")
	mustGit("config", "user.name", "t")
	mustGit("config", "user.email", "t@t")
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "init")
	return root
}

func TestRunClaudeApplyDistribute_GreenCommits(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := gitInit(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	rc := runClaudeApplyDistribute([]string{
		"--root", root,
		"--source", "composer.md",
		"--check-cmd", "true",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	out, err := os.ReadFile(filepath.Join(root, "composer.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "<!-- homePath: src -->") {
		t.Fatalf("missing marker:\n%s", out)
	}
	logOut, err := exec.Command("git", "-C", root, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if strings.Count(string(logOut), "\n") != 2 {
		t.Fatalf("expected 2 commits, got:\n%s", logOut)
	}
	if !strings.Contains(string(logOut), "auto-distribute") {
		t.Fatalf("commit message missing tag:\n%s", logOut)
	}
}

func TestRunClaudeApplyDistribute_RedLogsLedger(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := gitInit(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	ledger := filepath.Join(root, "ledger.jsonl")
	rc := runClaudeApplyDistribute([]string{
		"--root", root,
		"--source", "composer.md",
		"--check-cmd", "false",
		"--ledger", ledger,
	})
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (red check)", rc)
	}
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"subdir":"src"`) {
		t.Fatalf("ledger missing subdir:\n%s", s)
	}
	if !strings.Contains(s, `"name":"Plugin rules"`) {
		t.Fatalf("ledger missing name:\n%s", s)
	}
	statusOut, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if !strings.Contains(string(statusOut), "composer.md") {
		t.Fatalf("expected dirty composer.md after red, got:\n%s", statusOut)
	}
}

func TestRunClaudeApplyDistribute_DryRunSkipsCheck(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := gitInit(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	rc := runClaudeApplyDistribute([]string{
		"--root", root,
		"--source", "composer.md",
		"--check-cmd", "false",
		"--dry-run",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0 (dry-run)", rc)
	}
	out, err := os.ReadFile(filepath.Join(root, "composer.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "<!-- homePath: src -->") {
		t.Fatalf("dry-run still mutates source; missing marker:\n%s", out)
	}
}

func TestRunClaudeLintDistribute_NoCandidates(t *testing.T) {
	root := gitInit(t, map[string]string{
		"composer.md": "# Top\nGeneral note.\n",
		"src/a.go":    "package src\n",
	})
	rc := runClaudeLintDistribute([]string{
		"--root", root,
		"--source", "composer.md",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
}

func TestRunClaudeLintDistribute_FlagsCandidates(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := gitInit(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	rc := runClaudeLintDistribute([]string{
		"--root", root,
		"--source", "composer.md",
	})
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (candidates)", rc)
	}
}

func TestRunClaudeLintSubdir_FlagsAndOptOut(t *testing.T) {
	root := gitInit(t, map[string]string{
		"CLAUDE.md": strings.Join([]string{
			"# Plugin rules",
			"`src/plugins/alpha/main.go` config",
			"`src/plugins/beta/main.go` config",
			"`src/plugins/gamma/main.go` cage",
			"`src/plugins/CLAUDE.md` rules",
		}, "\n") + "\n",
		"src/plugins/CLAUDE.md":     "# Plugins\nrules\n",
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	rc := runClaudeLintSubdir([]string{"--root", root})
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (issue)", rc)
	}
}
