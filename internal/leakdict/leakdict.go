// Package leakdict is the shared dictionary + scanner used by the
// leak-guard lint and the commit-msg hook. It lives in its own
// package because both internal/lints and internal/hooks need access
// to it (lints already imports hooks via hooksdrift, so a sibling
// package avoids an import cycle).
package leakdict

import "strings"

// Dictionary is the canonical list of terms that must not appear in
// spore source / commit messages. Matched case-insensitively as
// substrings; the terms are long enough or distinctive enough that
// false positives are very unlikely.
var Dictionary = []string{
	"skyhelm",
	"skywing",
	"skytower",
	"skypad",
	"wingbot",
	"skyler",
	"skybot",
	"helm-mcom",
	"marketercom",
	"marketer-deploy",
	"helm-coord",
	"/home/sky/nix-config",
	"~/projects/nix-config",
}

// dictionaryLower caches the lowercased dictionary for the hot scan
// path.
var dictionaryLower = func() []string {
	out := make([]string, len(Dictionary))
	for i, t := range Dictionary {
		out[i] = strings.ToLower(t)
	}
	return out
}()

// FormFor returns the canonical (mixed-case) dictionary entry for a
// lowercased term. Falls back to the input if no match.
func FormFor(lowerTerm string) string {
	for _, original := range Dictionary {
		if strings.ToLower(original) == lowerTerm {
			return original
		}
	}
	return lowerTerm
}

// Merge returns the dictionary plus any caller-supplied extra terms,
// all lowercased.
func Merge(extra []string) []string {
	if len(extra) == 0 {
		return dictionaryLower
	}
	out := append([]string{}, dictionaryLower...)
	for _, e := range extra {
		if s := strings.TrimSpace(e); s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// ScanLine returns every dictionary term that appears (case-
// insensitively) in line. Empty result means clean.
func ScanLine(line string, extra []string) []string {
	terms := Merge(extra)
	lower := strings.ToLower(line)
	var hits []string
	for _, term := range terms {
		if strings.Contains(lower, term) {
			hits = append(hits, FormFor(term))
		}
	}
	return hits
}

// ScanMessage returns the first dictionary hit in msg (canonical
// form) or "" if msg is clean. Used by the commit-msg hook to reject
// commits introducing a leak.
func ScanMessage(msg string, extra []string) string {
	terms := Merge(extra)
	lower := strings.ToLower(msg)
	for _, term := range terms {
		if strings.Contains(lower, term) {
			return FormFor(term)
		}
	}
	return ""
}
