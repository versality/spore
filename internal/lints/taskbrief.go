package lints

import (
	"os"
	"path/filepath"
	"strings"
)

// TaskBrief rejects a `# Brief` H1 heading in tasks/<slug>.md bodies.
// wt-task prepends `Brief:` to the worker prompt; an explicit `# Brief`
// heading is a no-op anti-pattern. Match is case-insensitive and
// applies to any H1 in the body; H2 and deeper (`## Brief`) are fine.
type TaskBrief struct {
	TasksDir string
}

func (TaskBrief) Name() string { return "task-brief" }

func (l TaskBrief) Run(root string) ([]Issue, error) {
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
		if lineNo, ok := briefH1Line(raw); ok {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    lineNo,
				Message: "'# Brief' heading (drop it; wt-task already prepends 'Brief:')",
			})
		}
	}
	return issues, nil
}

// briefH1Line returns the 1-indexed line number of the first body H1
// whose canonical text is "brief" (case-insensitive). The body starts
// after the closing `---` frontmatter fence; a file without closed
// frontmatter is treated as all-body.
func briefH1Line(raw []byte) (int, bool) {
	lines := strings.Split(string(raw), "\n")
	start := 0
	if len(lines) > 0 && strings.TrimRight(lines[0], " \t") == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimRight(lines[i], " \t") == "---" {
				start = i + 1
				break
			}
		}
	}
	for j := start; j < len(lines); j++ {
		body := strings.TrimRight(lines[j], " \t\r")
		if !strings.HasPrefix(body, "# ") {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(body, "#")))
		if text == "brief" {
			return j + 1, true
		}
	}
	return 0, false
}
