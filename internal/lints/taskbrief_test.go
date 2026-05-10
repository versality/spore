package lints

import (
	"testing"
)

func TestTaskBrief_FlagsLeadingHeading(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/dirty.md": "---\nslug: dirty\nstatus: active\n---\n\n# Brief\n\nbody\n",
		"tasks/clean.md": "---\nslug: clean\nstatus: active\n---\n\n## Plan\n\nbody\n",
		"tasks/no-fm.md": "no fm here\n# Brief\n",
	})
	issues, err := TaskBrief{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Path != "tasks/dirty.md" || issues[0].Line != 6 {
		t.Fatalf("expected one tasks/dirty.md:6 issue, got %v", issues)
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

func TestTaskBrief_BlankLinesIgnored(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/blanks.md": "---\nslug: x\n---\n\n\n\n# Brief\n",
	})
	issues, err := TaskBrief{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || issues[0].Line != 7 {
		t.Fatalf("expected hit at line 7, got %v", issues)
	}
}
