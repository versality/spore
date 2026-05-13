package task

import (
	"sort"

	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	PriorityCritical = "critical"
	PriorityHigh     = "high"
	PriorityMedium   = "medium"
	PriorityLow      = "low"
)

// DefaultPriority is what `spore task new` assigns when --priority is
// omitted. medium is the neutral middle of the ladder; critical and high
// are reserved for explicit operator intent.
const DefaultPriority = PriorityMedium

// Priorities lists every valid priority value, highest first. The order
// is also the promote-order: index 0 is picked before index 1.
var Priorities = []string{
	PriorityCritical,
	PriorityHigh,
	PriorityMedium,
	PriorityLow,
}

// IsValidPriority reports whether s is one of the four ladder values.
// Empty is not valid; callers wanting "missing is allowed" check that
// separately before calling.
func IsValidPriority(s string) bool {
	for _, p := range Priorities {
		if p == s {
			return true
		}
	}
	return false
}

// PriorityRank returns a sort key for a priority value: lower wins.
// Unknown or empty values sort after every valid value so a missing
// `priority:` does not jump the queue.
func PriorityRank(s string) int {
	for i, p := range Priorities {
		if p == s {
			return i
		}
	}
	return len(Priorities)
}

// SortByPromoteOrder sorts metas in place by (priority desc, created
// asc, slug asc). Tasks of equal priority go in created-order so the
// oldest waiting task wins the tiebreak, with slug as a final
// deterministic fallback when created strings collide.
func SortByPromoteOrder(metas []frontmatter.Meta) {
	sort.SliceStable(metas, func(i, j int) bool {
		ri, rj := PriorityRank(metas[i].Priority), PriorityRank(metas[j].Priority)
		if ri != rj {
			return ri < rj
		}
		if metas[i].Created != metas[j].Created {
			return metas[i].Created < metas[j].Created
		}
		return metas[i].Slug < metas[j].Slug
	})
}
