// Package state parses and writes the coordinator's state.md file.
// The format is a markdown document with H2/H3 sections, an active
// tasks table, recent events, rules, and directives. The coordinator
// reads state.md on boot and writes it before cycling context.
package state

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

type Doc struct {
	Sections []Section
}

type Section struct {
	Level    int
	Heading  string
	Body     string
	Children []Section
}

type TaskRow struct {
	Slug   string
	Status string
	Note   string
}

type Event struct {
	Time    time.Time
	Kind    string
	Message string
}

// Parse reads a state.md document into a Doc. Sections are identified
// by H2 (##) and H3 (###) headings. H3 sections nest under the
// preceding H2. Anything before the first heading is stored as a
// level-0 section with an empty heading.
func Parse(content []byte) Doc {
	if len(bytes.TrimSpace(content)) == 0 {
		return Doc{}
	}
	lines := strings.Split(string(content), "\n")
	var doc Doc
	var cur *Section
	var body strings.Builder
	hadContent := false

	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimRight(body.String(), "\n")
			body.Reset()
		}
	}

	for _, line := range lines {
		level, heading := parseHeading(line)
		if level == 0 {
			if cur == nil {
				if line == "" && !hadContent {
					continue
				}
				hadContent = true
				cur = &Section{Level: 0}
				doc.Sections = append(doc.Sections, *cur)
				cur = &doc.Sections[len(doc.Sections)-1]
			}
			body.WriteString(line)
			body.WriteByte('\n')
			continue
		}

		flush()

		sec := Section{Level: level, Heading: heading}
		if level == 3 && len(doc.Sections) > 0 {
			parent := &doc.Sections[len(doc.Sections)-1]
			if parent.Level == 2 {
				parent.Children = append(parent.Children, sec)
				cur = &parent.Children[len(parent.Children)-1]
				body.Reset()
				continue
			}
		}
		doc.Sections = append(doc.Sections, sec)
		cur = &doc.Sections[len(doc.Sections)-1]
		body.Reset()
	}
	flush()

	return doc
}

func parseHeading(line string) (level int, heading string) {
	trimmed := strings.TrimRight(line, " \t")
	if strings.HasPrefix(trimmed, "### ") {
		return 3, strings.TrimSpace(trimmed[4:])
	}
	if strings.HasPrefix(trimmed, "## ") {
		return 2, strings.TrimSpace(trimmed[3:])
	}
	return 0, ""
}

// Write serialises a Doc back to bytes.
func Write(doc Doc) []byte {
	var buf bytes.Buffer
	for i, sec := range doc.Sections {
		writeSection(&buf, sec, i > 0)
	}
	return buf.Bytes()
}

func writeSection(buf *bytes.Buffer, sec Section, needsGap bool) {
	if sec.Level > 0 {
		if needsGap {
			buf.WriteByte('\n')
		}
		fmt.Fprintf(buf, "%s %s\n", strings.Repeat("#", sec.Level), sec.Heading)
	}
	if sec.Body != "" {
		buf.WriteString(sec.Body)
		buf.WriteByte('\n')
	}
	for _, child := range sec.Children {
		writeSection(buf, child, true)
	}
}

// FindSection returns the first section with a heading matching name
// (case-insensitive). Returns nil if not found.
func (d *Doc) FindSection(name string) *Section {
	lower := strings.ToLower(name)
	for i := range d.Sections {
		if strings.ToLower(d.Sections[i].Heading) == lower {
			return &d.Sections[i]
		}
		for j := range d.Sections[i].Children {
			if strings.ToLower(d.Sections[i].Children[j].Heading) == lower {
				return &d.Sections[i].Children[j]
			}
		}
	}
	return nil
}

// ParseTaskTable extracts rows from a markdown table in the given
// section body. Expects columns: slug, status, note (in any order,
// identified by header names). Returns nil when no table is found.
func ParseTaskTable(body string) []TaskRow {
	lines := strings.Split(body, "\n")
	headerIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "|") && strings.Contains(strings.ToLower(line), "slug") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 || headerIdx+2 >= len(lines) {
		return nil
	}

	headers := splitTableRow(lines[headerIdx])
	colSlug, colStatus, colNote := -1, -1, -1
	for i, h := range headers {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "slug":
			colSlug = i
		case "status":
			colStatus = i
		case "note":
			colNote = i
		}
	}
	if colSlug < 0 || colStatus < 0 {
		return nil
	}

	var rows []TaskRow
	for _, line := range lines[headerIdx+2:] {
		if !strings.Contains(line, "|") {
			break
		}
		cols := splitTableRow(line)
		row := TaskRow{}
		if colSlug < len(cols) {
			row.Slug = strings.TrimSpace(cols[colSlug])
		}
		if colStatus < len(cols) {
			row.Status = strings.TrimSpace(cols[colStatus])
		}
		if colNote >= 0 && colNote < len(cols) {
			row.Note = strings.TrimSpace(cols[colNote])
		}
		if row.Slug != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	return strings.Split(line, "|")
}

// RenderTaskTable produces a markdown table from task rows.
func RenderTaskTable(rows []TaskRow) string {
	var buf strings.Builder
	buf.WriteString("| slug | status | note |\n")
	buf.WriteString("| ---- | ------ | ---- |\n")
	for _, r := range rows {
		fmt.Fprintf(&buf, "| %s | %s | %s |\n", r.Slug, r.Status, r.Note)
	}
	return buf.String()
}
