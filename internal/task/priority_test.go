package task

import (
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestIsValidPriority(t *testing.T) {
	cases := map[string]bool{
		"critical": true,
		"high":     true,
		"medium":   true,
		"low":      true,
		"":         false,
		"urgent":   false,
		"p0":       false,
		"High":     false,
	}
	for in, want := range cases {
		if got := IsValidPriority(in); got != want {
			t.Errorf("IsValidPriority(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPriorityRankOrder(t *testing.T) {
	order := []string{
		PriorityCritical,
		PriorityHigh,
		PriorityMedium,
		PriorityLow,
	}
	for i := 0; i < len(order)-1; i++ {
		if PriorityRank(order[i]) >= PriorityRank(order[i+1]) {
			t.Errorf("rank(%q) !< rank(%q)", order[i], order[i+1])
		}
	}
	if PriorityRank("") <= PriorityRank(PriorityLow) {
		t.Errorf("empty priority must rank after low")
	}
	if PriorityRank("bogus") <= PriorityRank(PriorityLow) {
		t.Errorf("unknown priority must rank after low")
	}
}

func TestSortByPromoteOrder(t *testing.T) {
	metas := []frontmatter.Meta{
		{Slug: "a", Priority: PriorityLow, Created: "2026-01-01T00:00:00Z"},
		{Slug: "b", Priority: PriorityCritical, Created: "2026-01-02T00:00:00Z"},
		{Slug: "c", Priority: PriorityHigh, Created: "2026-01-03T00:00:00Z"},
		{Slug: "d", Priority: PriorityHigh, Created: "2026-01-01T00:00:00Z"},
		{Slug: "e", Priority: "", Created: "2026-01-01T00:00:00Z"},
		{Slug: "f", Priority: PriorityMedium, Created: "2026-01-01T00:00:00Z"},
	}
	SortByPromoteOrder(metas)
	want := []string{"b", "d", "c", "f", "a", "e"}
	for i, w := range want {
		if metas[i].Slug != w {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, metas[i].Slug, w, slugs(metas))
		}
	}
}

func TestSortByPromoteOrderSlugTiebreak(t *testing.T) {
	metas := []frontmatter.Meta{
		{Slug: "zeta", Priority: PriorityMedium, Created: "2026-01-01T00:00:00Z"},
		{Slug: "alpha", Priority: PriorityMedium, Created: "2026-01-01T00:00:00Z"},
	}
	SortByPromoteOrder(metas)
	if metas[0].Slug != "alpha" {
		t.Errorf("slug tiebreak failed: got %v", slugs(metas))
	}
}

func slugs(metas []frontmatter.Meta) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.Slug
	}
	return out
}
