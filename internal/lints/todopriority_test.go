package lints

import "testing"

func TestTodoPriority(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"docs/todo/README.md":   "convention only\n",
		"docs/todo/ok.md":       "**Status**: open\n**Priority**: queued\n",
		"docs/todo/missing.md":  "**Status**: open\nno priority here\n",
		"docs/todo/done.md":     "**Status**: done\nno priority needed\n",
		"docs/todo/bad-prio.md": "**Status**: open\n**Priority**: someday\n",
	})
	issues, err := TodoPriority{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := map[string]bool{}
	for _, i := range issues {
		got[i.Path] = true
	}
	if !got["docs/todo/missing.md"] || !got["docs/todo/bad-prio.md"] || got["docs/todo/ok.md"] || got["docs/todo/done.md"] || got["docs/todo/README.md"] {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestTodoPriority_NoDir(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	issues, err := TodoPriority{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %v", issues)
	}
}
