package lints

import (
	"os"
	"path/filepath"
	"strings"
)

// TaskBrief rejects a leading `# Brief` heading in tasks/<slug>.md
// bodies. wt-task prepends `Brief:` to the worker prompt; a leading
// `# Brief` heading dups it.
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
		if line, ok := firstBodyLine(raw); ok && line.text == "# Brief" {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    line.lineNo,
				Message: "leading '# Brief' heading (drop it; wt-task already prepends 'Brief:')",
			})
		}
	}
	return issues, nil
}

type bodyLine struct {
	text   string
	lineNo int
}

// firstBodyLine returns the first non-blank line after the closing
// `---` frontmatter fence, plus its 1-indexed line number. ok is
// false when the file has no closed frontmatter or no body content.
func firstBodyLine(raw []byte) (bodyLine, bool) {
	lines := strings.Split(string(raw), "\n")
	fm := 0
	for i, ln := range lines {
		t := strings.TrimRight(ln, " \t")
		if t == "---" {
			fm++
			if fm >= 2 {
				for j := i + 1; j < len(lines); j++ {
					body := strings.TrimRight(lines[j], " \t\r")
					if strings.TrimSpace(body) == "" {
						continue
					}
					return bodyLine{text: body, lineNo: j + 1}, true
				}
				return bodyLine{}, false
			}
		}
	}
	return bodyLine{}, false
}
