package lints

import (
	"sort"
	"strings"
	"testing"
)

func repeatLines(prefix string, n int) string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		lines[i] = prefix
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestClaudeTotalSize_RootLineCapAndSubdirAndOptOut(t *testing.T) {
	rootBig := repeatLines("filler", 12)
	rootSmall := "# Top\nok\n"
	subBig := repeatLines("rule", 8)
	subSmall := "# Sub\nok\n"
	rootOptOut := repeatLines("filler", 12) + "<!-- lint: totalsize-ok -->\n"

	root := newTestRepo(t, map[string]string{
		"CLAUDE.md":       rootBig,
		"AGENTS.md":       rootOptOut,
		"sub/CLAUDE.md":   subBig,
		"sub/AGENTS.md":   subSmall,
		"sub/nested/x.md": "noise\n",
		"other/CLAUDE.md": rootSmall,
	})

	lint := ClaudeTotalSize{
		RootLineLimit:   10,
		RootCharLimit:   1000,
		SubdirLineLimit: 5,
	}
	issues, err := lint.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var got []string
	for _, i := range issues {
		got = append(got, i.Path)
	}
	sort.Strings(got)
	want := []string{"CLAUDE.md", "sub/CLAUDE.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}

	for _, i := range issues {
		switch i.Path {
		case "CLAUDE.md":
			if !strings.Contains(i.Message, "lines /") || !strings.Contains(i.Message, "chars") {
				t.Fatalf("root msg shape wrong: %q", i.Message)
			}
		case "sub/CLAUDE.md":
			if strings.Contains(i.Message, "chars") {
				t.Fatalf("subdir msg should not mention chars: %q", i.Message)
			}
			if !strings.Contains(i.Message, "lines (limit:") {
				t.Fatalf("subdir msg shape wrong: %q", i.Message)
			}
		}
		if i.Line != 0 {
			t.Fatalf("expected whole-file (Line=0), got %d for %s", i.Line, i.Path)
		}
	}
}

func TestClaudeTotalSize_RootCharCap(t *testing.T) {
	body := strings.Repeat("a", 200) + "\n"
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": body,
	})

	lint := ClaudeTotalSize{
		RootLineLimit:   1000,
		RootCharLimit:   100,
		SubdirLineLimit: 1000,
	}
	issues, err := lint.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if issues[0].Path != "CLAUDE.md" {
		t.Fatalf("expected CLAUDE.md, got %s", issues[0].Path)
	}
}

func TestClaudeTotalSize_AllUnderCap(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md":     "# Top\nok\n",
		"AGENTS.md":     "# Agents\nok\n",
		"sub/CLAUDE.md": "# Sub\nok\n",
	})
	issues, err := ClaudeTotalSize{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %v", issues)
	}
}
