// Package frontmatter parses and writes the YAML-subset envelope at
// the head of a tasks/<slug>.md file. Pure stdlib.
//
// Grammar: a leading `---` line, one or more `key: value` scalar
// lines, a closing `---` line, then arbitrary body bytes. No
// nesting, no flow style, no anchors. Values may carry one optional
// pair of double quotes which are stripped on parse, so
// `title: "hello: world"` survives as `hello: world`.
package frontmatter

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Meta is the parsed frontmatter view. Status, Slug, Title, Created,
// Project, Host, Agent, and Session are first-class scalars; Needs is
// a first-class list. Any other recognised key lands in Extra so a
// Parse / Write round trip preserves it.
//
// Session is the tmux session name the spawner registered for this
// task. The kernel's own ensureSession path uses the computed
// "spore/<project>/<slug>" name, but downstream spawners that mint
// their own session names (e.g. "🐈 acme/my-task [opus]") write
// the real name here so reap/done/merge can target the live session
// instead of a stale computed one.
type Meta struct {
	Status  string
	Slug    string
	Title   string
	Created string
	Project string
	Host    string
	Agent   string
	Session string
	Needs   []string
	Extra   map[string]string
}

// Parse splits content at the leading and closing `---` fence lines
// and returns the parsed Meta plus the body bytes after the closing
// delimiter. A missing or malformed envelope is an error; callers
// that want soft handling (e.g. tasks/README.md) inspect the error.
func Parse(content []byte) (Meta, []byte, error) {
	lines, ends := scanLines(content)
	if len(lines) == 0 || !isFenceLine(lines[0]) {
		return Meta{}, nil, fmt.Errorf("frontmatter: missing opening '---' fence")
	}

	var m Meta
	closed := false
	closeIdx := 0
	var listTarget *[]string

	for i := 1; i < len(lines); i++ {
		ln := lines[i]
		if isFenceLine(ln) {
			closed = true
			closeIdx = i
			break
		}

		if item, ok := parseListItem(ln); ok && listTarget != nil {
			*listTarget = append(*listTarget, item)
			continue
		}
		listTarget = nil

		key, val, ok := splitKV(ln)
		if !ok {
			return Meta{}, nil, fmt.Errorf("frontmatter:%d: malformed line %q", i+1, ln)
		}
		val = stripDoubleQuotes(val)
		switch key {
		case "status":
			m.Status = val
		case "slug":
			m.Slug = val
		case "title":
			m.Title = val
		case "created":
			m.Created = val
		case "project":
			m.Project = val
		case "host":
			m.Host = val
		case "agent":
			m.Agent = val
		case "session":
			m.Session = val
		case "needs":
			listTarget = &m.Needs
		default:
			if m.Extra == nil {
				m.Extra = make(map[string]string)
			}
			m.Extra[key] = val
		}
	}

	if !closed {
		return Meta{}, nil, fmt.Errorf("frontmatter: missing closing '---' fence")
	}

	body := content[ends[closeIdx]:]
	if len(body) == 0 {
		return m, nil, nil
	}
	return m, body, nil
}

// Write serialises Meta back into the `---`...`---` envelope and
// appends body. Field order is fixed (Status, Slug, Title, Created,
// Project) followed by Extra in sorted key order, so Parse + Write
// round-trips byte-for-byte when callers preserve insertion via
// Extra.
func Write(m Meta, body []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	writeScalar(&buf, "status", m.Status)
	writeScalar(&buf, "slug", m.Slug)
	writeScalar(&buf, "title", m.Title)
	writeScalar(&buf, "created", m.Created)
	writeScalar(&buf, "project", m.Project)
	writeScalar(&buf, "host", m.Host)
	writeScalar(&buf, "agent", m.Agent)
	writeScalar(&buf, "session", m.Session)
	writeBlockList(&buf, "needs", m.Needs)

	keys := make([]string, 0, len(m.Extra))
	for k := range m.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeScalar(&buf, k, m.Extra[k])
	}
	buf.WriteString("---\n")
	buf.Write(body)
	return buf.Bytes()
}

func writeScalar(buf *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(buf, "%s: %s\n", key, value)
}

func writeBlockList(buf *bytes.Buffer, key string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(buf, "%s:\n", key)
	for _, item := range items {
		fmt.Fprintf(buf, "  - %s\n", item)
	}
}

func parseListItem(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "- ") {
		return "", false
	}
	return strings.TrimSpace(trimmed[2:]), true
}

func stripDoubleQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func scanLines(b []byte) (lines []string, ends []int) {
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			lines = append(lines, string(b[start:i]))
			ends = append(ends, i+1)
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, string(b[start:]))
		ends = append(ends, len(b))
	}
	return
}

func isFenceLine(line string) bool {
	if !strings.HasPrefix(line, "---") {
		return false
	}
	for _, r := range line[3:] {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func splitKV(line string) (key, value string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", false
	}
	key = line[:colon]
	if !validKey(key) {
		return "", "", false
	}
	rest := line[colon+1:]
	rest = strings.TrimLeft(rest, " \t")
	return key, rest, true
}

func validKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r == '_':
		case i > 0 && (r == '-' || (r >= '0' && r <= '9')):
		default:
			return false
		}
	}
	return true
}
