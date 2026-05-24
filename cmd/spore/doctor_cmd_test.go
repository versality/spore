package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/versality/spore/internal/hooks/settings"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/testpath"
)

func TestDoctorReportsMissingCodexJSON(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("spore.toml", []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":   "#!/bin/sh\nexit 0\n",
			"tmux":  "#!/bin/sh\nexit 0\n",
			"spore": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want non-green")
	}
	if !strings.Contains(stdout, `"code": "missing-worker-agent"`) || !strings.Contains(stdout, `"tool": "codex"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestDoctorReadyProject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, filepath.Join("configs", "claude", "hooks-config.json"), string(mustReadRepoFile(t, filepath.Join("configs", "claude", "hooks-config.json"))))
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":    "#!/bin/sh\nexit 0\n",
			"tmux":   "#!/bin/sh\nexit 0\n",
			"spore":  "#!/bin/sh\nexit 0\n",
			"claude": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	code, stdout, stderr := captureFn(t, func() int { return runDoctor(nil) })
	if code != 0 {
		t.Fatalf("doctor exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "doctor: ready") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func mustReadRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	root := repoRootByMarker(t)
	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func repoRootByMarker(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	wd := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		if next == wd {
			t.Fatal("repo root not found")
		}
		wd = next
	}
}

func TestDoctorReportsCodexRuntimeHookDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("spore.toml", "[fleet.workers]\ndefault = \"codex\"\n")
	write(filepath.Join("configs", "codex", "hooks-config.json"), `{"events":{"Stop":[{"command":"spore hooks watch-inbox","timeout":10}]}}`)
	write(filepath.Join(".codex", "hooks.json"), `{"events":{"Stop":[{"command":"stale"}]}}`)
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":   "#!/bin/sh\nexit 0\n",
			"tmux":  "#!/bin/sh\nexit 0\n",
			"spore": "#!/bin/sh\nexit 0\n",
			"codex": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want drift warning")
	}
	if !strings.Contains(stdout, `"code": "runtime-hooks-drift"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestDoctorAcceptsCoordinatorRuntimeHooks(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	sourcePath := filepath.Join("configs", "codex", "hooks-config.json")
	writeDoctorFile(t, sourcePath, `{
  "events": {
    "PreToolUse": [
      {
        "command": "spore hooks codex pre-tool-use",
        "timeout": 10
      }
    ],
    "Stop": [
      {
        "command": "spore coordinator token-monitor",
        "timeout": 10,
        "kinds": ["coordinator"]
      },
      {
        "command": "spore fleet replenish-hook",
        "timeout": 30,
        "kinds": ["coordinator"]
      },
      {
        "command": "spore hooks plan-ready-mechanical",
        "timeout": 10,
        "kinds": ["worker"]
      },
      {
        "command": "spore hooks watch-inbox",
        "timeout": 604800,
        "kinds": ["coordinator", "worker"]
      },
      {
        "command": "spore worker token-monitor",
        "timeout": 10,
        "kinds": ["worker"]
      }
    ]
  }
}`)
	rendered, ok, err := settings.RenderCodex(sourcePath, task.SessionKindCoordinator)
	if err != nil || !ok {
		t.Fatalf("RenderCodex ok=%v err=%v", ok, err)
	}
	writeDoctorFile(t, filepath.Join(".codex", "hooks.json"), string(rendered))
	installDoctorTools(t, "codex")

	issues := hookConfigIssues(root, "codex")
	for _, issue := range issues {
		if issue.Code == "runtime-hooks-drift" {
			t.Fatalf("coordinator runtime reported as worker drift: %#v", issues)
		}
	}
}

func TestDoctorReportsMissingLifecycleHook(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, "spore.toml", "[fleet.workers]\ndefault = \"codex\"\n")
	writeDoctorFile(t, filepath.Join("configs", "codex", "hooks-config.json"), `{"events":{"Stop":[{"command":"custom hook","timeout":10}]}}`)
	installDoctorTools(t, "codex")

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want lifecycle warning")
	}
	if !strings.Contains(stdout, `"code": "missing-lifecycle-hook"`) || !strings.Contains(stdout, `spore hooks watch-inbox`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestDoctorReportsLifecycleHookEventDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, "spore.toml", "[fleet.workers]\ndefault = \"codex\"\n")
	writeDoctorFile(t, filepath.Join("configs", "codex", "hooks-config.json"), `{"events":{"SessionStart":[{"command":"spore hooks watch-inbox","timeout":604800,"kinds":["coordinator","worker"]}]}}`)
	installDoctorTools(t, "codex")

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want lifecycle warning")
	}
	if !strings.Contains(stdout, `"code": "lifecycle-hook-event-drift"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestDoctorReportsLifecycleHookTimeoutAndKindsDrift(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, "spore.toml", "[fleet.workers]\ndefault = \"codex\"\n")
	writeDoctorFile(t, filepath.Join("configs", "codex", "hooks-config.json"), `{"events":{"Stop":[{"command":"spore hooks watch-inbox","timeout":10,"kinds":["worker"]}]}}`)
	installDoctorTools(t, "codex")

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want lifecycle warning")
	}
	if !strings.Contains(stdout, `"code": "lifecycle-hook-timeout-drift"`) || !strings.Contains(stdout, `"code": "lifecycle-hook-kinds-drift"`) {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestInstallPreservesPartialLifecycleConfigAndDoctorWarns(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, "spore.toml", "[fleet.workers]\ndefault = \"codex\"\n")
	installDoctorTools(t, "codex")
	partial := "{\n" +
		"  \"events\": {\n" +
		"    \"Stop\": [\n" +
		"      {\n" +
		"        \"command\": \"spore hooks watch-inbox\",\n" +
		"        \"timeout\": 604800,\n" +
		"        \"asyncRewake\": true,\n" +
		"        \"kinds\": [\"coordinator\", \"worker\"]\n" +
		"      }\n" +
		"    ]\n" +
		"  }\n" +
		"}\n"
	sourcePath := filepath.Join("configs", "codex", "hooks-config.json")
	writeDoctorFile(t, sourcePath, partial)

	if code := runInstall([]string{"--root", root}); code != 0 {
		t.Fatalf("install exit code = %d, want 0", code)
	}
	body, err := os.ReadFile(filepath.Join(root, sourcePath))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != partial {
		t.Fatalf("install rewrote partial config:\n%s", body)
	}

	code, stdout, _ := captureFn(t, func() int { return runDoctor([]string{"--json"}) })
	if code == 0 {
		t.Fatal("doctor exit = 0, want lifecycle warning")
	}
	if !strings.Contains(stdout, `"code": "missing-lifecycle-hook"`) {
		t.Fatalf("stdout = %s", stdout)
	}
	if !strings.Contains(stdout, "spore coordinator token-monitor") {
		t.Fatalf("stdout = %s", stdout)
	}
	if strings.Contains(stdout, "missing spore hooks watch-inbox") {
		t.Fatalf("watch-inbox hook reported as missing:\n%s", stdout)
	}
}

func TestDoctorAllowsCustomExtraHooks(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeDoctorFile(t, filepath.Join("configs", "claude", "hooks-config.json"), `{"events":{"Stop":[{"command":"custom hook","timeout":10}]}}`)
	installDoctorTools(t, "claude")

	issues := lifecycleSourceIssues("claude", filepath.Join(root, "configs", "claude", "hooks-config.json"))
	for _, issue := range issues {
		if strings.Contains(issue.Message, "custom hook") {
			t.Fatalf("custom hook reported as issue: %#v", issues)
		}
	}
}

func writeDoctorFile(t *testing.T, rel, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rel, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installDoctorTools(t *testing.T, agent string) {
	t.Helper()
	fakeTools := map[string]string{
		"git":   "#!/bin/sh\nexit 0\n",
		"tmux":  "#!/bin/sh\nexit 0\n",
		"spore": "#!/bin/sh\nexit 0\n",
		agent:   "#!/bin/sh\nexit 0\n",
	}
	h := testpath.Install(t, testpath.Options{FakeTools: fakeTools})
	t.Setenv("PATH", h.BinDir)
}
