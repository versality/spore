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
	return StateDirForProject("")
}

// StateDirForProject returns "$XDG_STATE_HOME/spore/<project>" for
// projectRoot, falling back to "$HOME/.local/state/spore/<project>".
func StateDirForProject(projectRoot string) (string, error) {
	project, err := ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	base, err := stateBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "spore", project), nil
}

// InboxDir returns "<StateDir>/<slug>/inbox".
func InboxDir(slug string) (string, error) {
	return InboxDirForProject("", slug)
}

// InboxDirForProject returns "<StateDirForProject>/<slug>/inbox".
func InboxDirForProject(projectRoot, slug string) (string, error) {
	s, err := StateDirForProject(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(s, slug, "inbox"), nil
}

// CoordinatorStateDir returns the state root used by the singleton
// coordinator inboxes.
func CoordinatorStateDir() (string, error) {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d, nil
	}
	base, err := stateBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "spore", "coordinator"), nil
}

// CoordinatorInboxDirForProject returns the singleton coordinator
// inbox path for projectRoot.
func CoordinatorInboxDirForProject(projectRoot string) (string, error) {
	project, err := ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	root, err := CoordinatorStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, project, "inbox"), nil
}

func stateBaseDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base != "" {
		return base, nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("task: HOME and XDG_STATE_HOME both unset")
	}
	return filepath.Join(home, ".local", "state"), nil
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

// CountUnreadInbox returns the number of *.json files sitting at the
// top level of slug's inbox (unread). Returns 0 when the directory
// does not exist. Mirrors wt-go internal/inbox.CountUnread.
func CountUnreadInbox(slug string) (int, string, error) {
	dir, err := InboxDir(slug)
	if err != nil {
		return 0, "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, dir, nil
		}
		return 0, dir, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		n++
	}
	return n, dir, nil
}

// UnmergedCommits returns the count of commits reachable from
// refs/heads/<branch> but not from main. Returns 0 when the branch
// does not exist (already deleted by a prior merge).
func UnmergedCommits(projectRoot, branch string) (int, error) {
	if gitCmd(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() != nil {
		return 0, nil
	}
	mainRef := "main"
	if gitCmd(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/main").Run() != nil {
		mainRef = "master"
	}
	out, err := gitCmd(projectRoot, "rev-list", mainRef+".."+branch).Output()
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	return strings.Count(s, "\n") + 1, nil
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
