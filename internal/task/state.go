package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StateDir returns "$XDG_STATE_HOME/spore/<project>" if XDG_STATE_HOME
// is set, else "$HOME/.local/state/spore/<project>". Project name comes
// from the basename of the git toplevel (or cwd if not a repo).
func StateDir() (string, error) {
	project, err := ProjectName("")
	if err != nil {
		return "", err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("task: HOME and XDG_STATE_HOME both unset")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project), nil
}

// InboxDir returns "<StateDir>/<slug>/inbox".
func InboxDir(slug string) (string, error) {
	s, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(s, slug, "inbox"), nil
}

// ProjectName returns the basename of the git toplevel rooted at
// projectRoot, falling back to the basename of projectRoot itself
// when not a git repo. Pass "" to use the current working directory.
func ProjectName(projectRoot string) (string, error) {
	if projectRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		projectRoot = wd
	}
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--show-toplevel")
	if out, err := cmd.Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if root != "" {
			return filepath.Base(root), nil
		}
	}
	return filepath.Base(projectRoot), nil
}
