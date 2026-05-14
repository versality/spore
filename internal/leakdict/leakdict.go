// Package leakdict is the shared dictionary + scanner used by the
// leak-guard lint and the commit-msg hook. It lives in its own
// package because both internal/lints and internal/hooks need access
// to it (lints already imports hooks via hooksdrift, so a sibling
// package avoids an import cycle).
package leakdict

import (
	"regexp"
	"strings"
	"sync"
)

// Dictionary is the canonical list of terms that must not appear in
// spore source / commit messages. Matched case-insensitively. Terms
// composed entirely of word characters (letters, digits, underscore)
// are matched with word boundaries so common English substrings do
// not false-fire (e.g. "rower" must not match "browser" or
// "narrower"). Terms with non-word characters (e.g. paths, hyphenated
// slugs) are matched as plain substrings.
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
	"rower",
}

var dictionaryLower = func() []string {
	out := make([]string, len(Dictionary))
	for i, t := range Dictionary {
		out[i] = strings.ToLower(t)
	}
	return out
}()

var patternCache sync.Map // lowercased term -> *regexp.Regexp

func patternFor(lowerTerm string) *regexp.Regexp {
	if r, ok := patternCache.Load(lowerTerm); ok {
		return r.(*regexp.Regexp)
	}
	quoted := regexp.QuoteMeta(lowerTerm)
	if isWordChar(lowerTerm[0]) {
		quoted = `\b` + quoted
	}
	if isWordChar(lowerTerm[len(lowerTerm)-1]) {
		quoted = quoted + `\b`
	}
	r := regexp.MustCompile(`(?i)` + quoted)
	patternCache.Store(lowerTerm, r)
	return r
}

func isWordChar(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

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

// ScanLine returns every dictionary term that appears in line. Empty
// result means clean.
func ScanLine(line string, extra []string) []string {
	terms := Merge(extra)
	var hits []string
	for _, term := range terms {
		if patternFor(term).MatchString(line) {
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
	for _, term := range terms {
		if patternFor(term).MatchString(msg) {
			return FormFor(term)
		}
	}
	return ""
}
