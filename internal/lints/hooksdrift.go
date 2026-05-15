package lints

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/hooks/settings"
)

// HooksDrift fails when SettingsPath diverges from the merge of:
//   - the rendered hooks settings for HooksConfigPath
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
	// Codex switches the renderer to the .codex/hooks.json shape
	// (top-level `{"hooks": {...}}`, no $schema, no permissions).
	// ExtrasPath is ignored when set - the codex format has no
	// overlay layer.
	Codex bool
}

func (HooksDrift) Name() string { return "hooks-drift" }

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

	var (
		merged []byte
		ok     bool
		err    error
	)
	if l.Codex {
		merged, ok, err = settings.RenderCodex(filepath.Join(root, hooksPath), l.Kind)
	} else {
		merged, ok, err = settings.RenderClaude(filepath.Join(root, hooksPath), filepath.Join(root, extrasPath), l.Kind)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
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
