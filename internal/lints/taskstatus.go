package lints

import (
	"fmt"
	"os"
	"path/filepath"
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

var defaultTaskStatuses = []string{"parked", "active", "blocked", "done"}

var taskStatusLine = regexp.MustCompile(`(?m)^status[[:space:]]*:`)

func (l TaskStatus) Run(root string) ([]Issue, error) {
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

	allowed := l.Allowed
	if len(allowed) == 0 {
		allowed = defaultTaskStatuses
	}
	allowSet := make(map[string]bool, len(allowed))
	for _, s := range allowed {
		allowSet[s] = true
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
		val := strings.TrimSpace(m.Status)
		if val == "" {
			continue
		}
		if allowSet[val] {
			continue
		}
		issues = append(issues, Issue{
			Path:    rel,
			Line:    statusLineNumber(raw),
			Message: fmt.Sprintf("invalid status %q (valid: %s)", val, strings.Join(allowed, " ")),
		})
	}
	return issues, nil
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
