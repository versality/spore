package task

import (
	"fmt"
	"os"
	"path/filepath"
)

func paneBootstrapCommand(agentCmd string) string {
	return `tmux set-window-option -t "$TMUX_PANE" remain-on-exit on >/dev/null 2>&1 || true; exec ` + agentCmd
}

func ensureCodexWorktreeLayer(worktree string) error {
	// Codex linked-worktree discovery only considers a project layer if
	// the worktree path itself has a .codex/ directory. Hook declarations
	// still come from the root checkout .codex/hooks.json; this directory
	// is intentionally just the layer marker.
	// https://github.com/openai/codex/blob/9f42c89c0112771dc29100a6f3fc904049b2655f/codex-rs/config/src/loader/mod.rs#L1156-L1165
	// https://github.com/openai/codex/blob/9f42c89c0112771dc29100a6f3fc904049b2655f/codex-rs/config/src/loader/mod.rs#L1267-L1322
	if err := os.MkdirAll(filepath.Join(worktree, ".codex"), 0o755); err != nil {
		return fmt.Errorf("ensure codex worktree layer: %w", err)
	}
	return nil
}

func copyBriefToWorktree(tasksDir, worktree, slug string) error {
	src := filepath.Join(tasksDir, slug+".md")
	body, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rel := filepath.Base(filepath.Clean(tasksDir))
	dst := filepath.Join(worktree, rel, slug+".md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, body, 0o644)
}

// stageInitialPrompt writes the task brief to <worktree>/.wt/initial-prompt
// so ensureSession's sh-wrapped agent launch can `cat` it into the first
// user message. Called on every ensureSession (not just first-time worktree
// creation) so that a re-mint into an existing worktree (the wedge-recovery
// path) still gets a fresh prompt. Safe to overwrite: .wt/initial-prompt
// is a transient stage file, never the operator's source-of-truth brief.
// Soft-fails on a missing source brief.
func stageInitialPrompt(tasksDir, worktree, slug string) error {
	src := filepath.Join(tasksDir, slug+".md")
	body, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	promptDir := filepath.Join(worktree, ".wt")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(promptDir, "initial-prompt"), body, 0o644)
}
