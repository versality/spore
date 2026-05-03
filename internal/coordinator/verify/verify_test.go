package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDecideRealImpl(t *testing.T) {
	v := decide("abc123 feat: add auth", "", "none", "wt-merge", "", "", "",
		true, true)
	if v != RealImpl {
		t.Errorf("got %s, want real-impl", v)
	}
}

func TestDecideBogusEvidence(t *testing.T) {
	v := decide("", "", "none", "other", "", "", "commit:abc-unresolved",
		false, false)
	if v != BogusEvidence {
		t.Errorf("got %s, want bogus-evidence", v)
	}
}

func TestDecideLostToReflog(t *testing.T) {
	v := decide("", "abc1234", "none", "other", "", "", "",
		false, false)
	if v != LostToReflog {
		t.Errorf("got %s, want lost-to-reflog", v)
	}
}

func TestDecideCrossRepo(t *testing.T) {
	v := decide("", "", "none", "other", "", "/work/projects/other", "",
		false, false)
	if v != CrossRepo {
		t.Errorf("got %s, want cross-repo", v)
	}
}

func TestDecideRationalClose(t *testing.T) {
	cases := []struct {
		name      string
		finalTool string
		finalText string
	}{
		{"abandon_tool", "wt-abandon", ""},
		{"tell_abandoned", "tell-abandoned", ""},
		{"text_abandoned", "other", "I abandoned this task because it was superseded"},
		{"already_done", "other", "The work is already done upstream"},
		{"rejected", "other", "The approach was rejected by the operator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := decide("", "", "none", tc.finalTool, tc.finalText, "", "",
				false, false)
			if v != RationalClose {
				t.Errorf("got %s, want rational-close", v)
			}
		})
	}
}

func TestDecideSuspectHallucination(t *testing.T) {
	v := decide("", "", "none", "other", "All green and merged. Done.", "", "",
		false, false)
	if v != SuspectHallucination {
		t.Errorf("got %s, want suspect-hallucination", v)
	}
}

func TestDecideSuspectHallucinationNotWhenGitCommitSeen(t *testing.T) {
	v := decide("", "", "none", "other", "All green and merged. Done.", "", "",
		true, false)
	if v != Unknown {
		t.Errorf("got %s, want unknown (git commit was seen)", v)
	}
}

func TestDecideUnknown(t *testing.T) {
	v := decide("", "", "none", "other", "some random text", "", "",
		false, false)
	if v != Unknown {
		t.Errorf("got %s, want unknown", v)
	}
}

func TestFormatResult(t *testing.T) {
	r := Result{
		Slug:              "fix-auth",
		Verdict:           RealImpl,
		GitCommit:         "abc1234 feat: fix auth",
		MergeEntry:        "2026-05-01T10:00:00+03:00 fix-auth->done",
		FinalTool:         "wt-merge",
		FinalText:         "Merged successfully",
		LastTimestamp:     "2026-05-01T10:00:00Z",
		FrontmatterStatus: "done",
	}
	out := r.Format()
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "fix-auth: real-impl") {
		t.Errorf("missing slug+verdict line in:\n%s", out)
	}
	if !contains(out, "verdict: real-impl") {
		t.Errorf("missing verdict line in:\n%s", out)
	}
}

func TestFormatLostToReflog(t *testing.T) {
	r := Result{
		Slug:      "lost-work",
		Verdict:   LostToReflog,
		ReflogSHA: "abc1234",
	}
	out := r.Format()
	if !contains(out, "verdict: lost-to-reflog: abc1234") {
		t.Errorf("missing reflog verdict in:\n%s", out)
	}
}

func TestFindMergeEvent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "events.jsonl")

	events := []map[string]any{
		{
			"ts":    "2026-05-01T10:00:00Z",
			"event": "status-flip",
			"payload": map[string]string{
				"slug": "fix-auth", "to": "done", "caller": "fix-auth",
			},
		},
		{
			"ts":    "2026-05-01T11:00:00Z",
			"event": "status-flip",
			"payload": map[string]string{
				"slug": "fix-auth", "to": "done", "caller": "coordinator",
			},
		},
	}
	var content []byte
	for _, ev := range events {
		b, _ := json.Marshal(ev)
		content = append(content, b...)
		content = append(content, '\n')
	}
	os.WriteFile(f, content, 0o644)

	result := findMergeEvent("fix-auth", f)
	if result == "none" {
		t.Fatal("expected merge event, got none")
	}
	if !contains(result, "fix-auth->done") {
		t.Errorf("expected fix-auth->done, got %q", result)
	}
}

func TestFindMergeEventNoMatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "events.jsonl")
	os.WriteFile(f, []byte{}, 0o644)

	result := findMergeEvent("nonexistent", f)
	if result != "none" {
		t.Errorf("expected none, got %q", result)
	}
}

func TestExtractFrontmatterField(t *testing.T) {
	content := []byte("---\nstatus: active\nslug: test\ncreated: 2026-05-01\n---\n# Body\n")
	if got := extractFrontmatterField(content, "status"); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	if got := extractFrontmatterField(content, "created"); got != "2026-05-01" {
		t.Errorf("created = %q, want 2026-05-01", got)
	}
	if got := extractFrontmatterField(content, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}

func TestExtractEvidenceBullets(t *testing.T) {
	content := "---\nstatus: done\nevidence_required: [go-test]\n---\n\n## Evidence\n\n- commit: abc1234 green\n- file: internal/foo.go\n\n## Other\n"
	bullets := extractEvidenceBullets(content)
	if len(bullets) != 2 {
		t.Fatalf("expected 2 bullets, got %d: %v", len(bullets), bullets)
	}
	if bullets[0] != "commit: abc1234 green" {
		t.Errorf("bullet[0] = %q", bullets[0])
	}
}

func TestIsTaskOrMerge(t *testing.T) {
	cases := []struct {
		subj string
		slug string
		want bool
	}{
		{"tasks/fix-auth: flip done", "fix-auth", true},
		{"tasks/fix-auth.md update", "fix-auth", true},
		{"Merge branch main", "fix-auth", true},
		{"feat: add auth handler", "fix-auth", false},
	}
	for _, tc := range cases {
		if got := isTaskOrMerge(tc.subj, tc.slug); got != tc.want {
			t.Errorf("isTaskOrMerge(%q, %q) = %v, want %v", tc.subj, tc.slug, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("got %q, want hi", got)
	}
}

func TestCollapseSpaces(t *testing.T) {
	if got := collapseSpaces("a  b   c"); got != "a b c" {
		t.Errorf("got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && len(s) >= len(sub) &&
		(s == sub || findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
