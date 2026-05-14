package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupRowerFixture(t *testing.T, slug, status, body string) (worktree, state, project string) {
	t.Helper()
	root := t.TempDir()
	worktree = filepath.Join(root, "worktree")
	state = filepath.Join(root, "state")
	project = "demo"

	if err := os.MkdirAll(filepath.Join(worktree, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nslug: " + slug + "\nstatus: " + status + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(worktree, "tasks", slug+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	return worktree, state, project
}

func planReadyFiles(t *testing.T, inbox string) []map[string]string {
	t.Helper()
	var out []map[string]string
	entries, err := os.ReadDir(inbox)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(inbox, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestPlanReadyMechanical_EmitsWhenPlanAndNoTell(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"# brief\n\nrun X.\n\n## Plan\n\n1. step\n")

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}

	inbox := filepath.Join(state, project, "inbox")
	files := planReadyFiles(t, inbox)
	if len(files) != 1 {
		t.Fatalf("want 1 envelope, got %d", len(files))
	}
	if got := files[0]["body"]; !strings.HasPrefix(got, "plan ready: "+slug) {
		t.Errorf("body=%q, want plan-ready prefix", got)
	}
	if files[0]["source"] != "plan-ready-mechanical" {
		t.Errorf("source=%q", files[0]["source"])
	}
}

func TestPlanReadyMechanical_NoopWhenTellAlreadyPresent(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"## Plan\n- a\n")

	inbox := filepath.Join(state, project, "inbox")
	if err := ensureInbox(inbox); err != nil {
		t.Fatal(err)
	}
	existing := map[string]string{
		"ts":     "2026-05-13T09:00:00+03:00",
		"source": "worker",
		"body":   "plan ready: " + slug,
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(inbox, "00-existing.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	files := planReadyFiles(t, inbox)
	if len(files) != 1 {
		t.Errorf("want 1 envelope (no re-emit), got %d", len(files))
	}
}

func TestPlanReadyMechanical_NoopWhenTellAlreadyInRead(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"## Plan\n- a\n")

	inbox := filepath.Join(state, project, "inbox")
	if err := ensureInbox(inbox); err != nil {
		t.Fatal(err)
	}
	existing := map[string]string{
		"ts":     "2026-05-13T09:00:00+03:00",
		"source": "worker",
		"body":   "plan ready: " + slug + " here is the plan",
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(filepath.Join(inbox, "read", "00-claimed.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	files := planReadyFiles(t, inbox)
	if len(files) != 0 {
		t.Errorf("want 0 envelopes in top-level inbox, got %d", len(files))
	}
}

func TestPlanReadyMechanical_NoopWhenNoPlanSection(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"# brief\n\nrun X.\n\n## Notes\n\nno plan yet.\n")

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	inbox := filepath.Join(state, project, "inbox")
	if files := planReadyFiles(t, inbox); len(files) != 0 {
		t.Errorf("want 0 envelopes, got %d", len(files))
	}
}

func TestPlanReadyMechanical_NoopWhenStatusNotActive(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "done",
		"## Plan\n- a\n")

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	inbox := filepath.Join(state, project, "inbox")
	if files := planReadyFiles(t, inbox); len(files) != 0 {
		t.Errorf("want 0 envelopes when status=done, got %d", len(files))
	}
}

func TestPlanReadyMechanical_NoopWhenTaskFileMissing(t *testing.T) {
	slug := "ghost-task"
	state := t.TempDir()
	worktree := t.TempDir()
	project := "demo"
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, project)); !os.IsNotExist(err) {
		t.Errorf("expected coordinator project dir not created, got err=%v", err)
	}
}

func TestPlanReadyMechanical_IgnoresPlanInsideFence(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"# brief\n\n```\n## Plan\n- not a real heading\n```\n\nbody.\n")

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	inbox := filepath.Join(state, project, "inbox")
	if files := planReadyFiles(t, inbox); len(files) != 0 {
		t.Errorf("want 0 envelopes (fenced plan), got %d", len(files))
	}
}

func TestPlanReadyMechanical_DetectsPlanAtH3(t *testing.T) {
	slug := "demo-task"
	worktree, state, project := setupRowerFixture(t, slug, "active",
		"## Section\n\n### Plan\n\n- a\n")

	if err := PlanReadyMechanical(slug, worktree, project); err != nil {
		t.Fatalf("PlanReadyMechanical: %v", err)
	}
	inbox := filepath.Join(state, project, "inbox")
	if files := planReadyFiles(t, inbox); len(files) != 1 {
		t.Errorf("want 1 envelope (H3 plan), got %d", len(files))
	}
}

func TestPlanReadyMechanicalEnv_RequiresSlugAndProject(t *testing.T) {
	t.Setenv("SPORE_TASK_SLUG", "")
	t.Setenv("WT_PROJECT", "demo")
	if err := PlanReadyMechanicalEnv(); err != nil {
		t.Errorf("missing slug should noop, got %v", err)
	}
	t.Setenv("SPORE_TASK_SLUG", "x")
	t.Setenv("WT_PROJECT", "")
	if err := PlanReadyMechanicalEnv(); err != nil {
		t.Errorf("missing project should noop, got %v", err)
	}
}

func TestPlanReadyMechanicalEnv_HappyPath(t *testing.T) {
	slug := "env-task"
	worktree, state, project := setupRowerFixture(t, slug, "active", "## Plan\n- a\n")

	t.Setenv("SPORE_TASK_SLUG", slug)
	t.Setenv("WT_PROJECT", project)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(worktree); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if err := PlanReadyMechanicalEnv(); err != nil {
		t.Fatalf("PlanReadyMechanicalEnv: %v", err)
	}
	inbox := filepath.Join(state, project, "inbox")
	if files := planReadyFiles(t, inbox); len(files) != 1 {
		t.Errorf("want 1 envelope via env entry, got %d", len(files))
	}
}
