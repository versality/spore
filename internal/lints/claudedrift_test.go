package lints

import (
	"strings"
	"testing"
)

func setupDriftRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	all := map[string]string{
		"rules/core/a.md": "# a body",
		"rules/core/b.md": "## b body",
	}
	for k, v := range files {
		all[k] = v
	}
	return newTestRepo(t, all)
}

func TestClaudeDrift_NoDrift(t *testing.T) {
	root := setupDriftRepo(t, map[string]string{
		"rules/consumers/host.txt": "# target: HOST.md\ncore/a\ncore/b\n",
		"HOST.md":                  "# a body\n\n## b body\n",
	})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %v", issues)
	}
}

func TestClaudeDrift_MultipleTargets(t *testing.T) {
	root := setupDriftRepo(t, map[string]string{
		"rules/consumers/host.txt": "# target: CLAUDE.md\n# target: AGENTS.md\ncore/a\n",
		"CLAUDE.md":                "# a body\n",
		"AGENTS.md":                "stale content\n",
	})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "AGENTS.md" {
		t.Fatalf("expected one AGENTS.md drift issue, got %v", issues)
	}
}

func TestClaudeDrift_DetectsDrift(t *testing.T) {
	root := setupDriftRepo(t, map[string]string{
		"rules/consumers/host.txt": "# target: HOST.md\ncore/a\ncore/b\n",
		"HOST.md":                  "stale content\n",
	})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "drift") {
		t.Fatalf("expected one drift issue, got %v", issues)
	}
	if issues[0].Path != "HOST.md" {
		t.Fatalf("issue path: got %q want HOST.md", issues[0].Path)
	}
}

func TestClaudeDrift_MissingTarget(t *testing.T) {
	root := setupDriftRepo(t, map[string]string{
		"rules/consumers/host.txt": "# target: HOST.md\ncore/a\n",
	})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "missing render target") {
		t.Fatalf("expected one missing-target issue, got %v", issues)
	}
}

func TestClaudeDrift_NoTargetDirectiveSkipped(t *testing.T) {
	root := setupDriftRepo(t, map[string]string{
		"rules/consumers/host.txt": "# just a description\ncore/a\n",
	})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues for consumer with no target directive, got %v", issues)
	}
}

func TestClaudeDrift_NoConsumersDir(t *testing.T) {
	root := newTestRepo(t, map[string]string{"a.go": "package x\n"})
	issues, err := ClaudeDrift{ConsumersDir: "rules/consumers", RulesDir: "rules"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues when consumers dir absent, got %v", issues)
	}
}
