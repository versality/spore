package lints

import (
	"strings"
	"testing"
)

func configuredLint(t *testing.T, root string, lint Lint) Lint {
	t.Helper()
	cfg, err := LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	return ApplyConfig([]Lint{lint}, cfg)[0]
}

func TestLintConfig_EmDashAllowlist(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.emdash]
allowlist = ["allowed.md"]
`,
		"allowed.md": "allowed " + em + "\n",
		"other.md":   "stray " + em + "\n",
	})
	issues, err := configuredLint(t, root, EmDash{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "other.md" {
		t.Fatalf("expected only other.md, got %v", issues)
	}
}

func TestLintConfig_ClaudeDriftExternalRender(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.claude-drift]
consumers_dir = "consumers"
rules_dir = "rules"
render_cmd = "printf '%s\n' '# rendered'"
`,
		"consumers/host.txt": "# target: HOST.md\nignored\n",
		"HOST.md":            "# rendered\n",
	})
	issues, err := configuredLint(t, root, ClaudeDrift{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected zero issues, got %v", issues)
	}
}

func TestLintConfig_ClaudeDriftConsumersCmd(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.claude-drift]
consumers_cmd = "sh consumers.sh"
`,
		"consumers.sh": `#!/bin/sh
cat <<'JSON'
[{"name":"host","target_path":"HOST.md","rendered_text":"fresh\n"}]
JSON
`,
		"HOST.md": "stale\n",
	})
	issues, err := configuredLint(t, root, ClaudeDrift{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "HOST.md" || !strings.Contains(issues[0].Message, "drift") {
		t.Fatalf("expected one HOST.md drift issue, got %v", issues)
	}
}

func TestLintConfig_FileSizeLimitAndExt(t *testing.T) {
	long := strings.Repeat("x\n", 6)
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.filesize]
limit = 5
ext = [".sh"]
`,
		"big.go": long,
		"big.sh": long,
	})
	issues, err := configuredLint(t, root, FileSize{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "big.sh" {
		t.Fatalf("expected only big.sh, got %v", issues)
	}
}

func TestLintConfig_ClaudeTotalSizeLimitsAndLegacyMarker(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.claude-totalsize]
root_line_limit = 2
root_char_limit = 1000
subdir_line_limit = 2
`,
		"AGENTS.md":      "a\nb\nc\n",
		"sub/AGENTS.md":  "a\nb\nc\n",
		"sub2/AGENTS.md": "a\nb\nc\n<!-- lint: file-size-ok -->\n",
		"sub3/AGENTS.md": "a\nb\n",
		"sub4/README.md": "a\nb\nc\n",
	})
	issues, err := configuredLint(t, root, ClaudeTotalSize{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected root and subdir issues only, got %v", issues)
	}
}

func TestLintConfig_CommentNoiseExtAndSkipPath(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.comment-noise]
ext = [".sh"]
skip_path = ["templates/"]
`,
		"noisy.go":           "package x\n// Logging\nfunc x() {}\n",
		"noisy.sh":           "# Logging\necho hi\n",
		"templates/noisy.sh": "# Logging\necho hi\n",
	})
	issues, err := configuredLint(t, root, CommentNoise{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "noisy.sh" {
		t.Fatalf("expected only noisy.sh, got %v", issues)
	}
}

func TestLintConfig_DecorationExtAndSkipPath(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"spore.toml": `[lint.decoration]
ext = [".sh"]
skip_path = ["templates/"]
`,
		"banner.go":           "package x\n// ---\n",
		"banner.sh":           "# ---\necho hi\n",
		"templates/banner.sh": "# ---\necho hi\n",
	})
	issues, err := configuredLint(t, root, Decoration{}).Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "banner.sh" {
		t.Fatalf("expected only banner.sh, got %v", issues)
	}
}
