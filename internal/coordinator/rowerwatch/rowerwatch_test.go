package rowerwatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProbe is a deterministic Probe for tests. Per-slug idle/sha
// tables, plus optional capture of last DoneVerdict slug.
type fakeProbe struct {
	mainRoot    string
	headSHA     map[string]string // key: projectRoot|branch
	opencodeSec map[string]int    // key: wtDir
	opencodeOK  map[string]bool
	claudeSec   map[string]int
	claudeOK    map[string]bool
	fleet       string
	verdict     map[string]string
}

func (f *fakeProbe) GitMainRoot() string { return f.mainRoot }
func (f *fakeProbe) GitHeadSHA(p, b string) string {
	return f.headSHA[p+"|"+b]
}
func (f *fakeProbe) OpencodeIdleSecs(wtDir string, _ time.Time) (int, bool) {
	return f.opencodeSec[wtDir], f.opencodeOK[wtDir]
}
func (f *fakeProbe) ClaudeIdleSecs(wtDir string, _ time.Time) (int, bool) {
	return f.claudeSec[wtDir], f.claudeOK[wtDir]
}
func (f *fakeProbe) FleetStatus() string { return f.fleet }
func (f *fakeProbe) DoneVerdict(slug string) string {
	return f.verdict[slug]
}

func fixedNow() time.Time {
	loc := time.FixedZone("EEST", 3*60*60)
	return time.Date(2026, 5, 10, 12, 0, 0, 0, loc)
}

// scaffold creates a tasks/ tree with one task file per (slug, status,
// agent) tuple and the matching .worktrees/<slug> dir, then returns
// the project root. Inbox is a child of stateDir so IsCoordinator
// passes.
type taskSpec struct {
	slug   string
	status string
	agent  string
}

func scaffoldProject(t *testing.T, specs []taskSpec) string {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	wtRoot := filepath.Join(root, ".worktrees")
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		body := "---\nslug: " + s.slug + "\nstatus: " + s.status + "\nagent: " + s.agent + "\n---\n"
		if err := os.WriteFile(filepath.Join(tasks, s.slug+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wtRoot, s.slug), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// newCfg returns a Config with Inbox under StateDir and a deterministic
// Probe pre-wired.
func newCfg(t *testing.T, root string, probe Probe) Config {
	t.Helper()
	state := t.TempDir()
	inbox := filepath.Join(state, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	c := Config{
		StateDir:     state,
		Inbox:        inbox,
		ProjectsFile: filepath.Join(t.TempDir(), "no-such-projects-file"),
		MainRoot:     root,
		Now:          fixedNow,
		Probe:        probe,
	}
	return c
}

func TestGateSkipsWhenInboxNotUnderStateDir(t *testing.T) {
	t.Setenv("SKYBOT_INBOX", "")
	cfg := Config{
		StateDir: t.TempDir(),
		Inbox:    filepath.Join(t.TempDir(), "other"),
		Probe:    &fakeProbe{},
	}.Defaults()
	res := Watch(cfg)
	if !res.Skipped {
		t.Fatalf("expected Skipped, got %+v", res)
	}
}

func TestAppearedTransitionForFirstObservation(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	probe := &fakeProbe{
		mainRoot:  root,
		headSHA:   map[string]string{root + "|wt/foo": "abc1234"},
		claudeSec: map[string]int{filepath.Join(root, ".worktrees", "foo"): 30},
		claudeOK:  map[string]bool{filepath.Join(root, ".worktrees", "foo"): true},
	}
	res := Watch(newCfg(t, root, probe))
	if res.Skipped {
		t.Fatal("unexpected Skipped")
	}
	if len(res.Transitions) != 1 {
		t.Fatalf("want 1 transition, got %v", res.Transitions)
	}
	want := "APPEARED foo (active, claude, idle=30s)"
	if res.Transitions[0] != want {
		t.Fatalf("want %q got %q", want, res.Transitions[0])
	}
}

func TestStuckDebounceTakesTwoFlapsToFire(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
	}
	cfg := newCfg(t, root, probe)

	// pass 1: APPEARED, not stuck.
	if r := Watch(cfg); len(r.Transitions) != 1 {
		t.Fatalf("pass1: want 1, got %v", r.Transitions)
	}

	// pass 2: idle now over threshold, but debounce holds (flap=1).
	probe.claudeSec[wtDir] = 1000
	r := Watch(cfg)
	if len(r.Transitions) != 0 {
		t.Fatalf("pass2: want 0 transitions (debouncing), got %v", r.Transitions)
	}

	// pass 3: still stuck; flap reaches 2, fires STUCK.
	r = Watch(cfg)
	if len(r.Transitions) != 1 {
		t.Fatalf("pass3: want 1, got %v", r.Transitions)
	}
	if !strings.HasPrefix(r.Transitions[0], "STUCK foo (claude, idle=") {
		t.Fatalf("pass3: unexpected line %q", r.Transitions[0])
	}

	// pass 4: idle drops; debounce holds again.
	probe.claudeSec[wtDir] = 30
	r = Watch(cfg)
	if len(r.Transitions) != 0 {
		t.Fatalf("pass4: want 0, got %v", r.Transitions)
	}

	// pass 5: still not stuck; UNSTUCK fires.
	r = Watch(cfg)
	if len(r.Transitions) != 1 || !strings.HasPrefix(r.Transitions[0], "UNSTUCK foo") {
		t.Fatalf("pass5: want UNSTUCK, got %v", r.Transitions)
	}
}

func TestSingleFlapDoesNotTrigger(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
	}
	cfg := newCfg(t, root, probe)

	Watch(cfg) // APPEARED

	// One stuck observation, then back: nothing fires.
	probe.claudeSec[wtDir] = 1000
	if r := Watch(cfg); len(r.Transitions) != 0 {
		t.Fatalf("want 0, got %v", r.Transitions)
	}
	probe.claudeSec[wtDir] = 30
	if r := Watch(cfg); len(r.Transitions) != 0 {
		t.Fatalf("after flap-recover want 0, got %v", r.Transitions)
	}
}

func TestDisappearedAugmentsWithDoneVerdict(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
		verdict:   map[string]string{"foo": "real-impl"},
	}
	cfg := newCfg(t, root, probe)

	if r := Watch(cfg); len(r.Transitions) != 1 {
		t.Fatalf("seed: want 1, got %v", r.Transitions)
	}

	// Flip the task to done.
	body := "---\nslug: foo\nstatus: done\nagent: claude\n---\n"
	if err := os.WriteFile(filepath.Join(root, "tasks", "foo.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := Watch(cfg)
	if len(r.Transitions) != 1 {
		t.Fatalf("disappear: want 1, got %v", r.Transitions)
	}
	want := "DISAPPEARED foo (active -> done, verdict=real-impl)"
	if r.Transitions[0] != want {
		t.Fatalf("want %q got %q", want, r.Transitions[0])
	}
}

func TestDisappearedWithoutVerdictWhenStillActive(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
		verdict:   map[string]string{"foo": "real-impl"},
	}
	cfg := newCfg(t, root, probe)

	Watch(cfg)

	// Flip to paused: not done, so verdict must NOT be appended.
	body := "---\nslug: foo\nstatus: paused\nagent: claude\n---\n"
	os.WriteFile(filepath.Join(root, "tasks", "foo.md"), []byte(body), 0o644)

	r := Watch(cfg)
	want := "DISAPPEARED foo (active -> paused)"
	if len(r.Transitions) != 1 || r.Transitions[0] != want {
		t.Fatalf("want %q got %v", want, r.Transitions)
	}
}

func TestHeadMovedRequiresOptIn(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		headSHA:   map[string]string{root + "|wt/foo": "aaaaaaa"},
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
	}
	cfg := newCfg(t, root, probe)

	Watch(cfg) // seed

	probe.headSHA[root+"|wt/foo"] = "bbbbbbb"

	// Off by default.
	r := Watch(cfg)
	if len(r.Transitions) != 0 {
		t.Fatalf("HeadMovedOn=false: want 0, got %v", r.Transitions)
	}

	// Reseed and turn it on.
	cfg.HeadMovedOn = true
	probe.headSHA[root+"|wt/foo"] = "ccccccc"
	r = Watch(cfg)
	if len(r.Transitions) != 1 {
		t.Fatalf("HeadMovedOn=true: want 1, got %v", r.Transitions)
	}
	want := "HEAD-MOVED foo (bbbbbbb -> ccccccc)"
	if r.Transitions[0] != want {
		t.Fatalf("want %q got %q", want, r.Transitions[0])
	}
}

func TestFleetRunningOverridesStuck(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	wtDir := filepath.Join(root, ".worktrees", "foo")
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{wtDir: 30},
		claudeOK:  map[string]bool{wtDir: true},
	}
	cfg := newCfg(t, root, probe)

	Watch(cfg)
	// Bump idle past threshold but mark fleet as running.
	probe.claudeSec[wtDir] = 1000
	probe.fleet = "fleet status: ...\nclaude-running: foo (session=foo)\n"
	if r := Watch(cfg); len(r.Transitions) != 0 {
		t.Fatalf("fleet running override: want 0, got %v", r.Transitions)
	}
	if r := Watch(cfg); len(r.Transitions) != 0 {
		t.Fatalf("fleet running override 2: want 0, got %v", r.Transitions)
	}
}

func TestStateRoundTripPreservesFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	want := []*rower{
		{
			slug:     "spore/foo",
			baseSlug: "foo",
			status:   "active",
			headSHA:  "abc1234",
			agent:    "claude",
			idleSecs: 42,
			newStuck: true,
			newFlap:  3,
		},
		{
			slug:     "nix/bar",
			baseSlug: "bar",
			status:   "active",
			headSHA:  "",
			agent:    "opencode",
			idleSecs: -1,
			newStuck: false,
			newFlap:  0,
		},
	}
	if err := writeState(path, dir, want, fixedNow()); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	got, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	foo := got["spore/foo"]
	if foo.Status != "active" || foo.HeadSHA != "abc1234" || foo.Agent != "claude" ||
		foo.IdleSecs != 42 || !foo.Stuck || foo.Flap != 3 ||
		foo.Branch != "wt/foo" {
		t.Fatalf("foo round-trip mismatch: %+v", foo)
	}
	bar := got["nix/bar"]
	if bar.Status != "active" || bar.IdleSecs != -1 || bar.Stuck || bar.Flap != 0 ||
		bar.Branch != "wt/bar" {
		t.Fatalf("bar round-trip mismatch: %+v", bar)
	}
}

func TestWatchSkipsInactiveTasks(t *testing.T) {
	root := scaffoldProject(t, []taskSpec{
		{"foo", "active", "claude"},
		{"bar", "draft", "claude"},
		{"baz", "done", "claude"},
	})
	probe := &fakeProbe{
		mainRoot:  root,
		claudeSec: map[string]int{filepath.Join(root, ".worktrees", "foo"): 30},
		claudeOK:  map[string]bool{filepath.Join(root, ".worktrees", "foo"): true},
	}
	r := Watch(newCfg(t, root, probe))
	if len(r.Transitions) != 1 {
		t.Fatalf("want 1, got %v", r.Transitions)
	}
	if !strings.HasPrefix(r.Transitions[0], "APPEARED foo ") {
		t.Fatalf("want APPEARED foo, got %q", r.Transitions[0])
	}
}

func TestWatchMultiProjectPrefixesSlugs(t *testing.T) {
	a := scaffoldProject(t, []taskSpec{{"foo", "active", "claude"}})
	b := scaffoldProject(t, []taskSpec{{"bar", "active", "claude"}})

	stateDir := t.TempDir()
	inbox := filepath.Join(stateDir, "inbox")
	os.MkdirAll(inbox, 0o700)

	// Symlink-free base names: a's leaf and b's leaf are the temp
	// dir leaves. Just record what they are.
	aName := filepath.Base(a)
	bName := filepath.Base(b)

	projectsFile := filepath.Join(t.TempDir(), "projects")
	body := a + "\n" + b + "\n"
	if err := os.WriteFile(projectsFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	probe := &fakeProbe{
		mainRoot:  a,
		claudeSec: map[string]int{filepath.Join(a, ".worktrees", "foo"): 30, filepath.Join(b, ".worktrees", "bar"): 30},
		claudeOK:  map[string]bool{filepath.Join(a, ".worktrees", "foo"): true, filepath.Join(b, ".worktrees", "bar"): true},
	}
	cfg := Config{
		StateDir:     stateDir,
		Inbox:        inbox,
		ProjectsFile: projectsFile,
		MainRoot:     a,
		Now:          fixedNow,
		Probe:        probe,
	}
	r := Watch(cfg)
	if len(r.Transitions) != 2 {
		t.Fatalf("want 2 transitions, got %v", r.Transitions)
	}
	wantA := "APPEARED " + aName + "/foo (active, claude, idle=30s)"
	wantB := "APPEARED " + bName + "/bar (active, claude, idle=30s)"
	got := strings.Join(r.Transitions, "\n")
	if !strings.Contains(got, wantA) || !strings.Contains(got, wantB) {
		t.Fatalf("want both prefixed slugs, got:\n%s", got)
	}
}

func TestFmtDur(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0s"},
		{59, "59s"},
		{60, "1m"},
		{3599, "59m"},
		{3600, "1h00m"},
		{7320, "2h02m"},
	}
	for _, tc := range cases {
		if got := fmtDur(tc.in); got != tc.want {
			t.Errorf("fmtDur(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeClaudeProjectDir(t *testing.T) {
	in := "/home/sky/nix-config/.worktrees/foo"
	want := "-home-sky-nix-config--worktrees-foo"
	if got := encodeClaudeProjectDir(in); got != want {
		t.Errorf("encode = %q want %q", got, want)
	}
}

func TestFormatBlock(t *testing.T) {
	r := Result{Transitions: []string{"APPEARED foo (a)", "STUCK bar (b)"}}
	want := "SKYHELM ROWER WATCH:\n  APPEARED foo (a)\n  STUCK bar (b)\n"
	if got := r.Format(); got != want {
		t.Errorf("Format mismatch:\n%q\n%q", got, want)
	}
	if (Result{}).Format() != "" {
		t.Error("empty result should format empty")
	}
}

func TestFleetRuntimeLineMatches(t *testing.T) {
	out := "fleet status: x\ncodex-running: foo (session=...)\nclaude-idle-wake-pending: bar (session=...)\nclaude-running: baz-extra (session=...)\n"
	if l := fleetRuntimeLine(out, "foo"); !strings.Contains(l, "codex-running: foo") {
		t.Errorf("foo: %q", l)
	}
	if l := fleetRuntimeLine(out, "bar"); !strings.Contains(l, "idle-wake-pending: bar") {
		t.Errorf("bar: %q", l)
	}
	if l := fleetRuntimeLine(out, "baz"); l != "" {
		t.Errorf("baz must not match baz-extra: %q", l)
	}
}
