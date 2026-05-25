package workerfinish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBlocksActiveTaskWithoutEvidence(t *testing.T) {
	env := newEnv(t)
	env.writeTask(`---
status: active
slug: demo
title: Demo
evidence_required: [command]
---

Worker completed validation.
`)

	res, err := Run(env.config(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 2 || res.Reason != "evidence-blocked" {
		t.Fatalf("result = %+v, want evidence-blocked exit 2", res)
	}
	env.assertTaskLacks("worker-state:")
}

func TestRunBlocksDirtyWorktree(t *testing.T) {
	env := newEnv(t)
	env.writeTask(commandEvidenceTask())

	res, err := Run(env.config(0, " M src/app.ts\n"))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 2 || res.Reason != "dirty-worktree" {
		t.Fatalf("result = %+v, want dirty-worktree exit 2", res)
	}
	env.assertTaskHas("worker-state: needs-cleanup")
	env.assertTaskHas("worker-result: dirty-worktree")
}

func TestRunIgnoresSporeRuntimeAndOwnTaskDirtyEntries(t *testing.T) {
	env := newEnv(t)
	env.writeTask(commandEvidenceTask())

	status := strings.Join([]string{
		" M tasks/demo.md",
		"?? .codex/hooks.json",
		"?? .claude/settings.local.json",
		"?? .wt/initial-prompt",
		"",
	}, "\n")
	res, err := Run(env.config(0, status))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Reason != WorkerResultNoChange {
		t.Fatalf("result = %+v, want no-code-change", res)
	}
	env.assertTaskHas("worker-state: awaiting-operator")
}

func TestRunMarksNoCodeChangeWithCommandEvidence(t *testing.T) {
	env := newEnv(t)
	env.writeTask(commandEvidenceTask())

	res, err := Run(env.config(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Reason != WorkerResultNoChange {
		t.Fatalf("result = %+v, want no-code-change", res)
	}
	env.assertTaskHas("worker-state: awaiting-operator")
	env.assertTaskHas("worker-result: no-code-change")
}

func TestRunMarksReadyToMergeWithCommitAndTestEvidence(t *testing.T) {
	env := newEnv(t)
	env.writeTask(`---
status: active
slug: demo
title: Demo
evidence_required: [commit, test]
---

## Evidence

- commit: deadbee implemented worker finish
- test: internal/hooks/workerfinish/workerfinish_test.go covers finish states
`)

	res, err := Run(env.config(2, ""))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Reason != WorkerResultMerge {
		t.Fatalf("result = %+v, want ready-to-merge", res)
	}
	env.assertTaskHas("worker-state: awaiting-operator")
	env.assertTaskHas("worker-result: ready-to-merge")
}

func commandEvidenceTask() string {
	return `---
status: active
slug: demo
title: Demo
evidence_required: [command]
---

## Evidence

- command: spore lint
`
}

type testEnv struct {
	t        *testing.T
	worktree string
	project  string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	worktree := filepath.Join(dir, "worktree")
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(worktree, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	return &testEnv{t: t, worktree: worktree, project: project}
}

func (e *testEnv) config(unmerged int, status string) Config {
	return Config{
		Slug:        "demo",
		Worktree:    e.worktree,
		ProjectRoot: e.project,
		Status: func(string) (string, error) {
			return status, nil
		},
		Unmerged: func(projectRoot, branch string) (int, error) {
			if projectRoot != e.project {
				e.t.Fatalf("projectRoot = %q, want %q", projectRoot, e.project)
			}
			if branch != "wt/demo" {
				e.t.Fatalf("branch = %q, want wt/demo", branch)
			}
			return unmerged, nil
		},
	}
}

func (e *testEnv) writeTask(body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.worktree, "tasks", "demo.md"), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) taskBody() string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.worktree, "tasks", "demo.md"))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

func (e *testEnv) assertTaskHas(needle string) {
	e.t.Helper()
	if !strings.Contains(e.taskBody(), needle) {
		e.t.Fatalf("task missing %q:\n%s", needle, e.taskBody())
	}
}

func (e *testEnv) assertTaskLacks(needle string) {
	e.t.Helper()
	if strings.Contains(e.taskBody(), needle) {
		e.t.Fatalf("task unexpectedly contains %q:\n%s", needle, e.taskBody())
	}
}
