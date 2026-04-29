package lints

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// FileSize flags any tracked source file with more than Limit lines.
// Long files cap an LLM agent's ability to orient and tend to grow
// unboundedly once past a threshold; split into focused modules.
type FileSize struct {
	Limit int
}

func (FileSize) Name() string { return "filesize" }

func (l FileSize) Run(root string) ([]Issue, error) {
	limit := l.Limit
	if limit <= 0 {
		limit = 500
	}
	files, err := listFiles(root, sourceExts)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		if isGenerated(rel) {
			continue
		}
		path := filepath.Join(root, rel)
		n, err := countLines(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if n > limit {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("%d lines exceeds %d-line limit; split into focused modules", n, limit),
			})
		}
	}
	return issues, nil
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	n := 0
	for scanner.Scan() {
		n++
	}
	return n, scanner.Err()
}
