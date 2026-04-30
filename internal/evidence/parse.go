package evidence

import "strings"

// Parse extracts each `- kind: rest` bullet from the body's
// `## Evidence` section. Returns nil when the section is absent.
//
// The section is identified by a `## Evidence` heading
// (case-insensitive on "Evidence") and ends at the next `## ` heading
// or EOF. Bullets must start with `- ` (or `-\t`); the kind keyword is
// the substring before the first colon. Bullets without a recognised
// kind are silently skipped (lints fire on those separately so this
// stays robust on partial briefs).
//
// Continuation lines (indented under a bullet) are ignored: each
// bullet's Rest is whatever follows the colon on the bullet's own
// line, trimmed.
func Parse(body string) ([]Item, error) {
	lines := strings.Split(body, "\n")
	startIdx := -1
	for i, ln := range lines {
		if isEvidenceHeading(ln) {
			startIdx = i + 1
			break
		}
	}
	if startIdx < 0 {
		return nil, nil
	}
	var out []Item
	for i := startIdx; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(ln, "## ") {
			break
		}
		it, ok := parseBullet(ln)
		if !ok {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func isEvidenceHeading(line string) bool {
	if !strings.HasPrefix(line, "## ") {
		return false
	}
	rest := strings.TrimSpace(line[3:])
	return strings.EqualFold(rest, "Evidence")
}

func parseBullet(line string) (Item, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "-\t") {
		return Item{}, false
	}
	rest := strings.TrimLeft(t[2:], " \t")
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return Item{}, false
	}
	kind := strings.TrimSpace(rest[:colon])
	if !IsKind(kind) {
		return Item{}, false
	}
	body := strings.TrimSpace(rest[colon+1:])
	return Item{Kind: kind, Rest: body}, true
}
