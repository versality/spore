package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/hooks/codex"
)

func TestCodexStopChainFromRegistryWorkerOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("WT_SESSION_KIND", "worker")
	t.Setenv("SPORE_PROJECT_ROOT", root)

	chain, ok := codexStopChainFromRegistry()
	if !ok {
		t.Fatal("chain not active in spore worker context")
	}
	got := chainCommands(chain)
	want := []string{
		"spore worker token-monitor",
		"spore hooks plan-ready-mechanical",
		"spore hooks worker-finish",
		"spore hooks watch-inbox",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("chain = %#v, want %#v", got, want)
	}
}

func TestCodexStopChainFromRegistryCoordinatorOrder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_SESSION_KIND", "coordinator")
	t.Setenv("SPORE_PROJECT_ROOT", root)

	chain, ok := codexStopChainFromRegistry()
	if !ok {
		t.Fatal("chain not active in spore coordinator context")
	}
	got := chainCommands(chain)
	want := []string{
		"spore coordinator token-monitor",
		"spore fleet replenish-hook",
		"spore hooks watch-inbox",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("chain = %#v, want %#v", got, want)
	}
}

func TestCodexStopChainFromRegistryNoopOutsideSpore(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("WT_SESSION_KIND", "worker")
	t.Setenv("SPORE_PROJECT_ROOT", "")

	if chain, ok := codexStopChainFromRegistry(); ok || len(chain) != 0 {
		t.Fatalf("chain = %#v ok=%v, want no-op", chain, ok)
	}
}

func TestCodexStopChainFromRegistryNoopWithoutSessionKind(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte("[fleet]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_PROJECT_ROOT", root)

	if chain, ok := codexStopChainFromRegistry(); ok || len(chain) != 0 {
		t.Fatalf("chain = %#v ok=%v, want no-op", chain, ok)
	}
}

func chainCommands(chain []codex.ChainHook) []string {
	out := make([]string, 0, len(chain))
	for _, hook := range chain {
		out = append(out, strings.Join(hook.Argv, " "))
	}
	return out
}
