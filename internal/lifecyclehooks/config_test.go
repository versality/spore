package lifecyclehooks

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/versality/spore/internal/hooks/settings"
)

func TestBundledConfigsContainRegistryHooks(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		driver string
		path   string
	}{
		{driver: DriverCodex, path: filepath.Join(root, "configs", "codex", "hooks-config.json")},
		{driver: DriverClaude, path: filepath.Join(root, "configs", "claude", "hooks-config.json")},
	}
	for _, tt := range tests {
		t.Run(tt.driver, func(t *testing.T) {
			cfg := readConfig(t, tt.path)
			for _, hook := range ForDriver(tt.driver) {
				got, ok := findCommand(cfg, hook.Command)
				if !ok {
					t.Fatalf("%s missing registry command %q", tt.path, hook.Command)
				}
				if got.Event != hook.Event {
					t.Fatalf("%s command %q event = %q, want %q", tt.path, hook.Command, got.Event, hook.Event)
				}
				if got.Timeout != hook.Timeout {
					t.Fatalf("%s command %q timeout = %d, want %d", tt.path, hook.Command, got.Timeout, hook.Timeout)
				}
				if !reflect.DeepEqual(got.Kinds, hook.Kinds) {
					t.Fatalf("%s command %q kinds = %#v, want %#v", tt.path, hook.Command, got.Kinds, hook.Kinds)
				}
			}
		})
	}
}

func TestBundledConfigsHaveNoUnregisteredSporeManagedCommands(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		driver string
		path   string
	}{
		{driver: DriverCodex, path: filepath.Join(root, "configs", "codex", "hooks-config.json")},
		{driver: DriverClaude, path: filepath.Join(root, "configs", "claude", "hooks-config.json")},
	}
	for _, tt := range tests {
		t.Run(tt.driver, func(t *testing.T) {
			cfg := readConfig(t, tt.path)
			registry := make(map[string]bool)
			for _, hook := range ForDriver(tt.driver) {
				registry[hook.Command] = true
			}
			for event, bins := range cfg.Events {
				for _, bin := range bins {
					if !strings.HasPrefix(bin.Command, "spore ") {
						continue
					}
					if !registry[bin.Command] {
						t.Fatalf("%s event %s has unregistered Spore-managed command %q", tt.path, event, bin.Command)
					}
				}
			}
		})
	}
}

func readConfig(t *testing.T, path string) settings.Config {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func findCommand(cfg settings.Config, command string) (struct {
	settings.Bin
	Event string
}, bool) {
	for event, bins := range cfg.Events {
		for _, bin := range bins {
			if bin.Command == command {
				return struct {
					settings.Bin
					Event string
				}{Bin: bin, Event: event}, true
			}
		}
	}
	return struct {
		settings.Bin
		Event string
	}{}, false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
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
