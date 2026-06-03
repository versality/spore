package lints

import (
	"fmt"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// CodexEffortHighOnly rejects tasks/<slug>.md when `agent: codex` is
// paired with `effort:` set to anything other than `high`. Empty
// effort is fine - the launcher defaults to high. Status filter:
// fires for draft|active|blocked. done is historical (no backfill).
type CodexEffortHighOnly struct {
	TasksDir string
}

func (CodexEffortHighOnly) Name() string { return "codex-effort-high-only" }

func (l CodexEffortHighOnly) Run(root string) ([]Issue, error) {
	var issues []Issue
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			return nil
		}
		if m.Agent != "codex" {
			return nil
		}
		effort := strings.TrimSpace(m.Extra["effort"])
		if effort == "" || effort == "high" {
			return nil
		}
		switch m.Status {
		case "draft", "active", "blocked":
		default:
			return nil
		}
		issues = append(issues, Issue{
			Path:    rel,
			Message: fmt.Sprintf("agent=codex effort=%s status=%s (must be exactly 'high')", effort, m.Status),
		})
		return nil
	})
	return issues, err
}
