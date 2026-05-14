package spore

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHandoverSettingsWireCommunicationHooks(t *testing.T) {
	raw, err := os.ReadFile("bootstrap/handover/settings.json")
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
	if !rowerContinueOrderedCorrectly(settings.Hooks["Stop"]) {
		t.Fatal("worker-continue must run after plan-ready-mechanical and before watch-inbox")
	}
}

func rowerContinueOrderedCorrectly(groups []handoverHookGroup) bool {
	planIdx, rowerIdx, watchIdx := -1, -1, -1
	idx := 0
	for _, group := range groups {
		for _, hook := range group.Hooks {
			switch hook.Command {
			case "/usr/local/bin/spore hooks plan-ready-mechanical":
				planIdx = idx
			case "/usr/local/bin/spore hooks worker-continue":
				rowerIdx = idx
			case "/usr/local/bin/spore hooks watch-inbox":
				watchIdx = idx
			}
			idx++
		}
	}
	if planIdx < 0 || rowerIdx < 0 || watchIdx < 0 {
		return false
	}
	return planIdx < rowerIdx && rowerIdx < watchIdx
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
