package task

import "github.com/versality/spore/internal/task/frontmatter"

// Canonical on-disk task statuses. The state machine is intentionally
// minimal: draft is "not yet runnable", active is "runner owns it",
// blocked is "not runnable, named reason in blocker:", done is
// terminal.
const (
	StatusDraft   = "draft"
	StatusActive  = "active"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

func IsActive(status string) bool {
	return status == StatusActive
}

func IsDone(status string) bool {
	return status == StatusDone
}

func IsDraft(status string) bool {
	return status == StatusDraft
}

func IsBlocked(status string) bool {
	return status == StatusBlocked
}

// Promotable is true for tasks the fleet replenisher may auto-flip to
// active: status is draft and no gate keeps them out of the pool.
// blocked tasks are never auto-promoted; they wait for an explicit
// unblock.
func Promotable(m frontmatter.Meta) bool {
	return IsDraft(m.Status) && m.Gate == ""
}
