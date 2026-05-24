package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/testpath"
)

func TestHookSourceConfigReadinessWarnsForMissingCodexConfig(t *testing.T) {
	root := t.TempDir()
	note := hookSourceConfigReadiness(root, "codex")
	if !strings.Contains(note, "Codex hook source config missing") {
		t.Fatalf("note = %q", note)
	}
}

func TestHookSourceConfigReadinessWarnsForMissingClaudeConfig(t *testing.T) {
	root := t.TempDir()
	note := hookSourceConfigReadiness(root, "claude")
	if !strings.Contains(note, "Claude hook source config missing") {
		t.Fatalf("note = %q", note)
	}
}

func TestHookSourceConfigReadinessAcceptsCustomizedConfig(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "configs", "codex", "hooks-config.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"events":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	note := hookSourceConfigReadiness(root, "codex")
	if strings.Contains(note, "Codex hook source config missing") {
		t.Fatalf("note = %q", note)
	}
	if !strings.Contains(note, "runtime hook file") {
		t.Fatalf("note = %q, want runtime hook info", note)
	}
}

func TestDetectRepoMappedReportsInstalledHookConfigsPresent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), []byte("module x\n"))
	notes, err := detectRepoMapped(root)
	if err != nil {
		t.Fatalf("detectRepoMapped: %v", err)
	}
	if !strings.Contains(notes, "config file") {
		t.Fatalf("notes = %q, want installed config file note", notes)
	}
	if strings.Contains(notes, "hook source config missing") {
		t.Fatalf("notes = %q, want source config present after install", notes)
	}
}

func TestDetectRepoMappedReportsSetupReadinessWarnings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), []byte("module x\n"))
	writeFile(t, filepath.Join(root, "spore.toml"), []byte("[fleet.workers]\ndefault = \"codex\"\n"))
	h := testpath.Install(t, testpath.Options{
		FakeTools: map[string]string{
			"git":    "#!/bin/sh\nexit 0\n",
			"tmux":   "#!/bin/sh\nexit 0\n",
			"spore":  "#!/bin/sh\nexit 0\n",
			"claude": "#!/bin/sh\nexit 0\n",
		},
	})
	t.Setenv("PATH", h.BinDir)

	notes, err := detectRepoMapped(root)
	if err != nil {
		t.Fatalf("detectRepoMapped: %v", err)
	}
	if !strings.Contains(notes, "missing-worker-agent:codex") {
		t.Fatalf("notes = %q, want codex readiness warning", notes)
	}
}
