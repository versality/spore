package lints

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "README.md" {
			continue
		}
		path := filepath.Join(abs, e.Name())
		rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if todoStatusDoneRE.Match(raw) {
			continue
		}
		if !todoPriorityRE.Match(raw) {
			issues = append(issues, Issue{
				Path:    rel,
				Message: "missing '**Priority**: critical|queued|maybe' line",
			})
		}
	}
	return issues, nil
}
