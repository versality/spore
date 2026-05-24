package hooks

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CodexHooksForKind emits a complete, deterministic .codex/hooks.json
// blob from the same HookBin event map SettingsForKind consumes. The
// JSON shape differs from claude's settings.json: codex reads a flat
// top-level object `{"hooks": {...}}` with no $schema, no permissions.
// Vendor contract: https://developers.openai.com/codex/hooks.
// Same kind-filter and matcher-consolidation rules as SettingsForKind.
//
// When kind is non-empty, a HookBin is included only if its Kinds is
// empty (unscoped) or contains kind. When kind is "", every bin is
// emitted (back-compat with kind-blind callers).
func CodexHooksForKind(events map[string][]HookBin, kind string) ([]byte, error) {
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
	top := codexTop{}
	if len(hooksMap) > 0 {
		top.Hooks = hooksMap
	}
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal codex hooks: %w", err)
	}
	b = append(b, '\n')
	return b, nil
}

type codexTop struct {
	Hooks map[string][]hookGroup `json:"hooks,omitempty"`
}
