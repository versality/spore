package lints

import (
	"strings"
	"testing"
)

func TestClaudeSize_Short(t *testing.T) {
	body := "# Short\nline1\nline2\n"
	root := newTestRepo(t, map[string]string{"CLAUDE.md": body})
	issues, err := ClaudeSize{Limit: 5}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %v", issues)
	}
}

func TestClaudeSize_Overlong(t *testing.T) {
	lines := []string{"# Big"}
	for i := 0; i < 10; i++ {
		lines = append(lines, "filler")
	}
	lines = append(lines, "# Small", "ok")
	body := strings.Join(lines, "\n") + "\n"

	root := newTestRepo(t, map[string]string{"CLAUDE.md": body})
	issues, err := ClaudeSize{Limit: 5}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Message, `"Big"`) {
		t.Fatalf("expected Big section flagged, got %v", issues[0])
	}
}

func TestClaudeSize_OptOut(t *testing.T) {
	lines := []string{"# Big"}
	for i := 0; i < 10; i++ {
		lines = append(lines, "filler")
	}
	lines = append(lines, "<!-- lint: size-ok -->")
	body := strings.Join(lines, "\n") + "\n"

	root := newTestRepo(t, map[string]string{"CLAUDE.md": body})
	issues, err := ClaudeSize{Limit: 5}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues with opt-out, got %v", issues)
	}
}

func TestClaudeSize_SubdirFile(t *testing.T) {
	lines := []string{"# Rules"}
	for i := 0; i < 10; i++ {
		lines = append(lines, "rule")
	}
	body := strings.Join(lines, "\n") + "\n"

	root := newTestRepo(t, map[string]string{
		"sub/CLAUDE.md": body,
		"CLAUDE.md":     "# Top\nok\n",
	})
	issues, err := ClaudeSize{Limit: 5}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue in sub/CLAUDE.md, got %v", issues)
	}
	if issues[0].Path != "sub/CLAUDE.md" {
		t.Fatalf("expected sub/CLAUDE.md, got %s", issues[0].Path)
	}
}
