package lints

import (
	"strings"
	"testing"
)

func TestCommentNoise_SectionLabel(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "label-above-real-line",
			body: "// Logging\nlog.Print(\"x\")\n",
			want: 1,
		},
		{
			name: "label-followed-by-comment-clean",
			body: "// Logging\n// helper for the logger\nlog.Print(\"x\")\n",
			want: 0,
		},
		{
			name: "label-then-blank-then-real",
			body: "// Logging\n\nlog.Print(\"x\")\n",
			want: 1,
		},
		{
			name: "label-followed-by-closer",
			body: "// Logging\n}\n",
			want: 0,
		},
		{
			name: "label-with-trailing-colon-allowed",
			body: "// Logging:\nlog.Print(\"x\")\n",
			want: 0,
		},
		{
			name: "lowercase-not-a-label",
			body: "// helper\nlog.Print(\"x\")\n",
			want: 0,
		},
		{
			name: "four-words-not-a-label",
			body: "// One Two Three Four\nlog.Print(\"x\")\n",
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanCommentNoise(strings.NewReader(tc.body), "x")
			labels := 0
			for _, i := range got {
				if strings.Contains(i.Message, "section-label") {
					labels++
				}
			}
			if labels != tc.want {
				t.Fatalf("section-label hits: got %d want %d (issues=%v)", labels, tc.want, got)
			}
		})
	}
}

func TestCommentNoise_BareTodo(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bare-todo", "// TODO: refactor this\n", 1},
		{"todo-with-issue", "// TODO(#42): refactor\n", 0},
		{"todo-with-url", "// TODO: see https://example.com/x\n", 0},
		{"todo-link-on-followup-line", "// TODO: fix this\n// see https://example.com\n", 0},
		{"fixme-bare", "// FIXME bug here\n", 1},
		{"plain-comment", "// just a note\n", 0},
		{"todo-with-see-file", "// TODO drop this\n// see docs/plan.md\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanCommentNoise(strings.NewReader(tc.body), "x")
			todos := 0
			for _, i := range got {
				if strings.Contains(i.Message, "bare TODO") {
					todos++
				}
			}
			if todos != tc.want {
				t.Fatalf("bare-todo hits: got %d want %d (issues=%v)", todos, tc.want, got)
			}
		})
	}
}

func TestCommentNoise_DatedEvent(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"tested-date", "// Tested 2025-04-18 on staging\n", 1},
		{"as-of", "// as of 2026-01 the API returns 200\n", 1},
		{"shipped-date", "// Shipped 2024-11-30 in v2\n", 1},
		{"plain-date", "// the 2025-04-18 release notes\n", 0},
		{"present-tense", "// the API returns 200\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanCommentNoise(strings.NewReader(tc.body), "x")
			dated := 0
			for _, i := range got {
				if strings.Contains(i.Message, "dated event ref") {
					dated++
				}
			}
			if dated != tc.want {
				t.Fatalf("dated-event hits: got %d want %d (issues=%v)", dated, tc.want, got)
			}
		})
	}
}

func TestCommentNoise_RunInRepo(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"clean.go": "package x\n// helper for the logger\nfunc x() {}\n",
		"noisy.go": "package x\n// Logging\nfunc log() {}\n",
	})
	issues, err := CommentNoise{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "noisy.go" {
		t.Fatalf("expected one noisy.go issue, got %v", issues)
	}
}
