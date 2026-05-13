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

func TestClaudeDrift_ConsumersCmd_NoDrift(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"HOST.md":   "# rendered host\n",
		"AGENTS.md": "# rendered agents\n",
	})
	cmd := `printf '%s' '[` +
		`{"name":"host","target_path":"HOST.md","rendered_text":"# rendered host\n"},` +
		`{"name":"agents","target_path":"AGENTS.md","rendered_text":"# rendered agents\n"}` +
		`]'`
	issues, err := ClaudeDrift{ConsumersCmd: cmd}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %v", issues)
	}
}

func TestClaudeDrift_ConsumersCmd_DetectsDriftAndMissing(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"HOST.md": "stale\n",
	})
	cmd := `printf '%s' '[` +
		`{"name":"host","target_path":"HOST.md","rendered_text":"fresh\n"},` +
		`{"name":"agents","target_path":"AGENTS.md","rendered_text":"# rendered agents\n"}` +
		`]'`
	issues, err := ClaudeDrift{ConsumersCmd: cmd}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %v", issues)
	}
	byPath := map[string]string{}
	for _, i := range issues {
		byPath[i.Path] = i.Message
	}
	if !strings.Contains(byPath["HOST.md"], "drift") {
		t.Fatalf("HOST.md: expected drift message, got %q", byPath["HOST.md"])
	}
	if !strings.Contains(byPath["AGENTS.md"], "missing render target") {
		t.Fatalf("AGENTS.md: expected missing-target message, got %q", byPath["AGENTS.md"])
	}
}

func TestClaudeDrift_ConsumersCmd_BypassesFileLayout(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"HOST.md": "# rendered host\n",
	})
	cmd := `printf '%s' '[{"name":"host","target_path":"HOST.md","rendered_text":"# rendered host\n"}]'`
	issues, err := ClaudeDrift{
		ConsumersCmd: cmd,
		ConsumersDir: "does/not/exist",
		RenderCmd:    "false",
	}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues (consumers_cmd should win), got %v", issues)
	}
}

func TestClaudeDrift_ConsumersCmd_BadJSON(t *testing.T) {
	root := newTestRepo(t, map[string]string{"a.go": "package x\n"})
	_, err := ClaudeDrift{ConsumersCmd: `printf 'not json'`}.Run(root)
	if err == nil || !strings.Contains(err.Error(), "parse JSON") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestClaudeDrift_ConsumersCmd_EmptyTargetPath(t *testing.T) {
	root := newTestRepo(t, map[string]string{"a.go": "package x\n"})
	cmd := `printf '%s' '[{"name":"host","target_path":"","rendered_text":"x"}]'`
	_, err := ClaudeDrift{ConsumersCmd: cmd}.Run(root)
	if err == nil || !strings.Contains(err.Error(), "empty target_path") {
		t.Fatalf("expected empty target_path error, got %v", err)
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
