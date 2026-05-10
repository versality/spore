package queueclassifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTask(t *testing.T, dir, slug, body string) {
	t.Helper()
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	return root, tasks
}

func cfgFor(root string) Config {
	return Config{
		Project:    root,
		StateFile:  filepath.Join(root, "absent-state.md"),
		ThreadFile: filepath.Join(root, "absent-threads.jsonl"),
		LocalHost:  "skytower",
	}
}

func findRow(t *testing.T, rows []Row, slug string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Slug == slug {
			return r
		}
	}
	t.Fatalf("row %s not found in %+v", slug, rows)
	return Row{}
}

func TestClassifyDraftReadyPromotes(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\n---\nbody\n")
	rows, err := Classify(cfgFor(root))
	if err != nil {
		t.Fatal(err)
	}
	r := findRow(t, rows, "alpha")
	if r.Class != ClassRunnablePromote || r.Reason != "draft-ready" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyDraftFleetAtFloorWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\n---\n")
	cfg := cfgFor(root)
	cfg.Floor = 6
	cfg.ActiveLive = 6
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "fleet-at-floor" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyDraftBudgetTightenWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\n---\n")
	cfg := cfgFor(root)
	cfg.BudgetAdvice = "tighten"
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "budget:tighten" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyDraftBudgetRationWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\n---\n")
	cfg := cfgFor(root)
	cfg.BudgetAdvice = "ration"
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "budget:ration" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyMissingNeedsReclassifies(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\nneeds:\n  - missing-dep\n  - other-missing\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassInvalidNeedsReclassify {
		t.Fatalf("got %+v", r)
	}
	if !strings.HasPrefix(r.Reason, "missing-needs:") {
		t.Fatalf("reason = %q", r.Reason)
	}
	if !strings.Contains(r.Reason, "missing-dep") || !strings.Contains(r.Reason, "other-missing") {
		t.Fatalf("reason missing one of the needs: %q", r.Reason)
	}
}

func TestClassifyUnsatisfiedNeedsWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "dep", "---\nstatus: active\n---\n")
	writeTask(t, tasks, "alpha", "---\nstatus: draft\nneeds:\n  - dep\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "needs:dep" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyAllNeedsDonePromotes(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "dep", "---\nstatus: done\n---\n")
	writeTask(t, tasks, "alpha", "---\nstatus: draft\nneeds:\n  - dep\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassRunnablePromote || r.Reason != "draft-ready" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyParkedSchedulerOperatorOwned(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: parked\nscheduler: blocked until operator approves the design\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassOperatorBlocked || r.Reason != "scheduler-operator-owned" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyParkedTrackerWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: parked\nscheduler: epic-tracker rollup\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "scheduler-pending" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyParkedReadyPromotes(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: parked\nscheduler: ready to start now\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassRunnablePromote || r.Reason != "scheduler-satisfied" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyParkedReadyFleetAtFloorWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: parked\nscheduler: ready to start now\n---\n")
	cfg := cfgFor(root)
	cfg.Floor = 4
	cfg.ActiveLive = 5
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "fleet-at-floor" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyParkedReadyBudgetTightens(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: parked\nscheduler: ready to start now\n---\n")
	cfg := cfgFor(root)
	cfg.BudgetAdvice = "tighten"
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "budget:tighten" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyPausedReadyResumes(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: paused\nscheduler: ready to resume\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassResume || r.Reason != "scheduler-satisfied" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyPausedIgnoresFleetFloor(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: paused\nscheduler: ready to resume\n---\n")
	cfg := cfgFor(root)
	cfg.Floor = 4
	cfg.ActiveLive = 99
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassResume {
		t.Fatalf("paused must not gate on fleet floor; got %+v", r)
	}
}

func TestClassifyPausedBudgetWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: paused\nscheduler: ready to resume\n---\n")
	cfg := cfgFor(root)
	cfg.BudgetAdvice = "ration"
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "budget:ration" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyPausedSchedulerSilentWaits(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha",
		"---\nstatus: paused\nscheduler: when sky says so\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassWaitingTrigger || r.Reason != "scheduler-pending" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyBlockedWithOpenQuestionIsOperatorBlocked(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: blocked\n---\n")
	stateFile := filepath.Join(root, "state.md")
	state := "## Open operator questions\n- alpha: should we ship?\n\n## Other\n"
	if err := os.WriteFile(stateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(root)
	cfg.StateFile = stateFile
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassOperatorBlocked || r.Reason != "operator-owner-present" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyBlockedWithRecentOperatorNotice(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: blocked\n---\n")
	stateFile := filepath.Join(root, "state.md")
	state := "## Recent events\n- 2026-05-10 alpha operator notify ping sent\n"
	if err := os.WriteFile(stateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(root)
	cfg.StateFile = stateFile
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassOperatorBlocked {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyBlockedWithNotifyThreadIsOperatorBlocked(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: blocked\n---\n")
	thread := filepath.Join(root, "threads.jsonl")
	if err := os.WriteFile(thread, []byte(`{"slug":"alpha","ts":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := cfgFor(root)
	cfg.ThreadFile = thread
	rows, _ := Classify(cfg)
	r := findRow(t, rows, "alpha")
	if r.Class != ClassOperatorBlocked {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyBlockedWithoutOperatorOwnerReclassifies(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: blocked\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassInvalidNeedsReclassify || r.Reason != "blocked-without-operator-owner" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyActivePassesThrough(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: active\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassActive || r.Reason != "already-active" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyDonePassesThrough(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: done\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassDone || r.Reason != "closed" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyUnknownStatusReclassifies(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: weird\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassInvalidNeedsReclassify || r.Reason != "invalid-status" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifyEmptyStatusReclassifies(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nfoo: bar\n---\n")
	rows, _ := Classify(cfgFor(root))
	r := findRow(t, rows, "alpha")
	if r.Class != ClassInvalidNeedsReclassify || r.Reason != "invalid-status" {
		t.Fatalf("got %+v", r)
	}
}

func TestClassifySkipsForeignHost(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "alpha", "---\nstatus: draft\nhost: othertower\n---\n")
	writeTask(t, tasks, "beta", "---\nstatus: draft\nhost: skytower\n---\n")
	rows, _ := Classify(cfgFor(root))
	for _, r := range rows {
		if r.Slug == "alpha" {
			t.Fatalf("alpha should be skipped, got %+v", r)
		}
	}
	findRow(t, rows, "beta")
}

func TestClassifyReadmeSkipped(t *testing.T) {
	root, tasks := newProject(t)
	writeTask(t, tasks, "README", "---\nstatus: draft\n---\n")
	writeTask(t, tasks, "alpha", "---\nstatus: draft\n---\n")
	rows, _ := Classify(cfgFor(root))
	for _, r := range rows {
		if r.Slug == "README" {
			t.Fatalf("README must be skipped, got %+v", r)
		}
	}
}

func TestClassifyMissingTasksDirErrors(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Project: root, LocalHost: "skytower"}
	if _, err := Classify(cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestClassifyMissingProjectErrors(t *testing.T) {
	if _, err := Classify(Config{LocalHost: "skytower"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestFormatTSV(t *testing.T) {
	rows := []Row{
		{Class: "runnable-promote", Slug: "alpha", Status: "draft", Reason: "draft-ready"},
		{Class: "waiting-trigger", Slug: "beta", Status: "parked", Reason: "scheduler-pending"},
	}
	got := FormatTSV(rows)
	want := "runnable-promote\talpha\tdraft\tdraft-ready\n" +
		"waiting-trigger\tbeta\tparked\tscheduler-pending\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FormatTSV(nil) != "" {
		t.Fatal("nil should be empty")
	}
}

func TestSchedulerOperatorOwnedKeywords(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"operator decides", true},
		{"manual review", true},
		{"blocked until ops nods", true},
		{"needs sudo", true},
		{"hardware swap", true},
		{"approval needed", true},
		{"epic-tracker rollup", false},
		{"ready", false},
		{"", false},
	}
	for _, tc := range cases {
		if schedulerOperatorOwned(tc.s) != tc.want {
			t.Errorf("schedulerOperatorOwned(%q) = %v want %v", tc.s, !tc.want, tc.want)
		}
	}
}

func TestSchedulerWaitingTriggerKeywords(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"never started directly", true},
		{"epic-tracker", true},
		{"rollup", true},
		{"my-tracker fires", true},
		{"resume when atlas-foo lands", true},
		{"resume when atlas-foo begins", true},
		{"resume when atlas-foo completes", true},
		{"contractor-name (no t-r-a-c-k-e-r)", false},
		{"ready", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := schedulerWaitingTrigger(tc.s); got != tc.want {
			t.Errorf("schedulerWaitingTrigger(%q) = %v want %v", tc.s, got, tc.want)
		}
	}
}

func TestSchedulerTriggerSatisfiedRespectsGuards(t *testing.T) {
	if schedulerTriggerSatisfied("operator owned", false) {
		t.Fatal("operator-owned must not satisfy")
	}
	if schedulerTriggerSatisfied("epic-tracker", false) {
		t.Fatal("tracker must not satisfy")
	}
	if !schedulerTriggerSatisfied("anything goes", true) {
		t.Fatal("non-blocking scheduler with needs should satisfy")
	}
	if !schedulerTriggerSatisfied("ready", false) {
		t.Fatal("ready keyword should satisfy")
	}
	if schedulerTriggerSatisfied("when sky says so", false) {
		t.Fatal("vague scheduler with no needs should not satisfy")
	}
	if schedulerTriggerSatisfied("", true) {
		t.Fatal("empty scheduler should never satisfy")
	}
}

func TestDefaultsClampsFloorAndBudget(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/x")
	t.Setenv("SKYHELM_STATE_FILE", "")
	t.Setenv("WT_STATE", "/tmp/y")
	t.Setenv("HARNESS_NOTIFY_THREAD_FILE", "")
	t.Setenv("SKYHELM_LOCAL_HOST", "myhost")
	c := Config{Floor: 0, BudgetAdvice: "wat"}.Defaults()
	if c.Floor != DefaultFloor {
		t.Errorf("Floor = %d", c.Floor)
	}
	if c.BudgetAdvice != "ok" {
		t.Errorf("BudgetAdvice = %q", c.BudgetAdvice)
	}
	if c.StateFile != "/tmp/x/state.md" {
		t.Errorf("StateFile = %q", c.StateFile)
	}
	if c.ThreadFile != "/tmp/y/harness-notify-threads.jsonl" {
		t.Errorf("ThreadFile = %q", c.ThreadFile)
	}
	if c.LocalHost != "myhost" {
		t.Errorf("LocalHost = %q", c.LocalHost)
	}
}

func TestReadFrontmatterParsesNeedsList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	body := "---\nstatus: draft\nscheduler: when ready\nneeds:\n  - dep1\n  - dep2  # comment\n  - dep3\nfoo: bar\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fm := readFrontmatter(path)
	if fm.fields["status"] != "draft" {
		t.Errorf("status = %q", fm.fields["status"])
	}
	if fm.fields["scheduler"] != "when ready" {
		t.Errorf("scheduler = %q", fm.fields["scheduler"])
	}
	if got := strings.Join(fm.needs, ","); got != "dep1,dep2,dep3" {
		t.Errorf("needs = %q", got)
	}
}
