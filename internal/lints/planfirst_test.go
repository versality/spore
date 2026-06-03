package lints

import "testing"

func taskWithBody(slug, status, effort, body string) string {
	hdr := "---\nslug: " + slug + "\nstatus: " + status + "\nagent: claude\n"
	if effort != "" {
		hdr += "effort: " + effort + "\n"
	}
	return hdr + "---\n\n" + body
}

func TestPlanFirstRequired(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/active-high-noplan.md": taskWithBody("active-high-noplan", "active", "high", "body without plan\n"),
		"tasks/active-high-plan.md":   taskWithBody("active-high-plan", "active", "high", "## Plan\n\n_TBD_\n"),
		"tasks/active-medium.md":      taskWithBody("active-medium", "active", "medium", "no plan, no problem\n"),
		"tasks/draft-high.md":         taskWithBody("draft-high", "draft", "high", "drafts skipped\n"),
		"tasks/done-high.md":          taskWithBody("done-high", "done", "high", "no backfill\n"),
		"tasks/blocked-xhigh.md":      taskWithBody("blocked-xhigh", "blocked", "xhigh", "no plan\n"),
		"tasks/active-veryhigh.md":    taskWithBody("active-veryhigh", "active", "very-high", "no plan\n"),
	})
	issues, err := PlanFirstRequired{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	wantHit := []string{
		"tasks/active-high-noplan.md",
		"tasks/blocked-xhigh.md",
		"tasks/active-veryhigh.md",
	}
	wantSkip := []string{
		"tasks/active-high-plan.md",
		"tasks/active-medium.md",
		"tasks/draft-high.md",
		"tasks/done-high.md",
	}
	for _, p := range wantHit {
		if !got[p] {
			t.Errorf("missing hit on %s; got %v", p, got)
		}
	}
	for _, p := range wantSkip {
		if got[p] {
			t.Errorf("unexpected hit on %s", p)
		}
	}
}
