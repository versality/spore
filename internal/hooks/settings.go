package hooks

import (
	"encoding/json"
	"fmt"
	"sort"
)

// HookBin describes one hook binary entry for claude-code's
// settings.json. Name is a human label (not emitted into JSON).
// BinPath is the shell command to run. Matcher is an optional tool
// name regex (used for PreToolUse/PostToolUse); leave empty for
// Stop/Notification hooks.
type HookBin struct {
	Name        string
	BinPath     string
	Matcher     string
	Timeout     int  // seconds; 0 omits the field (claude-code default)
	Async       bool // run without blocking the agent
	AsyncRewake bool // long-running hook that wakes the agent on exit 2
}

// Settings emits a complete, deterministic settings.json blob for
// claude-code. The events map keys are hook event names (Stop,
// Notification, PostToolUse, UserPromptSubmit, PreToolUse, ...).
// Empty slices are omitted. Keys are sorted at every level.
// Hooks with the same Matcher within one event are consolidated
// into a single group.
func Settings(events map[string][]HookBin) ([]byte, error) {
	hooksMap := make(map[string][]hookGroup)

	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		groups, err := consolidate(events[name])
		if err != nil {
			return nil, err
		}
		if len(groups) > 0 {
			hooksMap[name] = groups
		}
	}

	top := settingsTop{
		Schema: "https://json.schemastore.org/claude-code-settings.json",
	}
	if len(hooksMap) > 0 {
		top.Hooks = hooksMap
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}

type settingsTop struct {
	Schema string                 `json:"$schema"`
	Hooks  map[string][]hookGroup `json:"hooks,omitempty"`
}

type hookGroup struct {
	Hooks   []hookEntry `json:"hooks"`
	Matcher string      `json:"matcher,omitempty"`
}

// hookEntry field order is alphabetical for deterministic JSON output.
type hookEntry struct {
	Async       bool   `json:"async,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout,omitempty"`
	Type        string `json:"type"`
}

// consolidate merges HookBins with the same Matcher into a single
// hookGroup, preserving insertion order of both groups and entries.
func consolidate(bins []HookBin) ([]hookGroup, error) {
	if len(bins) == 0 {
		return nil, nil
	}

	type accum struct {
		matcher string
		entries []hookEntry
	}
	seen := make(map[string]int)
	var groups []accum

	for _, b := range bins {
		if b.BinPath == "" {
			return nil, fmt.Errorf("hook %q: empty BinPath", b.Name)
		}
		entry := hookEntry{
			Async:       b.Async,
			AsyncRewake: b.AsyncRewake,
			Command:     b.BinPath,
			Timeout:     b.Timeout,
			Type:        "command",
		}
		idx, ok := seen[b.Matcher]
		if !ok {
			idx = len(groups)
			seen[b.Matcher] = idx
			groups = append(groups, accum{matcher: b.Matcher})
		}
		groups[idx].entries = append(groups[idx].entries, entry)
	}

	result := make([]hookGroup, len(groups))
	for i, g := range groups {
		result[i] = hookGroup{
			Hooks:   g.entries,
			Matcher: g.matcher,
		}
	}
	return result, nil
}
