package budget

import "time"

// Advice returns the current advice band ("ok", "tighten", "ration").
// Empty when state is unreadable; callers treat that as "ok" since the
// budget gate must not block hook flows on its own failure.
func Advice() string {
	s, err := loadState()
	if err != nil {
		return ""
	}
	now := time.Now().UTC()
	if queryNeedsRefresh(s, now) {
		refreshUsageSnapshot(s, now)
		_ = writeState(s)
	}
	computeAggregates(s, now)
	return s.Advice
}
