package lints

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// forEachTask walks the task directory under root and invokes fn once
// per `.md` file, in filename order. tasksDir defaults to "tasks" when
// empty. A missing directory is not an error: fn is never called and
// nil is returned. Directories and non-`.md` entries are skipped.
//
// fn receives the repo-relative slash path and the raw file bytes; it
// owns frontmatter parsing and any per-lint skip logic so each lint
// keeps its exact predicate. A non-nil fn return aborts the walk.
func forEachTask(root, tasksDir string, fn func(rel string, raw []byte) error) error {
	dir := tasksDir
	if dir == "" {
		dir = "tasks"
	}
	abs := filepath.Join(root, dir)
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil
	}
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
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(abs, name))
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(filepath.Join(dir, name))
		if err := fn(rel, raw); err != nil {
			return err
		}
	}
	return nil
}
