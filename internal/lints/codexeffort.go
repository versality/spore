package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// CodexEffortHighOnly rejects tasks/<slug>.md when `agent: codex` is
// paired with `effort:` set to anything other than `high`. Empty
// effort is fine - the launcher defaults to high. Status filter:
// fires for draft|active|paused|blocked|parked. done is historical
// (no backfill).
type CodexEffortHighOnly struct {
	TasksDir string
}

func (CodexEffortHighOnly) Name() string { return "codex-effort-high-only" }

func (l CodexEffortHighOnly) Run(root string) ([]Issue, error) {
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
		if m.Agent != "codex" {
			continue
		}
		effort := strings.TrimSpace(m.Extra["effort"])
		if effort == "" || effort == "high" {
			continue
		}
		switch m.Status {
		case "draft", "active", "paused", "blocked", "parked":
		default:
			continue
		}
		issues = append(issues, Issue{
			Path:    rel,
			Message: fmt.Sprintf("agent=codex effort=%s status=%s (must be exactly 'high')", effort, m.Status),
		})
	}
	return issues, nil
}
