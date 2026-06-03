package lints

import (
	"path/filepath"
	"regexp"
)

// TodoPriority enforces a `**Priority**:` line on every non-done file
// under docs/todo/ (skipping README.md). Convention defined per
// project; the lint shape is portable.
type TodoPriority struct {
	TodoDir string
}

func (TodoPriority) Name() string { return "todo-priority" }

var (
	todoStatusDoneRE = regexp.MustCompile(`(?im)^(?:\*\*Status\*\*|## Status[^\w]|## Status:)[^\w]*[^\w]done\b`)
	todoPriorityRE   = regexp.MustCompile(`(?m)^\*\*Priority\*\*:[ \t]*(critical|queued|maybe)\b`)
)

func (l TodoPriority) Run(root string) ([]Issue, error) {
	dir := l.TodoDir
	if dir == "" {
		dir = "docs/todo"
	}
	var issues []Issue
	err := forEachTask(root, dir, func(rel string, raw []byte) error {
		if filepath.Base(rel) == "README.md" {
			return nil
		}
		if todoStatusDoneRE.Match(raw) {
			return nil
		}
		if !todoPriorityRE.Match(raw) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: "missing '**Priority**: critical|queued|maybe' line",
			})
		}
		return nil
	})
	return issues, err
}
