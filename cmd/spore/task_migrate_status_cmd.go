package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/task/frontmatter"
)

// runTaskMigrateStatus rewrites the `status:` field of every tasks/*.md
// whose current value matches --from to --to, round-tripping through
// the frontmatter package so unrelated fields keep their formatting.
// Used to drain a now-legacy status (e.g. paused -> blocked) without
// hand-editing every file. --dry-run prints the plan without writing.
func runTaskMigrateStatus(args []string) error {
	fs := flag.NewFlagSet("task migrate-status", flag.ContinueOnError)
	dir := fs.String("dir", "tasks", "tasks directory to migrate")
	dry := fs.Bool("dry-run", false, "print planned changes, do not write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: spore task migrate-status [--dir tasks] [--dry-run] <from> <to>")
	}
	from := fs.Arg(0)
	to := fs.Arg(1)
	if from == "" || to == "" {
		return fmt.Errorf("spore task migrate-status: <from> and <to> must be non-empty")
	}
	if from == to {
		return fmt.Errorf("spore task migrate-status: <from> and <to> are identical (%q)", from)
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

	var changed int
	for _, name := range names {
		path := filepath.Join(*dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "migrate-status: skip %s (parse: %v)\n", path, err)
			continue
		}
		if strings.TrimSpace(m.Status) != from {
			continue
		}
		m.Status = to
		out := frontmatter.Write(m, body)
		fmt.Printf("%s: %s -> %s\n", path, from, to)
		if !*dry {
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return err
			}
		}
		changed++
	}
	fmt.Fprintf(os.Stderr, "migrate-status: changed=%d from=%s to=%s dry-run=%v\n", changed, from, to, *dry)
	return nil
}
