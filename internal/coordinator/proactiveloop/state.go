package proactiveloop

import (
	"bufio"
	"os"
	"strings"
)

// activeStateSlugs returns the slug column of every row under
// `## Active tasks` in state.md (excluding the header divider and
// header row).
func activeStateSlugs(stateFile string) []string {
	f, err := os.Open(stateFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []string
	in := false
	for scan.Scan() {
		line := scan.Text()
		if isStateHeader(line, "Active tasks") {
			in = true
			continue
		}
		if in && strings.HasPrefix(line, "## ") {
			break
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if isMarkdownTableDivider(line) {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		slug := strings.TrimSpace(parts[1])
		if slug == "" || slug == "slug" {
			continue
		}
		out = append(out, slug)
	}
	return out
}

func isMarkdownTableDivider(line string) bool {
	for _, r := range line {
		switch r {
		case '|', '-', ':', ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func isStateHeader(line, name string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "## ")) == name
}

// staleStateRows returns slug:status entries for any active-table row
// whose task is not local-active in tasks/.
func staleStateRows(stateFile string, tasks taskIndex) []string {
	var out []string
	for _, slug := range activeStateSlugs(stateFile) {
		if tasks.remoteHosts[slug] {
			continue
		}
		status, ok := tasks.statusBySlug[slug]
		if !ok {
			out = append(out, slug+":missing")
			continue
		}
		if status != "active" {
			out = append(out, slug+":"+status)
		}
	}
	return out
}
