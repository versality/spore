package task

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/coordinator"
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

// CoordinatorStateDir returns the host-wide coordinator root. It
// delegates to the central resolver in internal/coordinator so every
// coordinator package agrees on one layout.
func CoordinatorStateDir() (string, error) {
	return coordinator.StateDir(), nil
}

// CoordinatorInboxDirForProject returns the singleton coordinator
// inbox path for projectRoot. The layout is <CoordinatorStateDir>/<project>/inbox.
func CoordinatorInboxDirForProject(projectRoot string) (string, error) {
	project, err := ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	return coordinator.ProjectInbox(project), nil
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

// ProjectName returns the basename of the main repo containing
// projectRoot, falling back to the basename of projectRoot itself
// when not a git repo. Pass "" to use the current working directory.
//
// Resolved via `git rev-parse --git-common-dir`: in a linked worktree
// this returns the main repo's .git path, so dirname yields the main
// repo root regardless of cwd. Using --show-toplevel here would return
// the worktree path and silently rename the project to the worktree
// slug from any sandboxed-agent cwd.
func ProjectName(projectRoot string) (string, error) {
	if projectRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		projectRoot = wd
	}
	if out, err := gitCmd(projectRoot, "rev-parse", "--git-common-dir").Output(); err == nil {
		common := strings.TrimSpace(string(out))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(projectRoot, common)
			}
			return filepath.Base(filepath.Dir(common)), nil
		}
	}
	return filepath.Base(projectRoot), nil
}

// CountUnreadInbox returns the number of *.json files sitting at the
// top level of slug's inbox (unread). Returns 0 when the directory
// does not exist. Mirrors wt-go internal/inbox.CountUnread.
func CountUnreadInbox(slug string) (int, string, error) {
	return CountUnreadInboxForProject("", slug)
}

// CountUnreadInboxForProject is the project-rooted variant of
// CountUnreadInbox. Used by the idle-evictor sweep, which iterates
// across configured projects and cannot rely on cwd.
func CountUnreadInboxForProject(projectRoot, slug string) (int, string, error) {
	dir, err := InboxDirForProject(projectRoot, slug)
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

// LastCommitTime returns the committer-date of the tip commit on
// refs/heads/wt/<slug>. ok=false when the branch is missing or the
// timestamp cannot be parsed; callers should treat ok=false as "no
// commit on record" (the idle-evictor predicate counts a missing
// branch as "no recent progress", since a worker that has never
// committed has by definition not made progress).
func LastCommitTime(projectRoot, slug string) (time.Time, bool) {
	branch := "wt/" + slug
	if gitCmd(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() != nil {
		return time.Time{}, false
	}
	out, err := gitCmd(projectRoot, "log", "-1", "--format=%ct", "refs/heads/"+branch).Output()
	if err != nil {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
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
