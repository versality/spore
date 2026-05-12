// Package rowerwatch is the coordinator Stop-hook helper that
// surfaces rower-state transitions between turns. It walks the
// active-task set (frontmatter under each project's tasks/), diffs
// against a persisted NDJSON snapshot, and emits one line per
// transition (APPEARED, DISAPPEARED, STUCK, UNSTUCK, HEAD-MOVED).
// DISAPPEARED into status=done augments the line with the
// coordinator/verify verdict so the next turn sees the verdict
// without having to ask.
//
// Ported from harness/skyhelm-rower-watch (bash). The output banner
// "SKYHELM ROWER WATCH:" is preserved verbatim so the downstream
// Stop-hook reminder grep stays unchanged across the consumer swap.
package rowerwatch

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const Banner = "SKYHELM ROWER WATCH:"

// Config carries the tunables. Defaults match the bash watcher so a
// drop-in replacement preserves classifier behaviour.
type Config struct {
	StuckOpencodeSecs int  // default 600
	StuckClaudeSecs   int  // default 900
	Debounce          int  // default 2; consecutive observations before stuck flips
	HeadMovedOn       bool // default false; HEAD-MOVED is informational, off by default
}

func (c Config) Defaults() Config {
	if c.StuckOpencodeSecs <= 0 {
		c.StuckOpencodeSecs = 600
	}
	if c.StuckClaudeSecs <= 0 {
		c.StuckClaudeSecs = 900
	}
	if c.Debounce <= 0 {
		c.Debounce = 2
	}
	return c
}

// Snapshot is one row of the NDJSON state file. Field names map to
// the bash schema verbatim so an in-place state file written by one
// implementation parses cleanly in the other.
type Snapshot struct {
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Branch   string `json:"branch"`
	HeadSHA  string `json:"head_sha"`
	Agent    string `json:"agent"`
	IdleSecs int    `json:"idle_secs"`
	Stuck    bool   `json:"stuck"`
	Flap     int    `json:"flap"`
	LastSeen string `json:"last_seen"`
}

// TaskRef is the per-project active-task descriptor the caller passes
// to Run. Slug is the project-qualified slug (multi-project only);
// BaseSlug is the unqualified filename slug; ProjectRoot points at
// the owning project. Agent defaults to "claude" when empty.
type TaskRef struct {
	Slug        string
	BaseSlug    string
	ProjectRoot string
	Status      string
	Agent       string
}

// FinalStatus is the post-disappearance status the caller resolved
// for a previously-seen slug whose task file is gone or no longer
// active. Empty Status means the task file is missing entirely; the
// caller signals that with Status = "missing".
type FinalStatus struct {
	Status  string
	Verdict string
}

// Env wires the side-effecting probes through function values so
// tests fill in deterministic fakes. Every field must be non-nil in
// production; Run does not nil-check.
type Env struct {
	Now func() time.Time
	// Active returns every status=active rower across every project,
	// already prefixed with the project name when more than one
	// project is registered.
	Active func() []TaskRef
	// HeadSHA returns the short-7 commit hash of `wt/<base_slug>` in
	// the project worktree, or "" when the worktree is gone or the
	// rev-parse fails.
	HeadSHA func(projectRoot, baseSlug string) string
	// Idle returns the idle seconds and whether the measurement
	// succeeded. Failure (ok=false) keeps the rower out of stuck
	// classification regardless of threshold.
	Idle func(projectRoot, baseSlug, agent string) (secs int, ok bool)
	// Resolve produces the post-disappearance status + verdict for a
	// slug that was in the prior snapshot but is not in the current
	// active set.
	Resolve func(slug, baseSlug, projectRoot string) FinalStatus
	// LoadState returns the persisted snapshot rows. An empty result
	// means first-run.
	LoadState func() ([]Snapshot, error)
	// SaveState writes the next snapshot. Best-effort: a failure
	// here does not suppress the current turn's transitions.
	SaveState func([]Snapshot) error
}

// Result is the diff outcome. Transitions are pre-formatted, one
// string per emitted line in stable order (APPEARED+STUCK+UNSTUCK
// first by slug, then DISAPPEARED by slug).
type Result struct {
	Transitions []string
	Next        []Snapshot
}

// Run computes the transition set for the current turn. It loads the
// prior snapshot, classifies every active rower, and emits one
// transition per state delta. Side effects: SaveState is called once
// before return; its error is swallowed (transitions stay valid for
// this turn regardless of disk failure).
func Run(cfg Config, env Env) Result {
	cfg = cfg.Defaults()

	prior, _ := env.LoadState()
	priorBySlug := make(map[string]Snapshot, len(prior))
	for _, s := range prior {
		priorBySlug[s.Slug] = s
	}

	now := env.Now()
	active := env.Active()
	sort.Slice(active, func(i, j int) bool { return active[i].Slug < active[j].Slug })

	var transitions []string
	next := make([]Snapshot, 0, len(active))
	currentSlugs := make(map[string]struct{}, len(active))

	for _, t := range active {
		agent := t.Agent
		if agent == "" {
			agent = "claude"
		}
		currentSlugs[t.Slug] = struct{}{}

		head := env.HeadSHA(t.ProjectRoot, t.BaseSlug)
		idleSecs, idleOk := env.Idle(t.ProjectRoot, t.BaseSlug, agent)
		if !idleOk {
			idleSecs = -1
		}

		stuckThreshold := cfg.StuckClaudeSecs
		if agent == "opencode" {
			stuckThreshold = cfg.StuckOpencodeSecs
		}
		stuckRaw := idleOk && idleSecs > stuckThreshold

		entry := Snapshot{
			Slug:     t.Slug,
			Status:   t.Status,
			Branch:   "wt/" + t.BaseSlug,
			HeadSHA:  head,
			Agent:    agent,
			IdleSecs: idleSecs,
			LastSeen: now.Format(time.RFC3339),
		}

		p, seen := priorBySlug[t.Slug]
		if !seen {
			parts := []string{t.Status, agent}
			if idleOk && idleSecs >= 0 {
				parts = append(parts, "idle="+formatDur(idleSecs))
			}
			transitions = append(transitions, fmt.Sprintf("APPEARED %s (%s)", t.Slug, strings.Join(parts, ", ")))
			entry.Stuck = false
			entry.Flap = 0
			next = append(next, entry)
			continue
		}

		newStuck := p.Stuck
		newFlap := 0
		if stuckRaw == p.Stuck {
			newFlap = 0
		} else {
			newFlap = p.Flap + 1
			if newFlap >= cfg.Debounce {
				newStuck = stuckRaw
				newFlap = 0
				idleH := formatDur(idleSecs)
				if newStuck {
					transitions = append(transitions, fmt.Sprintf("STUCK %s (%s, idle=%s)", t.Slug, agent, idleH))
				} else {
					transitions = append(transitions, fmt.Sprintf("UNSTUCK %s (%s, idle=%s)", t.Slug, agent, idleH))
				}
			}
		}
		entry.Stuck = newStuck
		entry.Flap = newFlap

		if cfg.HeadMovedOn && p.HeadSHA != "" && head != "" && head != p.HeadSHA && t.Status == p.Status {
			transitions = append(transitions, fmt.Sprintf("HEAD-MOVED %s (%s -> %s)", t.Slug, p.HeadSHA, head))
		}

		next = append(next, entry)
	}

	disappeared := make([]string, 0)
	for slug := range priorBySlug {
		if _, ok := currentSlugs[slug]; ok {
			continue
		}
		disappeared = append(disappeared, slug)
	}
	sort.Strings(disappeared)

	for _, slug := range disappeared {
		p := priorBySlug[slug]
		baseSlug := slug
		if i := strings.IndexByte(slug, '/'); i >= 0 {
			baseSlug = slug[i+1:]
		}
		final := env.Resolve(slug, baseSlug, "")
		newStatus := final.Status
		if newStatus == "" {
			newStatus = "?"
		}
		if final.Verdict != "" {
			transitions = append(transitions, fmt.Sprintf("DISAPPEARED %s (%s -> %s, verdict=%s)", slug, p.Status, newStatus, final.Verdict))
		} else {
			transitions = append(transitions, fmt.Sprintf("DISAPPEARED %s (%s -> %s)", slug, p.Status, newStatus))
		}
	}

	_ = env.SaveState(next)

	return Result{Transitions: transitions, Next: next}
}

// FormatBlock renders the stderr block emitted on a non-empty
// transition set. Empty input returns "".
func FormatBlock(transitions []string) string {
	if len(transitions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(Banner)
	b.WriteByte('\n')
	for _, t := range transitions {
		b.WriteString("  ")
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return b.String()
}

func formatDur(s int) string {
	if s < 0 {
		return "?"
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm", s/60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
}
