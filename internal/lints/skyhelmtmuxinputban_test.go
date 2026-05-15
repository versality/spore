package lints

import (
	"strings"
	"testing"
)

func TestSkyhelmTmuxInputBan_FlagsLiteralAgentTarget(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"bash/launch.sh":     "tmux send-keys -t skyhelm:0 \"hi\" Enter\n",
		"harness/poke.sh":    "tmux paste-buffer -t codex.0\n",
		"nix/features/x.nix": "let cmd = ''tmux send-keys -t opencode \"x\"''; in cmd\n",
	})
	issues, err := SkyhelmTmuxInputBan{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d: %v", len(issues), issues)
	}
	for _, i := range issues {
		if !strings.Contains(i.Message, "production code must not type into agent TUI") {
			t.Errorf("unexpected message: %q", i.Message)
		}
	}
}

func TestSkyhelmTmuxInputBan_FlagsControlPlaneAnyTarget(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"nix/packages/wt/main.sh":           "tmux send-keys -t $session \"go\" Enter\n",
		"nix/packages/wt-go/runner/main.go": "exec.Command(\"tmux\", \"paste-buffer\", \"-t\", target)\n",
	})
	issues, err := SkyhelmTmuxInputBan{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d: %v", len(issues), issues)
	}
	for _, i := range issues {
		if !strings.Contains(i.Message, "agent control plane must not invoke") {
			t.Errorf("unexpected message: %q", i.Message)
		}
	}
}

func TestSkyhelmTmuxInputBan_AllowsSelfTargetingViaSkipPath(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"harness/worker-loop.sh":  "tmux send-keys -t skyhelm:0 \"self-poke\"\n",
		"harness/skyhelm-boot.sh": "tmux send-keys -t skyhelm \"restart\"\n",
		"bash/other.sh":           "tmux send-keys -t skyhelm \"forbidden\"\n",
	})
	issues, err := SkyhelmTmuxInputBan{
		SkipPath: []string{"harness/worker-loop.sh", "harness/skyhelm-boot.sh"},
	}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (only bash/other.sh), got %d: %v", len(issues), issues)
	}
	if issues[0].Path != "bash/other.sh" {
		t.Errorf("expected hit on bash/other.sh, got %q", issues[0].Path)
	}
}

func TestSkyhelmTmuxInputBan_IgnoresNonTmuxMentions(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"bash/x.sh":          "echo \"skyhelm started\"\n",
		"harness/notes.sh":   "# describes codex behavior; not a tmux call\nls -la\n",
		"nix/features/y.nix": "{ description = \"claude integration\"; }\n",
	})
	issues, err := SkyhelmTmuxInputBan{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}

func TestSkyhelmTmuxInputBan_IgnoresCommentedOutForm(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"bash/x.sh":                      "#tmux send-keys -t skyhelm \"x\"\n# tmux send-keys -t codex \"y\"\n",
		"nix/packages/wt-go/runner/x.go": "// tmux send-keys -t any \"x\"\n// exec.Command(\"tmux\", \"send-keys\")\n",
	})
	issues, err := SkyhelmTmuxInputBan{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues on commented-out forms, got %v", issues)
	}
}

func TestSkyhelmTmuxInputBan_SkipsTestFixturesAndSelfCheck(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"harness/test-fixture.sh":                      "tmux send-keys -t skyhelm \"ok\"\n",
		"harness/check-skyhelm-tmux-input-ban.sh":      "tmux send-keys -t skyhelm \"matches itself\"\n",
		"nix/packages/wt-go/runner/main_test.go":       "tmux send-keys -t any \"go\"\n",
		"nix/packages/wt-go/runner/testdata/sample.sh": "tmux send-keys -t any \"go\"\n",
	})
	issues, err := SkyhelmTmuxInputBan{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues on allowlisted/self/test paths, got %v", issues)
	}
}
