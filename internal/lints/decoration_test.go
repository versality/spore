package lints

import "testing"

func TestDecoration_Patterns(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"hash-dashes", "# ---", true},
		{"hash-dashes-label", "# --- foo ---", true},
		{"double-semi-equals", ";; ====", true},
		{"slash-dashes", "// ---", true},
		{"block-equals", "/* ===", true},
		{"two-dashes-ok", "# --", false},
		{"normal-comment", "# just a comment", false},
		{"code-line", "x = 1", false},
		{"hash-stars", "# ***", true},
		{"hash-underscores", "# ___", true},
		{"hash-tildes", "# ~~~", true},
		{"indented-banner", "  # ---", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reDecoration.MatchString(tc.line)
			if got != tc.want {
				t.Fatalf("reDecoration.MatchString(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestDecoration_RunInRepo(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"clean.go":  "package x\n// helper for something\nfunc x() {}\n",
		"banner.sh": "#!/bin/bash\n# --- section ---\necho hi\n",
		"ok.nix":    "{ config }: {\n  x = 1;\n}\n",
	})
	issues, err := Decoration{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %v", issues)
	}
	if issues[0].Path != "banner.sh" || issues[0].Line != 2 {
		t.Fatalf("unexpected issue: %v", issues[0])
	}
}
