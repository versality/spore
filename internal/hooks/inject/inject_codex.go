package inject

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/hooks/settings"
)

// CodexHooksConfigEnv overrides the path to the codex source
// hooks-config.json. Empty falls back to
// <projectRoot>/configs/codex/hooks-config.json.
const CodexHooksConfigEnv = "SPORE_CODEX_HOOKS_CONFIG"

const (
	defaultCodexHooksConfigRel = "configs/codex/hooks-config.json"
	codexTargetRel             = ".codex/hooks.json"
)

// InjectCodex renders <projectRoot>/configs/codex/hooks-config.json
// and writes the result atomically to <projectRoot>/.codex/hooks.json.
//
// Return semantics mirror Inject:
//   - (path, true, nil) on a fresh write or refresh,
//   - (path, false, nil) when the existing content already matches,
//   - ("", false, nil) when SkipEnv is "1" or the source is absent.
//
// targetDir is accepted for the older call shape but ignored. Codex
// resolves hook declarations for linked worktrees from the root
// checkout layer, so workers and coordinators share the same project
// root adapter file.
func InjectCodex(projectRoot, targetDir, kind string) (string, bool, error) {
	return InjectCodexWithEnv(projectRoot, targetDir, kind, os.Getenv)
}

// InjectCodexWithEnv is InjectCodex with a pluggable getenv for tests.
func InjectCodexWithEnv(projectRoot, targetDir, kind string, getenv func(string) string) (string, bool, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv(SkipEnv) == "1" {
		return "", false, nil
	}
	hooksPath := getenv(CodexHooksConfigEnv)
	if hooksPath == "" {
		hooksPath = filepath.Join(projectRoot, defaultCodexHooksConfigRel)
	}
	rendered, ok, err := settings.RenderCodex(hooksPath, kind)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}

	target := filepath.Join(projectRoot, codexTargetRel)
	if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, rendered) {
		return target, false, nil
	}
	if err := writeAtomic(target, rendered); err != nil {
		return "", false, err
	}
	return target, true, nil
}

// writeAtomic writes contents to path via a tempfile + rename. Shared
// by Inject and InjectCodex so a partial write never lands on disk.
func writeAtomic(target string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename to %s: %w", target, err)
	}
	return nil
}
