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
