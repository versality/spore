package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Edit opens tasks/<slug>.md in the user's $EDITOR (default "vi").
// It execs the editor directly, replacing the current process only
// when called from the CLI entry point. For library use, it spawns
// a subprocess and waits.
func Edit(tasksDir, slug string) error {
	path := filepath.Join(tasksDir, slug+".md")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("task %s: %w", slug, err)
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
