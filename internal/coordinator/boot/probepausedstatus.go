package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/coordinator/workerwatch"
	"github.com/versality/spore/internal/task/frontmatter"
)

// probePausedStatusFunc lets tests inject the project scan. Default is
// the real workerwatch reader.
var probePausedStatusFunc = probePausedStatusScan

// probePausedStatus reports any tasks/*.md across configured project
// roots that still carry `status: paused`. The status was retired in
// favor of parked/blocked; lingering files trip wt-reconcile's
// frontmatter validator. Emits a per-file list when non-empty, silent
// otherwise.
func probePausedStatus(_ Config) (int, string) {
	hits, err := probePausedStatusFunc()
	if err != nil {
		return 2, fmt.Sprintf("failed: paused-status scan: %v\n", err)
	}
	if len(hits) == 0 {
		return 0, ""
	}
	sort.Strings(hits)
	var b strings.Builder
	fmt.Fprintf(&b, "paused-status files (run 'spore task migrate-status paused blocked' per project):\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "  - %s\n", h)
	}
	return 2, b.String()
}

func probePausedStatusScan() ([]string, error) {
	projects, err := workerwatch.ResolveProjectRoots(workerwatch.DefaultProjectsFile())
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, root := range projects {
		dir := filepath.Join(root, "tasks")
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			m, _, err := frontmatter.Parse(raw)
			if err != nil {
				continue
			}
			if strings.TrimSpace(m.Status) == "paused" {
				hits = append(hits, path)
			}
		}
	}
	return hits, nil
}
