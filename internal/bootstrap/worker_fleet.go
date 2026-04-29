package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// detectWorkerFleetReady runs an in-process smoke of the task data
// layer: allocate a slug, write a tasks/<slug>.md, parse it back,
// then remove it. This avoids the tmux + worktree side effects of
// `spore task start` so the gate is hermetic. The full
// new->start->done lifecycle is covered by bootstrap/smoke.sh.
func detectWorkerFleetReady(root string) (string, error) {
	if root == "" {
		return "", errors.New("worker-fleet-ready: empty root")
	}
	tasksDir := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return "", err
	}
	base := task.Slugify("spore-bootstrap-smoke")
	slug, err := task.Allocate(tasksDir, base)
	if err != nil {
		return "", err
	}
	path := filepath.Join(tasksDir, slug+".md")
	defer os.Remove(path)

	m := frontmatter.Meta{
		Status:  "draft",
		Slug:    slug,
		Title:   "spore bootstrap smoke",
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := os.WriteFile(path, frontmatter.Write(m, []byte("\nsmoke check.\n")), 0o644); err != nil {
		return "", err
	}
	got, err := task.List(tasksDir)
	if err != nil {
		return "", err
	}
	found := false
	for _, mm := range got {
		if mm.Slug == slug && mm.Status == "draft" {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("smoke task %q absent from `task.List`", slug)
	}
	return "task data layer round-tripped (" + slug + ")", nil
}
