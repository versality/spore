package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// AutoCommitDrift stages and commits any uncommitted changes under
// tasksDir to the current branch. Idempotent: no-ops when the tree
// is clean. Returns nil when there is nothing to commit.
func AutoCommitDrift(tasksDir string) error {
	projectRoot, err := ProjectRootFromTasksDir(tasksDir)
	if err != nil {
		return err
	}

	out, err := gitCmd(projectRoot, "status", "--porcelain", "--", tasksDir).Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}

	if o, err := gitCmd(projectRoot, "add", "--", tasksDir).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(o)))
	}
	if o, err := gitCmd(projectRoot, "commit", "-m", "tasks: auto-commit drift").CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(o)))
	}
	return nil
}

// AutoCommitOptions configures AutoCommit, the safety-wrapped driver
// behind `spore task auto-commit`. It runs from systemd-user path
// units; the wrapper adds repo validation, a flock against concurrent
// merges, and a staged-non-task-paths guard on top of AutoCommitDrift.
type AutoCommitOptions struct {
	// Repo is the repo root. Required. Must contain .git.
	Repo string
	// Lock is the flock path. Empty defaults to
	// $WT_STATE/merge-<basename(repo)>.lock with WT_STATE falling
	// back to $HOME/.local/state/wt. The dir is created on demand.
	Lock string
}

// ErrAutoCommitLocked signals lock contention. The caller should
// exit 0 silently; another worker holds the merge lock.
var ErrAutoCommitLocked = errors.New("auto-commit: lock held by another process")

// AutoCommitStagedNonTasksError lists staged paths outside tasksDir.
// Callers should refuse with exit 2 and print Paths.
type AutoCommitStagedNonTasksError struct {
	Paths []string
}

func (e *AutoCommitStagedNonTasksError) Error() string {
	return fmt.Sprintf("auto-commit: refusing with staged non-task paths:\n  %s",
		strings.Join(e.Paths, "\n  "))
}

// AutoCommit is the systemd-driven entry point. It:
//   - requires opts.Repo to be a git repo,
//   - no-ops when tasks/ is absent,
//   - takes an flock (30s wait); on contention returns ErrAutoCommitLocked,
//   - refuses when non-tasks/ paths are staged,
//   - delegates to AutoCommitDrift on $repo/tasks.
func AutoCommit(opts AutoCommitOptions) error {
	if opts.Repo == "" {
		return fmt.Errorf("auto-commit: --repo is required")
	}
	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return fmt.Errorf("auto-commit: resolve repo: %w", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return fmt.Errorf("auto-commit: not a git repo: %s", repo)
	}
	tasksDir := filepath.Join(repo, "tasks")
	if fi, err := os.Stat(tasksDir); err != nil || !fi.IsDir() {
		return nil
	}

	lockPath := opts.Lock
	if lockPath == "" {
		state := os.Getenv("WT_STATE")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("auto-commit: resolve WT_STATE: %w", err)
			}
			state = filepath.Join(home, ".local", "state", "wt")
		}
		if err := os.MkdirAll(state, 0o755); err != nil {
			return fmt.Errorf("auto-commit: mkdir state: %w", err)
		}
		lockPath = filepath.Join(state, "merge-"+strings.ReplaceAll(filepath.Base(repo), "/", "-")+".lock")
	} else {
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			return fmt.Errorf("auto-commit: mkdir lock dir: %w", err)
		}
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("auto-commit: open lock: %w", err)
	}
	defer f.Close()

	if err := flockWait(int(f.Fd()), 30); err != nil {
		if errors.Is(err, ErrAutoCommitLocked) {
			return err
		}
		return fmt.Errorf("auto-commit: flock: %w", err)
	}

	staged, err := gitCmd(repo, "diff", "--cached", "--name-only").Output()
	if err != nil {
		return fmt.Errorf("auto-commit: git diff --cached: %w", err)
	}
	var nonTasks []string
	for _, line := range strings.Split(strings.TrimRight(string(staged), "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "tasks/") {
			nonTasks = append(nonTasks, line)
		}
	}
	if len(nonTasks) > 0 {
		return &AutoCommitStagedNonTasksError{Paths: nonTasks}
	}

	return AutoCommitDrift(tasksDir)
}

// flockWait acquires an exclusive non-blocking flock on fd, polling
// up to waitSeconds. Returns ErrAutoCommitLocked on timeout.
func flockWait(fd int, waitSeconds int) error {
	for i := 0; i <= waitSeconds; i++ {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			return err
		}
		if i == waitSeconds {
			break
		}
		time.Sleep(time.Second)
	}
	return ErrAutoCommitLocked
}
