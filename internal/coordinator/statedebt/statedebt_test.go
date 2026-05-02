package statedebt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleState = `## Active tasks

Some task list.

## CRITICAL LESSON: always verify before claiming (2026-04-15)

harness: coordinator-verify-done

The lesson body here.

### SKYHELM SELF-LESSON: watch the reflog (2026-03-01)

Old lesson without harness pointer.

## RULE: no force push

Ancient rule with no date.

## CRITICAL LESSON: fresh insight (2026-05-01)

Very recent, not yet actionable.
`

func TestScanClassifications(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "state.md")
	os.WriteFile(f, []byte(sampleState), 0o644)

	result, err := Scan(Config{StateFile: f, AgeDays: 14})
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(result.Blocks))
	}

	want := []struct {
		heading string
		class   Classification
	}{
		{"CRITICAL LESSON: always verify", Lifted},
		{"SKYHELM SELF-LESSON: watch the reflog", StaleLiftCandidate},
		{"RULE: no force push", StaleLiftCandidate},
		{"CRITICAL LESSON: fresh insight", Pending},
	}

	for i, w := range want {
		if result.Blocks[i].Classification != w.class {
			t.Errorf("block %d (%s): got %s, want %s",
				i, w.heading, result.Blocks[i].Classification, w.class)
		}
		if !strings.Contains(result.Blocks[i].Heading, strings.Split(w.heading, ":")[0]) {
			t.Errorf("block %d heading mismatch: %s", i, result.Blocks[i].Heading)
		}
	}
}

func TestScanStaleCount(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "state.md")
	os.WriteFile(f, []byte(sampleState), 0o644)

	result, err := Scan(Config{StateFile: f, AgeDays: 14})
	if err != nil {
		t.Fatal(err)
	}

	if result.StaleCount != 2 {
		t.Errorf("StaleCount = %d, want 2", result.StaleCount)
	}
}

func TestScanMissingFile(t *testing.T) {
	result, err := Scan(Config{StateFile: "/nonexistent/state.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 0 {
		t.Errorf("expected 0 blocks from missing file, got %d", len(result.Blocks))
	}
}

func TestScanEmpty(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "state.md")
	os.WriteFile(f, []byte("## Active tasks\n\nJust tasks, no lessons.\n"), 0o644)

	result, err := Scan(Config{StateFile: f})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(result.Blocks))
	}
}

func TestFormatVerbose(t *testing.T) {
	result := ScanResult{
		Blocks: []Block{
			{Heading: "## CRITICAL LESSON: foo", Classification: Lifted},
			{Heading: "### RULE: bar", Classification: StaleLiftCandidate},
		},
	}
	out := FormatVerbose(result)
	if !strings.Contains(out, "lifted") {
		t.Errorf("expected lifted in output: %s", out)
	}
	if !strings.Contains(out, "stale-lift-candidate") {
		t.Errorf("expected stale-lift-candidate in output: %s", out)
	}
}

func TestFormatVerboseEmpty(t *testing.T) {
	out := FormatVerbose(ScanResult{})
	if !strings.Contains(out, "no CRITICAL LESSON") {
		t.Errorf("expected 'no' message: %s", out)
	}
}

func TestFormatSummary(t *testing.T) {
	result := ScanResult{
		StaleCount:    2,
		StaleHeadings: []string{"watch the reflog", "no force push"},
	}
	out := FormatSummary(result)
	if !strings.Contains(out, "stale-lift-candidate:") {
		t.Errorf("expected stale-lift-candidate prefix: %s", out)
	}
	if !strings.Contains(out, "watch the reflog") {
		t.Errorf("expected heading in summary: %s", out)
	}
}

func TestFormatSummaryNoStale(t *testing.T) {
	if out := FormatSummary(ScanResult{}); out != "" {
		t.Errorf("expected empty summary, got %q", out)
	}
}

func TestFindLatestDate(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"foo 2026-04-15 bar 2026-05-01", "2026-05-01"},
		{"no dates here", ""},
		{"single 2026-03-10", "2026-03-10"},
	}
	for _, tc := range cases {
		if got := findLatestDate(tc.input); got != tc.want {
			t.Errorf("findLatestDate(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestClassifyHarnessPointer(t *testing.T) {
	b := classify("## RULE: foo", "harness: some-script\nBody text.", "2026-01-01")
	if b.Classification != Lifted {
		t.Errorf("expected lifted, got %s", b.Classification)
	}
	if !b.HasHarness {
		t.Error("expected HasHarness = true")
	}
}
