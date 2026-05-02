package verify

import (
	"fmt"
	"strings"
)

// Format renders the result in the verdict block format consumers
// can pipe directly into a coordinator log or a tell message.
func (r Result) Format() string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%s: %s\n", r.Slug, r.Verdict)
	fmt.Fprintf(&buf, "  git: %s\n", orNone(r.GitCommit))
	fmt.Fprintf(&buf, "  events: %s\n", r.MergeEntry)
	fmt.Fprintf(&buf, "  session: %s @ %s\n", r.FinalTool, r.LastTimestamp)
	fmt.Fprintf(&buf, "  final-text: %s\n", orDefault(r.FinalText, "(no assistant text)"))
	fmt.Fprintf(&buf, "  frontmatter: %s\n", r.FrontmatterStatus)
	if r.CrossRepoPath != "" {
		fmt.Fprintf(&buf, "  cross-repo: %s\n", r.CrossRepoPath)
	}
	if r.ReflogSHA != "" {
		fmt.Fprintf(&buf, "  reflog: %s\n", r.ReflogSHA)
	}
	if r.EvidenceStatus != "" {
		if r.EvidenceFailures != "" {
			fmt.Fprintf(&buf, "  evidence: failed: %s\n", r.EvidenceFailures)
		} else {
			fmt.Fprintf(&buf, "  evidence: ok\n")
		}
	}
	switch r.Verdict {
	case LostToReflog:
		fmt.Fprintf(&buf, "verdict: %s: %s\n", r.Verdict, r.ReflogSHA)
	case BogusEvidence:
		fmt.Fprintf(&buf, "verdict: %s: %s\n", r.Verdict, r.EvidenceFailures)
	default:
		fmt.Fprintf(&buf, "verdict: %s\n", r.Verdict)
	}
	return buf.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
