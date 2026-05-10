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
		"| just switch | - | oos | operator-only |",
		"| nix/packages/wt/wt-task | - | unwrapped | n |",
		"| nix/packages/wt/skyhelm-* | - | unwrapped | n |",
		"| nix/packages/wt/codex-* | - | unwrapped | n |",
		"| nix/packages/sky-harness/sky-harness.sh | - | unwrapped | n |",
		"| nix/packages/skyler-tools/skyler-report-bug.sh | - | unwrapped | n |",
		"| harness/skyhelm-boot | - | unwrapped | n |",
		"| harness/skyhelm-no-source-edits.sh | - | unwrapped | n |",
		"| harness/opencode-rower-liveness.sh | - | unwrapped | n |",
	}
	root := newTestRepo(t, map[string]string{
		"justfile": justfileFixture,
		"docs/todo/harness-universal-error-warning.md":   mkCaptureSignalDoc(rows...),
		"nix/packages/wt/wt-task":                        "x\n",
		"nix/packages/wt/skyhelm-boot":                   "x\n",
		"nix/packages/wt/codex-stop-hook":                "x\n",
		"nix/packages/sky-harness/sky-harness.sh":        "x\n",
		"nix/packages/skyler-tools/skyler-report-bug.sh": "x\n",
		"harness/skyhelm-boot":                           "x\n",
		"harness/skyhelm-no-source-edits.sh":             "x\n",
		"harness/opencode-rower-liveness.sh":             "x\n",
	})
	issues, err := CaptureSignalCoverage{}.Run(root)
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
		"| just switch | - | oos | operator-only |",
		"| nix/packages/wt/wt-task | - | unwrapped | n |",
		"| nix/packages/wt/skyhelm-* | - | unwrapped | n |",
		"| nix/packages/wt/codex-* | - | unwrapped | n |",
		"| nix/packages/sky-harness/sky-harness.sh | - | unwrapped | n |",
		"| nix/packages/skyler-tools/skyler-report-bug.sh | - | unwrapped | n |",
		"| harness/skyhelm-boot | - | unwrapped | n |",
		"| harness/skyhelm-no-source-edits.sh | - | unwrapped | n |",
		"| harness/opencode-rower-liveness.sh | - | unwrapped | n |",
	}
	root := newTestRepo(t, map[string]string{
		"justfile": justfileFixture,
		"docs/todo/harness-universal-error-warning.md":   mkCaptureSignalDoc(rows...),
		"nix/packages/wt/wt-task":                        "x\n",
		"nix/packages/wt/skyhelm-boot":                   "x\n",
		"nix/packages/wt/codex-stop-hook":                "x\n",
		"nix/packages/sky-harness/sky-harness.sh":        "x\n",
		"nix/packages/skyler-tools/skyler-report-bug.sh": "x\n",
		"harness/skyhelm-boot":                           "x\n",
		"harness/skyhelm-no-source-edits.sh":             "x\n",
		"harness/opencode-rower-liveness.sh":             "x\n",
	})
	issues, err := CaptureSignalCoverage{}.Run(root)
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
		"justfile": justfileFixture,
		"docs/todo/harness-universal-error-warning.md": "## Coverage Matrix\n\n(no rows)\n",
	})
	issues, err := CaptureSignalCoverage{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "could not parse") {
		t.Fatalf("expected empty-matrix issue, got %v", issues)
	}
}

func TestCaptureSignalCoverage_MissingDoc(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := CaptureSignalCoverage{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "missing doc") {
		t.Fatalf("expected missing-doc issue, got %v", issues)
	}
}
