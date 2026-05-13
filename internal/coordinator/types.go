// Package coordinator hosts shared types and helpers used by the
// reactive coordinator primitives (worker-watch already lives in
// the workerwatch subpackage; idle-watchdog, proactive-loop, and
// queue-classifier will land in their own subdirectories). This
// package contains only the value types and path helpers they share.
package coordinator

import "time"

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
