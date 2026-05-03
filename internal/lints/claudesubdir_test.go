package lints

import (
	"strings"
	"testing"
)

func TestClaudeSubdir_Clean(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": "# Top\nGeneral rules.\n`nix/foo.nix` and `docs/bar.md`.\n",
		"nix/a.nix": "{ }\n",
		"docs/b.md": "hi\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %v", issues)
	}
}

func TestClaudeSubdir_Dominated(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": strings.Join([]string{
			"# Plugin rules",
			"`src/plugins/alpha/main.go` config",
			"`src/plugins/beta/main.go` config",
			"`src/plugins/gamma/main.go` cage",
			"`src/plugins/CLAUDE.md` rules",
		}, "\n") + "\n",
		"src/plugins/CLAUDE.md":     "# Plugins\nPlugin-specific rules.\n",
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Message, "src/plugins") {
		t.Fatalf("expected src/plugins in message, got %v", issues[0])
	}
}

func TestClaudeSubdir_OptOut(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": strings.Join([]string{
			"# Plugin rules",
			"<!-- lint: scope-ok -->",
			"`src/plugins/alpha/main.go` config",
			"`src/plugins/beta/main.go` config",
			"`src/plugins/gamma/main.go` cage",
			"`src/plugins/CLAUDE.md` rules",
		}, "\n") + "\n",
		"src/plugins/CLAUDE.md":     "# Plugins\nPlugin-specific rules.\n",
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 with opt-out, got %v", issues)
	}
}

func TestClaudeSubdir_BelowThreshold(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md":             "# Small\n`src/plugins/one.go` only one ref\n",
		"src/plugins/CLAUDE.md": "# Plugins\nrules\n",
		"src/plugins/one.go":    "package plugins\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 (below min paths), got %v", issues)
	}
}

func TestClaudeSubdir_OwnScopeIgnored(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"src/plugins/CLAUDE.md": strings.Join([]string{
			"# Plugins",
			"`src/plugins/alpha/main.go` config",
			"`src/plugins/beta/main.go` config",
			"`src/plugins/gamma/main.go` cage",
		}, "\n") + "\n",
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
		"CLAUDE.md":                 "# Top\nok\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 (own scope), got %v", issues)
	}
}
