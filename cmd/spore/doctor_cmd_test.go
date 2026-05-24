package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, rel := range []string{
		filepath.Join("configs", "claude", "hooks-config.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rel, []byte(`{"events":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
