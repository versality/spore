// Package inject writes a kind-scoped claude-code settings.local.json
// into a spawned session's working tree, so the per-session hook set
// is filtered to its kind (coordinator | worker) before claude-code
// reads ~/.claude/settings.json. The spawn-time injection is the
// Layer A scoping mechanism described in docs/session-kind.md: the
// claude-code per-project settings file beats the user-level one, so
// an operator-interactive claude in a project that has not been spawned
// by spore (no settings.local.json present) keeps the user-level
// behaviour unchanged.
package inject

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/hooks/settings"
)

// SkipEnv, when set to "1", suppresses the write entirely. Lets an
// operator opt out per-spawn without editing the project tree (e.g.
// when iterating on a hand-rolled .claude/settings.local.json).
const SkipEnv = "SPORE_SKIP_SETTINGS_INJECT"

// HooksConfigEnv overrides the path to the source hooks-config.json.
// Empty falls back to <projectRoot>/configs/claude/hooks-config.json.
const HooksConfigEnv = "SPORE_HOOKS_CONFIG"

// SettingsExtrasEnv overrides the path to the source settings-extras.json
// (merged on top of the rendered hook settings). Empty falls back to
// <projectRoot>/configs/claude/settings-extras.json.
const SettingsExtrasEnv = "SPORE_SETTINGS_EXTRAS"

// defaultHooksConfigRel is the conventional source location, matching
// bootstrap/scripts/hooks-render.sh's CLAUDE_DIR/hooks-config.json.
const defaultHooksConfigRel = "configs/claude/hooks-config.json"

// defaultExtrasRel matches bootstrap/scripts/hooks-render.sh's
// CLAUDE_DIR/settings-extras.json.
const defaultExtrasRel = "configs/claude/settings-extras.json"

// targetRel is the path inside the session tree where claude-code
// finds per-project local-only settings.
const targetRel = ".claude/settings.local.json"

// Inject renders <projectRoot>/configs/claude/hooks-config.json with
// kind-scoping kind, merges <projectRoot>/configs/claude/settings-extras.json
// when present, and writes the result atomically to
// <targetDir>/.claude/settings.local.json.
//
// Returns (path, true, nil) when a file was written or refreshed,
// (path, false, nil) when the existing content already matches (no
// rename happened), and ("", false, nil) for no-op cases:
//   - $SPORE_SKIP_SETTINGS_INJECT == "1"
//   - source hooks-config.json absent
//
// projectRoot is the source of hooks-config.json + settings-extras.json
// (typically the spore-managed project root). targetDir is where the
// .claude/settings.local.json file is written; for a worker that's the
// worktree, for the coordinator it's the project root itself.
func Inject(projectRoot, targetDir, kind string) (string, bool, error) {
	return InjectWithEnv(projectRoot, targetDir, kind, os.Getenv)
}

// InjectWithEnv is Inject with a pluggable getenv for tests.
func InjectWithEnv(projectRoot, targetDir, kind string, getenv func(string) string) (string, bool, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv(SkipEnv) == "1" {
		return "", false, nil
	}
	hooksPath := getenv(HooksConfigEnv)
	if hooksPath == "" {
		hooksPath = filepath.Join(projectRoot, defaultHooksConfigRel)
	}
	extrasPath := getenv(SettingsExtrasEnv)
	if extrasPath == "" {
		extrasPath = filepath.Join(projectRoot, defaultExtrasRel)
	}

	merged, ok, err := settings.RenderClaude(hooksPath, extrasPath, kind)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}

	target := filepath.Join(targetDir, targetRel)
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, merged) {
		return target, false, nil
	}
	if err := writeAtomic(target, merged); err != nil {
		return "", false, err
	}
	return target, true, nil
}
