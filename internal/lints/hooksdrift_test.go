package lints

import (
	"testing"

	"github.com/versality/spore/internal/hooks"
)

func renderHooksFixture(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	hooksConfig := []byte(`{"events":{"Stop":[{"command":"/bin/echo stop"}]}}`)
	extras := []byte(`{"permissions":{"allow":["Bash(/bin/echo)"]}}` + "\n")
	rendered, err := hooks.Settings(map[string][]hooks.HookBin{
		"Stop": {{BinPath: "/bin/echo stop"}},
	})
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	merged, err := mergeJSONObjects(rendered, extras)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return hooksConfig, extras, merged
}

func TestHooksDrift_Match(t *testing.T) {
	hooksConfig, extras, settings := renderHooksFixture(t)
	root := newTestRepo(t, map[string]string{
		"configs/claude/hooks-config.json":    string(hooksConfig),
		"configs/claude/settings-extras.json": string(extras),
		"configs/claude/settings.json":        string(settings),
	})
	issues, err := HooksDrift{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no drift, got %v", issues)
	}
}

func TestHooksDrift_Stale(t *testing.T) {
	hooksConfig, extras, _ := renderHooksFixture(t)
	root := newTestRepo(t, map[string]string{
		"configs/claude/hooks-config.json":    string(hooksConfig),
		"configs/claude/settings-extras.json": string(extras),
		"configs/claude/settings.json":        `{"$schema":"x"}` + "\n",
	})
	issues, err := HooksDrift{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected drift hit, got %v", issues)
	}
}

func TestHooksDrift_NoConfig(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := HooksDrift{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues when config absent, got %v", issues)
	}
}
