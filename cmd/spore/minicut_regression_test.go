package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
