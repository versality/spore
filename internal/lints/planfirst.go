package lints

import (
	"fmt"
	"os"
	"path/filepath"
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
	dir := l.TasksDir
	if dir == "" {
		dir = "tasks"
	}
	abs := filepath.Join(root, dir)
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(abs, e.Name())
		rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		effort := strings.TrimSpace(m.Extra["effort"])
		switch effort {
		case "high", "xhigh", "very-high", "very_high":
		default:
			continue
		}
		switch m.Status {
		case "active", "blocked":
		default:
			continue
		}
		if !planHeadingRE.Match(body) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("effort=%s status=%s missing '## Plan' heading", effort, m.Status),
			})
		}
	}
	return issues, nil
}
