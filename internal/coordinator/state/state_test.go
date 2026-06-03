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

	if sec := doc.FindSection("Active tasks"); sec == nil {
		t.Fatal("expected Active tasks section")
	} else if sec.Level != 2 {
		t.Errorf("Active tasks level = %d, want 2", sec.Level)
	}

	if sec := doc.FindSection("Directives"); sec == nil {
		t.Fatal("expected Directives section")
	} else if !strings.Contains(sec.Body, "Stand down") {
		t.Errorf("Directives body missing expected text")
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
		return
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
