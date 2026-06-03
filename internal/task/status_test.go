package task

import (
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestPromotableDraftWithEmptyGate(t *testing.T) {
	cases := []struct {
		name string
		meta frontmatter.Meta
		want bool
	}{
		{"draft", frontmatter.Meta{Status: StatusDraft}, true},
		{"draft gated", frontmatter.Meta{Status: StatusDraft, Gate: "after x"}, false},
		{"blocked", frontmatter.Meta{Status: StatusBlocked}, false},
		{"active", frontmatter.Meta{Status: StatusActive}, false},
		{"unknown", frontmatter.Meta{Status: "weird"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Promotable(tc.meta); got != tc.want {
				t.Errorf("Promotable(%+v) = %v, want %v", tc.meta, got, tc.want)
			}
		})
	}
}

func TestPromotableHonoursLegacySchedulerFallback(t *testing.T) {
	m, _, err := frontmatter.Parse([]byte("---\nstatus: draft\nslug: x\nscheduler: after y\n---\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if Promotable(m) {
		t.Fatal("Promotable should be false when scheduler fallback populates Gate")
	}
}
