package lints

import (
	"strings"
	"testing"
)

func TestTaskPriority_FlagsMissingOnActive(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/active-missing.md": "---\n" +
			"status: active\n" +
			"slug: active-missing\n" +
			"title: Active without priority\n" +
			"---\n",
	})
	issues, err := TaskPriority{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "missing 'priority:'") {
		t.Fatalf("expected missing-priority issue, got %v", issues)
	}
}

func TestTaskPriority_FlagsInvalid(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/bogus.md": "---\n" +
			"status: backlog\n" +
			"slug: bogus\n" +
			"title: Bogus priority\n" +
			"priority: urgent\n" +
			"---\n",
	})
	issues, err := TaskPriority{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "want critical|high|medium|low") {
		t.Fatalf("expected invalid-priority issue, got %v", issues)
	}
}

func TestTaskPriority_PassesDoneAndValid(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/done-missing.md": "---\n" +
			"status: done\n" +
			"slug: done-missing\n" +
			"title: Done without priority\n" +
			"---\n",
		"tasks/valid.md": "---\n" +
			"status: active\n" +
			"slug: valid\n" +
			"title: Valid task\n" +
			"priority: high\n" +
			"---\n",
	})
	issues, err := TaskPriority{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}
