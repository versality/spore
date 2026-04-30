package evidence

import "strings"

// Required reads `evidence_required` from a parsed frontmatter map and
// returns the declared kinds. Returns nil when the key is absent.
//
// Three input shapes are accepted:
//
//   - []string: pass-through.
//   - []any (e.g. from a generic YAML decoder): each string element is
//     kept; non-strings are skipped.
//   - string (the shape spore's frontmatter parser stores): parsed as
//     a YAML inline list `[a, b, c]`; bare scalars are also accepted
//     so a single-kind contract `evidence_required: commit` works.
//
// Items are trimmed of whitespace; empty entries are dropped.
func Required(meta map[string]any) []string {
	v, ok := meta["evidence_required"]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return cleanList(x)
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return cleanList(out)
	case string:
		return parseInlineList(x)
	}
	return nil
}

// parseInlineList accepts `[a, b, c]` (with optional whitespace around
// each item) and a bare scalar `commit`. Returns nil for empty input.
func parseInlineList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := s[1 : len(s)-1]
		if strings.TrimSpace(inner) == "" {
			return nil
		}
		parts := strings.Split(inner, ",")
		return cleanList(parts)
	}
	return []string{s}
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e != "" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
