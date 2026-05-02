package lints

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeSize flags top-level sections in CLAUDE.md files that exceed a
// line threshold. A "top-level section" runs from one `# ` header to
// the next; sub-headers (`## `, `### `) are internal structure and do
// not split. Blank lines count.
//
// Opt out by placing `<!-- lint: size-ok -->` inside the section body.
type ClaudeSize struct {
	Limit int
}

func (ClaudeSize) Name() string { return "claude-size" }

func (l ClaudeSize) Run(root string) ([]Issue, error) {
	limit := l.Limit
	if limit <= 0 {
		limit = 40
	}
	files, err := listFiles(root, map[string]bool{"CLAUDE.md": true})
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, rel := range files {
		if !strings.HasSuffix(rel, "CLAUDE.md") {
			continue
		}
		path := filepath.Join(root, rel)
		found, err := scanClaudeSize(path, rel, limit)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		issues = append(issues, found...)
	}
	return issues, nil
}

const claudeSizeMarker = "<!-- lint: size-ok -->"

func scanClaudeSize(path, rel string, limit int) ([]Issue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var issues []Issue
	var sectionName string
	var sectionStart int
	var sectionLines int
	var hasMarker bool

	emit := func() {
		if sectionName != "" && sectionLines > limit && !hasMarker {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    sectionStart,
				Message: fmt.Sprintf("section %q has %d lines (limit %d)", sectionName, sectionLines, limit),
			})
		}
	}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(line, "# ") {
			emit()
			sectionName = strings.TrimPrefix(line, "# ")
			sectionStart = lineNo
			sectionLines = 0
			hasMarker = false
			continue
		}
		sectionLines++
		if strings.Contains(line, claudeSizeMarker) {
			hasMarker = true
		}
	}
	emit()
	return issues, scanner.Err()
}
