package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// PlanReadyMechanical implements the Stop-hook mechanical fix for the
// "worker wrote a ## Plan section but never told the coordinator" wedge.
// When tasks/<slug>.md has a Plan section and the coordinator inbox
// holds no `plan ready: <slug>` envelope yet, the hook drops one in.
// All decision misses (no file, no Plan, not active, tell already
// recorded) exit silently; only real I/O failures bubble out.
//
// slug names the task (from $SPORE_TASK_SLUG); worktree is the worker's
// CWD that contains tasks/<slug>.md; project keys the coordinator inbox
// (from $WT_PROJECT).
func PlanReadyMechanical(slug, worktree, project string) error {
	if slug == "" || worktree == "" || project == "" {
		return nil
	}

	taskFile := filepath.Join(worktree, "tasks", slug+".md")
	raw, err := os.ReadFile(taskFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("plan-ready-mechanical: read task: %w", err)
	}

	meta, body, err := frontmatter.Parse(raw)
	if err != nil {
		return nil
	}
	if meta.Status != "active" {
		return nil
	}
	if !HasPlanSection(body) {
		return nil
	}

	inbox := CoordinatorInbox(project)
	already, err := planReadyRecorded(inbox, slug)
	if err != nil {
		return fmt.Errorf("plan-ready-mechanical: scan inbox: %w", err)
	}
	if already {
		return nil
	}

	body_ := fmt.Sprintf(
		"plan ready: %s (auto-emitted by plan-ready-mechanical hook; worker wrote Plan but did not post)",
		slug,
	)
	return writeTellEnvelope(inbox, "plan-ready-mechanical", body_, "plan-ready")
}

// PlanReadyMechanicalEnv is the env-driven entry point for the Stop
// hook. Reads $SPORE_TASK_SLUG, $WT_PROJECT, and uses os.Getwd() for the
// worktree. Returns nil noop if any required input is missing so the
// hook stays silent in non-worker contexts.
func PlanReadyMechanicalEnv() error {
	slug := os.Getenv("SPORE_TASK_SLUG")
	project := os.Getenv("WT_PROJECT")
	if slug == "" || project == "" {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return PlanReadyMechanical(slug, wd, project)
}

// planReadyRecorded reports whether the coordinator's inbox already
// holds a `plan ready: <slug>` tell for this slug, either pending in
// the top-level inbox dir or claimed under read/. Match is on the body
// prefix so the auto-emitted suffix does not matter.
func planReadyRecorded(inbox, slug string) (bool, error) {
	prefix := "plan ready: " + slug
	for _, sub := range []string{"", "read"} {
		dir := filepath.Join(inbox, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			body, ok := ReadTellBody(filepath.Join(dir, e.Name()))
			if !ok {
				continue
			}
			if strings.HasPrefix(body, prefix) {
				return true, nil
			}
		}
	}
	return false, nil
}
