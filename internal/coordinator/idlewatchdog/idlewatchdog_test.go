package idlewatchdog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedNow() time.Time {
	loc := time.FixedZone("EEST", 3*60*60)
	return time.Date(2026, 5, 10, 12, 18, 30, 0, loc)
}

// runRecorder captures calls to the Runner shim and lets each test
// stub specific binaries. Unstubbed binaries return empty stdout +
// exit 0 (matching the bash `|| rc=0` fallback for fleet/ls/classifier
// success).
type runRecorder struct {
	mu    sync.Mutex
	calls []runCall
	stubs map[string]runStub
}

type runCall struct {
	name string
	args []string
}

type runStub struct {
	stdout string
	code   int
	err    error
}

func newRecorder() *runRecorder {
	return &runRecorder{stubs: map[string]runStub{}}
}

func (r *runRecorder) stub(name string, stdout string, code int) {
	r.stubs[name] = runStub{stdout: stdout, code: code}
}

func (r *runRecorder) run(name string, args ...string) (string, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, runCall{name: name, args: append([]string{}, args...)})
	if s, ok := r.stubs[name]; ok {
		return s.stdout, s.code, s.err
	}
	return "", 0, nil
}

func newCfg(t *testing.T) (Config, string, *runRecorder, *escRecorder, *notifyRecorder) {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	esc := &escRecorder{}
	notif := &notifyRecorder{}
	wtState := filepath.Join(root, "wt-state")
	if err := os.MkdirAll(wtState, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		StateDir:      stateDir,
		ProjectRoot:   root,
		Inbox:         stateDir,
		LocalHost:     "skytower",
		WTState:       wtState,
		AgentsFlag:    filepath.Join(wtState, "agents-enabled"),
		NotifyThreads: filepath.Join(wtState, "harness-notify-threads.jsonl"),
		Now:           fixedNow,
		Run:           rec.run,
		Notifier:      notif.notify,
		Escalator:     esc.escalate,
		LookPath:      func(string) (string, error) { return "", errors.New("not found") },
		Hostname:      func() (string, error) { return "skytower", nil },
	}
	return cfg.Defaults(), root, rec, esc, notif
}

type escRecorder struct {
	calls []escCall
}
type escCall struct {
	bin, summary, body string
	after              int
}

func (e *escRecorder) escalate(bin, summary, body string, after, _ int) {
	e.calls = append(e.calls, escCall{bin, summary, body, after})
}

type notifyRecorder struct {
	calls [][]string
}

func (n *notifyRecorder) notify(_ string, args []string) {
	n.calls = append(n.calls, append([]string{}, args...))
}

func TestHookGateSkipsWhenInboxNotUnderStateDir(t *testing.T) {
	cfg, _, _, _, _ := newCfg(t)
	cfg.HookMode = true
	cfg.Inbox = filepath.Join(t.TempDir(), "elsewhere")
	res := Check(cfg)
	if res.ExitCode != 0 || res.Stdout != "" {
		t.Fatalf("expected silent skip, got %+v", res)
	}
}

func TestHookGateSkipsOnEmptyInbox(t *testing.T) {
	cfg, _, _, _, _ := newCfg(t)
	cfg.HookMode = true
	cfg.Inbox = ""
	res := Check(cfg)
	if res.ExitCode != 0 || res.Stdout != "" {
		t.Fatalf("expected silent skip, got %+v", res)
	}
}

func TestNoFindingsReportsOK(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=0 duplicates=0\n", 0)
	res := Check(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stdout=%q)", res.ExitCode, res.Stdout)
	}
	if !strings.HasSuffix(res.Stdout, ": ok\n") {
		t.Fatalf("expected ok line, got %q", res.Stdout)
	}
}

func TestNoFindingsClearsStateFiles(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=1\n", 0)
	for _, p := range []string{cfg.StatePath, cfg.HookState, cfg.FirstSeen, cfg.Escalated} {
		if err := os.WriteFile(p, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if res := Check(cfg); res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	for _, p := range []string{cfg.StatePath, cfg.HookState, cfg.FirstSeen, cfg.Escalated} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, err=%v", p, err)
		}
	}
}

func TestDeadActiveRowersFinding(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=3 active-dead=2 duplicates=1\n", 2)
	res := Check(cfg)
	if res.ExitCode != 2 {
		t.Fatalf("expected exit 2, got %d", res.ExitCode)
	}
	want := []string{"dead-active-rowers: 2", "duplicate-rowers: 1"}
	for _, w := range want {
		if !contains(res.Findings, w) {
			t.Errorf("missing finding %q in %v", w, res.Findings)
		}
	}
	if !strings.Contains(res.Stdout, "coordinator action needed") {
		t.Errorf("missing action-needed line:\n%s", res.Stdout)
	}
}

func TestAllActiveIdleFinding(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=3 active-idle=3\n", 0)
	res := Check(cfg)
	if !contains(res.Findings, "all-active-rowers-idle: 3") {
		t.Errorf("missing finding in %v", res.Findings)
	}
}

func TestUnreadInboxOnlyInHookMode(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	if err := os.WriteFile(filepath.Join(cfg.Inbox, "evt.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	standalone := Check(cfg)
	if contains(standalone.Findings, "unread-inbox: 1 at "+cfg.Inbox) {
		t.Errorf("standalone should not surface unread-inbox: %v", standalone.Findings)
	}

	cfg.HookMode = true
	hook := Check(cfg)
	want := "unread-inbox: 1 at " + cfg.Inbox
	if !contains(hook.Findings, want) {
		t.Errorf("hook missing %q in %v", want, hook.Findings)
	}
}

func TestStaleStateActiveRow(t *testing.T) {
	cfg, root, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	writeTask(t, root, "park-me", "parked", "skytower")
	writeTask(t, root, "elsewhere", "active", "skywing")
	state := "## Active tasks\n\n| slug | host |\n|------|------|\n| park-me | skytower |\n| elsewhere | skywing |\n"
	if err := os.WriteFile(cfg.StateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if !contains(res.Findings, "stale-state-active-row: park-me status=parked") {
		t.Errorf("missing stale-state finding: %v", res.Findings)
	}
	for _, f := range res.Findings {
		if strings.Contains(f, "elsewhere") {
			t.Errorf("remote-host slug must not be flagged: %q", f)
		}
	}
}

func TestRoadmapOpenItem(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	state := "## Roadmap\n\n- [ ] reconcile something\n- [x] done item\n\n## Other\n"
	if err := os.WriteFile(cfg.StateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if !contains(res.Findings, "roadmap-open: reconcile something") {
		t.Errorf("missing roadmap finding: %v", res.Findings)
	}
}

func TestPromotableDraftFromClassifier(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "runnable-promote\tnext-thing\tdraft\tready\n", 0)
	if err := os.WriteFile(cfg.AgentsFlag, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if !contains(res.Findings, "promotable-draft: next-thing") {
		t.Errorf("missing promotable-draft: %v", res.Findings)
	}
}

func TestPromotableDraftRequiresAgentsEnabled(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "runnable-promote\tnext-thing\tdraft\tready\n", 0)
	res := Check(cfg)
	if contains(res.Findings, "promotable-draft: next-thing") {
		t.Errorf("agents-disabled should suppress: %v", res.Findings)
	}
}

func TestBlockedWithoutOperatorOwner(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "invalid-needs-reclassify\tstuck\tblocked\tblocked-without-operator-owner\n", 0)
	res := Check(cfg)
	if !contains(res.Findings, "blocked-without-operator-question: stuck") {
		t.Errorf("missing finding: %v", res.Findings)
	}
}

func TestQueueClassifierError(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "boom\nlater\n", 1)
	res := Check(cfg)
	if !contains(res.Findings, "queue-classifier-error: boom") {
		t.Errorf("missing classifier error: %v", res.Findings)
	}
}

func TestCoastDetectedOnlyWhenNoOtherFindings(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "waiting-trigger\tcoast-me\tpaused\tscheduler-pending\n", 0)
	if err := os.WriteFile(cfg.AgentsFlag, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if !contains(res.Findings, "coast-detected: coast-me") {
		t.Errorf("missing coast-detected: %v", res.Findings)
	}
}

func TestHookModeDedupsOnRepeatedRun(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	cfg.HookMode = true
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=1\n", 2)

	first := Check(cfg)
	if first.ExitCode != 2 {
		t.Fatalf("first call should fire, got %d", first.ExitCode)
	}

	second := Check(cfg)
	if second.ExitCode != 0 {
		t.Fatalf("second call should be quiet, got %d (stdout=%q)", second.ExitCode, second.Stdout)
	}
	if !strings.Contains(second.Stdout, "unchanged coordinator state") {
		t.Errorf("missing unchanged-state line: %q", second.Stdout)
	}
}

func TestHookModeFiresOnUnreadInboxEvenIfDeduped(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	cfg.HookMode = true
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=1\n", 2)
	if err := os.WriteFile(cfg.HookState, fpFor(t, "dead-active-rowers: 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Inbox, "ev.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if res.ExitCode != 2 {
		t.Fatalf("unread-inbox must keep gate firing, got %d (stdout=%q)", res.ExitCode, res.Stdout)
	}
}

func TestStandaloneStdoutListsEveryFinding(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=1 duplicates=2\n", 2)
	res := Check(cfg)
	if !strings.Contains(res.Stdout, "dead-active-rowers: 1\n") ||
		!strings.Contains(res.Stdout, "duplicate-rowers: 2\n") {
		t.Errorf("standalone should list each finding:\n%s", res.Stdout)
	}
}

func TestHookStdoutPrintsBucketCounts(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	cfg.HookMode = true
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=1 duplicates=2\n", 2)
	res := Check(cfg)
	if !strings.Contains(res.Stdout, "dead-active-rowers count=1 sample=dead-active-rowers: 1") {
		t.Errorf("missing bucket line:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "duplicate-rowers count=1 sample=duplicate-rowers: 2") {
		t.Errorf("missing duplicate bucket:\n%s", res.Stdout)
	}
}

func TestNotifyFiresOnNewFindings(t *testing.T) {
	cfg, _, rec, _, notif := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=1 active-dead=1\n", 2)
	res := Check(cfg)
	if !res.Notified {
		t.Fatalf("notify should fire, res=%+v", res)
	}
	if len(notif.calls) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(notif.calls))
	}
	got := strings.Join(notif.calls[0], " ")
	if !strings.Contains(got, "--kind skyhelm-idle-watchdog") {
		t.Errorf("notify args missing kind: %s", got)
	}
}

func TestNotifySkippedOnRepeatWithSameFindings(t *testing.T) {
	cfg, _, rec, _, notif := newCfg(t)
	rec.stub(cfg.WTTaskBin, "active-live=1 active-dead=1\n", 2)
	if r := Check(cfg); !r.Notified {
		t.Fatal("first call must notify")
	}
	if r := Check(cfg); r.Notified {
		t.Errorf("second call must skip notify (no new findings)")
	}
	if len(notif.calls) != 1 {
		t.Errorf("expected 1 notify total, got %d", len(notif.calls))
	}
}

func TestEscalationFiresAfterEscalateAfterSeconds(t *testing.T) {
	cfg, _, rec, esc, _ := newCfg(t)
	cfg.EscalateAfter = 60
	cfg.EscalateBin = "/usr/bin/skywarden"
	rec.stub(cfg.WTTaskBin, "active-live=1 active-dead=1\n", 2)

	t0 := fixedNow()
	cfg.Now = func() time.Time { return t0 }
	if r := Check(cfg); r.Escalated {
		t.Fatal("first call must NOT escalate (zero quiet time)")
	}

	cfg.Now = func() time.Time { return t0.Add(120 * time.Second) }
	r := Check(cfg)
	if !r.Escalated {
		t.Fatalf("escalation should fire after 120s elapsed, res=%+v", r)
	}
	if len(esc.calls) != 1 {
		t.Fatalf("expected 1 escalate call, got %d", len(esc.calls))
	}
	if esc.calls[0].after != 60 {
		t.Errorf("EscalateAfter not propagated: %d", esc.calls[0].after)
	}
}

func TestEscalationOnlyOncePerAggregate(t *testing.T) {
	cfg, _, rec, esc, _ := newCfg(t)
	cfg.EscalateAfter = 10
	cfg.EscalateBin = "/usr/bin/skywarden"
	rec.stub(cfg.WTTaskBin, "active-live=1 active-dead=1\n", 2)

	t0 := fixedNow()
	cfg.Now = func() time.Time { return t0 }
	Check(cfg)

	cfg.Now = func() time.Time { return t0.Add(60 * time.Second) }
	if r := Check(cfg); !r.Escalated {
		t.Fatal("expected escalation on second call")
	}

	cfg.Now = func() time.Time { return t0.Add(120 * time.Second) }
	if r := Check(cfg); r.Escalated {
		t.Errorf("escalation must be suppressed by escalated marker")
	}
	if len(esc.calls) != 1 {
		t.Errorf("expected 1 escalate call, got %d", len(esc.calls))
	}
}

func TestEscalationResetsWhenAggregateChanges(t *testing.T) {
	cfg, _, rec, esc, _ := newCfg(t)
	cfg.EscalateAfter = 10
	cfg.EscalateBin = "/usr/bin/skywarden"
	rec.stub(cfg.WTTaskBin, "active-live=1 active-dead=1\n", 2)

	t0 := fixedNow()
	cfg.Now = func() time.Time { return t0 }
	Check(cfg)
	cfg.Now = func() time.Time { return t0.Add(60 * time.Second) }
	Check(cfg)
	if len(esc.calls) != 1 {
		t.Fatalf("setup expected 1 escalate, got %d", len(esc.calls))
	}

	rec.stubs = map[string]runStub{}
	rec.stub(cfg.WTTaskBin, "active-live=2 active-dead=2\n", 2)
	cfg.Now = func() time.Time { return t0.Add(70 * time.Second) }
	if r := Check(cfg); r.Escalated {
		t.Fatalf("first call after change must reset, not escalate again, res=%+v", r)
	}
	if _, err := os.Stat(cfg.Escalated); !os.IsNotExist(err) {
		t.Errorf("escalated marker should be cleared on aggregate change")
	}
}

func TestParseFleetCounts(t *testing.T) {
	c := parseFleetCounts("active-live=4 active-dead=1 active-unknown=0 duplicates=2 active-idle=1 idle-unread=3 idle-wake-stuck=1\n")
	if c.activeLive != 4 || c.activeDead != 1 || c.duplicates != 2 || c.idleUnread != 3 || c.idleWakeStuck != 1 || c.activeIdle != 1 {
		t.Errorf("parseFleetCounts wrong: %+v", c)
	}
}

func TestParseClassifierRowsSkipsBlanks(t *testing.T) {
	rows := parseClassifierRows("a\tb\tc\td\n\n\tno-class\n")
	if len(rows) != 1 || rows[0].slug != "b" || rows[0].reason != "d" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestSectionLinesStopsAtNextHeader(t *testing.T) {
	body := "## Roadmap\n- [ ] one\n- [x] two\n\n## After\n- [ ] not-included\n"
	got := openRoadmapItems(body)
	if len(got) != 1 || got[0] != "one" {
		t.Errorf("openRoadmapItems = %v", got)
	}
}

func TestActiveStateSlugsHandlesDivider(t *testing.T) {
	body := "## Active tasks\n| slug | host |\n|------|------|\n| go-live | skytower |\n"
	got := activeStateSlugs(body)
	if len(got) != 1 || got[0] != "go-live" {
		t.Errorf("activeStateSlugs = %v", got)
	}
}

func TestCoastCandidateGate_NotifyThreadBlocks(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "waiting-trigger\tnoisy\tpaused\tscheduler-pending\n", 0)
	if err := os.WriteFile(cfg.AgentsFlag, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	threads := filepath.Join(cfg.WTState, "harness-notify-threads.jsonl")
	if err := os.MkdirAll(filepath.Dir(threads), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(threads, []byte(`{"slug":"noisy"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.NotifyThreads = threads
	res := Check(cfg)
	if contains(res.Findings, "coast-detected: noisy") {
		t.Errorf("notify-threaded slug must not coast: %v", res.Findings)
	}
}

func TestCoastCandidateGate_RationBlocks(t *testing.T) {
	cfg, _, rec, _, _ := newCfg(t)
	rec.stub(cfg.WTTaskBin, "", 0)
	rec.stub(cfg.Classifier, "waiting-trigger\tslow\tpaused\tscheduler-pending\n", 0)
	if err := os.WriteFile(cfg.AgentsFlag, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.BudgetAdvice = "ration"
	res := Check(cfg)
	if contains(res.Findings, "coast-detected: slow") {
		t.Errorf("ration must suppress coast: %v", res.Findings)
	}
}

func TestCoastCandidateAllowed_BudgetGates(t *testing.T) {
	cfg, _, _, _, _ := newCfg(t)
	state := "## Roadmap\n- [ ] mention slug-x in roadmap\n"
	cases := []struct {
		name   string
		budget string
		want   bool
	}{
		{"ok_passes", "ok", true},
		{"ration_blocks", "ration", false},
		{"tighten_with_mention_passes", "tighten", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coastCandidateAllowed(cfg, state, tc.budget, "slug-x")
			if got != tc.want {
				t.Errorf("budget=%s got %v want %v", tc.budget, got, tc.want)
			}
		})
	}
	if coastCandidateAllowed(cfg, "## Roadmap\n- [ ] unrelated\n", "tighten", "missing") {
		t.Error("tighten without roadmap mention must fail the gate")
	}
}

func TestOutsideGitRepoIsOK(t *testing.T) {
	cfg, _, _, _, _ := newCfg(t)
	cfg.ProjectRoot = ""
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("outside repo must exit 0, got %d (stdout=%q)", res.ExitCode, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "outside git repo") && !strings.Contains(res.Stdout, "no tasks dir") {
		t.Errorf("expected outside-repo / no-tasks-dir line, got %q", res.Stdout)
	}
}

func TestNoTasksDirIsOK(t *testing.T) {
	cfg, root, _, _, _ := newCfg(t)
	if err := os.RemoveAll(filepath.Join(root, "tasks")); err != nil {
		t.Fatal(err)
	}
	res := Check(cfg)
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "no tasks dir") {
		t.Errorf("expected no-tasks-dir ok, got %d %q", res.ExitCode, res.Stdout)
	}
}

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/skyt")
	t.Setenv("SKYHELM_IDLE_WATCHDOG_ESCALATE_AFTER", "42")
	t.Setenv("SKYHELM_IDLE_WATCHDOG_NOTIFY", "0")
	t.Setenv("WT_FLEET_FLOOR", "9")
	c := Config{Hostname: func() (string, error) { return "h", nil }}.Defaults()
	if c.StateDir != "/tmp/skyt" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.EscalateAfter != 42 {
		t.Errorf("EscalateAfter = %d", c.EscalateAfter)
	}
	if c.Notify {
		t.Errorf("Notify should be false on NOTIFY=0")
	}
	if c.Floor != 9 {
		t.Errorf("Floor = %d", c.Floor)
	}
}

func TestPickEscalateBinPriority(t *testing.T) {
	cases := []struct {
		name  string
		known map[string]bool
		want  string
	}{
		{"skywarden_first", map[string]bool{"skywarden": true, "ntfy-push": true}, "skywarden"},
		{"ntfy_when_no_skywarden", map[string]bool{"ntfy-push": true, "ssh": true}, "ntfy-push"},
		{"ssh_fallback", map[string]bool{"ssh": true}, "ssh-skywing-skywarden"},
		{"none", map[string]bool{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookPath := func(name string) (string, error) {
				if tc.known[name] {
					return "/usr/bin/" + name, nil
				}
				return "", errors.New("not found")
			}
			got := pickEscalateBin(lookPath)
			if tc.want == "" && got != "" {
				t.Errorf("expected empty, got %q", got)
			}
			if tc.want != "" && !strings.HasSuffix(got, tc.want) {
				t.Errorf("got %q want suffix %q", got, tc.want)
			}
		})
	}
}

func writeTask(t *testing.T, root, slug, status, host string) {
	t.Helper()
	body := fmt.Sprintf("---\nslug: %s\nstatus: %s\nhost: %s\n---\n", slug, status, host)
	path := filepath.Join(root, "tasks", slug+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func fpFor(t *testing.T, finding string) []byte {
	t.Helper()
	fps := fingerprintList([]string{finding})
	return []byte(strings.Join(fps, "\n") + "\n")
}
