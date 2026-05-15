package lints

import (
	"testing"
)

func TestTaskBrief(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantHit  bool
		wantLine int
	}{
		{
			name:     "leading h1 brief",
			body:     "---\nslug: x\nstatus: active\n---\n\n# Brief\n\nbody\n",
			wantHit:  true,
			wantLine: 6,
		},
		{
			name:    "h2 brief allowed",
			body:    "---\nslug: x\nstatus: active\n---\n\n## Brief\n\nbody\n",
			wantHit: false,
		},
		{
			name:     "lowercase h1 brief",
			body:     "---\nslug: x\nstatus: active\n---\n\n# brief\n\nbody\n",
			wantHit:  true,
			wantLine: 6,
		},
		{
			name:    "empty file",
			body:    "",
			wantHit: false,
		},
		{
			name:     "h1 brief mid-body",
			body:     "---\nslug: x\n---\n\nintro\n\n# Brief\n\nbody\n",
			wantHit:  true,
			wantLine: 7,
		},
		{
			name:     "blank lines before brief",
			body:     "---\nslug: x\n---\n\n\n\n# Brief\n",
			wantHit:  true,
			wantLine: 7,
		},
		{
			name:    "brief without space (not h1)",
			body:    "---\nslug: x\n---\n\n#Brief\n",
			wantHit: false,
		},
		{
			name:    "no frontmatter no brief",
			body:    "intro\n\nplan\n",
			wantHit: false,
		},
		{
			name:     "no frontmatter with brief",
			body:     "# Brief\n",
			wantHit:  true,
			wantLine: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newTestRepo(t, map[string]string{
				"tasks/case.md": tc.body,
			})
			issues, err := TaskBrief{}.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if tc.wantHit {
				if len(issues) != 1 || issues[0].Line != tc.wantLine || issues[0].Path != "tasks/case.md" {
					t.Fatalf("want one tasks/case.md:%d issue, got %v", tc.wantLine, issues)
				}
			} else if len(issues) != 0 {
				t.Fatalf("want no issues, got %v", issues)
			}
		})
	}
}

func TestTaskBrief_NoTasksDir(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"README.md": "no tasks dir\n",
	})
	issues, err := TaskBrief{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}
