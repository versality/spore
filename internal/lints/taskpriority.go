package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if task.IsDone(m.Status) {
			continue
		}
		if m.Priority == "" {
			issues = append(issues, Issue{
				Path:    rel,
				Message: "missing 'priority:' (want critical|high|medium|low)",
			})
			continue
		}
		if !task.IsValidPriority(m.Priority) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("priority %q: want critical|high|medium|low", m.Priority),
			})
		}
	}
	return issues, nil
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
