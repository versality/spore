package lints

import (
	"os/exec"
	"strings"
	"testing"
)

const justfileFixture = `default: check
check:
    @true
fmt:
    @true
switch:
    @true
`

func mkCaptureSignalDoc(rows ...string) string {
	b := strings.Builder{}
	b.WriteString("# Doc\n\n## Coverage Matrix\n\n")
	b.WriteString("| id | tag | coverage | note |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return b.String()
}

func TestCaptureSignalCoverage_Pass(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH")
	}
	rows := []string{
		"| just default | - | oos | alias |",
		"| just check | - | direct | wraps |",
		"| just fmt | - | unwrapped | follow-up X |",
		"| just switch | - | oos | manual-only |",
		"| bin/wrap-* | - | unwrapped | n |",
		"| bin/tool | - | unwrapped | n |",
	}
	root := newTestRepo(t, map[string]string{
		"justfile":         justfileFixture,
		"docs/coverage.md": mkCaptureSignalDoc(rows...),
		"bin/wrap-a":       "x\n",
		"bin/wrap-b":       "x\n",
		"bin/tool":         "x\n",
	})
	issues, err := CaptureSignalCoverage{
		DocPath:      "docs/coverage.md",
		BoundedFiles: []string{"bin/*"},
	}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}

func TestCaptureSignalCoverage_MissingRecipe(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH")
	}
	rows := []string{
		"| just default | - | oos | alias |",
		"| just check | - | direct | wraps |",
		"| just switch | - | oos | manual-only |",
		"| bin/tool | - | unwrapped | n |",
	}
	root := newTestRepo(t, map[string]string{
		"justfile":         justfileFixture,
		"docs/coverage.md": mkCaptureSignalDoc(rows...),
		"bin/tool":         "x\n",
	})
	issues, err := CaptureSignalCoverage{
		DocPath:      "docs/coverage.md",
		BoundedFiles: []string{"bin/*"},
	}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, i := range issues {
		if strings.Contains(i.Message, "just fmt") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'just fmt' missing-row issue; got %v", issues)
	}
}

func TestCaptureSignalCoverage_EmptyMatrix(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH")
	}
	root := newTestRepo(t, map[string]string{
		"justfile":         justfileFixture,
		"docs/coverage.md": "## Coverage Matrix\n\n(no rows)\n",
	})
	issues, err := CaptureSignalCoverage{DocPath: "docs/coverage.md"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "could not parse") {
		t.Fatalf("expected empty-matrix issue, got %v", issues)
	}
}

func TestCaptureSignalCoverage_MissingDoc(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := CaptureSignalCoverage{DocPath: "docs/coverage.md"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "missing doc") {
		t.Fatalf("expected missing-doc issue, got %v", issues)
	}
}

func TestCaptureSignalCoverage_NoDocPath_NoOp(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := CaptureSignalCoverage{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues with empty DocPath, got %v", issues)
	}
}
