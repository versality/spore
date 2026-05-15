package inject

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/hooks"
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
// with kind-scoping kind and writes the result atomically to
// <targetDir>/.codex/hooks.json.
//
// Return semantics mirror Inject:
//   - (path, true, nil) on a fresh write or refresh,
//   - (path, false, nil) when the existing content already matches,
//   - ("", false, nil) when SkipEnv is "1" or the source is absent.
//
// projectRoot is the source of the hooks config (the spore-managed
// project root). targetDir is where .codex/hooks.json is written
// (worktree for a worker, project root for the coordinator).
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
	hooksRaw, err := os.ReadFile(hooksPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read codex hooks-config: %w", err)
	}
	var input hooksConfigInput
	if err := json.Unmarshal(hooksRaw, &input); err != nil {
		return "", false, fmt.Errorf("parse %s: %w", hooksPath, err)
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
	rendered, err := hooks.CodexHooksForKind(events, kind)
	if err != nil {
		return "", false, fmt.Errorf("render kind=%q: %w", kind, err)
	}

	target := filepath.Join(targetDir, codexTargetRel)
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
