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
	if got := ForDriver(DriverClaude); len(got) != 10 {
		t.Fatalf("claude hooks = %d, want 10", len(got))
	}
	if got := ForDriver(DriverCodex); len(got) != 7 {
		t.Fatalf("codex hooks = %d, want 7", len(got))
	}
}

func TestWorkerFinishHookOrder(t *testing.T) {
	claude := ForDriver(DriverClaude)
	if indexCommand(claude, "spore hooks worker-finish") > indexCommand(claude, "spore hooks worker-continue") {
		t.Fatal("claude worker-finish must run before worker-continue")
	}
	if indexCommand(claude, "spore hooks worker-finish") > indexCommand(claude, "spore hooks watch-inbox") {
		t.Fatal("claude worker-finish must run before watch-inbox")
	}

	codex := ForDriver(DriverCodex)
	if indexCommand(codex, "spore hooks worker-finish") > indexCommand(codex, "spore hooks watch-inbox") {
		t.Fatal("codex worker-finish must run before watch-inbox")
	}
}

func TestWatchInboxHooksAreAsync(t *testing.T) {
	for _, driver := range []string{DriverClaude, DriverCodex} {
		for _, hook := range ForDriver(driver) {
			if hook.Command == "spore hooks watch-inbox" && !hook.Async {
				t.Fatalf("%s watch-inbox hook must be async: %#v", driver, hook)
			}
		}
	}
}

func indexCommand(hooks []Hook, command string) int {
	for i, hook := range hooks {
		if hook.Command == command {
			return i
		}
	}
	return len(hooks) + 1
}
