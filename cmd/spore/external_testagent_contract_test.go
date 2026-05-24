//go:build external_testagent

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/claudepolicy"
	"github.com/versality/spore/codexpolicy"
	"github.com/versality/spore/internal/hooks/settings"
)

func TestExternalTestagentAcceptsWorkerArgv(t *testing.T) {
	bin := externalTestagentBin(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "claude", args: append([]string{"claude"}, claudepolicy.InteractiveArgs("", "")[1:]...)},
		{name: "codex", args: append([]string{"codex"}, externalTestagentCodexArgs("medium")...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--auto-exit", "1ms"}, tt.args...)
			cmd := exec.Command(bin, args...)
			cmd.Dir = t.TempDir()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("testagent %s argv rejected: %v: %s", tt.name, err, out)
			}
		})
	}
}

func TestExternalTestagentDocumentsCodexArgvGap(t *testing.T) {
	args := codexpolicy.InteractiveArgs("", "medium")
	if !containsArg(args, "--dangerously-bypass-hook-trust") {
		t.Fatalf("Spore Codex argv missing hook trust bypass: %#v", args)
	}
}

func TestExternalTestagentValidatesRenderedClaudeSettings(t *testing.T) {
	bin := externalTestagentBin(t)
	root := t.TempDir()
	t.Chdir(root)
	if code := runInstall([]string{"--root", root}); code != 0 {
		t.Fatalf("runInstall exit = %d", code)
	}
	settings := filepath.Join(root, ".claude", "settings.local.json")
	cmd := exec.Command(bin, "claude", "validate", "--settings", settings, "--strict")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("testagent claude validate rejected rendered settings: %v: %s", err, out)
	}
}

func TestExternalTestagentDocumentsCodexHookConfigGap(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if code := runInstall([]string{"--root", root}); code != 0 {
		t.Fatalf("runInstall exit = %d", code)
	}
	body, ok, err := settings.RenderCodex(filepath.Join(root, "configs", "codex", "hooks-config.json"), "worker")
	if err != nil || !ok {
		t.Fatalf("render codex hooks: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(string(body), `"hooks"`) {
		t.Fatalf("rendered codex hooks missing top-level hooks:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("unexpected codex config.toml state: %v", err)
	}
}

func externalTestagentCodexArgs(effort string) []string {
	var out []string
	for _, arg := range codexpolicy.InteractiveArgs("", effort)[1:] {
		if arg == "--dangerously-bypass-hook-trust" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func externalTestagentBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("SPORE_TESTAGENT_BIN")
	if bin == "" {
		t.Skip("SPORE_TESTAGENT_BIN is required when running -tags external_testagent")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("SPORE_TESTAGENT_BIN=%s: %v", bin, err)
	}
	return bin
}
