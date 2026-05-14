package task

import "github.com/versality/spore/internal/task/frontmatter"

// Canonical on-disk task statuses. The state machine is intentionally
// minimal: draft is "not yet runnable", active is "runner owns it",
// blocked is "not runnable, named reason in blocker:", done is
// terminal. parked, paused, and backlog were retired by
// drop-parked-status-gate; they survive only as legacy reads via
// AliasFromLegacy.
const (
	StatusDraft   = "draft"
	StatusActive  = "active"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

// AliasFromLegacy maps pre-collapse on-disk values into the canonical
// four. Reads stay safe while the operator's `spore task
// migrate-status` rewrites files in place. backlog still meant "not
// yet runnable" so it reads as draft. paused and parked were both
// "not runnable, waiting on something" so they read as blocked.
func AliasFromLegacy(status string) string {
	switch status {
	case "backlog":
		return StatusDraft
	case "paused", "parked":
		return StatusBlocked
	default:
		return status
	}
}

func CanonicalStatus(status string) string {
	return AliasFromLegacy(status)
}

func IsActive(status string) bool {
	return CanonicalStatus(status) == StatusActive
}

func IsDone(status string) bool {
	return CanonicalStatus(status) == StatusDone
}

func IsDraft(status string) bool {
	return CanonicalStatus(status) == StatusDraft
}

func IsBlocked(status string) bool {
	return CanonicalStatus(status) == StatusBlocked
}

// Promotable is true for tasks the fleet replenisher may auto-flip to
// active: status is draft and no gate keeps them out of the pool.
// blocked tasks are never auto-promoted; they wait for an explicit
// unblock.
func Promotable(m frontmatter.Meta) bool {
	return IsDraft(m.Status) && m.Gate == ""
}
