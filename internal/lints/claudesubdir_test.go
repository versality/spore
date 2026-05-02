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
			"# Bot rules",
			"`nix/features/bots/wingbot.nix` config",
			"`nix/features/bots/skyler.nix` config",
			"`nix/features/bots/lookout/cage.nix` cage",
			"`nix/features/bots/CLAUDE.md` rules",
		}, "\n") + "\n",
		"nix/features/bots/CLAUDE.md":         "# Bots\nBot-specific rules.\n",
		"nix/features/bots/wingbot.nix":        "{ }\n",
		"nix/features/bots/skyler.nix":         "{ }\n",
		"nix/features/bots/lookout/cage.nix":   "{ }\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Message, "nix/features/bots") {
		t.Fatalf("expected nix/features/bots in message, got %v", issues[0])
	}
}

func TestClaudeSubdir_OptOut(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"CLAUDE.md": strings.Join([]string{
			"# Bot rules",
			"<!-- lint: scope-ok -->",
			"`nix/features/bots/wingbot.nix` config",
			"`nix/features/bots/skyler.nix` config",
			"`nix/features/bots/lookout/cage.nix` cage",
			"`nix/features/bots/CLAUDE.md` rules",
		}, "\n") + "\n",
		"nix/features/bots/CLAUDE.md":         "# Bots\nBot-specific rules.\n",
		"nix/features/bots/wingbot.nix":        "{ }\n",
		"nix/features/bots/skyler.nix":         "{ }\n",
		"nix/features/bots/lookout/cage.nix":   "{ }\n",
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
		"CLAUDE.md": "# Small\n`nix/features/bots/one.nix` only one ref\n",
		"nix/features/bots/CLAUDE.md": "# Bots\nrules\n",
		"nix/features/bots/one.nix":   "{ }\n",
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
		"nix/features/bots/CLAUDE.md": strings.Join([]string{
			"# Bots",
			"`nix/features/bots/wingbot.nix` config",
			"`nix/features/bots/skyler.nix` config",
			"`nix/features/bots/lookout/cage.nix` cage",
		}, "\n") + "\n",
		"nix/features/bots/wingbot.nix":      "{ }\n",
		"nix/features/bots/skyler.nix":        "{ }\n",
		"nix/features/bots/lookout/cage.nix":  "{ }\n",
		"CLAUDE.md":                           "# Top\nok\n",
	})
	issues, err := ClaudeSubdir{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 (own scope), got %v", issues)
	}
}
