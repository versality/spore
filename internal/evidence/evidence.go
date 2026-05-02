// Package evidence parses and verifies the evidence contract for a
// spore task. Shape C: a frontmatter `evidence_required: [a, b, c]`
// inline list declares the required evidence kinds; the body's
// `## Evidence` section fills in `- <kind>: <rest>` bullets. Verify
// cross-checks the two and emits a verdict the done gate uses to
// allow or refuse a status flip.
//
// Verdicts are deliberately structural: this package never shells out
// to git or reads the working tree. A live-state verifier (see
// internal/coordinator/verify) can layer commit-history and session-
// transcript checks on top; spore's own gate stays pure so the unit
// tests run in-process without git fixtures.
package evidence

import (
	"slices"
	"time"
)

// Verdict is the outcome of cross-checking declared evidence against
// what the body provides. The done gate maps each verdict to allow or
// refuse via Blocks.
type Verdict string

const (
	RealImpl             Verdict = "real-impl"
	RationalClose        Verdict = "rational-close"
	CrossRepo            Verdict = "cross-repo"
	SuspectHallucination Verdict = "suspect-hallucination"
	BogusEvidence        Verdict = "bogus-evidence"
	Unknown              Verdict = "unknown"
)

// Item is one parsed bullet from the `## Evidence` section.
type Item struct {
	Kind string
	Rest string
}

// Kinds is the recognised set of evidence kinds. Lints reject anything
// outside this set in evidence_required; Parse silently skips unknown
// kinds in the body so a partial brief lints cleanly elsewhere.
var Kinds = []string{
	"commit", "command", "file", "test", "side-by-side", "doc-link",
}

// IsKind reports whether s is in Kinds.
func IsKind(s string) bool {
	return slices.Contains(Kinds, s)
}

// Blocks reports whether a verdict refuses a done flip. The set is
// fixed by the brief: suspect-hallucination, bogus-evidence, unknown.
// real-impl, rational-close, cross-repo all pass.
func Blocks(v Verdict) bool {
	switch v {
	case SuspectHallucination, BogusEvidence, Unknown:
		return true
	}
	return false
}

// ContractStart marks the day the evidence gate begins. The first
// SoakWindow after that, the gate is warn-only by default; after the
// window, blocking verdicts hard-fail. The operator can keep the
// permanent override in place by exporting SPORE_EVIDENCE_WARN_ONLY=1.
var ContractStart = time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

// SoakWindow is the warn-only grace period after ContractStart.
const SoakWindow = 7 * 24 * time.Hour

// InSoakWindow reports whether now falls inside the grace period.
func InSoakWindow(now time.Time) bool {
	return now.Before(ContractStart.Add(SoakWindow))
}
