package lints

import (
	"sort"
	"strings"
	"testing"
)

func taskNeedsMD(slug, status, needsBlock string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("slug: " + slug + "\n")
	b.WriteString("title: t\n")
	b.WriteString("status: " + status + "\n")
	if needsBlock != "" {
		b.WriteString(needsBlock)
		if !strings.HasSuffix(needsBlock, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n")
	return b.String()
}

func TestTaskNeeds(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string // substrings expected in issue messages
	}{
		{
			name: "no tasks dir is a no-op",
			files: map[string]string{
				"README.md": "hi\n",
			},
		},
		{
			name: "absent needs is fine",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", ""),
			},
		},
		{
			name: "empty needs header is fine",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n"),
			},
		},
		{
			name: "needs: [] is fine",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs: []\n"),
			},
		},
		{
			name: "valid block list",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - b\n"),
				"tasks/b.md": taskNeedsMD("b", "active", ""),
			},
		},
		{
			name: "scalar shape rejected",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs: foo\n"),
			},
			want: []string{"must be a YAML block list (got: foo)"},
		},
		{
			name: "inline list shape rejected",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs: [a, b]\n"),
			},
			want: []string{"must be a YAML block list (got: [a, b])"},
		},
		{
			name: "missing dep reported",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - ghost\n"),
			},
			want: []string{"needs: 'ghost' has no task file (tasks/ghost.md)"},
		},
		{
			name: "self-reference reported",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - a\n"),
			},
			want: []string{"needs: contains self-reference 'a'"},
		},
		{
			name: "blocked dep does not fail",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - b\n"),
				"tasks/b.md": taskNeedsMD("b", "blocked", ""),
			},
		},
		{
			name: "two-node cycle",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - b\n"),
				"tasks/b.md": taskNeedsMD("b", "active", "needs:\n  - a\n"),
			},
			want: []string{"cycle detected:"},
		},
		{
			name: "three-node cycle",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - b\n"),
				"tasks/b.md": taskNeedsMD("b", "active", "needs:\n  - c\n"),
				"tasks/c.md": taskNeedsMD("c", "active", "needs:\n  - a\n"),
			},
			want: []string{"cycle detected:"},
		},
		{
			name: "cycle reported once per ring",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - b\n"),
				"tasks/b.md": taskNeedsMD("b", "active", "needs:\n  - a\n"),
				"tasks/c.md": taskNeedsMD("c", "active", "needs:\n  - a\n"),
			},
			want: []string{"cycle detected:"},
		},
		{
			name: "self-ref does not double-fire as cycle",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - a\n"),
			},
			want: []string{"self-reference"},
		},
		{
			name: "missing-dep does not double-fire as cycle",
			files: map[string]string{
				"tasks/a.md": taskNeedsMD("a", "active", "needs:\n  - ghost\n"),
			},
			want: []string{"has no task file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			issues, err := TaskNeeds{}.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, i.Message)
			}
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("issue count: got %d want %d (issues=%v)", len(got), len(tt.want), issues)
			}
			for i, sub := range tt.want {
				if !strings.Contains(got[i], sub) {
					t.Fatalf("issue[%d]=%q missing %q (all=%v)", i, got[i], sub, got)
				}
			}
		})
	}
}
