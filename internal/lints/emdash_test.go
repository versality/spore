package lints

import (
	"strings"
	"testing"
)

const (
	em = "\u2014"
	en = "\u2013"
)

func TestEmDash_ScanGoldens(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []int
	}{
		{"clean", "no dashes here\nplain hyphen - still fine\n", nil},
		{"em-dash", "first line\nthis " + em + " here\n", []int{2}},
		{"en-dash", "this " + en + " here\n", []int{1}},
		{"both", em + "\n" + en + "\n", []int{1, 2}},
		{"multiline-clean", "line one\nline two\nline three\n", nil},
		{"invalid-utf8", string([]byte{0xff, 0xfe, 0x00, 0x00}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanEmDash(strings.NewReader(tc.body), "x")
			if len(got) != len(tc.want) {
				t.Fatalf("issue count: got %d (%v), want %d", len(got), got, len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].Line != want {
					t.Errorf("issue %d: line=%d want %d", i, got[i].Line, want)
				}
			}
		})
	}
}

func TestEmDash_RunInRepo(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"clean.go":  "package x\n",
		"dirty.go":  "package x\n// note " + em + " here\n",
		"image.png": string([]byte{0x89, 0x50, 0x4e, 0x47}),
	})
	issues, err := EmDash{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "dirty.go" || issues[0].Line != 2 {
		t.Fatalf("expected one dirty.go:2 issue, got %v", issues)
	}
}

func TestEmDash_AllowlistSkipsRule(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"rules/core/no-emdash.md": "Don't use " + em + " or " + en + ".\n",
		"other.md":                "stray " + em + " here\n",
	})
	issues, err := EmDash{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "other.md" {
		t.Fatalf("expected one other.md issue (allowlist skips rules/core/no-emdash.md), got %v", issues)
	}
}
