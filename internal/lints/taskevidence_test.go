package lints

import (
	"strings"
	"testing"
)

func TestTaskEvidence_PassFixture(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/good.md": "---\n" +
			"status: done\n" +
			"slug: good\n" +
			"title: Good Fixture\n" +
			"evidence_required: [commit, file]\n" +
			"---\n" +
			"## Evidence\n" +
			"- commit: a1b2c3d4 shipped it\n" +
			"- file: internal/foo.go added Foo\n",
	})
	issues, err := TaskEvidence{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues on pass fixture, got %v", issues)
	}
}

func TestTaskEvidence_FailFixture_MissingBullet(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/missing.md": "---\n" +
			"status: done\n" +
			"slug: missing\n" +
			"title: Missing Bullet\n" +
			"evidence_required: [commit, test]\n" +
			"---\n" +
			"All implemented and merged.\n" +
			"## Evidence\n" +
			"- commit: a1b2c3d4 shipped\n",
	})
	issues, err := TaskEvidence{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue on fail fixture")
	}
	if !containsIssueWith(issues, "suspect-hallucination") {
		t.Errorf("expected suspect-hallucination issue, got %v", issues)
	}
}

func TestTaskEvidence_FailFixture_UnknownKind(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/bad-kind.md": "---\n" +
			"status: draft\n" +
			"slug: bad-kind\n" +
			"title: Bad Kind\n" +
			"evidence_required: [commit, screenshot]\n" +
			"---\n",
	})
	issues, err := TaskEvidence{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsIssueWith(issues, "unknown kind \"screenshot\"") {
		t.Errorf("expected unknown-kind issue, got %v", issues)
	}
}

func TestTaskEvidence_PreContract_Skipped(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/pre.md": "---\n" +
			"status: done\n" +
			"slug: pre\n" +
			"title: Pre-contract\n" +
			"---\n" +
			"shipped it long ago, no contract\n",
	})
	issues, err := TaskEvidence{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("pre-contract task should be skipped, got %v", issues)
	}
}

func TestTaskEvidence_NotDoneStatus_Skipped(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/active.md": "---\n" +
			"status: active\n" +
			"slug: active\n" +
			"title: Active\n" +
			"evidence_required: [commit]\n" +
			"---\n" +
			"## Evidence\n",
	})
	issues, err := TaskEvidence{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("active task with missing evidence should be skipped, got %v", issues)
	}
}

func containsIssueWith(issues []Issue, want string) bool {
	for _, i := range issues {
		if strings.Contains(i.Message, want) {
			return true
		}
	}
	return false
}
