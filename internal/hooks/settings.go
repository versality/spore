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
// Stop/Notification hooks. Kinds is the optional session-kind filter
// (consumed by SettingsForKind; never emitted to settings.json).
// An empty Kinds slice means "all kinds".
type HookBin struct {
	Name        string
	BinPath     string
	Matcher     string
	Timeout     int      // seconds; 0 omits the field (claude-code default)
	Async       bool     // run without blocking the agent
	AsyncRewake bool     // long-running hook that wakes the agent on exit 2
	Kinds       []string // session-kind filter; empty means "all kinds"
}

// SettingsForKind emits a complete, deterministic settings.json blob
// for claude-code. The events map keys are hook event names (Stop,
// Notification, PostToolUse, UserPromptSubmit, PreToolUse, ...).
// Empty slices are omitted. Keys are sorted at every level.
// Hooks with the same Matcher within one event are consolidated
// into a single group.
//
// When kind is non-empty, a HookBin is included only if its Kinds
// is empty (unscoped) or contains kind. When kind is "", every bin
// is included regardless of Kinds.
func SettingsForKind(events map[string][]HookBin, kind string) ([]byte, error) {
	hooksMap := make(map[string][]hookGroup)

	names := make([]string, 0, len(events))
	for name := range events {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		filtered := filterByKind(events[name], kind)
		groups, err := consolidate(filtered)
		if err != nil {
			return nil, err
		}
		if len(groups) > 0 {
			hooksMap[name] = groups
		}
	}

	top := settingsTop{
		Schema:      "https://json.schemastore.org/claude-code-settings.json",
		Permissions: &permissions{DefaultMode: "bypassPermissions"},
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
	Schema      string                 `json:"$schema"`
	Permissions *permissions           `json:"permissions,omitempty"`
	Hooks       map[string][]hookGroup `json:"hooks,omitempty"`
}

type permissions struct {
	DefaultMode string `json:"defaultMode"`
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

// filterByKind drops HookBins that do not apply to kind. kind == ""
// is the wildcard (every bin passes). Otherwise a bin passes when its
// Kinds slice is empty (unscoped, applies everywhere) or contains kind.
func filterByKind(bins []HookBin, kind string) []HookBin {
	if kind == "" {
		return bins
	}
	out := make([]HookBin, 0, len(bins))
	for _, b := range bins {
		if len(b.Kinds) == 0 {
			out = append(out, b)
			continue
		}
		for _, k := range b.Kinds {
			if k == kind {
				out = append(out, b)
				break
			}
		}
	}
	return out
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
