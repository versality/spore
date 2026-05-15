package task

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// IdleReapThreshold is how long a tmux session must sit without
// activity before pause/block reaps it. Sessions younger than this
// are kept alive: a mid-tool-call worker or a pane the operator just
// stopped typing into is not "abandoned". Override via
// SPORE_IDLE_REAP_SECS for tests / operator tuning.
const IdleReapThreshold = 5 * time.Minute

// matchingSlugSessions lists every tmux session that belongs to
// (project, slug), regardless of formula drift between spawn and
// kill. Delegates shape recognition to MatchSlug; also includes the
// frontmatter-recorded session when tmux confirms it is alive (so
// external spawners that chose a non-kernel name still get reaped).
// Returns nil when tmux isn't running or no session matches.
func matchingSlugSessions(tasksDir, projectRoot, slug string) []string {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	project := projectNameOrBase(projectRoot)
	recorded := ""
	if m, err := readTaskMeta(tasksDir, slug); err == nil {
		recorded = m.Session
	}
	seen := map[string]bool{}
	var matches []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		matches = append(matches, name)
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		if MatchSlug(line, project, slug) {
			add(line)
		}
	}
	if recorded != "" && hasSession(recorded) {
		add(recorded)
	}
	return matches
}

// killAllSlugSessions tears down every tmux session matching slug
// for the project. Errors are swallowed - this is best-effort cleanup;
// the status flip stays the source of truth. Pass-through no-op when
// tmux isn't running or nothing matches.
func killAllSlugSessions(tasksDir, projectRoot, slug string) {
	for _, name := range matchingSlugSessions(tasksDir, projectRoot, slug) {
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	}
}

// reapIdleSlugSessions kills only the matching sessions whose tmux
// activity is older than IdleReapThreshold (paused/blocked semantics:
// keep mid-run sessions alive). projectRoot is derived from tasksDir;
// any read or parse failure leaves the session alone.
func reapIdleSlugSessions(tasksDir, slug string) {
	projectRoot, err := projectRootFromTasksDir(tasksDir)
	if err != nil {
		return
	}
	threshold := IdleReapThreshold
	if v := os.Getenv("SPORE_IDLE_REAP_SECS"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			threshold = time.Duration(secs) * time.Second
		}
	}
	now := time.Now()
	for _, name := range matchingSlugSessions(tasksDir, projectRoot, slug) {
		idle, ok := sessionIdle(name, now)
		if !ok {
			continue
		}
		if idle < threshold {
			continue
		}
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
	}
}

// sessionIdle reports how long a tmux session has gone without pane
// activity. Returns (0, false) when the activity stamp can't be read
// (session gone, tmux quiet, garbage value); the false signals the
// caller to leave the session alone rather than treat unknown as
// stale.
func sessionIdle(name string, now time.Time) (time.Duration, bool) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", name, "#{session_activity}").Output()
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, false
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	last := time.Unix(secs, 0)
	if last.After(now) {
		return 0, true
	}
	return now.Sub(last), true
}

func hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func branchExists(projectRoot, branch string) bool {
	return gitCmd(projectRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}
