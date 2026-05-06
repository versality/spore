package lints

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeDistribute scans a markdown source body and flags top-level
// `# ` sections whose backticked path tokens concentrate under one
// top-level repo subdir. The heuristic mirrors ClaudeSubdir but runs
// on the source SIDE (one composer body, before render) instead of
// the rendered SIDE (every CLAUDE.md in the tree). It is intended to
// surface sections that should carry a `<!-- homePath: <subdir> -->`
// marker so the composer renders them into `<subdir>/CLAUDE.md`
// instead of root.
//
// Excludes lists top-level segment names that never host a subdir
// CLAUDE.md (typically `docs`, `templates`); sections concentrated on
// these are cross-cutting and stay at root.
//
// A section is skipped when its body already carries a
// `<!-- homePath: ... -->` or `<!-- lint: scope-ok -->` marker.
type ClaudeDistribute struct {
	Source       string
	MinPaths     int
	FloorPercent int
	Excludes     []string
}

func (ClaudeDistribute) Name() string { return "claude-distribute" }

// Candidate is one section that the heuristic flags. Line is
// 1-indexed and points at the section's `# ` heading.
type Candidate struct {
	Line    int
	Name    string
	Subdir  string
	Count   int
	Total   int
	Percent int
}

func (l ClaudeDistribute) Run(root string) ([]Issue, error) {
	if l.Source == "" {
		return nil, fmt.Errorf("claude-distribute: Source is required")
	}
	cands, err := l.Scan(root)
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(cands))
	for _, c := range cands {
		issues = append(issues, Issue{
			Path: l.Source,
			Line: c.Line,
			Message: fmt.Sprintf(
				"section %q -> homePath: %s (%d/%d, %d%%)",
				c.Name, c.Subdir, c.Count, c.Total, c.Percent,
			),
		})
	}
	return issues, nil
}

// Scan returns the raw candidate set (used by the apply path so the
// caller can mutate the source file with the same data).
func (l ClaudeDistribute) Scan(root string) ([]Candidate, error) {
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

	excluded := map[string]bool{}
	for _, e := range l.Excludes {
		excluded[e] = true
	}

	srcPath := filepath.Join(root, l.Source)
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("claude-distribute: open %s: %w", l.Source, err)
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

	var out []Candidate
	var cur *section

	flush := func() {
		if cur == nil || cur.hasMarker {
			return
		}
		counts := map[string]int{}
		total := 0
		for p := range cur.paths {
			seg := p
			if i := strings.IndexByte(p, '/'); i > 0 {
				seg = p[:i]
			}
			if !topDirs[seg] {
				continue
			}
			counts[seg]++
			total++
		}
		if total < minPaths {
			return
		}
		bestSeg := ""
		bestCount := 0
		for s, c := range counts {
			if excluded[s] {
				continue
			}
			if c > bestCount {
				bestSeg = s
				bestCount = c
			}
		}
		if bestSeg == "" {
			return
		}
		pct := bestCount * 100 / total
		if pct < floorPct {
			return
		}
		out = append(out, Candidate{
			Line:    cur.startLine,
			Name:    cur.name,
			Subdir:  bestSeg,
			Count:   bestCount,
			Total:   total,
			Percent: pct,
		})
	}

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(line, "# ") {
			flush()
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
		if strings.Contains(line, "<!-- homePath:") {
			cur.hasMarker = true
		}
		if strings.Contains(line, "<!-- lint: scope-ok -->") {
			cur.hasMarker = true
		}
		for _, m := range reBacktickPath.FindAllStringSubmatch(line, -1) {
			tok := m[1]
			if !strings.Contains(tok, "/") {
				continue
			}
			if !looksLikePathToken(tok) {
				continue
			}
			cur.paths[tok] = true
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claude-distribute: scan %s: %w", l.Source, err)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, nil
}

// looksLikePathToken matches the bash `^[a-zA-Z][a-zA-Z0-9_.-]*\/`
// guard: token must start with an ASCII letter, then path-safe
// characters, then a slash. Filters out absolute paths, URLs, and
// regex fragments that happen to contain a slash.
func looksLikePathToken(tok string) bool {
	if tok == "" {
		return false
	}
	c := tok[0]
	if !isASCIILetter(c) {
		return false
	}
	for i := 1; i < len(tok); i++ {
		ch := tok[i]
		if ch == '/' {
			return true
		}
		if !isASCIILetter(ch) && !isASCIIDigit(ch) && ch != '_' && ch != '.' && ch != '-' {
			return false
		}
	}
	return false
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isASCIIDigit(c byte) bool  { return c >= '0' && c <= '9' }

// ApplyMarkers inserts a `<!-- homePath: <subdir> -->` line directly
// after each candidate section's heading. Mutates the source file in
// place. Returns the candidate set actually applied (skipping any
// section whose body somehow already carries a marker, since Scan
// would not have flagged it). Idempotent: a second call with the
// same input is a no-op because Scan skips marked sections.
func (l ClaudeDistribute) ApplyMarkers(root string, cands []Candidate) error {
	if len(cands) == 0 {
		return nil
	}
	srcPath := filepath.Join(root, l.Source)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("claude-distribute: read %s: %w", l.Source, err)
	}
	lines := strings.Split(string(data), "\n")

	byLine := map[int]Candidate{}
	for _, c := range cands {
		byLine[c.Line] = c
	}

	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		if c, ok := byLine[i+1]; ok {
			b.WriteString("\n\n<!-- homePath: ")
			b.WriteString(c.Subdir)
			b.WriteString(" -->")
		}
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return os.WriteFile(srcPath, []byte(b.String()), 0o644)
}
