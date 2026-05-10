package lints

import "testing"

func TestOverviewDrift_Match(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"OVERVIEW.md": "rendered content\n",
	})
	l := OverviewDrift{
		Target:    "OVERVIEW.md",
		RenderCmd: []string{"printf", "rendered content\n"},
	}
	issues, err := l.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no drift, got %v", issues)
	}
}

func TestOverviewDrift_Mismatch(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"OVERVIEW.md": "stale\n",
	})
	l := OverviewDrift{
		Target:    "OVERVIEW.md",
		RenderCmd: []string{"printf", "fresh\n"},
	}
	issues, err := l.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "OVERVIEW.md" {
		t.Fatalf("expected one OVERVIEW.md issue, got %v", issues)
	}
}

func TestOverviewDrift_Missing(t *testing.T) {
	root := newTestRepo(t, map[string]string{"README.md": "x\n"})
	l := OverviewDrift{
		Target:    "OVERVIEW.md",
		RenderCmd: []string{"printf", "fresh\n"},
	}
	issues, err := l.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected missing-target issue, got %v", issues)
	}
}
