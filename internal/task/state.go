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
	if out, err := gitCmd(projectRoot, "rev-parse", "--show-toplevel").Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if root != "" {
			return filepath.Base(root), nil
		}
	}
	return filepath.Base(projectRoot), nil
}

// gitCmd returns `git -c safe.directory=<abs(projectRoot)> -C
// <projectRoot> <args...>`. safe.directory shields against repos
// imported via rsync, which preserves the source uid and trips git's
// dubious-ownership guard. The narrower form (one explicit path) is
// used instead of `*` so we only trust the project being acted on.
func gitCmd(projectRoot string, args ...string) *exec.Cmd {
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		abs = projectRoot
	}
	full := append([]string{"-c", "safe.directory=" + abs, "-C", projectRoot}, args...)
	return exec.Command("git", full...)
}
