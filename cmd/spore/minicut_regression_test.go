package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/testpath"
)

func TestMinicutRegressionMissingSporeAndCodexHooksAreReported(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("spore.toml", []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":   "#!/bin/sh\nexit 0\n",
			"tmux":  "#!/bin/sh\nexit 0\n",
			"codex": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want non-green readiness")
	}
	for _, want := range []string{`"code": "missing-spore"`, `"code": "missing-codex-hooks-config"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %s, want %s", stdout, want)
		}
	}
}

func TestMinicutRegressionCodexStartRendersWatchInbox(t *testing.T) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		t.Skip("test expects git on host")
	}
	root := t.TempDir()
	t.Chdir(root)
	runGitCmd(t, root, "init", "-q", "-b", "main")
	runGitCmd(t, root, "config", "user.email", "test@example.com")
	runGitCmd(t, root, "config", "user.name", "Test")
	if err := os.WriteFile("spore.toml", []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runInstall([]string{"--root", root}); code != 0 {
		t.Fatalf("runInstall exit = %d", code)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-q", "-m", "fixture")
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git", "tmux", "sh", "sleep"},
		FakeTools: map[string]string{
			"spore": "#!/bin/sh\nexit 0\n",
			"codex": "#!/bin/sh\nexec sleep 30\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	if err := runTaskNew([]string{"--no-edit", "--agent", "codex", "Codex Smoke"}); err != nil {
		t.Fatalf("task new: %v", err)
	}
	session, err := task.Start("tasks", "codex-smoke", nil)
	if err != nil {
		t.Fatalf("task start: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run() })
	body, err := os.ReadFile(filepath.Join(root, ".worktrees", "codex-smoke", ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read rendered codex hooks: %v", err)
	}
	if !strings.Contains(string(body), "watch-inbox") {
		t.Fatalf("codex hooks missing watch-inbox:\n%s", body)
	}
}

func TestMinicutRegressionCodexRenderedWatchInboxDrainsTell(t *testing.T) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		t.Skip("test expects git on host")
	}
	sporeBin := buildSporeForTest(t)
	root := t.TempDir()
	t.Chdir(root)
	runGitCmd(t, root, "init", "-q", "-b", "main")
	runGitCmd(t, root, "config", "user.email", "test@example.com")
	runGitCmd(t, root, "config", "user.name", "Test")
	if err := os.WriteFile("spore.toml", []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runInstall([]string{"--root", root}); code != 0 {
		t.Fatalf("runInstall exit = %d", code)
	}
	runGitCmd(t, root, "add", ".")
	runGitCmd(t, root, "commit", "-q", "-m", "fixture")
	h := testpath.Install(t, testpath.Options{
		RealTools: []string{"git", "tmux", "sh", "sleep"},
		FakeTools: map[string]string{
			"codex": "#!/bin/sh\nexec sleep 30\n",
		},
	})
	if err := os.Symlink(sporeBin, filepath.Join(h.BinDir, "spore")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", h.BinDir)

	if err := runTaskNew([]string{"--no-edit", "--agent", "codex", "Codex Inbox"}); err != nil {
		t.Fatalf("task new: %v", err)
	}
	session, err := task.Start("tasks", "codex-inbox", nil)
	if err != nil {
		t.Fatalf("task start: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run() })
	if err := task.Tell("codex-inbox", "hello from dispatcher"); err != nil {
		t.Fatalf("task tell: %v", err)
	}
	inbox, err := task.InboxDirForProject(root, "codex-inbox")
	if err != nil {
		t.Fatal(err)
	}
	if n, _, err := task.CountUnreadInboxForProject(root, "codex-inbox"); err != nil || n == 0 {
		t.Fatalf("unread before hook = %d err=%v, want >0", n, err)
	}
	command := renderedCodexWatchInboxCommand(t, filepath.Join(root, ".worktrees", "codex-inbox", ".codex", "hooks.json"))
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = filepath.Join(root, ".worktrees", "codex-inbox")
	cmd.Env = append(os.Environ(),
		"PATH="+h.BinDir,
		"SPORE_TASK_INBOX="+inbox,
		"WATCH_TIMEOUT=1",
		"WATCH_SETTLE=0",
	)
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
		t.Fatalf("watch-inbox command err=%v out=%s, want exit 2 after drain", err, out)
	}
	if !strings.Contains(string(out), "hello from dispatcher") {
		t.Fatalf("watch-inbox output = %s", out)
	}
	if n, _, err := task.CountUnreadInboxForProject(root, "codex-inbox"); err != nil || n != 0 {
		t.Fatalf("unread after hook = %d err=%v, want 0", n, err)
	}
	if _, err := os.Stat(filepath.Join(inbox, "read")); err != nil {
		t.Fatalf("read inbox dir missing after drain: %v", err)
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func buildSporeForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	out := filepath.Join(t.TempDir(), "spore")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/spore")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "gocache"))
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build spore: %v: %s", err, buildOut)
	}
	return out
}

func renderedCodexWatchInboxCommand(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered codex hooks: %v", err)
	}
	var cfg struct {
		Events map[string][]struct {
			Command string `json:"command"`
		} `json:"events"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse rendered codex hooks: %v", err)
	}
	for _, hook := range cfg.Events["Stop"] {
		if strings.Contains(hook.Command, "watch-inbox") {
			return hook.Command
		}
	}
	for _, group := range cfg.Hooks["Stop"] {
		for _, hook := range group.Hooks {
			if strings.Contains(hook.Command, "watch-inbox") {
				return hook.Command
			}
		}
	}
	t.Fatalf("rendered codex hooks missing watch-inbox:\n%s", body)
	return ""
}
