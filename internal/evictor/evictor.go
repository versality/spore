// Package evictor flips genuinely idle workers to status: blocked so
// the fleet can re-allocate the slot. A worker is "idle" when all of
// (a) its tmux session has not seen activity within the threshold,
// (b) its inbox holds no unread events, and (c) the wt/<slug> branch
// has not advanced within the threshold. Composes existing primitives
// (task.SessionIdle, task.MatchingSlugSessions, task.LastCommitTime,
// task.CountUnreadInboxForProject, task.BlockAuto) so the predicate
// stays a pure function over their values.
//
// Pairs with internal/task.SelfBlockOnCoordinatorTell (worker-driven
// self-block): one half is "worker posted a question and freed its
// slot", the other half is "worker walked away and we freed the slot
// for it". Both flip via task.BlockAuto, both record an
// auto:<reason> blocker so downstream queue / waybar / watchdog
// classifiers treat them identically.
package evictor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// BlockerKey is the stable blocker reason recorded on the worker's
// frontmatter when the evictor flips it. Mirrors the
// auto:posted-question-to-coordinator convention from selfblock.go.
const BlockerKey = "auto:idle-no-progress"

// DefaultIdleThreshold is the soak window before a worker counts as
// idle-no-progress. Override per-project via SPORE_EVICTOR_IDLE_SECS
// (matches the shape of SPORE_IDLE_REAP_SECS in lifecycle_session.go).
const DefaultIdleThreshold = 10 * time.Minute

// KillSwitchEnv disables the evictor at runtime when its value is
// one of 0|false|off|no (any other value, or unset, leaves it on).
// Parallels WT_TELL_AUTO_BLOCK in the downstream rename layer.
const KillSwitchEnv = "SPORE_EVICTOR"

// IdleSecsEnv overrides the soak window in seconds when set to a
// non-negative integer. Same shape as SPORE_IDLE_REAP_SECS.
const IdleSecsEnv = "SPORE_EVICTOR_IDLE_SECS"

// Inputs is the per-slug snapshot the predicate evaluates. All three
// signals are required to evict; any "unknown" leaves the worker
// alone.
type Inputs struct {
	// SessionPresent is true when at least one tmux session matches
	// the slug. False (no live session) means there is nothing to
	// reclaim - evict logic ignores the slug entirely.
	SessionPresent bool

	// Idle is how long the tmux session has gone without pane
	// activity. Meaningful only when IdleKnown is true; (0, false)
	// signals a probe failure (tmux quiet, garbage stamp) and short-
	// circuits to "do not evict".
	Idle      time.Duration
	IdleKnown bool

	// UnreadInbox is the count of *.json events at the top level of
	// the slug's inbox dir. Zero means drained.
	UnreadInbox int

	// LastCommit is the committer-date of the wt/<slug> branch tip.
	// LastCommitKnown=false means the branch is missing or unreadable;
	// the predicate treats that as "no recent progress" (a worker
	// that has never committed AND is idle AND has a drained inbox
	// is correctly evicted - there is no progress signal to
	// distinguish from idle).
	LastCommit      time.Time
	LastCommitKnown bool
}

// ShouldEvict is the pure predicate. Returns true iff all three idle
// signals hold simultaneously against the threshold. Pure function:
// no I/O, no clock - callers supply now and threshold.
func ShouldEvict(now time.Time, threshold time.Duration, in Inputs) bool {
	if !in.SessionPresent || !in.IdleKnown {
		return false
	}
	if in.Idle < threshold {
		return false
	}
	if in.UnreadInbox != 0 {
		return false
	}
	if in.LastCommitKnown && now.Sub(in.LastCommit) < threshold {
		return false
	}
	return true
}

// Probe is the side-effecting half of the sweep: gathers per-slug
// inputs by talking to tmux, the inbox dir, and git. Split out from
// Run so tests stub it without spinning up a real worktree.
type Probe interface {
	// SlugInputs gathers Inputs for one slug. Implementations must
	// not panic on missing data: the predicate treats "unknown" as
	// "do not evict", so probes return zero-values rather than
	// errors when a signal is genuinely absent.
	SlugInputs(projectRoot, tasksDir, slug string, now time.Time) Inputs
}

// realProbe is the production Probe. Composes existing task helpers
// instead of re-implementing the tmux/inbox/git reads.
type realProbe struct{}

// RealProbe returns a Probe that uses the live tmux/git/inbox stack.
func RealProbe() Probe { return realProbe{} }

func (realProbe) SlugInputs(projectRoot, tasksDir, slug string, now time.Time) Inputs {
	in := Inputs{}

	sessions := task.MatchingSlugSessions(tasksDir, projectRoot, slug)
	if len(sessions) > 0 {
		in.SessionPresent = true
		// Use the freshest idle reading across matching sessions:
		// a worker may have a sibling session (legacy + current
		// shape) and the youngest activity wins (kindest to the
		// worker, matches reapIdleSlugSessions semantics).
		freshest := time.Duration(-1)
		for _, name := range sessions {
			idle, ok := task.SessionIdle(name, now)
			if !ok {
				continue
			}
			if freshest < 0 || idle < freshest {
				freshest = idle
			}
		}
		if freshest >= 0 {
			in.Idle = freshest
			in.IdleKnown = true
		}
	}

	if n, _, err := task.CountUnreadInboxForProject(projectRoot, slug); err == nil {
		in.UnreadInbox = n
	} else {
		// Unknown inbox -> set to 1 so the predicate refuses to evict
		// on a read error. Mirrors reapIdleSlugSessions: "false means
		// leave it alone".
		in.UnreadInbox = 1
	}

	if last, ok := task.LastCommitTime(projectRoot, slug); ok {
		in.LastCommit = last
		in.LastCommitKnown = true
	}

	return in
}

// Config drives one sweep. ProjectRoot is the directory whose tasks/
// dir holds the slug files; TasksDir is the sub-path within it
// ("tasks" by default). Threshold is the soak window. Now is the
// reference time the predicate uses (tests pin it; production passes
// time.Now()).
type Config struct {
	ProjectRoot string
	TasksDir    string
	Threshold   time.Duration
	Now         func() time.Time

	// Probe overrides the production tmux/inbox/git probe in tests.
	// Nil -> RealProbe().
	Probe Probe

	// DryRun reports what would be evicted without flipping
	// frontmatter. Used by `spore fleet evict-idle --dry-run` for
	// operator inspection.
	DryRun bool

	// Block is the BlockAuto sink. Nil -> task.BlockAuto. Stubbed
	// in tests that want to observe the call without running the
	// full flip flow (real BlockAuto reaps tmux sessions, which the
	// run-test in this package does drive end to end).
	Block func(tasksDir, slug, blocker string) error
}

// Decision records the per-slug verdict for one sweep.
type Decision struct {
	Slug    string
	Evicted bool
	Reason  string // when Evicted=false, a one-liner; empty when Evicted=true.
	Inputs  Inputs
	Err     error
}

// Report is the aggregate sweep outcome. Slugs lists every active
// task considered; Decisions has one entry per slug.
type Report struct {
	Slugs     []string
	Decisions []Decision

	// Disabled is true when the kill-switch ($SPORE_EVICTOR in
	// {0,false,off,no}) suppressed the sweep. The slug lists are
	// empty in that case.
	Disabled bool
}

// Run executes one sweep across every status=active task in
// cfg.TasksDir. Errors from per-slug probes or flips are recorded in
// the per-Decision.Err so one bad slug does not abort the others
// (best-effort semantics: the timer fires again on the next tick).
func Run(cfg Config) (Report, error) {
	if Disabled() {
		return Report{Disabled: true}, nil
	}
	if cfg.TasksDir == "" {
		cfg.TasksDir = "tasks"
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = ResolveThreshold()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Probe == nil {
		cfg.Probe = RealProbe()
	}
	if cfg.Block == nil {
		cfg.Block = task.BlockAuto
	}

	metas, err := task.List(cfg.TasksDir)
	if err != nil {
		return Report{}, fmt.Errorf("evictor: list tasks: %w", err)
	}

	now := cfg.Now()
	var rep Report
	for _, m := range metas {
		if !task.IsActive(m.Status) {
			continue
		}
		rep.Slugs = append(rep.Slugs, m.Slug)
		dec := evaluate(cfg, m, now)
		rep.Decisions = append(rep.Decisions, dec)
	}
	return rep, nil
}

func evaluate(cfg Config, m frontmatter.Meta, now time.Time) Decision {
	dec := Decision{Slug: m.Slug}
	dec.Inputs = cfg.Probe.SlugInputs(cfg.ProjectRoot, cfg.TasksDir, m.Slug, now)
	if !ShouldEvict(now, cfg.Threshold, dec.Inputs) {
		dec.Reason = describe(dec.Inputs, cfg.Threshold, now)
		return dec
	}
	if cfg.DryRun {
		dec.Evicted = true
		dec.Reason = "dry-run"
		return dec
	}
	if err := cfg.Block(cfg.TasksDir, m.Slug, BlockerKey); err != nil {
		// "Already blocked" is a tolerable race: skyhelm could
		// have blocked the worker between the read and the flip.
		// Same shape as SelfBlockOnCoordinatorTell.
		if isAlreadyBlocked(err) {
			dec.Reason = "already blocked"
			return dec
		}
		dec.Err = fmt.Errorf("block %s: %w", m.Slug, err)
		return dec
	}
	dec.Evicted = true
	return dec
}

func describe(in Inputs, threshold time.Duration, now time.Time) string {
	switch {
	case !in.SessionPresent:
		return "no live tmux session"
	case !in.IdleKnown:
		return "idle stamp unreadable"
	case in.Idle < threshold:
		return fmt.Sprintf("active within %s (idle %s)", threshold, in.Idle.Round(time.Second))
	case in.UnreadInbox != 0:
		return fmt.Sprintf("inbox has %d unread", in.UnreadInbox)
	case in.LastCommitKnown && now.Sub(in.LastCommit) < threshold:
		return fmt.Sprintf("recent commit (%s ago)", now.Sub(in.LastCommit).Round(time.Second))
	default:
		return ""
	}
}

// Disabled reports whether the kill-switch env disables the sweep.
// Value matching mirrors the WT_TELL_AUTO_BLOCK style: any of
// 0|false|off|no (case-insensitive) suppresses; anything else (or
// unset) keeps the sweep on.
func Disabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(KillSwitchEnv)))
	switch v {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// ResolveThreshold returns the soak window honoring
// $SPORE_EVICTOR_IDLE_SECS (non-negative integer, seconds). Falls
// back to DefaultIdleThreshold when unset or unparseable.
func ResolveThreshold() time.Duration {
	if v := strings.TrimSpace(os.Getenv(IdleSecsEnv)); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return DefaultIdleThreshold
}

// isAlreadyBlocked mirrors selfblock.isAlreadyBlockedErr (status
// mismatch on the active->blocked flip). Kept local to avoid
// importing the internal sentinel through task's public surface.
func isAlreadyBlocked(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), `(want "active")`)
}

// WriteReport renders a sweep result as one human-readable line per
// active slug to w. Used by the CLI subcommand and ignored by
// production systemd runs (the unit logs go to journal).
func WriteReport(w io.Writer, rep Report) {
	if rep.Disabled {
		fmt.Fprintln(w, "evictor: disabled ($SPORE_EVICTOR off)")
		return
	}
	if len(rep.Decisions) == 0 {
		fmt.Fprintln(w, "evictor: no active tasks")
		return
	}
	for _, d := range rep.Decisions {
		if d.Err != nil {
			fmt.Fprintf(w, "evictor: %s: %v\n", d.Slug, d.Err)
			continue
		}
		if d.Evicted {
			fmt.Fprintf(w, "evictor: %s evicted (%s)\n", d.Slug, BlockerKey)
			continue
		}
		fmt.Fprintf(w, "evictor: %s kept (%s)\n", d.Slug, d.Reason)
	}
}

// Static check that the package imports compile against the API we
// rely on. Removed by the linker; safe to leave.
var _ = errors.New
