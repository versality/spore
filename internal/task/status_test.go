package task

import (
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestCanonicalStatusAliasesLegacyToBacklog(t *testing.T) {
	cases := map[string]string{
		StatusDraft:   StatusBacklog,
		StatusPaused:  StatusBacklog,
		StatusParked:  StatusBacklog,
		StatusBlocked: StatusBacklog,
		StatusBacklog: StatusBacklog,
		StatusActive:  StatusActive,
		StatusDone:    StatusDone,
		"custom":      "custom",
	}
	for in, want := range cases {
		if got := CanonicalStatus(in); got != want {
			t.Errorf("CanonicalStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPromotableBacklogWithEmptyGate(t *testing.T) {
	cases := []struct {
		name string
		meta frontmatter.Meta
		want bool
	}{
		{"legacy draft", frontmatter.Meta{Status: StatusDraft}, true},
		{"legacy parked", frontmatter.Meta{Status: StatusParked}, true},
		{"new backlog", frontmatter.Meta{Status: StatusBacklog}, true},
		{"backlog gated", frontmatter.Meta{Status: StatusBacklog, Gate: "after x"}, false},
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
