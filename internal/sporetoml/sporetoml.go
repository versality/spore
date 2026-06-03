// Package sporetoml is the shared leaf parser for the tiny TOML subset
// spore.toml uses. Several kernel packages (matter, sandboxcfg, lints,
// initconfig, fleet, align) each hand-rolled the same line scanner plus
// comment/quote/list helpers; this package is the single source of truth
// they all repoint to.
//
// The subset accepted is deliberately small: `[section]` headers,
// `key = value` scalar lines, blank lines, and `#` comments. A `#`
// inside a quoted value is preserved. Callers keep their own
// struct/map mapping logic and layer it on top of ScanSections.
package sporetoml

import (
	"bufio"
	"strings"
)

// StripComment trims everything after a bare `#`, treating a `#` that
// falls inside a single- or double-quoted run as literal. A quote opens
// a run that only the same quote rune closes, so `'a # b'` and
// `"a # b"` survive intact. This is the quote-aware variant; values
// that never contain quotes (bare integers, etc.) get the same result a
// naive trailing-`#` strip would.
func StripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

// StripQuotes removes a single matching pair of surrounding single or
// double quotes. A value without matching surrounding quotes (or shorter
// than two runes) is returned unchanged.
func StripQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// SplitList splits a comma-separated list, honouring single/double
// quotes so a comma inside a quoted element does not split it. Quote
// runes are consumed (not emitted); each element is trimmed and empty
// elements are dropped. This matches the lints splitList semantics.
func SplitList(raw string) []string {
	var out []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
				continue
			}
			b.WriteByte(ch)
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == ',':
			addListPart(&out, b.String())
			b.Reset()
		default:
			b.WriteByte(ch)
		}
	}
	addListPart(&out, b.String())
	return out
}

func addListPart(out *[]string, part string) {
	part = strings.TrimSpace(part)
	if part != "" {
		*out = append(*out, part)
	}
}

// Line is one non-blank, non-header content line inside a section.
// Section is the full bracket contents of the most recent header (e.g.
// "fleet.workers.ratio"), or "" before the first header. Text is the
// comment-stripped, trimmed line. LineNum is 1-based.
//
// ScanSections does no `key = value` validation: each caller gates the
// malformed-entry check on whether Section is one it cares about, so the
// shared scanner must hand the raw in-section line back untouched. Use
// SplitKeyValue to split Text.
type Line struct {
	Section string
	Text    string
	LineNum int
}

// ScanSections walks content, applies StripComment, skips blank lines,
// tracks the current `[section]` header, and invokes fn for every other
// non-blank line with the active section in scope. Callers decide which
// sections matter and whether a line is malformed; this only does the
// bufio + comment-strip + header-tracking framing every parser shared.
func ScanSections(content string, fn func(Line) error) error {
	scanner := bufio.NewScanner(strings.NewReader(content))
	section := ""
	for lineNum := 1; scanner.Scan(); lineNum++ {
		text := strings.TrimSpace(StripComment(scanner.Text()))
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			section = strings.TrimSpace(text[1 : len(text)-1])
			continue
		}
		if err := fn(Line{Section: section, Text: text, LineNum: lineNum}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// SplitKeyValue splits a `key = value` line on the first `=`. ok is
// false when there is no `=` or it sits in the first column (the
// `eq <= 0` rejection every parser used). key is trimmed; rawValue is
// trimmed but keeps quotes and brackets so the caller interprets it.
func SplitKeyValue(line string) (key, rawValue string, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:eq]), strings.TrimSpace(line[eq+1:]), true
}
