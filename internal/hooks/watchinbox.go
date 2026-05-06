package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrWake signals that the inbox was drained and the agent should wake
// (exit code 2 in claude-code's Stop hook protocol).
var ErrWake = errors.New("inbox drained")

// WatchInbox is the Stop-hook implementation for inbox-based wakeup.
// It watches $WT_STATE/<slug>/inbox/ for arriving .json files, drains
// them into stdout formatted as "[ts] source: body", moves claimed
// files to inbox/read/, and returns ErrWake when files were drained.
// Returns nil on timeout (no files), or a real error on failure.
func WatchInbox(slug string) error {
	return watchInboxAt(workerInbox(slug), os.Stdout, os.Stderr, defaultWatchOpts())
}

// WatchInboxAt is the env-driven Stop-hook entry point. It targets
// inboxDir directly instead of computing it from a slug, so callers
// like `spore hooks watch-inbox` (no positional) can pass
// $SPORE_TASK_INBOX, which the consumer harness sets per spawn (rower:
// $WT_STATE/<slug>/inbox; coordinator: $SKYHELM_STATE_DIR/<project>/inbox).
func WatchInboxAt(inboxDir string) error {
	return watchInboxAt(inboxDir, os.Stdout, os.Stderr, defaultWatchOpts())
}

type watchOpts struct {
	timeout     time.Duration
	settle      time.Duration
	initWatcher func(dir string) (inboxWaiter, error)
	sleep       func(time.Duration)
}

// inboxWaiter abstracts inotify so tests can drive it without the
// kernel. Wait returns true on event, false on timeout.
type inboxWaiter interface {
	Wait() (woke bool, err error)
	Close() error
}

func defaultWatchOpts() watchOpts {
	return watchOpts{
		timeout:     envDurationSeconds("WATCH_TIMEOUT", 604800),
		settle:      envDurationSeconds("WATCH_SETTLE", 1),
		initWatcher: initPlatformWatcher,
		sleep:       time.Sleep,
	}
}

func watchInbox(slug string, stdout, stderr io.Writer, opts watchOpts) error {
	return watchInboxAt(workerInbox(slug), stdout, stderr, opts)
}

func watchInboxAt(inboxDir string, stdout, stderr io.Writer, opts watchOpts) error {
	if err := ensureInbox(inboxDir); err != nil {
		return err
	}

	if n, err := drainInbox(inboxDir, stdout); err != nil {
		return err
	} else if n > 0 {
		return ErrWake
	}

	w, werr := opts.initWatcher(inboxDir)
	if werr != nil || w == nil {
		fmt.Fprintf(stderr, "watch-inbox: inotifywait unavailable; sleeping %ds\n",
			int(opts.timeout.Seconds()))
		opts.sleep(opts.timeout)
		if n, err := drainInbox(inboxDir, stdout); err != nil {
			return err
		} else if n > 0 {
			return ErrWake
		}
		return nil
	}
	defer w.Close()

	woke, _ := w.Wait()
	if woke {
		opts.sleep(opts.settle)
	}

	if n, err := drainInbox(inboxDir, stdout); err != nil {
		return err
	} else if n > 0 {
		return ErrWake
	}
	return nil
}

// drainInbox lists *.json at the top level of inboxDir, atomically
// moves each to inboxDir/read/, and prints "[ts] source: body" for
// every file this caller claimed. Returns the claim count.
func drainInbox(inboxDir string, stdout io.Writer) (int, error) {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return 0, nil
	}
	sort.Strings(names)

	readDir := filepath.Join(inboxDir, "read")
	var claimed []string
	for _, n := range names {
		src := filepath.Join(inboxDir, n)
		dst := filepath.Join(readDir, n)
		if err := os.Rename(src, dst); err == nil {
			claimed = append(claimed, dst)
		}
	}
	if len(claimed) == 0 {
		return 0, nil
	}
	for _, f := range claimed {
		ts, source, body := readTellFile(f)
		fmt.Fprintf(stdout, "[%s] %s: %s\n", ts, source, body)
	}
	return len(claimed), nil
}

func readTellFile(path string) (ts, source, body string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var ev struct {
		Ts     string `json:"ts"`
		Source string `json:"source"`
		Body   string `json:"body"`
		Slug   string `json:"slug"`
		Msg    string `json:"msg"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &ev); err != nil {
		return
	}
	if ev.Source == "" && ev.Msg != "" {
		ev.Source = "tell"
	}
	if ev.Body == "" {
		ev.Body = ev.Msg
	}
	return ev.Ts, ev.Source, ev.Body
}

func workerInbox(slug string) string {
	return filepath.Join(wtStateDir(), slug, "inbox")
}

func wtStateDir() string {
	if v := os.Getenv("WT_STATE"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "wt")
	}
	return ""
}

func ensureInbox(inbox string) error {
	for _, sub := range []string{"", ".tmp", "read"} {
		if err := os.MkdirAll(filepath.Join(inbox, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func envDurationSeconds(key string, defaultSec int) time.Duration {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscan(v, &n); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultSec) * time.Second
}
