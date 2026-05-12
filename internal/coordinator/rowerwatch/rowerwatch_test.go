package rowerwatch

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// fakeEnv is a stub Env wired with deterministic in-memory state.
type fakeEnv struct {
	now    time.Time
	active []TaskRef
	heads  map[string]string // base_slug -> sha
	idle   map[string]struct {
		secs int
		ok   bool
	} // base_slug -> idle
	resolved map[string]FinalStatus // slug -> final
	state    []Snapshot
	saved    []Snapshot
}

func (f *fakeEnv) toEnv() Env {
	return Env{
		Now:    func() time.Time { return f.now },
		Active: func() []TaskRef { return f.active },
		HeadSHA: func(_, base string) string {
			return f.heads[base]
		},
		Idle: func(_, base, _ string) (int, bool) {
			r, ok := f.idle[base]
			if !ok {
				return 0, false
			}
			return r.secs, r.ok
		},
		Resolve: func(slug, _, _ string) FinalStatus {
			return f.resolved[slug]
		},
		LoadState: func() ([]Snapshot, error) { return f.state, nil },
		SaveState: func(s []Snapshot) error {
			f.saved = append([]Snapshot(nil), s...)
			return nil
		},
	}
}

func TestAppearedNoPriorSnapshot(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "claude"},
		},
		heads: map[string]string{"foo": "abcdef1"},
		idle: map[string]struct {
			secs int
			ok   bool
		}{"foo": {secs: 42, ok: true}},
	}
	got := Run(Config{}, f.toEnv())
	want := []string{"APPEARED foo (active, claude, idle=42s)"}
	if !reflect.DeepEqual(got.Transitions, want) {
		t.Fatalf("transitions: got %v want %v", got.Transitions, want)
	}
	if len(f.saved) != 1 || f.saved[0].Slug != "foo" || f.saved[0].Stuck || f.saved[0].Flap != 0 {
		t.Fatalf("saved snapshot: %+v", f.saved)
	}
}

func TestAppearedNoIdleObservation(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "opencode"},
		},
	}
	got := Run(Config{}, f.toEnv())
	want := []string{"APPEARED foo (active, opencode)"}
	if !reflect.DeepEqual(got.Transitions, want) {
		t.Fatalf("transitions: got %v want %v", got.Transitions, want)
	}
}

func TestStuckDebouncedFlipsOnSecondTurn(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	prior := []Snapshot{{Slug: "foo", Status: "active", Agent: "claude", Stuck: false, Flap: 0}}
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "claude"},
		},
		idle: map[string]struct {
			secs int
			ok   bool
		}{"foo": {secs: 1200, ok: true}},
		state: prior,
	}

	// First observation crossing threshold: bumps flap, no transition.
	r1 := Run(Config{Debounce: 2}, f.toEnv())
	if len(r1.Transitions) != 0 {
		t.Fatalf("first stuck-flap turn: want silent, got %v", r1.Transitions)
	}
	if f.saved[0].Stuck || f.saved[0].Flap != 1 {
		t.Fatalf("flap counter: got stuck=%v flap=%d", f.saved[0].Stuck, f.saved[0].Flap)
	}

	// Second observation: still stuck, debounce trips.
	f.state = f.saved
	f.saved = nil
	r2 := Run(Config{Debounce: 2}, f.toEnv())
	want := []string{"STUCK foo (claude, idle=20m)"}
	if !reflect.DeepEqual(r2.Transitions, want) {
		t.Fatalf("second stuck-flap turn: got %v want %v", r2.Transitions, want)
	}
	if !f.saved[0].Stuck || f.saved[0].Flap != 0 {
		t.Fatalf("post-flip snapshot: got stuck=%v flap=%d", f.saved[0].Stuck, f.saved[0].Flap)
	}
}

func TestUnstuckDebouncedFlipsOnSecondTurn(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	prior := []Snapshot{{Slug: "foo", Status: "active", Agent: "claude", Stuck: true, Flap: 0}}
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "claude"},
		},
		idle: map[string]struct {
			secs int
			ok   bool
		}{"foo": {secs: 30, ok: true}},
		state: prior,
	}

	r1 := Run(Config{Debounce: 2}, f.toEnv())
	if len(r1.Transitions) != 0 {
		t.Fatalf("first unstuck-flap turn: want silent, got %v", r1.Transitions)
	}

	f.state = f.saved
	f.saved = nil
	r2 := Run(Config{Debounce: 2}, f.toEnv())
	want := []string{"UNSTUCK foo (claude, idle=30s)"}
	if !reflect.DeepEqual(r2.Transitions, want) {
		t.Fatalf("got %v want %v", r2.Transitions, want)
	}
}

func TestDisappearedWithDoneVerdict(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	f := &fakeEnv{
		now:    now,
		active: nil,
		state: []Snapshot{
			{Slug: "foo", Status: "active", Agent: "claude"},
			{Slug: "bar", Status: "active", Agent: "claude"},
		},
		resolved: map[string]FinalStatus{
			"foo": {Status: "done", Verdict: "real-impl"},
			"bar": {Status: "missing"},
		},
	}
	got := Run(Config{}, f.toEnv())
	sort.Strings(got.Transitions)
	want := []string{
		"DISAPPEARED bar (active -> missing)",
		"DISAPPEARED foo (active -> done, verdict=real-impl)",
	}
	if !reflect.DeepEqual(got.Transitions, want) {
		t.Fatalf("got %v want %v", got.Transitions, want)
	}
	if len(f.saved) != 0 {
		t.Fatalf("saved should be empty, got %+v", f.saved)
	}
}

func TestProjectPrefixedSlugFormatsBaseSlugForResolve(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	calls := map[string]string{}
	env := Env{
		Now:     func() time.Time { return now },
		Active:  func() []TaskRef { return nil },
		HeadSHA: func(string, string) string { return "" },
		Idle:    func(string, string, string) (int, bool) { return 0, false },
		LoadState: func() ([]Snapshot, error) {
			return []Snapshot{{Slug: "proj-a/foo", Status: "active", Agent: "claude"}}, nil
		},
		Resolve: func(slug, base, _ string) FinalStatus {
			calls[slug] = base
			return FinalStatus{Status: "done", Verdict: "real-impl"}
		},
		SaveState: func([]Snapshot) error { return nil },
	}
	got := Run(Config{}, env)
	if calls["proj-a/foo"] != "foo" {
		t.Fatalf("resolve base slug: got %q want %q", calls["proj-a/foo"], "foo")
	}
	if !reflect.DeepEqual(got.Transitions, []string{"DISAPPEARED proj-a/foo (active -> done, verdict=real-impl)"}) {
		t.Fatalf("transitions: %v", got.Transitions)
	}
}

func TestHeadMovedRespectsFlag(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	prior := []Snapshot{{Slug: "foo", Status: "active", Agent: "claude", HeadSHA: "aaaaaaa"}}
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "claude"},
		},
		heads: map[string]string{"foo": "bbbbbbb"},
		state: prior,
	}

	// HeadMovedOn false -> silent.
	r1 := Run(Config{}, f.toEnv())
	if len(r1.Transitions) != 0 {
		t.Fatalf("head-moved off: want silent, got %v", r1.Transitions)
	}

	f.saved = nil
	f.state = prior
	r2 := Run(Config{HeadMovedOn: true}, f.toEnv())
	want := []string{"HEAD-MOVED foo (aaaaaaa -> bbbbbbb)"}
	if !reflect.DeepEqual(r2.Transitions, want) {
		t.Fatalf("got %v want %v", r2.Transitions, want)
	}
}

func TestNoOpWhenStuckMatchesPrior(t *testing.T) {
	now := time.Date(2026, 5, 12, 22, 0, 0, 0, time.UTC)
	prior := []Snapshot{{Slug: "foo", Status: "active", Agent: "claude", Stuck: true, Flap: 0}}
	f := &fakeEnv{
		now: now,
		active: []TaskRef{
			{Slug: "foo", BaseSlug: "foo", ProjectRoot: "/r", Status: "active", Agent: "claude"},
		},
		idle: map[string]struct {
			secs int
			ok   bool
		}{"foo": {secs: 1500, ok: true}},
		state: prior,
	}
	got := Run(Config{}, f.toEnv())
	if len(got.Transitions) != 0 {
		t.Fatalf("want silent, got %v", got.Transitions)
	}
}

func TestFormatBlock(t *testing.T) {
	got := FormatBlock([]string{"APPEARED foo (active, claude)", "STUCK bar (claude, idle=15m)"})
	want := "SKYHELM ROWER WATCH:\n  APPEARED foo (active, claude)\n  STUCK bar (claude, idle=15m)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FormatBlock(nil) != "" {
		t.Fatalf("empty input should produce empty output")
	}
}

func TestFormatDur(t *testing.T) {
	cases := []struct {
		in  int
		out string
	}{
		{-1, "?"},
		{0, "0s"},
		{59, "59s"},
		{60, "1m"},
		{3599, "59m"},
		{3600, "1h00m"},
		{7325, "2h02m"},
	}
	for _, tc := range cases {
		if got := formatDur(tc.in); got != tc.out {
			t.Errorf("formatDur(%d): got %q want %q", tc.in, got, tc.out)
		}
	}
}

func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "state.ndjson")
	want := []Snapshot{
		{Slug: "foo", Status: "active", Branch: "wt/foo", HeadSHA: "abc", Agent: "claude", IdleSecs: 12, Stuck: false, Flap: 0, LastSeen: "2026-05-12T22:00:00Z"},
		{Slug: "bar", Status: "active", Branch: "wt/bar", HeadSHA: "", Agent: "opencode", IdleSecs: -1, Stuck: true, Flap: 0, LastSeen: "2026-05-12T22:00:00Z"},
	}
	if err := SaveStateFile(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadStateFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: got %+v want %+v", got, want)
	}
}

func TestLoadStateFileMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadStateFile(filepath.Join(dir, "absent.ndjson"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v want []", got)
	}
}

func TestLoadStateFileSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.ndjson")
	contents := strings.Join([]string{
		`{"slug":"ok","status":"active","agent":"claude"}`,
		`not json`,
		``,
		`{"slug":"","status":"active"}`,
		`{"slug":"also-ok","status":"active","agent":"opencode"}`,
	}, "\n") + "\n"
	if err := writeFile(path, contents); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadStateFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[0].Slug != "ok" || got[1].Slug != "also-ok" {
		t.Fatalf("got %+v", got)
	}
}

func writeFile(path, body string) error {
	return writeFileBytes(path, []byte(body))
}
