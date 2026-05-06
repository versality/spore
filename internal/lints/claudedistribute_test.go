package lints

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeDistribute_Clean(t *testing.T) {
	body := strings.Join([]string{
		"# Top",
		"General rule with `nix/foo.nix` and `docs/bar.md`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md": body,
		"nix/a.nix":   "{ }\n",
		"docs/b.md":   "hi\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected no candidates, got %v", cands)
	}
}

func TestClaudeDistribute_FlagsDominantSubdir(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"`src/plugins/delta/main.go` notes",
		"",
		"# Other",
		"Cross-cutting note.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
		"src/plugins/delta/main.go": "package delta\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %v", cands)
	}
	c := cands[0]
	if c.Subdir != "src" {
		t.Fatalf("subdir = %q, want src", c.Subdir)
	}
	if c.Name != "Plugin rules" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Percent != 100 {
		t.Fatalf("percent = %d, want 100", c.Percent)
	}
	if c.Line != 1 {
		t.Fatalf("line = %d, want 1", c.Line)
	}
}

func TestClaudeDistribute_HomePathMarkerSkips(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"<!-- homePath: src -->",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (already marked), got %v", cands)
	}
}

func TestClaudeDistribute_ScopeOkMarkerSkips(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"<!-- lint: scope-ok -->",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (scope-ok), got %v", cands)
	}
}

func TestClaudeDistribute_ExcludesIgnored(t *testing.T) {
	body := strings.Join([]string{
		"# Doc index",
		"`docs/a.md` and `docs/b.md` and `docs/c.md` and `docs/d.md`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md": body,
		"docs/a.md":   "a\n",
		"docs/b.md":   "b\n",
		"docs/c.md":   "c\n",
		"docs/d.md":   "d\n",
	})
	cands, err := ClaudeDistribute{
		Source:   "composer.md",
		Excludes: []string{"docs"},
	}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (docs excluded), got %v", cands)
	}
}

func TestClaudeDistribute_BelowMinPaths(t *testing.T) {
	body := strings.Join([]string{
		"# Few",
		"Only `src/plugins/one.go` referenced.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":           body,
		"src/plugins/one.go":    "package plugins\n",
		"src/plugins/CLAUDE.md": "rules\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (below min paths), got %v", cands)
	}
}

func TestClaudeDistribute_BelowFloorPercent(t *testing.T) {
	body := strings.Join([]string{
		"# Mixed",
		"`src/a.go` and `nix/b.nix` and `docs/c.md` and `bash/d.sh`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md": body,
		"src/a.go":    "package src\n",
		"nix/b.nix":   "{}\n",
		"docs/c.md":   "doc\n",
		"bash/d.sh":   "echo\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (no dominant), got %v", cands)
	}
}

func TestClaudeDistribute_IgnoresAbsoluteAndUrl(t *testing.T) {
	body := strings.Join([]string{
		"# Mixed",
		"References `/nix/store/abc/foo` and `https://example.com/x` and `re/.*\\.nix`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md": body,
		"src/a.go":    "package src\n",
	})
	cands, err := ClaudeDistribute{Source: "composer.md"}.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 (no plain path tokens), got %v", cands)
	}
}

func TestClaudeDistribute_RunEmitsIssues(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
	})
	issues, err := ClaudeDistribute{Source: "composer.md"}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if !strings.Contains(issues[0].Message, "homePath: src") {
		t.Fatalf("message = %q", issues[0].Message)
	}
}

func TestClaudeDistribute_ApplyMarkersIdempotent(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
		"# Cross-cutting",
		"`src/a.go` plus `nix/b.nix` plus `docs/c.md`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
		"src/a.go":                  "package src\n",
		"nix/b.nix":                 "{}\n",
		"docs/c.md":                 "d\n",
	})
	l := ClaudeDistribute{Source: "composer.md"}
	cands, err := l.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %v", cands)
	}
	if err := l.ApplyMarkers(root, cands); err != nil {
		t.Fatalf("ApplyMarkers: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "composer.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "<!-- homePath: src -->") {
		t.Fatalf("missing marker in output:\n%s", got)
	}
	if !strings.HasPrefix(string(got), "# Plugin rules\n\n<!-- homePath: src -->\n") {
		t.Fatalf("marker not placed under heading:\n%s", got)
	}

	cands2, err := l.Scan(root)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(cands2) != 0 {
		t.Fatalf("expected idempotent, got %v", cands2)
	}
	if err := l.ApplyMarkers(root, cands2); err != nil {
		t.Fatalf("second ApplyMarkers: %v", err)
	}
	got2, err := os.ReadFile(filepath.Join(root, "composer.md"))
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if string(got) != string(got2) {
		t.Fatalf("file changed on second apply:\nbefore:\n%s\nafter:\n%s", got, got2)
	}
}

func TestClaudeDistribute_ApplyMultipleSectionsKeepsLines(t *testing.T) {
	body := strings.Join([]string{
		"# Plugin rules",
		"`src/plugins/alpha/main.go` config",
		"`src/plugins/beta/main.go` config",
		"`src/plugins/gamma/main.go` cage",
		"",
		"# Nix only",
		"`nix/a.nix` plus `nix/b.nix` plus `nix/c.nix`.",
		"",
	}, "\n")
	root := newTestRepo(t, map[string]string{
		"composer.md":               body,
		"src/plugins/alpha/main.go": "package alpha\n",
		"src/plugins/beta/main.go":  "package beta\n",
		"src/plugins/gamma/main.go": "package gamma\n",
		"nix/a.nix":                 "{}\n",
		"nix/b.nix":                 "{}\n",
		"nix/c.nix":                 "{}\n",
	})
	l := ClaudeDistribute{Source: "composer.md"}
	cands, err := l.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %v", cands)
	}
	if err := l.ApplyMarkers(root, cands); err != nil {
		t.Fatalf("ApplyMarkers: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "composer.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := string(got)
	if !strings.Contains(out, "# Plugin rules\n\n<!-- homePath: src -->\n") {
		t.Fatalf("first marker missing:\n%s", out)
	}
	if !strings.Contains(out, "# Nix only\n\n<!-- homePath: nix -->\n") {
		t.Fatalf("second marker missing:\n%s", out)
	}
}
