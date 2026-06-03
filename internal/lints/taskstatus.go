package lints

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// TaskStatus validates the `status:` frontmatter scalar in tasks/*.md
// against the canonical enum. The bash predecessor lives at
// nix-config harness/check-task-status.sh and pins the same set as
// wt-task's VALID_STATUSES. Empty / missing status is allowed; only
// out-of-set values are flagged so legacy files that predate the
// field do not churn.
type TaskStatus struct {
	TasksDir string
	Allowed  []string
}

func (TaskStatus) Name() string { return "task-status" }

var defaultTaskStatuses = []string{"draft", "active", "blocked", "done"}

var taskStatusLine = regexp.MustCompile(`(?m)^status[[:space:]]*:`)

func (l TaskStatus) Run(root string) ([]Issue, error) {
	allowed := l.Allowed
	if len(allowed) == 0 {
		allowed = defaultTaskStatuses
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, s := range allowed {
		allowSet[s] = true
	}

	var issues []Issue
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		val := strings.TrimSpace(m.Status)
		if val == "" {
			return nil
		}
		if allowSet[val] {
			return nil
		}
		issues = append(issues, Issue{
			Path:    rel,
			Line:    statusLineNumber(raw),
			Message: fmt.Sprintf("invalid status %q (valid: %s)", val, strings.Join(allowed, " ")),
		})
		return nil
	})
	return issues, err
}

func statusLineNumber(raw []byte) int {
	lines := strings.Split(string(raw), "\n")
	fences := 0
	for i, ln := range lines {
		s := strings.TrimRight(ln, " \t\r")
		if s == "---" {
			fences++
			if fences >= 2 {
				return 0
			}
			continue
		}
		if fences == 1 && taskStatusLine.MatchString(s) {
			return i + 1
		}
	}
	return 0
}
