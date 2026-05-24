package lifecyclehooks

import "testing"

func TestRegistryHooksAreComplete(t *testing.T) {
	for _, hook := range Registry() {
		if hook.Driver != DriverClaude && hook.Driver != DriverCodex {
			t.Fatalf("hook driver = %q", hook.Driver)
		}
		if hook.Event == "" {
			t.Fatalf("hook has empty event: %#v", hook)
		}
		if hook.Command == "" {
			t.Fatalf("hook has empty command: %#v", hook)
		}
		if hook.Timeout <= 0 {
			t.Fatalf("hook timeout = %d: %#v", hook.Timeout, hook)
		}
		if len(hook.Kinds) == 0 && hook.Command != "spore hooks codex pre-tool-use" {
			t.Fatalf("hook has empty kinds: %#v", hook)
		}
		if len(hook.Docs) == 0 {
			t.Fatalf("hook has no docs: %#v", hook)
		}
	}
}

func TestForDriver(t *testing.T) {
	if got := ForDriver(DriverClaude); len(got) != 9 {
		t.Fatalf("claude hooks = %d, want 9", len(got))
	}
	if got := ForDriver(DriverCodex); len(got) != 6 {
		t.Fatalf("codex hooks = %d, want 6", len(got))
	}
}
