package lints

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// PlanFirstRequired rejects tasks/<slug>.md when effort=high|xhigh
// (incl. very-high aliases) and the worker is live but the body has
// no `## Plan` heading. The plan-first contract requires a Plan
// section before any source edit on high-effort workers.
//
// Status filter: fires only for active|blocked. draft is the
// operator's minting window; done is post-work (no backfill).
type PlanFirstRequired struct {
	TasksDir string
}

func (PlanFirstRequired) Name() string { return "plan-first-required" }

var planHeadingRE = regexp.MustCompile(`(?m)^##[ \t]+Plan[ \t]*$`)

func (l PlanFirstRequired) Run(root string) ([]Issue, error) {
	var issues []Issue
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		effort := strings.TrimSpace(m.Extra["effort"])
		switch effort {
		case "high", "xhigh", "very-high", "very_high":
		default:
			return nil
		}
		switch m.Status {
		case "active", "blocked":
		default:
			return nil
		}
		if !planHeadingRE.Match(body) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("effort=%s status=%s missing '## Plan' heading", effort, m.Status),
			})
		}
		return nil
	})
	return issues, err
}
