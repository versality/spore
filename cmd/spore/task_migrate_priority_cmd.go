package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// runTaskMigratePriority backfills `priority:` on tasks/*.md files that
// predate the field. Heuristic:
//
//   - already valid: skip.
//   - already present but invalid: error, operator fixes by hand.
//   - missing on active/paused/draft (canonical: active + backlog with
//     these legacy substatuses): medium.
//   - missing on parked: low.
//   - missing on blocked: skipped, listed under "needs operator priority"
//     so the operator can edit by hand. The lint will keep flagging
//     until they do.
//   - missing on done: skipped (priority is a promote-order signal).
//
// --dry-run prints the plan without touching files. Without a flag the
// migration writes in place.
func runTaskMigratePriority(args []string) error {
	fs := flag.NewFlagSet("task migrate-priority", flag.ContinueOnError)
	dir := fs.String("dir", "tasks", "tasks directory to migrate")
	dry := fs.Bool("dry-run", false, "print planned changes, do not write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: spore task migrate-priority [--dir tasks] [--dry-run]")
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var blocked []string
	var changed, skipped int
	for _, name := range names {
		path := filepath.Join(*dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate-priority: skip %s (parse: %v)\n", path, err)
			continue
		}
		if m.Priority != "" {
			if !task.IsValidPriority(m.Priority) {
				return fmt.Errorf("%s: priority %q invalid (want critical|high|medium|low); fix by hand", path, m.Priority)
			}
			skipped++
			continue
		}
		assigned := defaultPriorityFor(m.Status)
		if assigned == "" {
			if m.Status == "blocked" {
				blocked = append(blocked, m.Slug)
			}
			skipped++
			continue
		}
		m.Priority = assigned
		out := frontmatter.Write(m, body)
		fmt.Printf("%s -> %s (status=%s)\n", path, assigned, m.Status)
		if !*dry {
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return err
			}
		}
		changed++
	}
	fmt.Fprintf(os.Stderr, "migrate-priority: changed=%d skipped=%d dry-run=%v\n", changed, skipped, *dry)
	if len(blocked) > 0 {
		fmt.Fprintf(os.Stderr, "migrate-priority: blocked tasks need operator priority (edit by hand):\n")
		for _, slug := range blocked {
			fmt.Fprintf(os.Stderr, "  - %s\n", slug)
		}
	}
	return nil
}

// defaultPriorityFor returns the backfill priority for a task with the
// given legacy or canonical status. Returns "" when the task should be
// left untouched (done, blocked, or unknown).
func defaultPriorityFor(status string) string {
	switch status {
	case "active", "paused", "draft", "backlog":
		return task.PriorityMedium
	case "parked":
		return task.PriorityLow
	default:
		return ""
	}
}
