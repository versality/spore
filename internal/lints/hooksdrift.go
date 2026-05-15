package lints

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/hooks"
)

// HooksDrift fails when SettingsPath diverges from the merge of:
//   - hooks.Settings rendered from HooksConfigPath
//   - the JSON object at ExtrasPath (overlays)
//
// Defaults match nix-config: HooksConfigPath=configs/claude/hooks-config.json,
// ExtrasPath=configs/claude/settings-extras.json,
// SettingsPath=configs/claude/settings.json,
// FixHint="just hooks-render".
type HooksDrift struct {
	HooksConfigPath string
	ExtrasPath      string
	SettingsPath    string
	FixHint         string
	// Kind, when non-empty, filters the rendered settings to
	// bindings whose "kinds" list contains Kind (unscoped bindings
	// always pass). Empty Kind renders every binding (the default
	// user-level lint shape).
	Kind string
	// Codex switches the renderer to hooks.CodexHooksForKind so the
	// expected output uses the .codex/hooks.json shape (top-level
	// `{"hooks": {...}}`, no $schema, no permissions). ExtrasPath is
	// ignored when set - the codex format has no overlay layer.
	Codex bool
}

func (HooksDrift) Name() string { return "hooks-drift" }

type hooksConfigInput struct {
	Events map[string][]hooksConfigBin `json:"events"`
}

type hooksConfigBin struct {
	Command     string   `json:"command"`
	Matcher     string   `json:"matcher,omitempty"`
	Timeout     int      `json:"timeout,omitempty"`
	Async       bool     `json:"async,omitempty"`
	AsyncRewake bool     `json:"asyncRewake,omitempty"`
	Kinds       []string `json:"kinds,omitempty"`
}

func (l HooksDrift) Run(root string) ([]Issue, error) {
	hooksPath := l.HooksConfigPath
	if hooksPath == "" {
		hooksPath = "configs/claude/hooks-config.json"
	}
	extrasPath := l.ExtrasPath
	if extrasPath == "" {
		extrasPath = "configs/claude/settings-extras.json"
	}
	settingsPath := l.SettingsPath
	if settingsPath == "" {
		settingsPath = "configs/claude/settings.json"
	}
	hint := l.FixHint
	if hint == "" {
		hint = "just hooks-render"
	}

	hooksRaw, err := os.ReadFile(filepath.Join(root, hooksPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var input hooksConfigInput
	if err := json.Unmarshal(hooksRaw, &input); err != nil {
		return nil, fmt.Errorf("%s: %w", hooksPath, err)
	}
	events := make(map[string][]hooks.HookBin, len(input.Events))
	for name, bins := range input.Events {
		for _, b := range bins {
			events[name] = append(events[name], hooks.HookBin{
				BinPath:     b.Command,
				Matcher:     b.Matcher,
				Timeout:     b.Timeout,
				Async:       b.Async,
				AsyncRewake: b.AsyncRewake,
				Kinds:       b.Kinds,
			})
		}
	}
	var rendered []byte
	if l.Codex {
		rendered, err = hooks.CodexHooksForKind(events, l.Kind)
	} else {
		rendered, err = hooks.SettingsForKind(events, l.Kind)
	}
	if err != nil {
		return nil, err
	}

	merged := rendered
	if !l.Codex {
		extrasRaw, err := os.ReadFile(filepath.Join(root, extrasPath))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		merged, err = mergeJSONObjects(rendered, extrasRaw)
		if err != nil {
			return nil, fmt.Errorf("merge with %s: %w", extrasPath, err)
		}
	}

	actual, err := os.ReadFile(filepath.Join(root, settingsPath))
	if err != nil {
		if os.IsNotExist(err) {
			return []Issue{{
				Path:    settingsPath,
				Message: fmt.Sprintf("missing; run '%s' to render", hint),
			}}, nil
		}
		return nil, err
	}

	if !bytes.Equal(canonicalJSON(actual), canonicalJSON(merged)) {
		return []Issue{{
			Path:    settingsPath,
			Message: fmt.Sprintf("stale vs rendered hooks; run '%s'", hint),
		}}, nil
	}
	return nil, nil
}

// mergeJSONObjects shallowly merges b into a (b overrides a's top-level
// keys). nil/empty b returns a unchanged.
func mergeJSONObjects(a, b []byte) ([]byte, error) {
	var ma map[string]any
	if err := json.Unmarshal(a, &ma); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) > 0 {
		var mb map[string]any
		if err := json.Unmarshal(b, &mb); err != nil {
			return nil, err
		}
		for k, v := range mb {
			ma[k] = v
		}
	}
	out, err := json.MarshalIndent(ma, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// canonicalJSON re-serialises raw to the canonical form (sorted keys,
// 2-space indent) so byte comparison is whitespace-independent.
func canonicalJSON(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return out
}
