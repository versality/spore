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
			"gh":     "#!/bin/sh\nexit 0\n",
			"age":    "#!/bin/sh\nexit 0\n",
			"go":     "#!/bin/sh\nexit 0\n",
			"gofmt":  "#!/bin/sh\nexit 0\n",
			"just":   "#!/bin/sh\nexit 0\n",
			"nix":    "#!/bin/sh\nexit 0\n",
			"pgrep":  "#!/bin/sh\nexit 0\n",
			"sh":     "#!/bin/sh\nexit 0\n",
			"bash":   "#!/bin/sh\nexit 0\n",
			"wt":     "#!/bin/sh\nexit 0\n",
			"bwrap":  "#!/bin/sh\nexit 0\n",
			"ssh":    "#!/bin/sh\nexit 0\n",
			"scp":    "#!/bin/sh\nexit 0\n",
			"rsync":  "#!/bin/sh\nexit 0\n",
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

func TestInstallWarnsMissingSetupToolHints(t *testing.T) {
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
	for _, want := range []string{"missing-recommended-tools", "bash", "go", "missing-feature-tools", "bwrap", "rsync"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
}
