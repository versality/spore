package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/testpath"
)

func TestInstallWarnsMissingDefaultCodex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet.workers]\ndefault = \"codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
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

	code, _, stderr := captureFn(t, func() int { return runInstall([]string{"--root", root}) })
	if code != 0 {
		t.Fatalf("runInstall exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "missing-worker-agent:codex") {
		t.Fatalf("stderr = %q, want codex warning", stderr)
	}
}

func TestInstallReadyToolsProduceNoWarnings(t *testing.T) {
	root := t.TempDir()
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":    "#!/bin/sh\nexit 0\n",
			"tmux":   "#!/bin/sh\nexit 0\n",
			"spore":  "#!/bin/sh\nexit 0\n",
			"claude": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	code, _, stderr := captureFn(t, func() int { return runInstall([]string{"--root", root}) })
	if code != 0 {
		t.Fatalf("runInstall exit=%d stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want no warnings", stderr)
	}
}
