package lints

import (
	"strings"
	"testing"
)

func TestAgentMirror_NoDrift(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md":                 "# Root\nrules\n",
		"AGENTS.md":                 "# Root\nrules\n",
		"internal/infect/CLAUDE.md": "# Infect\nrules\n",
		"internal/infect/AGENTS.md": "# Infect\nrules\n",
	})
	issues, err := AgentMirror{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %v", issues)
	}
}

func TestAgentMirror_MissingAgents(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"internal/infect/CLAUDE.md": "# Infect\nrules\n",
	})
	issues, err := AgentMirror{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "internal/infect/AGENTS.md" {
		t.Fatalf("expected missing AGENTS issue, got %v", issues)
	}
}

func TestAgentMirror_DetectsDrift(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": "# Root\nrules\n",
		"AGENTS.md": "# Root\nstale\n",
	})
	issues, err := AgentMirror{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "drift") {
		t.Fatalf("expected one drift issue, got %v", issues)
	}
	if issues[0].Path != "AGENTS.md" {
		t.Fatalf("issue path: got %q want AGENTS.md", issues[0].Path)
	}
}
