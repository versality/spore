package lints

import (
	"sort"
	"strings"
	"testing"
)

func TestTaskStatus(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "all canonical statuses accepted",
			files: map[string]string{
				"tasks/a.md": "---\nslug: a\nstatus: active\n---\nbody\n",
				"tasks/b.md": "---\nslug: b\nstatus: draft\n---\nbody\n",
				"tasks/c.md": "---\nslug: c\nstatus: blocked\n---\nbody\n",
				"tasks/d.md": "---\nslug: d\nstatus: done\n---\nbody\n",
			},
			want: nil,
		},
		{
			name: "empty / missing status is allowed",
			files: map[string]string{
				"tasks/a.md": "---\nslug: a\nstatus:\n---\nbody\n",
				"tasks/b.md": "---\nslug: b\n---\nbody\n",
			},
			want: nil,
		},
		{
			name: "out-of-set value flagged with line",
			files: map[string]string{
				"tasks/bad.md": "---\nslug: bad\nstatus: parked\n---\nbody\n",
			},
			want: []string{"tasks/bad.md:3"},
		},
		{
			name: "typo flagged",
			files: map[string]string{
				"tasks/typo.md": "---\nslug: typo\ntitle: x\nstatus: acitve\n---\nbody\n",
			},
			want: []string{"tasks/typo.md:4"},
		},
		{
			name: "non-md files ignored",
			files: map[string]string{
				"tasks/README.md": "no frontmatter here\n",
				"tasks/notes.txt": "---\nstatus: bogus\n---\n",
			},
			want: nil,
		},
		{
			name:  "missing tasks/ is a no-op",
			files: map[string]string{"other.md": "x\n"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			issues, err := TaskStatus{}.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, locTag(i))
			}
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !equalStrings(got, want) {
				t.Fatalf("issues: got %v want %v (full: %v)", got, want, issues)
			}
		})
	}
}

func TestTaskStatusMessageMentionsValue(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/x.md": "---\nslug: x\nstatus: weird\n---\nbody\n",
	})
	issues, err := TaskStatus{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Message, `"weird"`) {
		t.Errorf("message %q missing offending value", issues[0].Message)
	}
	if !strings.Contains(issues[0].Message, "draft") {
		t.Errorf("message %q missing canonical set", issues[0].Message)
	}
}

func TestTaskStatusCustomAllowed(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/a.md": "---\nslug: a\nstatus: draft\n---\nbody\n",
	})
	issues, err := TaskStatus{Allowed: []string{"draft", "done"}}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues with custom set, got: %v", issues)
	}
}
