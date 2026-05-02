package lints

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ClaudeSubdir flags top-level sections in CLAUDE.md that are
// dominated by path references to a single subdir which already owns
// its own CLAUDE.md. The heuristic: collect backticked path tokens
// that begin with a known top-level repo directory, resolve each to
// the deepest CLAUDE.md-bearing ancestor ("scope"), and flag when the
// dominant scope is not the file's own scope and captures >=
// FloorPercent of unique paths (minimum MinPaths distinct paths).
//
// Opt out with `<!-- lint: scope-ok -->` inside the section body.
type ClaudeSubdir struct {
	MinPaths     int
	FloorPercent int
}

func (ClaudeSubdir) Name() string { return "claude-subdir" }

func (l ClaudeSubdir) Run(root string) ([]Issue, error) {
	minPaths := l.MinPaths
	if minPaths <= 0 {
		minPaths = 3
	}
	floorPct := l.FloorPercent
	if floorPct <= 0 {
		floorPct = 60
	}

	allFiles, err := listFiles(root, nil)
	if err != nil {
		return nil, err
	}

	topDirs := topLevelDirs(allFiles)
	claudeFiles := filterClaude(allFiles)
	scopeDirs := buildScopeDirs(claudeFiles)

	var issues []Issue
	for _, rel := range claudeFiles {
		ownScope := filepath.Dir(rel)
		if ownScope == "." {
			ownScope = ""
		}
		path := filepath.Join(root, rel)
		found, err := scanClaudeSubdir(path, rel, ownScope, topDirs, scopeDirs, minPaths, floorPct)
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

func topLevelDirs(files []string) map[string]bool {
	dirs := map[string]bool{}
	for _, f := range files {
		if i := strings.IndexByte(f, '/'); i > 0 {
			dirs[f[:i]] = true
		}
	}
	return dirs
}

func filterClaude(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "CLAUDE.md") {
			out = append(out, f)
		}
	}
	return out
}

func buildScopeDirs(claudeFiles []string) []string {
	var dirs []string
	for _, f := range claudeFiles {
		d := filepath.Dir(f)
		if d == "." {
			d = ""
		}
		dirs = append(dirs, d)
	}
	return dirs
}

func resolveScope(path string, scopeDirs []string) string {
	best := ""
	for _, scope := range scopeDirs {
		if scope == "" {
			continue
		}
		if strings.HasPrefix(path, scope+"/") || path == scope {
			if len(scope) > len(best) {
				best = scope
			}
		}
	}
	return best
}

const claudeSubdirMarker = "<!-- lint: scope-ok -->"

var reBacktickPath = regexp.MustCompile("`([^`]*)`")

func scanClaudeSubdir(path, rel, ownScope string, topDirs map[string]bool, scopeDirs []string, minPaths, floorPct int) ([]Issue, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	type section struct {
		name      string
		startLine int
		hasMarker bool
		paths     map[string]bool
	}

	var issues []Issue
	var cur *section

	emitSection := func() {
		if cur == nil || cur.hasMarker {
			return
		}
		scopeCounts := map[string]int{}
		total := 0
		for p := range cur.paths {
			first := p
			if i := strings.IndexByte(p, '/'); i > 0 {
				first = p[:i]
			}
			if !topDirs[first] {
				continue
			}
			s := resolveScope(p, scopeDirs)
			if s == "" {
				continue
			}
			scopeCounts[s]++
			total++
		}
		if total < minPaths {
			return
		}
		bestScope := ""
		bestCount := 0
		for s, c := range scopeCounts {
			if c > bestCount {
				bestScope = s
				bestCount = c
			}
		}
		if bestScope == "" || bestScope == ownScope {
			return
		}
		pct := bestCount * 100 / total
		if pct >= floorPct {
			issues = append(issues, Issue{
				Path: rel,
				Line: cur.startLine,
				Message: fmt.Sprintf(
					"section %q has %d/%d (%d%%) paths under %s/ - consider moving to %s/CLAUDE.md",
					cur.name, bestCount, total, pct, bestScope, bestScope,
				),
			})
		}
	}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(line, "# ") {
			emitSection()
			cur = &section{
				name:      strings.TrimPrefix(line, "# "),
				startLine: lineNo,
				paths:     map[string]bool{},
			}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.Contains(line, claudeSubdirMarker) {
			cur.hasMarker = true
		}
		for _, m := range reBacktickPath.FindAllStringSubmatch(line, -1) {
			tok := m[1]
			if !strings.Contains(tok, "/") {
				continue
			}
			if i := strings.IndexByte(tok, '/'); i > 0 {
				first := tok[:i]
				if topDirs[first] {
					cur.paths[tok] = true
				}
			}
		}
	}
	emitSection()
	return issues, scanner.Err()
}
