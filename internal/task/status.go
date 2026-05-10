package task

import "github.com/versality/spore/internal/task/frontmatter"

const (
	StatusBacklog = "backlog"
	StatusActive  = "active"
	StatusDone    = "done"

	StatusDraft   = "draft"
	StatusPaused  = "paused"
	StatusParked  = "parked"
	StatusBlocked = "blocked"
)

// AliasFromLegacy maps pre-collapse task statuses into the canonical
// state machine. Unknown and already-canonical values are unchanged.
func AliasFromLegacy(status string) string {
	switch status {
	case StatusDraft, StatusPaused, StatusParked, StatusBlocked:
		return StatusBacklog
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

func IsBacklog(status string) bool {
	return CanonicalStatus(status) == StatusBacklog
}

func Promotable(m frontmatter.Meta) bool {
	return IsBacklog(m.Status) && m.Gate == ""
}
