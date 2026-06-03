package spore

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// versionFromFile is the source of truth for the test expectation: read
// VERSION at test time so a release that bumps the file (and the tag)
// does not require an `embed_test.go` edit too. Keeps `just release`
// from cascading into hand-edited test fixtures.
func versionFromFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

func TestVersion(t *testing.T) {
	want := versionFromFile(t)
	if got := Version(); got != want {
		t.Fatalf("Version() = %q, want %q (from VERSION file)", got, want)
	}
}

func TestBuildVersion(t *testing.T) {
	want := versionFromFile(t)
	prev := buildCommit
	defer func() { buildCommit = prev }()

	buildCommit = "abc123"
	if got, exp := BuildVersion(), want+" (abc123)"; got != exp {
		t.Fatalf("BuildVersion() = %q, want %q", got, exp)
	}

	buildCommit = "unknown"
	if got, exp := BuildVersion(), want+" (commit unknown)"; got != exp {
		t.Fatalf("BuildVersion() = %q, want %q", got, exp)
	}
}

func TestHandoverSettingsWireCommunicationHooks(t *testing.T) {
	raw, err := BundledHandover.ReadFile("bootstrap/handover/settings.json")
	if err != nil {
		t.Fatalf("read handover settings: %v", err)
	}
	var settings struct {
		Hooks map[string][]handoverHookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("parse handover settings: %v", err)
	}

	if !hasCommand(settings.Hooks["PreToolUse"], "/home/spore/.claude/hooks/block-bg-bash.pl") {
		t.Fatal("handover settings lost block-bg-bash PreToolUse hook")
	}
	if !hasCommand(settings.Hooks["SessionStart"], "/home/spore/.claude/hooks/load-state-md.pl") {
		t.Fatal("handover settings lost load-state-md SessionStart hook")
	}
	if !hasAsync(settings.Hooks["Notification"], "/usr/local/bin/spore hooks notify-coordinator") {
		t.Fatal("handover settings missing async notify-coordinator Notification hook")
	}
	if !hasAsyncRewake(settings.Hooks["Stop"], "/usr/local/bin/spore hooks watch-inbox") {
		t.Fatal("handover settings missing asyncRewake watch-inbox Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore coordinator token-monitor") {
		t.Fatal("handover settings missing coordinator token-monitor Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore worker token-monitor") {
		t.Fatal("handover settings missing worker token-monitor Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore fleet replenish-hook") {
		t.Fatal("handover settings missing fleet replenish-hook Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore hooks plan-ready-mechanical") {
		t.Fatal("handover settings missing plan-ready-mechanical Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore hooks worker-continue") {
		t.Fatal("handover settings missing worker-continue Stop hook")
	}
	if !hasCommand(settings.Hooks["Stop"], "/usr/local/bin/spore hooks worker-stop-force-closing") {
		t.Fatal("handover settings missing worker-stop-force-closing Stop hook")
	}
	if !workerContinueOrderedCorrectly(settings.Hooks["Stop"]) {
		t.Fatal("worker-continue must run after plan-ready-mechanical and before watch-inbox")
	}
	if !forceClosingOrderedCorrectly(settings.Hooks["Stop"]) {
		t.Fatal("worker-stop-force-closing must run after worker-continue and before watch-inbox")
	}
}

func forceClosingOrderedCorrectly(groups []handoverHookGroup) bool {
	workerIdx, forceIdx, watchIdx := -1, -1, -1
	idx := 0
	for _, group := range groups {
		for _, hook := range group.Hooks {
			switch hook.Command {
			case "/usr/local/bin/spore hooks worker-continue":
				workerIdx = idx
			case "/usr/local/bin/spore hooks worker-stop-force-closing":
				forceIdx = idx
			case "/usr/local/bin/spore hooks watch-inbox":
				watchIdx = idx
			}
			idx++
		}
	}
	if workerIdx < 0 || forceIdx < 0 || watchIdx < 0 {
		return false
	}
	return workerIdx < forceIdx && forceIdx < watchIdx
}

func workerContinueOrderedCorrectly(groups []handoverHookGroup) bool {
	planIdx, workerIdx, watchIdx := -1, -1, -1
	idx := 0
	for _, group := range groups {
		for _, hook := range group.Hooks {
			switch hook.Command {
			case "/usr/local/bin/spore hooks plan-ready-mechanical":
				planIdx = idx
			case "/usr/local/bin/spore hooks worker-continue":
				workerIdx = idx
			case "/usr/local/bin/spore hooks watch-inbox":
				watchIdx = idx
			}
			idx++
		}
	}
	if planIdx < 0 || workerIdx < 0 || watchIdx < 0 {
		return false
	}
	return planIdx < workerIdx && workerIdx < watchIdx
}

type handoverHookGroup struct {
	Hooks []handoverHook `json:"hooks"`
}

type handoverHook struct {
	Command     string `json:"command"`
	Async       bool   `json:"async,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
}

func hasCommand(groups []handoverHookGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command {
				return true
			}
		}
	}
	return false
}

func hasAsync(groups []handoverHookGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command && hook.Async {
				return true
			}
		}
	}
	return false
}

func hasAsyncRewake(groups []handoverHookGroup, command string) bool {
	for _, group := range groups {
		for _, hook := range group.Hooks {
			if hook.Command == command && hook.AsyncRewake {
				return true
			}
		}
	}
	return false
}
