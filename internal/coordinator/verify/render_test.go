package verify

import (
	"strings"
	"testing"
)

func TestOrNone(t *testing.T) {
	if got := orNone(""); got != "none" {
		t.Errorf("orNone(%q) = %q, want none", "", got)
	}
	if got := orNone("abc"); got != "abc" {
		t.Errorf("orNone(%q) = %q, want abc", "abc", got)
	}
}

func TestOrDefault(t *testing.T) {
	if got := orDefault("", "fallback"); got != "fallback" {
		t.Errorf("orDefault(%q, %q) = %q, want fallback", "", "fallback", got)
	}
	if got := orDefault("value", "fallback"); got != "value" {
		t.Errorf("orDefault(%q, %q) = %q, want value", "value", "fallback", got)
	}
}

func TestFormatBogusEvidence(t *testing.T) {
	r := Result{
		Slug:             "bad-ev",
		Verdict:          BogusEvidence,
		EvidenceFailures: "commit:abc-unresolved",
	}
	out := r.Format()
	if !strings.Contains(out, "verdict: bogus-evidence: commit:abc-unresolved") {
		t.Errorf("missing bogus-evidence verdict line in:\n%s", out)
	}
}

func TestFormatCrossRepo(t *testing.T) {
	r := Result{
		Slug:          "cross",
		Verdict:       CrossRepo,
		CrossRepoPath: "/work/projects/other",
	}
	out := r.Format()
	if !strings.Contains(out, "verdict: cross-repo") {
		t.Errorf("missing cross-repo verdict in:\n%s", out)
	}
	if !strings.Contains(out, "cross-repo: /work/projects/other") {
		t.Errorf("missing cross-repo path in:\n%s", out)
	}
}

func TestFormatEvidenceOk(t *testing.T) {
	r := Result{
		Slug:           "ev-ok",
		Verdict:        RealImpl,
		GitCommit:      "abc1234 feat: done",
		EvidenceStatus: "ok",
	}
	out := r.Format()
	if !strings.Contains(out, "evidence: ok") {
		t.Errorf("missing evidence: ok in:\n%s", out)
	}
}

func TestFormatEvidenceFailed(t *testing.T) {
	r := Result{
		Slug:             "ev-fail",
		Verdict:          BogusEvidence,
		EvidenceStatus:   "failed",
		EvidenceFailures: "commit:no-sha",
	}
	out := r.Format()
	if !strings.Contains(out, "evidence: failed: commit:no-sha") {
		t.Errorf("missing evidence failed line in:\n%s", out)
	}
}

func TestFormatReflogSHA(t *testing.T) {
	r := Result{
		Slug:      "lost",
		Verdict:   LostToReflog,
		ReflogSHA: "deadbeef",
	}
	out := r.Format()
	if !strings.Contains(out, "reflog: deadbeef") {
		t.Errorf("missing reflog line in:\n%s", out)
	}
}

func TestFormatNoFinalText(t *testing.T) {
	r := Result{
		Slug:    "notxt",
		Verdict: Unknown,
	}
	out := r.Format()
	if !strings.Contains(out, "(no assistant text)") {
		t.Errorf("missing '(no assistant text)' fallback in:\n%s", out)
	}
}
