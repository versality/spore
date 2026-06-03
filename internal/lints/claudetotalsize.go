package lints

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeTotalSize flags whole-file CLAUDE.md / AGENTS.md that exceed
// total-size caps. The root file (CLAUDE.md or AGENTS.md at the repo
// root) is capped on lines and chars; whichever cap is hit first
// triggers the issue. Subdirectory mirrors get a smaller line cap and
// no char cap; their job is single-area pointer notes, not narrative.
//
// Opt out by placing `<!-- lint: totalsize-ok -->` anywhere in the file.
type ClaudeTotalSize struct {
	RootLineLimit   int
	RootCharLimit   int
	SubdirLineLimit int
}

const (
	defaultRootLineLimit   = 400
	defaultRootCharLimit   = 40000
	defaultSubdirLineLimit = 150
	claudeTotalSizeMarker  = "<!-- lint: totalsize-ok -->"
)

func (ClaudeTotalSize) Name() string { return "claude-totalsize" }

func (l ClaudeTotalSize) Run(root string) ([]Issue, error) {
	rootLines := l.RootLineLimit
	if rootLines <= 0 {
		rootLines = defaultRootLineLimit
	}
	rootChars := l.RootCharLimit
	if rootChars <= 0 {
		rootChars = defaultRootCharLimit
	}
	subdirLines := l.SubdirLineLimit
	if subdirLines <= 0 {
		subdirLines = defaultSubdirLineLimit
	}

	files, err := listFiles(root, map[string]bool{
		"CLAUDE.md": true,
		"AGENTS.md": true,
	})
	if err != nil {
		return nil, err
	}

	var issues []Issue
	for _, rel := range files {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if bytes.Contains(data, []byte(claudeTotalSizeMarker)) {
			continue
		}
		lines := countTotalLines(data)
		chars := len(data)

		if rel == "CLAUDE.md" || rel == "AGENTS.md" {
			if lines > rootLines || chars > rootChars {
				issues = append(issues, Issue{
					Path:    rel,
					Message: fmt.Sprintf("file is %d lines / %d chars (limit: %d lines, %d chars)", lines, chars, rootLines, rootChars),
				})
			}
			continue
		}
		if lines > subdirLines {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("file is %d lines (limit: %d lines)", lines, subdirLines),
			})
		}
	}
	return issues, nil
}

func countTotalLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
