package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// renderTaskLS formats the wt-task-ls section body, diffing against
// the prior boot's snapshot at $STATE_DIR/last-boot-tasks.txt. It
// always writes a fresh snapshot from the current run, even when the
// diff result is "unchanged".
func renderTaskLS(cfg Config, rc int, raw string) string {
	if raw == "" {
		return "(no output)\n"
	}
	if rc != 0 {
		return raw + tailNewline(raw)
	}

	current := parseTaskLS(raw)
	snapshotPath := filepath.Join(cfg.StateDir, "last-boot-tasks.txt")
	prev, prevExists := readSnapshot(snapshotPath)

	writeSnapshot(snapshotPath, current)

	if !prevExists {
		return fmt.Sprintf("(first boot, snapshot established; %d tasks)\n%s",
			len(current), raw+tailNewline(raw))
	}

	added, removed, changed := diffTasks(prev, current)
	if len(added) == 0 && len(removed) == 0 && len(changed) == 0 {
		return fmt.Sprintf("unchanged from last boot (%d tasks)\n", len(current))
	}

	var b strings.Builder
	if len(added) > 0 {
		b.WriteString("added:\n")
		curMap := tasksToMap(current)
		for _, slug := range added {
			fmt.Fprintf(&b, "  %s\t%s\n", slug, curMap[slug])
		}
	}
	if len(removed) > 0 {
		b.WriteString("removed:\n")
		prevMap := tasksToMap(prev)
		for _, slug := range removed {
			fmt.Fprintf(&b, "  %s\t(was %s)\n", slug, prevMap[slug])
		}
	}
	if len(changed) > 0 {
		b.WriteString("status changed:\n")
		for _, ch := range changed {
			fmt.Fprintf(&b, "  %s\t%s -> %s\n", ch.Slug, ch.Old, ch.New)
		}
	}
	return b.String()
}

// taskRow holds one slug+status pair from `wt task ls`. Order in the
// parsed slice is the natural sort order (LC_ALL=C) so the snapshot
// file is byte-stable across runs.
type taskRow struct{ Slug, Status string }

type taskChange struct{ Slug, Old, New string }

func parseTaskLS(raw string) []taskRow {
	rows := []taskRow{}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if i == 0 {
			continue // header row
		}
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		rows = append(rows, taskRow{Slug: fields[0], Status: fields[1]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })
	return rows
}

func tasksToMap(rows []taskRow) map[string]string {
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Slug] = r.Status
	}
	return m
}

func diffTasks(prev, cur []taskRow) (added, removed []string, changed []taskChange) {
	prevMap := tasksToMap(prev)
	curMap := tasksToMap(cur)

	for slug := range curMap {
		if _, ok := prevMap[slug]; !ok {
			added = append(added, slug)
		}
	}
	for slug := range prevMap {
		if _, ok := curMap[slug]; !ok {
			removed = append(removed, slug)
		}
	}
	for slug, old := range prevMap {
		if nw, ok := curMap[slug]; ok && old != nw {
			changed = append(changed, taskChange{Slug: slug, Old: old, New: nw})
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Slice(changed, func(i, j int) bool { return changed[i].Slug < changed[j].Slug })
	return
}

func readSnapshot(path string) ([]taskRow, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	rows := []taskRow{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, taskRow{Slug: fields[0], Status: fields[1]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })
	return rows, true
}

// writeSnapshot writes via tmp+rename so a crashed boot never leaves a
// partial snapshot that future diffs would treat as truth.
func writeSnapshot(path string, rows []taskRow) {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\n", r.Slug, r.Status)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func tailNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return ""
	}
	return "\n"
}
