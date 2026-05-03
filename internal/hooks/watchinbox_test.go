package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeWaiter struct {
	woke         bool
	err          error
	closed       bool
	afterWake    func()
	afterTimeout func()
}

func (f *fakeWaiter) Wait() (bool, error) {
	if f.woke && f.afterWake != nil {
		f.afterWake()
	}
	if !f.woke && f.afterTimeout != nil {
		f.afterTimeout()
	}
	return f.woke, f.err
}

func (f *fakeWaiter) Close() error {
	f.closed = true
	return nil
}

func setupInbox(t *testing.T) (state, slug, inbox string) {
	t.Helper()
	state = t.TempDir()
	slug = "rower-test"
	t.Setenv("WT_STATE", state)
	inbox = filepath.Join(state, slug, "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inbox, ".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	return state, slug, inbox
}

func writeTell(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWatchInbox_DrainPreCheckWakes(t *testing.T) {
	_, slug, inbox := setupInbox(t)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"2026-04-29T10:00:00+03:00","source":"coordinator","body":"hi"}`)
	writeTell(t, filepath.Join(inbox, "200-1-1.json"),
		`{"ts":"2026-04-29T10:00:01+03:00","source":"coordinator","body":"second"}`)

	var stdout, stderr bytes.Buffer
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func(string) (inboxWaiter, error) {
			t.Fatal("watcher should not init when pre-drain found files")
			return nil, nil
		},
		sleep: func(time.Duration) { t.Fatal("sleep should not be called") },
	}
	err := watchInbox(slug, &stdout, &stderr, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "[2026-04-29T10:00:00+03:00] coordinator: hi") {
		t.Errorf("missing first line: %q", out)
	}
	if !strings.Contains(out, "[2026-04-29T10:00:01+03:00] coordinator: second") {
		t.Errorf("missing second line: %q", out)
	}
	if _, err := os.Stat(filepath.Join(inbox, "100-1-1.json")); !os.IsNotExist(err) {
		t.Errorf("file should have moved to read/")
	}
	if _, err := os.Stat(filepath.Join(inbox, "read", "100-1-1.json")); err != nil {
		t.Errorf("expected file in read/: %v", err)
	}
}

func TestWatchInbox_TimeoutReturnsNil(t *testing.T) {
	_, slug, _ := setupInbox(t)

	var slept []time.Duration
	opts := watchOpts{
		timeout: time.Second, settle: 5 * time.Second,
		initWatcher: func(string) (inboxWaiter, error) {
			return &fakeWaiter{woke: false}, nil
		},
		sleep: func(d time.Duration) { slept = append(slept, d) },
	}
	err := watchInbox(slug, &bytes.Buffer{}, &bytes.Buffer{}, opts)
	if err != nil {
		t.Fatalf("got err=%v, want nil", err)
	}
	if len(slept) != 0 {
		t.Errorf("settle should not be called on timeout, got %v", slept)
	}
}

func TestWatchInbox_WakeOnEventDrains(t *testing.T) {
	_, slug, inbox := setupInbox(t)

	var slept []time.Duration
	w := &fakeWaiter{
		woke: true,
		afterWake: func() {
			writeTell(t, filepath.Join(inbox, "300-1-1.json"),
				`{"ts":"t","source":"s","body":"woke"}`)
		},
	}
	opts := watchOpts{
		timeout: time.Second, settle: 250 * time.Millisecond,
		initWatcher: func(string) (inboxWaiter, error) { return w, nil },
		sleep:       func(d time.Duration) { slept = append(slept, d) },
	}
	var stdout bytes.Buffer
	err := watchInbox(slug, &stdout, &bytes.Buffer{}, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	if !strings.Contains(stdout.String(), "[t] s: woke") {
		t.Errorf("missing drained line: %q", stdout.String())
	}
	if len(slept) != 1 || slept[0] != 250*time.Millisecond {
		t.Errorf("settle sleep wrong: %v", slept)
	}
	if !w.closed {
		t.Error("watcher not closed")
	}
}

func TestWatchInbox_InotifyFailFallsBackToSleep(t *testing.T) {
	_, slug, inbox := setupInbox(t)

	var slept []time.Duration
	opts := watchOpts{
		timeout: 7 * time.Second, settle: time.Second,
		initWatcher: func(string) (inboxWaiter, error) {
			return nil, errors.New("nope")
		},
		sleep: func(d time.Duration) {
			slept = append(slept, d)
			writeTell(t, filepath.Join(inbox, "fb.json"),
				`{"ts":"t","source":"s","body":"slept"}`)
		},
	}
	var stdout, stderr bytes.Buffer
	err := watchInbox(slug, &stdout, &stderr, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("sleep duration: %v", slept)
	}
	if !strings.Contains(stderr.String(), "inotifywait unavailable; sleeping 7s") {
		t.Errorf("stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[t] s: slept") {
		t.Errorf("stdout: %q", stdout.String())
	}
}

func TestWatchInbox_SleepFallbackEmptyReturnsNil(t *testing.T) {
	_, slug, _ := setupInbox(t)
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func(string) (inboxWaiter, error) {
			return nil, errors.New("nope")
		},
		sleep: func(time.Duration) {},
	}
	err := watchInbox(slug, &bytes.Buffer{}, &bytes.Buffer{}, opts)
	if err != nil {
		t.Fatalf("got err=%v, want nil", err)
	}
}

func TestWatchInbox_DrainAfterTimeoutCatchesRace(t *testing.T) {
	_, slug, inbox := setupInbox(t)
	w := &fakeWaiter{
		woke: false,
		afterTimeout: func() {
			writeTell(t, filepath.Join(inbox, "race.json"),
				`{"ts":"r","source":"s","body":"race"}`)
		},
	}
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func(string) (inboxWaiter, error) { return w, nil },
		sleep:       func(time.Duration) { t.Fatal("no settle on timeout") },
	}
	var stdout bytes.Buffer
	err := watchInbox(slug, &stdout, &bytes.Buffer{}, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	if !strings.Contains(stdout.String(), "[r] s: race") {
		t.Errorf("stdout: %q", stdout.String())
	}
}

func TestDrainInbox_AtomicClaim(t *testing.T) {
	_, _, inbox := setupInbox(t)
	writeTell(t, filepath.Join(inbox, "x.json"), `{"ts":"a","source":"b","body":"c"}`)

	var stdout bytes.Buffer
	n1, err := drainInbox(inbox, &stdout)
	if err != nil || n1 != 1 {
		t.Fatalf("first drain: n=%d err=%v", n1, err)
	}
	n2, err := drainInbox(inbox, &stdout)
	if err != nil || n2 != 0 {
		t.Errorf("second drain: n=%d err=%v", n2, err)
	}
}

func TestDrainInbox_IgnoresNonJsonAndDirs(t *testing.T) {
	_, _, inbox := setupInbox(t)
	writeTell(t, filepath.Join(inbox, "skip.txt"), "not json")
	if err := os.Mkdir(filepath.Join(inbox, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTell(t, filepath.Join(inbox, "ok.json"), `{"ts":"t","source":"s","body":"b"}`)

	var stdout bytes.Buffer
	n, err := drainInbox(inbox, &stdout)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWatchInboxAt_EnvDrivenInboxPath(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "coordinator-inbox")
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(inbox, ".tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"hello"}`)

	var stdout, stderr bytes.Buffer
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func(string) (inboxWaiter, error) {
			t.Fatal("watcher should not init when pre-drain found files")
			return nil, nil
		},
		sleep: func(time.Duration) {},
	}
	err := watchInboxAt(inbox, &stdout, &stderr, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	if !strings.Contains(stdout.String(), "[t] s: hello") {
		t.Errorf("stdout: %q", stdout.String())
	}
}

func TestReadTellFile_MalformedYieldsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, src, body := readTellFile(p)
	if ts != "" || src != "" || body != "" {
		t.Errorf("got (%q,%q,%q), want all empty", ts, src, body)
	}
}

func TestReadTellFile_TaskTellSchema(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tell.json")
	writeTell(t, p, `{"slug":"worker","ts":"2026-05-04T10:00:00Z","msg":"please check status"}`)

	ts, src, body := readTellFile(p)
	if ts != "2026-05-04T10:00:00Z" {
		t.Errorf("ts = %q", ts)
	}
	if src != "tell" {
		t.Errorf("source = %q, want tell", src)
	}
	if body != "please check status" {
		t.Errorf("body = %q", body)
	}
}
