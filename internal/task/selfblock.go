package task

import (
	"errors"
	"fmt"
	"strings"
)

// CoordinatorTellTarget is the slug value that, when used as the
// target of `spore task tell <target> <msg>` from a worker session,
// causes the worker's own slug to atomically flip to status: blocked.
// The rule is "posting a question to the coordinator == self-block":
// a worker that needs an operator-level decision must free its slot,
// and asking the coordinator IS the decision-deferral signal.
const CoordinatorTellTarget = "coordinator"

// SelfBlockBlocker is the blocker reason recorded on the worker's
// task frontmatter when SelfBlockOnCoordinatorTell fires. Kept stable
// so downstream tooling (queue classifier, idle watchdog, eviction
// daemon) can recognise auto-blocked workers and treat them like any
// other operator-bound block.
const SelfBlockBlocker = "auto:posted-question-to-coordinator"

// SelfBlockOnCoordinatorTell is the policy half of the
// tell-coordinator auto-block: given the target slug, the message
// body, and the caller's own slug, flip the caller to blocked when
// the target is the coordinator and the caller is itself a worker
// slug. A nil error is returned when no flip applies (target is not
// the coordinator, caller is unknown, caller is the coordinator
// itself, body matches a protocol-continuation prefix) and when the
// caller is already blocked (the "want active" frontmatter-state
// mismatch is tolerated as a no-op so a caller that happens to post
// twice does not error).
func SelfBlockOnCoordinatorTell(tasksDir, target, body, callerSlug string) error {
	if target != CoordinatorTellTarget {
		return nil
	}
	if callerSlug == "" || callerSlug == CoordinatorTellTarget {
		return nil
	}
	if !bodyTriggersAutoBlock(body) {
		return nil
	}
	err := BlockAuto(tasksDir, callerSlug, SelfBlockBlocker)
	if err == nil {
		return nil
	}
	if isAlreadyBlockedErr(err) {
		return nil
	}
	return fmt.Errorf("tell-coordinator self-block: %w", err)
}

// autoBlockExemptPrefixes lists message-body prefixes that signal a
// protocol-continuation tell (the worker is on-rails, not deferring
// a decision) and therefore must not trigger the self-block flip.
// The canonical case is `plan ready: <slug>`, defined by the
// plan-first contract: the worker has just written the plan and is
// requesting the coordinator's ack to start implementation.
var autoBlockExemptPrefixes = []string{
	"plan ready:",
}

// bodyTriggersAutoBlock returns true when the message body should
// cause SelfBlockOnCoordinatorTell to flip the caller. Bodies whose
// trimmed form starts with an exempt protocol prefix return false:
// the tell is on-protocol and the worker keeps its slot.
func bodyTriggersAutoBlock(body string) bool {
	trimmed := strings.TrimLeft(body, " \t")
	for _, p := range autoBlockExemptPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return false
		}
	}
	return true
}

// isAlreadyBlockedErr returns true when err is the "status mismatch"
// emitted by flipStatusWithBlocker on a slug whose status is not the
// expected `from` value. We treat that as a tolerable no-op so the
// auto-flip can repeat without escalating; any other error is real.
func isAlreadyBlockedErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errStatusMismatch) {
		return true
	}
	return strings.Contains(err.Error(), `(want "active")`)
}

// errStatusMismatch is a sentinel for future use by
// flipStatusWithBlocker callers that want to distinguish "wrong
// from-state" from other failures. Today the function returns a
// formatted string, so SelfBlockOnCoordinatorTell falls back to
// string matching; the sentinel keeps callers forward-compatible if
// flipStatusWithBlocker is refactored to use errors.Is.
var errStatusMismatch = errors.New("task status mismatch")
