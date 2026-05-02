package state

import (
	"strings"
	"testing"
)

const sampleState = `## Active tasks

| slug | status | note |
| ---- | ------ | ---- |
| fix-auth | active | wip |
| add-tests | paused | blocked on CI |

## Recent events

- spawned fix-auth
- reaped old-task

## Rules

### CRITICAL LESSON: always check inbox

harness: coordinator-verify

Body of the rule.

### RULE: no force push

More text here.

## Directives

Stand down at 22:00.
`

func TestParseRoundTrip(t *testing.T) {
	doc := Parse([]byte(sampleState))
	if len(doc.Sections) == 0 {
		t.Fatal("expected sections")
	}

	sec := doc.FindSection("Active tasks")
	if sec == nil {
		t.Fatal("expected Active tasks section")
	}
	if sec.Level != 2 {
		t.Errorf("Active tasks level = %d, want 2", sec.Level)
	}

	sec = doc.FindSection("Directives")
	if sec == nil {
		t.Fatal("expected Directives section")
	}
	if !strings.Contains(sec.Body, "Stand down") {
		t.Errorf("Directives body missing expected text")
	}
}

func TestParseTaskTable(t *testing.T) {
	doc := Parse([]byte(sampleState))
	sec := doc.FindSection("Active tasks")
	if sec == nil {
		t.Fatal("expected Active tasks section")
	}
	rows := ParseTaskTable(sec.Body)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Slug != "fix-auth" {
		t.Errorf("row[0].Slug = %q, want fix-auth", rows[0].Slug)
	}
	if rows[0].Status != "active" {
		t.Errorf("row[0].Status = %q, want active", rows[0].Status)
	}
	if rows[1].Note != "blocked on CI" {
		t.Errorf("row[1].Note = %q, want 'blocked on CI'", rows[1].Note)
	}
}

func TestRenderTaskTable(t *testing.T) {
	rows := []TaskRow{
		{Slug: "a", Status: "active", Note: "wip"},
		{Slug: "b", Status: "done", Note: ""},
	}
	out := RenderTaskTable(rows)
	if !strings.Contains(out, "| a | active | wip |") {
		t.Errorf("missing row a in:\n%s", out)
	}
	if !strings.Contains(out, "| b | done |  |") {
		t.Errorf("missing row b in:\n%s", out)
	}
}

func TestFindSectionCaseInsensitive(t *testing.T) {
	doc := Parse([]byte(sampleState))
	sec := doc.FindSection("active tasks")
	if sec == nil {
		t.Fatal("case-insensitive lookup failed")
	}
}

func TestFindSectionH3(t *testing.T) {
	doc := Parse([]byte(sampleState))
	sec := doc.FindSection("CRITICAL LESSON: always check inbox")
	if sec == nil {
		t.Fatal("expected to find H3 section")
	}
	if sec.Level != 3 {
		t.Errorf("expected level 3, got %d", sec.Level)
	}
	if !strings.Contains(sec.Body, "harness:") {
		t.Errorf("expected body with harness pointer")
	}
}

func TestWritePreservesStructure(t *testing.T) {
	input := "## First\n\nBody one.\n\n## Second\n\nBody two.\n"
	doc := Parse([]byte(input))
	out := string(Write(doc))
	if out != input {
		t.Errorf("round-trip mismatch:\ngot:\n%s\nwant:\n%s", out, input)
	}
}

func TestParseEmpty(t *testing.T) {
	doc := Parse([]byte(""))
	if len(doc.Sections) != 0 {
		t.Errorf("expected 0 sections from empty input, got %d", len(doc.Sections))
	}
}

func TestParsePreamble(t *testing.T) {
	input := "Some preamble text\n\n## Section\n\nBody.\n"
	doc := Parse([]byte(input))
	if len(doc.Sections) < 2 {
		t.Fatalf("expected 2+ sections, got %d", len(doc.Sections))
	}
	if doc.Sections[0].Level != 0 {
		t.Errorf("preamble level = %d, want 0", doc.Sections[0].Level)
	}
}
