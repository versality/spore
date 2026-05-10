package proactiveloop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fixedNow() time.Time {
	loc := time.FixedZone("EEST", 3*60*60)
	return time.Date(2026, 5, 10, 12, 18, 30, 0, loc)
}

// fakeRunner returns scripted responses keyed by space-joined argv;
// missing keys produce ("", 0). Each call is recorded for assertion.
type fakeRunner struct {
	mu      sync.Mutex
	scripts map[string]fakeResp
	calls   []string
}

type fakeResp struct {
	out  string
	code int
}

func newFakeRunner(scripts map[string]fakeResp) *fakeRunner {
	if scripts == nil {
		scripts = map[string]fakeResp{}
	}
	return &fakeRunner{scripts: scripts}
}

func (f *fakeRunner) run(name string, args ...string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	if r, ok := f.scripts[key]; ok {
		return r.out, r.code
	}
	return "", 0
}

func newCfg(t *testing.T, runner *fakeRunner) Config {
	t.Helper()
	state := t.TempDir()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	projectsFile := filepath.Join(state, "projects")
	if err := os.WriteFile(projectsFile, []byte(project+"\n"), 0o600); err != nil {
		t.Fatalf("write projects: %v", err)
	}
	cfg := Config{
		StateDir:     state,
		ProjectsFile: projectsFile,
		LocalHost:    "skytower",
		Floor:        5,
		FailOnStall:  false,
		Now:          fixedNow,
		Exec:         runner.run,
		Random:       func() string { return "stamp" },
	}
	return cfg.Defaults()
}

func writeTask(t *testing.T, cfg Config, slug, status, host string, needs []string) {
	t.Helper()
	project := readProjects(cfg)[0]
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("slug: " + slug + "\n")
	b.WriteString("status: " + status + "\n")
	if host != "" {
		b.WriteString("host: " + host + "\n")
	}
	if len(needs) > 0 {
		b.WriteString("needs:\n")
		for _, n := range needs {
			b.WriteString("  - " + n + "\n")
		}
	}
	b.WriteString("---\n")
	path := filepath.Join(project, "tasks", slug+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write task: %v", err)
	}
}

func TestTickNoTasksReturnsOK(t *testing.T) {
	cfg := newCfg(t, newFakeRunner(nil))
	res := Tick(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "no tasks") {
		t.Fatalf("expected 'no tasks' in stdout, got %q", res.Stdout)
	}
}

func TestTickLockSkipReturnsSkipped(t *testing.T) {
	cfg := newCfg(t, newFakeRunner(nil))
	writeTask(t, cfg, "alpha", "active", "", nil)

	lock, err := os.OpenFile(cfg.LockFile, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flock: %v", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	res := Tick(cfg)
	if !res.Skipped || res.ExitCode != 0 {
		t.Fatalf("expected Skipped exit 0, got %+v", res)
	}
	if !strings.Contains(res.Stdout, "already running") {
		t.Fatalf("expected 'already running', got %q", res.Stdout)
	}
}

func TestTickAllQuietReturnsOK(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":                        {out: "active-live=5\n"},
		"skyhelm-budget query":                        {out: `{"advice":"ok"}`},
		"skyhelm-idle-watchdog":                       {out: "ok\n"},
		"wt-task fleet replenish --floor 5 --dry-run": {out: "no promotable drafts"},
	})
	cfg := newCfg(t, runner)
	writeTask(t, cfg, "alpha", "active", "", nil)

	res := Tick(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.HasSuffix(strings.TrimSpace(res.Stdout), "skyhelm-proactive-loop: ok") {
		t.Fatalf("expected ok suffix, got %q", res.Stdout)
	}
	if _, err := os.Stat(cfg.LoopState); !os.IsNotExist(err) {
		t.Fatalf("LoopState should be cleared, stat err=%v", err)
	}
}

func TestTickDispatchesOnUnreadInbox(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":                        {out: "active-live=5\n"},
		"skyhelm-budget query":                        {out: `{"advice":"ok"}`},
		"skyhelm-idle-watchdog":                       {out: "ok\n"},
		"wt-task fleet replenish --floor 5 --dry-run": {out: "no promotable drafts"},
	})
	cfg := newCfg(t, runner)
	cfg.FailOnStall = false
	writeTask(t, cfg, "alpha", "active", "", nil)

	project := readProjects(cfg)[0]
	inbox := filepath.Join(cfg.StateDir, projectName(project), "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "msg.json"),
		[]byte(`{"source":"operator","body":"hi"}`), 0o600); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}

	res := Tick(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "sent wake") {
		t.Fatalf("expected wake dispatch, got %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "drain skyhelm inbox: 1 unread") {
		t.Fatalf("expected unread message, got %q", res.Stdout)
	}

	got, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("readdir inbox: %v", err)
	}
	wakes := 0
	for _, e := range got {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		body, _ := os.ReadFile(filepath.Join(inbox, e.Name()))
		if strings.Contains(string(body), `"source":"skyhelm-proactive-loop"`) {
			wakes++
		}
	}
	if wakes != 1 {
		t.Fatalf("expected exactly 1 wake envelope, got %d", wakes)
	}
}

func TestTickFailOnStallReturnsExit2(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":                        {out: "active-live=5\n"},
		"skyhelm-budget query":                        {out: `{"advice":"ok"}`},
		"skyhelm-idle-watchdog":                       {out: "ok\n"},
		"wt-task fleet replenish --floor 5 --dry-run": {out: "no promotable drafts"},
		"wt-task agents status":                       {out: "off\n"},
	})
	cfg := newCfg(t, runner)
	cfg.FailOnStall = true
	writeTask(t, cfg, "alpha", "active", "", nil)

	project := readProjects(cfg)[0]
	inbox := filepath.Join(cfg.StateDir, projectName(project), "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "msg.json"),
		[]byte(`{"source":"operator","body":"hi"}`), 0o600); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}

	res := Tick(cfg)
	if res.ExitCode != 2 {
		t.Fatalf("ExitCode = %d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unresolved coordinator work") {
		t.Fatalf("expected unresolved-stall stderr, got %q", res.Stderr)
	}
}

func TestTickDedupesIdenticalFingerprint(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":                        {out: "active-live=5\n"},
		"skyhelm-budget query":                        {out: `{"advice":"ok"}`},
		"skyhelm-idle-watchdog":                       {out: "ok\n"},
		"wt-task fleet replenish --floor 5 --dry-run": {out: "no promotable drafts"},
	})
	cfg := newCfg(t, runner)
	cfg.FailOnStall = false
	writeTask(t, cfg, "alpha", "active", "", nil)
	project := readProjects(cfg)[0]
	inbox := filepath.Join(cfg.StateDir, projectName(project), "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "msg.json"),
		[]byte(`{"source":"operator","body":"hi"}`), 0o600); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}

	res := Tick(cfg)
	if res.ExitCode != 0 {
		t.Fatalf("first Tick exit=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "sent wake") {
		t.Fatalf("first Tick should send: %q", res.Stdout)
	}

	res2 := Tick(cfg)
	if res2.ExitCode != 0 {
		t.Fatalf("second Tick exit=%d stderr=%q", res2.ExitCode, res2.Stderr)
	}
	if !strings.Contains(res2.Stdout, "recorded unchanged state") {
		t.Fatalf("second Tick should dedupe, got %q", res2.Stdout)
	}
}

func TestTickRationSkipsReplenish(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":  {out: "active-live=2\n"},
		"skyhelm-budget query":  {out: `{"advice":"ration"}`},
		"skyhelm-idle-watchdog": {out: "ok\n"},
	})
	cfg := newCfg(t, runner)
	writeTask(t, cfg, "alpha", "active", "", nil)

	res := Tick(cfg)
	if !strings.Contains(res.Stdout, "ration; skipping replenish") {
		t.Fatalf("expected ration skip, got %q", res.Stdout)
	}
	for _, c := range runner.calls {
		if strings.Contains(c, "fleet replenish") {
			t.Fatalf("must not call replenish under ration; calls=%v", runner.calls)
		}
	}
}

func TestTickRemoteHostTaskExcluded(t *testing.T) {
	runner := newFakeRunner(map[string]fakeResp{
		"wt-task fleet status":                        {out: "active-live=5\n"},
		"skyhelm-budget query":                        {out: `{"advice":"ok"}`},
		"skyhelm-idle-watchdog":                       {out: "ok\n"},
		"wt-task fleet replenish --floor 5 --dry-run": {out: "no promotable drafts"},
	})
	cfg := newCfg(t, runner)
	writeTask(t, cfg, "remote-only", "active", "otherhost", nil)
	res := Tick(cfg)
	if !strings.Contains(res.Stdout, "no tasks") {
		t.Fatalf("expected no-tasks for remote-only project: %q", res.Stdout)
	}
}

func TestParseActiveLive(t *testing.T) {
	cases := map[string]int{
		"active-live=7":                7,
		"prefix active-live=12 suffix": 12,
		"no count here":                0,
		"":                             0,
		"active-live=zero":             0,
	}
	for in, want := range cases {
		if got := parseActiveLive(in); got != want {
			t.Errorf("parseActiveLive(%q)=%d want %d", in, got, want)
		}
	}
}

func TestExtractJSONField(t *testing.T) {
	cases := []struct {
		blob, key, want string
	}{
		{`{"advice":"ok"}`, "advice", "ok"},
		{`{"a":"b","advice":"tighten","z":1}`, "advice", "tighten"},
		{`malformed`, "advice", ""},
		{`{"advice":42}`, "advice", ""},
	}
	for _, tc := range cases {
		if got := extractJSONField(tc.blob, tc.key); got != tc.want {
			t.Errorf("extractJSONField(%q,%q)=%q want %q", tc.blob, tc.key, got, tc.want)
		}
	}
}

func TestJoinCSVTruncates(t *testing.T) {
	got := joinCSV(3, []string{"a", "b", "c", "d", "e"})
	if got != "a,b,c,+2" {
		t.Fatalf("joinCSV got %q", got)
	}
}

func TestBuildActionsAggregates(t *testing.T) {
	out := buildActions(actionsInput{
		advice:         "ok",
		replenishFloor: 5,
		activeLive:     2,
		startable:      []string{"draft1"},
		stale:          []string{"foo:done"},
		unread:         3,
		unreadNewest:   "msg.json",
		watchdogRC:     0,
	})
	got := strings.Join(out, "|")
	for _, want := range []string{
		"start one of: draft1",
		"reconcile state.md active rows: foo:done",
		"drain skyhelm inbox: 3 unread newest=msg.json",
		"idle-watchdog said ok; handle this proactive-loop list instead",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in actions: %q", want, got)
		}
	}
}

func TestBuildActionsRationSuppressesStartActions(t *testing.T) {
	out := buildActions(actionsInput{
		advice:         "ration",
		replenishFloor: 0,
		activeLive:     0,
		startable:      []string{"draft1"},
		parked:         []string{"p1:reason"},
		paused:         []string{"q1:reason"},
		blocked:        []string{"b1:assign-owner"},
	})
	got := strings.Join(out, "|")
	if strings.Contains(got, "start one of") {
		t.Errorf("startables should be suppressed under ration: %q", got)
	}
	if !strings.Contains(got, "resolve blocked ownership") {
		t.Errorf("blocked actions still required under ration: %q", got)
	}
}

func TestActiveStateSlugs(t *testing.T) {
	state := t.TempDir()
	body := `# state

## Active tasks

| slug | status |
| ---- | ------ |
| alpha | active |
| beta  | paused |

## Other section

| not | counted |
`
	path := filepath.Join(state, "state.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := activeStateSlugs(path)
	want := []string{"alpha", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("slugs=%v want %v", got, want)
	}
}

func TestStaleStateRows(t *testing.T) {
	state := t.TempDir()
	body := "## Active tasks\n| slug | status |\n| --- | --- |\n| alpha | active |\n| beta | active |\n| ghost | active |\n"
	path := filepath.Join(state, "state.md")
	os.WriteFile(path, []byte(body), 0o600)
	tasks := taskIndex{
		statusBySlug: map[string]string{"alpha": "active", "beta": "paused"},
		remoteHosts:  map[string]bool{},
	}
	got := staleStateRows(path, tasks)
	want := map[string]bool{"beta:paused": true, "ghost:missing": true}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected stale entry %q", g)
		}
	}
}

func TestParseFrontmatterAndNeeds(t *testing.T) {
	tmp := t.TempDir()
	body := `---
slug: x
status: active
needs:
  - foo
  - bar # inline comment
host: skytower
---
body
`
	path := filepath.Join(tmp, "x.md")
	os.WriteFile(path, []byte(body), 0o600)
	if got := fmField(path, "status"); got != "active" {
		t.Errorf("status=%q", got)
	}
	if got := fmField(path, "host"); got != "skytower" {
		t.Errorf("host=%q", got)
	}
	needs := taskNeeds(path)
	if strings.Join(needs, ",") != "foo,bar" {
		t.Errorf("needs=%v", needs)
	}
}

func TestCountUnreadInboxes(t *testing.T) {
	cfg := newCfg(t, newFakeRunner(nil))
	project := readProjects(cfg)[0]
	inbox := filepath.Join(cfg.StateDir, projectName(project), "inbox")
	os.MkdirAll(inbox, 0o700)
	os.WriteFile(filepath.Join(inbox, "a.json"),
		[]byte(`{"source":"operator"}`), 0o600)
	os.WriteFile(filepath.Join(inbox, "b.json"),
		[]byte(`{"source":"skyhelm-proactive-loop"}`), 0o600)
	os.WriteFile(filepath.Join(inbox, "c.json"),
		[]byte(`{"source":"watchdog"}`), 0o600)
	count, newest := countUnreadInboxes(cfg, []string{project})
	if count != 2 {
		t.Errorf("count=%d want 2", count)
	}
	if newest != "c.json" {
		t.Errorf("newest=%q want c.json", newest)
	}
}

func TestClassifyParsesBuckets(t *testing.T) {
	runner := newFakeRunner(nil)
	out := strings.Join([]string{
		"runnable-promote\tslug-a\tdraft\t",
		"runnable-promote\tslug-b\tparked\tneeds-other",
		"resume\tslug-c\tpaused\trecent",
		"invalid-needs-reclassify\tslug-d\tblocked\tblocked-without-operator-owner",
		"invalid-needs-reclassify\tslug-e\tblocked\tunknown-reason",
		"invalid-needs-reclassify\tslug-f\tactive\tweird",
	}, "\n")
	runner.scripts = map[string]fakeResp{}
	cfg := newCfg(t, runner)
	cfg.ClassifierBin = "fakeclass"
	project := readProjects(cfg)[0]
	runner.scripts["fakeclass --project "+project+
		" --state "+cfg.StateFile+
		" --active-live 0 --floor 5"] = fakeResp{out: out}

	startable, parked, paused, blocked, invalid := classify(cfg, []string{project}, 5, 0)
	if strings.Join(startable, ",") != "slug-a" {
		t.Errorf("startable=%v", startable)
	}
	if strings.Join(parked, ",") != "slug-b:needs-other" {
		t.Errorf("parked=%v", parked)
	}
	if strings.Join(paused, ",") != "slug-c:recent" {
		t.Errorf("paused=%v", paused)
	}
	if strings.Join(blocked, ",") != "slug-d:assign-owner" {
		t.Errorf("blocked=%v", blocked)
	}
	if strings.Join(invalid, ",") != "slug-e:unknown-reason,slug-f:weird" {
		t.Errorf("invalid=%v", invalid)
	}
}

func TestFingerprintStableForIdenticalMessage(t *testing.T) {
	a := fingerprintOf("hello")
	b := fingerprintOf("hello")
	if a != b {
		t.Fatalf("fingerprint not stable: %q vs %q", a, b)
	}
	if a == fingerprintOf("hello!") {
		t.Fatal("fingerprint must differ for different inputs")
	}
}

func TestConfigDefaultsHonorEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/skytest")
	t.Setenv("WT_FLEET_FLOOR", "9")
	t.Setenv("SKYHELM_PROACTIVE_LOOP_REPEAT_AFTER", "120")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/skytest" {
		t.Errorf("StateDir=%q", c.StateDir)
	}
	if c.Floor != 9 {
		t.Errorf("Floor=%d", c.Floor)
	}
	if c.RepeatAfter != 120*time.Second {
		t.Errorf("RepeatAfter=%v", c.RepeatAfter)
	}
}
