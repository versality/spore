package hooks

import (
	"encoding/json"
	"fmt"
)

// HookBin describes one hook binary entry for claude-code's
// settings.json. Name is a human label (not emitted into JSON).
// BinPath is the shell command to run. Matcher is an optional tool
// name regex (used for PreToolUse/PostToolUse); leave empty for
// Stop/Notification hooks.
type HookBin struct {
	Name    string
	BinPath string
	Matcher string
	Timeout int // seconds; 0 omits the field (claude-code default)
}

// Settings emits a complete, deterministic settings.json blob for
// claude-code. Each slice maps to a hook event group: Stop,
// PostToolUse, Notification. Empty slices are omitted. Keys are
// sorted at every level (struct field order + map key sort).
func Settings(stops, postToolUse, notification []HookBin) ([]byte, error) {
	hooks := make(map[string][]hookGroup)
	if err := addGroup(hooks, "Notification", notification); err != nil {
		return nil, err
	}
	if err := addGroup(hooks, "PostToolUse", postToolUse); err != nil {
		return nil, err
	}
	if err := addGroup(hooks, "Stop", stops); err != nil {
		return nil, err
	}

	top := settingsTop{
		Schema: "https://json.schemastore.org/claude-code-settings.json",
	}
	if len(hooks) > 0 {
		top.Hooks = hooks
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}

// settingsTop is the root of settings.json. Field order matches
// alphabetical key order ($schema < hooks).
type settingsTop struct {
	Schema string                   `json:"$schema"`
	Hooks  map[string][]hookGroup   `json:"hooks,omitempty"`
}

// hookGroup is one matcher group inside a hook event array.
// Field order: hooks < matcher (alphabetical).
type hookGroup struct {
	Hooks   []hookEntry `json:"hooks"`
	Matcher string      `json:"matcher,omitempty"`
}

// hookEntry is a single hook command. Field order: command < timeout < type.
type hookEntry struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	Type    string `json:"type"`
}

func addGroup(m map[string][]hookGroup, event string, bins []HookBin) error {
	if len(bins) == 0 {
		return nil
	}
	for _, b := range bins {
		if b.BinPath == "" {
			return fmt.Errorf("hook %q: empty BinPath", b.Name)
		}
		entry := hookEntry{
			Command: b.BinPath,
			Timeout: b.Timeout,
			Type:    "command",
		}
		g := hookGroup{
			Hooks:   []hookEntry{entry},
			Matcher: b.Matcher,
		}
		m[event] = append(m[event], g)
	}
	return nil
}
