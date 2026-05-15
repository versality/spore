// Package settings centralizes the hooks-config.json schema, the
// per-kind render call, the settings-extras.json overlay merge, and
// the missing-file policy shared by the shell renderer
// (bootstrap/scripts/hooks-render.sh via `spore hooks render`),
// spawn-time injection (internal/hooks/inject), the hooks-drift lint
// (internal/lints), and the `spore hooks settings` CLI command.
//
// One schema, one renderer, one merge rule. New fields or policy
// tweaks land here once and apply everywhere.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/versality/spore/internal/hooks"
)

// Config is the JSON shape of a consumer's hooks-config.json.
type Config struct {
	Events map[string][]Bin `json:"events"`
}

// Bin is one hook binding inside an Events list. Field names mirror
// the on-disk schema; the in-memory hooks.HookBin shape uses BinPath
// instead of Command, so Events converts.
type Bin struct {
	Command     string   `json:"command"`
	Matcher     string   `json:"matcher,omitempty"`
	Timeout     int      `json:"timeout,omitempty"`
	Async       bool     `json:"async,omitempty"`
	AsyncRewake bool     `json:"asyncRewake,omitempty"`
	Kinds       []string `json:"kinds,omitempty"`
}

// Parse decodes hooks-config.json bytes into a Config.
func Parse(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// HookBins converts a parsed Config into the events map consumed by
// hooks.SettingsForKind / hooks.CodexHooksForKind. Bin order within
// each event is preserved.
func (c Config) HookBins() map[string][]hooks.HookBin {
	out := make(map[string][]hooks.HookBin, len(c.Events))
	for name, bins := range c.Events {
		for _, b := range bins {
			out[name] = append(out[name], hooks.HookBin{
				BinPath:     b.Command,
				Matcher:     b.Matcher,
				Timeout:     b.Timeout,
				Async:       b.Async,
				AsyncRewake: b.AsyncRewake,
				Kinds:       b.Kinds,
			})
		}
	}
	return out
}

// MergeExtras shallowly overlays the JSON object in extras on top of
// rendered (extras keys win). Empty extras returns rendered re-emitted
// as canonical (sorted, indented) JSON. Output ends with a newline so
// it round-trips byte-for-byte with the renderer's own output.
func MergeExtras(rendered, extras []byte) ([]byte, error) {
	var ma map[string]any
	if err := json.Unmarshal(rendered, &ma); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(extras)) > 0 {
		var mb map[string]any
		if err := json.Unmarshal(extras, &mb); err != nil {
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

// LoadConfig reads and parses a hooks-config.json file. A missing
// file returns (zero Config, false, nil) so callers can treat the
// absence as a no-op without distinguishing IO errors.
func LoadConfig(path string) (Config, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	c, err := Parse(raw)
	if err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, true, nil
}

// LoadExtras reads a settings-extras.json file. A missing file
// returns (nil, nil) so the caller renders without an overlay.
func LoadExtras(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return raw, nil
}

// RenderClaude is the full claude-code pipeline: load hooks-config,
// render for kind, overlay settings-extras. extrasPath may be empty
// or point at a missing file; the merge then no-ops. A missing
// hooksConfigPath returns (nil, false, nil) so spawn-time injectors
// can treat unconfigured projects as a no-op.
func RenderClaude(hooksConfigPath, extrasPath, kind string) ([]byte, bool, error) {
	cfg, ok, err := LoadConfig(hooksConfigPath)
	if err != nil || !ok {
		return nil, ok, err
	}
	rendered, err := hooks.SettingsForKind(cfg.HookBins(), kind)
	if err != nil {
		return nil, true, fmt.Errorf("render kind=%q: %w", kind, err)
	}
	if extrasPath == "" {
		return rendered, true, nil
	}
	extras, err := LoadExtras(extrasPath)
	if err != nil {
		return nil, true, err
	}
	merged, err := MergeExtras(rendered, extras)
	if err != nil {
		return nil, true, fmt.Errorf("merge extras: %w", err)
	}
	return merged, true, nil
}

// RenderCodex is RenderClaude's codex variant: no extras layer, output
// is the .codex/hooks.json shape.
func RenderCodex(hooksConfigPath, kind string) ([]byte, bool, error) {
	cfg, ok, err := LoadConfig(hooksConfigPath)
	if err != nil || !ok {
		return nil, ok, err
	}
	rendered, err := hooks.CodexHooksForKind(cfg.HookBins(), kind)
	if err != nil {
		return nil, true, fmt.Errorf("render codex kind=%q: %w", kind, err)
	}
	return rendered, true, nil
}
