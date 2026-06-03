package lints

import (
	"fmt"
	"os"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// TaskPriority enforces a `priority:` line on every non-done task file
// under tasks/. Values must be one of task.Priorities. The lint is
// warn-only during the soak window: callers gate exit code on
// PriorityWarnOnly().
type TaskPriority struct {
	TasksDir string
}

func (TaskPriority) Name() string { return "task-priority" }

func (l TaskPriority) Run(root string) ([]Issue, error) {
	var issues []Issue
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		if task.IsDone(m.Status) {
			return nil
		}
		if m.Priority == "" {
			issues = append(issues, Issue{
				Path:    rel,
				Message: "missing 'priority:' (want critical|high|medium|low)",
			})
			return nil
		}
		if !task.IsValidPriority(m.Priority) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("priority %q: want critical|high|medium|low", m.Priority),
			})
		}
		return nil
	})
	return issues, err
}

// PriorityWarnOnly reports whether task-priority findings should not
// fail the lint runner. The first deployment ships warn-only so a fresh
// repo without backfilled priorities does not break `just check`.
func PriorityWarnOnly() bool {
	if v := os.Getenv("SPORE_PRIORITY_WARN_ONLY"); v == "0" {
		return false
	}
	return true
}
