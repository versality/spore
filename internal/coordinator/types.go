// Package coordinator hosts shared types and helpers used by the
// reactive coordinator primitives (rower-watch, idle-watchdog,
// proactive-loop, queue-classifier). The primitives live in
// per-package subdirectories; this package contains only the value
// types and path helpers they share.
package coordinator

import "time"

// RowerView is one rower's snapshot inside a FleetSnapshot.
type RowerView struct {
	Slug        string
	Status      string
	Branch      string
	TmuxSession string
	LastSeen    time.Time
}

// FleetSnapshot captures the fleet's runtime state at a single tick.
// TakenAt is the wall-clock time the snapshot was assembled, used by
// downstream consumers to compute Freshness against their own clocks.
type FleetSnapshot struct {
	TakenAt time.Time
	Rowers  []RowerView
}

// TaskFrontmatter is the parsed YAML frontmatter of tasks/<slug>.md.
// Only fields consumed by the reactive primitives appear here; the
// task package is the source of truth for the full schema.
type TaskFrontmatter struct {
	Slug     string
	Status   string
	Priority string
	Agent    string
	Effort   string
}

// Verdict is the queue-classifier output value.
type Verdict string

const (
	VerdictRunnablePromote        Verdict = "runnable-promote"
	VerdictResume                 Verdict = "resume"
	VerdictWaitingTrigger         Verdict = "waiting-trigger"
	VerdictOperatorBlocked        Verdict = "operator-blocked"
	VerdictInvalidNeedsReclassify Verdict = "invalid-needs-reclassify"
)

// FreshnessLevel records how recent the source signal is.
type FreshnessLevel string

const (
	FreshnessFresh   FreshnessLevel = "fresh"
	FreshnessStale   FreshnessLevel = "stale"
	FreshnessUnknown FreshnessLevel = "unknown"
)

// Freshness packages a freshness level with the signal timestamp.
// SignalAt is the source signal's wall-clock time; consumers diff it
// against now to render "stale by N seconds" verdicts.
type Freshness struct {
	Level    FreshnessLevel
	SignalAt time.Time
}
