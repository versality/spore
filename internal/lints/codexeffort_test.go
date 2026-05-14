package lints

import (
	"strings"
	"testing"
)

func taskFM(slug, agent, effort, status string) string {
	b := strings.Builder{}
	b.WriteString("---\n")
	b.WriteString("slug: " + slug + "\n")
	b.WriteString("title: fixture for " + slug + "\n")
	b.WriteString("host: host-a\n")
	b.WriteString("project: example-consumer\n")
	b.WriteString("agent: " + agent + "\n")
	b.WriteString("status: " + status + "\n")
	b.WriteString("created: 2026-05-09T00:00:00+00:00\n")
	if effort != "" {
		b.WriteString("effort: " + effort + "\n")
	}
	b.WriteString("---\n\nfixture body\n")
	return b.String()
}

func TestCodexEffortHighOnly(t *testing.T) {
	files := map[string]string{
		"tasks/ok-codex-high.md":       taskFM("ok-codex-high", "codex", "high", "active"),
		"tasks/bad-codex-medium.md":    taskFM("bad-codex-medium", "codex", "medium", "active"),
		"tasks/bad-codex-xhigh.md":     taskFM("bad-codex-xhigh", "codex", "xhigh", "active"),
		"tasks/bad-codex-veryhigh.md":  taskFM("bad-codex-veryhigh", "codex", "very-high", "active"),
		"tasks/bad-codex-veryhighu.md": taskFM("bad-codex-veryhighu", "codex", "very_high", "active"),
		"tasks/bad-codex-low.md":       taskFM("bad-codex-low", "codex", "low", "active"),
		"tasks/ok-codex-noeffort.md":   taskFM("ok-codex-noeffort", "codex", "", "active"),
		"tasks/ok-claude-medium.md":    taskFM("ok-claude-medium", "claude", "medium", "active"),
		"tasks/done-codex-medium.md":   taskFM("done-codex-medium", "codex", "medium", "done"),
		"tasks/parked-codex-medium.md": taskFM("parked-codex-medium", "codex", "medium", "parked"),
		"tasks/draft-codex-medium.md":  taskFM("draft-codex-medium", "codex", "medium", "draft"),
	}
	root := newTestRepo(t, files)
	issues, err := CodexEffortHighOnly{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	hit := map[string]string{}
	for _, i := range issues {
		hit[i.Path] = i.Message
	}
	wantHit := []string{
		"tasks/bad-codex-medium.md",
		"tasks/bad-codex-xhigh.md",
		"tasks/bad-codex-veryhigh.md",
		"tasks/bad-codex-veryhighu.md",
		"tasks/bad-codex-low.md",
		"tasks/parked-codex-medium.md",
		"tasks/draft-codex-medium.md",
	}
	wantSkip := []string{
		"tasks/ok-codex-high.md",
		"tasks/ok-codex-noeffort.md",
		"tasks/ok-claude-medium.md",
		"tasks/done-codex-medium.md",
	}
	for _, p := range wantHit {
		if _, ok := hit[p]; !ok {
			t.Errorf("missing expected hit on %s; got %v", p, hit)
		}
	}
	for _, p := range wantSkip {
		if _, ok := hit[p]; ok {
			t.Errorf("unexpected hit on %s: %s", p, hit[p])
		}
	}
}
