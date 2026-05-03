package evidence

import (
	"fmt"
	"regexp"
	"strings"
)

// Verify cross-checks Required against Parse output and returns a
// verdict plus diagnostic lines. Pure structural check: no shell, no
// git, no filesystem. Live-state checks belong to a separate verifier.
//
// Precedence:
//
//  1. any required-kind bullet has empty or shape-implausible Rest
//     -> bogus-evidence.
//  2. every required kind has at least one substantive bullet:
//     - any bullet looks cross-repo: cross-repo.
//     - else: real-impl.
//  3. some required kinds are unmet, but every unmet kind is
//     documented as `N/A: <rationale>`: rational-close.
//  4. some required kinds are unmet, body claims completion in prose
//     (merged / shipped / implemented / ...): suspect-hallucination.
//  5. otherwise: unknown.
//
// When evidence_required is absent, Verify returns Unknown with a
// diagnostic so the caller can decide whether to skip the gate
// (pre-contract task) or treat it as a missing contract.
func Verify(meta map[string]any, body string) (Verdict, []string) {
	required := Required(meta)
	items, err := Parse(body)
	if err != nil {
		return Unknown, []string{err.Error()}
	}

	if len(required) == 0 {
		return Unknown, []string{"no evidence_required declared"}
	}

	byKind := map[string][]Item{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
	}

	var diags []string
	bogus := false
	crossRepo := false
	substantiveKinds := 0
	naDocumentedKinds := 0
	var unmet []string

	for _, req := range required {
		bullets := byKind[req]
		if len(bullets) == 0 {
			unmet = append(unmet, req)
			continue
		}
		hasSubstantive := false
		hasNA := false
		for _, b := range bullets {
			r := strings.TrimSpace(b.Rest)
			switch {
			case r == "":
				bogus = true
				diags = append(diags, fmt.Sprintf("%s: empty bullet rest", req))
			case isNARest(r):
				hasNA = true
			case !isPlausibleRest(req, r):
				bogus = true
				diags = append(diags, fmt.Sprintf("%s: rest does not match kind shape: %q", req, r))
			default:
				hasSubstantive = true
				if isCrossRepoRest(r) {
					crossRepo = true
					diags = append(diags, fmt.Sprintf("%s: cross-repo: %s", req, r))
				}
			}
		}
		switch {
		case hasSubstantive:
			substantiveKinds++
		case hasNA:
			naDocumentedKinds++
		default:
			// All bullets bogus; bogus already flagged.
			unmet = append(unmet, req)
		}
	}

	if bogus {
		return BogusEvidence, diags
	}

	if substantiveKinds == len(required) {
		if crossRepo {
			return CrossRepo, diags
		}
		return RealImpl, nil
	}

	if substantiveKinds+naDocumentedKinds == len(required) {
		return RationalClose, diags
	}

	diags = append(diags, fmt.Sprintf("missing required: %s", strings.Join(unmet, ", ")))
	if claimsCompletion(body) {
		return SuspectHallucination, diags
	}
	return Unknown, diags
}

// completionRe matches prose markers a rower writes when they think
// the work is done: "merged", "shipped", "all green", etc. Used to
// distinguish suspect-hallucination (claims work) from unknown
// (genuine ambiguity).
var completionRe = regexp.MustCompile(
	`(?i)\b(merged|shipped|landed|implemented|completed|fast-forward|all green|tests pass(ed)?|verified)\b`,
)

func claimsCompletion(body string) bool {
	return completionRe.MatchString(body)
}

// isNARest reports whether a bullet's Rest is a documented N/A. The
// rationale is everything after the leading marker; an empty
// rationale (`N/A` alone) still counts as documented.
func isNARest(r string) bool {
	upper := strings.ToUpper(strings.TrimSpace(r))
	return strings.HasPrefix(upper, "N/A") ||
		strings.HasPrefix(upper, "NA:") ||
		strings.HasPrefix(upper, "NA ")
}

// isPlausibleRest is a coarse shape check per kind. The aim is to
// catch bullets that are clearly the wrong content for the declared
// kind (e.g. `- commit: hello world`) without being so strict that a
// human-written reference fails. Unknown kinds default to permissive.
func isPlausibleRest(kind, r string) bool {
	switch kind {
	case "commit":
		if isCrossRepoRest(r) {
			return true
		}
		return hasHexShaPrefix(r)
	case "file", "test", "doc-link":
		return strings.ContainsAny(r, "/.")
	case "command":
		return strings.ContainsAny(r, "`$/") || strings.Contains(r, " ")
	case "side-by-side":
		return r != ""
	}
	return true
}

func hasHexShaPrefix(r string) bool {
	r = strings.TrimSpace(r)
	n := 0
	for _, c := range r {
		if isHex(c) {
			n++
			continue
		}
		break
	}
	return n >= 7
}

func isHex(c rune) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	}
	return false
}

// isCrossRepoRest detects references that point outside the current
// repo. Heuristics:
//
//   - leading `<repo>:<rest>` where <repo> looks like a bare repo
//     identifier (alnum, dot, hyphen, slash, underscore) and contains
//     at least one hyphen or slash so plain words like "ok:" don't
//     trip it;
//   - presence of a github.com / gitlab.com / codeberg.org URL.
//
// The bare-token check is deliberately strict: prose with embedded
// colons (e.g. a backtick example like “ `slug: real-impl` “) does
// not look like a leading repo identifier and stays in-repo.
func isCrossRepoRest(r string) bool {
	if i := strings.IndexByte(r, ':'); i > 0 {
		if isRepoToken(r[:i]) {
			return true
		}
	}
	for _, host := range []string{"github.com/", "gitlab.com/", "codeberg.org/"} {
		if strings.Contains(r, host) {
			return true
		}
	}
	return false
}

func isRepoToken(s string) bool {
	if s == "" || !strings.ContainsAny(s, "-/") {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-', c == '_', c == '/', c == '.':
		default:
			return false
		}
	}
	return true
}
