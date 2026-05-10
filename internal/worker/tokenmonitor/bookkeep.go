package tokenmonitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BookkeepingConfig holds the paths the wrap-bookkeeping touches. The
// zero value picks defaults rooted at $WT_STATE (or
// $HOME/.local/state/wt as a fallback).
type BookkeepingConfig struct {
	// StateDir is the worker state root ($WT_STATE).
	StateDir string
	// CountDir overrides the per-slug counter dir.
	CountDir string
	// MarkerDir overrides the per-(slug, session) marker dir.
	MarkerDir string
	// VoluntaryFile overrides the voluntary-events ledger.
	VoluntaryFile string
	// EventsFile overrides the events ledger.
	EventsFile string
	// Now is injected for tests.
	Now func() time.Time
}

func (b BookkeepingConfig) defaults() BookkeepingConfig {
	if b.StateDir == "" {
		b.StateDir = defaultWTStateDir()
	}
	if b.CountDir == "" {
		b.CountDir = filepath.Join(b.StateDir, "worker-wrap-count")
	}
	if b.MarkerDir == "" {
		b.MarkerDir = filepath.Join(b.StateDir, "worker-token-monitor")
	}
	if b.VoluntaryFile == "" {
		b.VoluntaryFile = filepath.Join(b.StateDir, "worker-voluntary-events.jsonl")
	}
	if b.EventsFile == "" {
		b.EventsFile = filepath.Join(b.StateDir, "events.jsonl")
	}
	if b.Now == nil {
		b.Now = func() time.Time { return time.Now().UTC() }
	}
	return b
}

func defaultWTStateDir() string {
	if d := os.Getenv("WT_STATE"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "wt")
}

// BookkeepingResult reports what the bookkeeping side-effects actually
// did. Count is the cumulative wrap count for this slug after this
// fire; FirstFire is true only on the first fire of a (slug, session)
// pair, so callers can dedupe operator-visible messaging.
type BookkeepingResult struct {
	Count     int
	FirstFire bool
}

// Bookkeep records a wrap fire's side-effects:
//
//   - bumps the per-slug cumulative counter at <state>/worker-wrap-count/<slug>
//     once per (slug, session) pair, idempotent on re-fire
//   - writes the per-(slug, session) marker at
//     <state>/worker-token-monitor/<slug>.<session>.wrap
//   - appends a voluntary-event row to <state>/worker-voluntary-events.jsonl
//   - appends a workr-token-wrap row to <state>/events.jsonl
//
// Each step is best-effort: any I/O error is silently absorbed and
// the call returns whatever progress it made. Bookkeep does nothing
// for a result with no slug or no fire.
func Bookkeep(bk BookkeepingConfig, sessionID string, result CheckResult) BookkeepingResult {
	if !result.ShouldFire || result.Slug == "" {
		return BookkeepingResult{}
	}
	bk = bk.defaults()
	sid := sessionID
	if sid == "" {
		sid = "unknown"
	}
	marker := filepath.Join(bk.MarkerDir, result.Slug+"."+sid+".wrap")
	first := !fileExists(marker)

	count := readCount(bk, result.Slug)
	if first {
		count++
		writeCount(bk, result.Slug, count)
	}

	logEvent(bk, result, sid)
	if first {
		appendVoluntary(bk, result, sid)
		os.MkdirAll(bk.MarkerDir, 0o700)
		touch(marker)
	}

	return BookkeepingResult{Count: count, FirstFire: first}
}

func readCount(bk BookkeepingConfig, slug string) int {
	path := filepath.Join(bk.CountDir, slug)
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	digits := strings.Builder{}
	for _, r := range string(body) {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0
	}
	return n
}

func writeCount(bk BookkeepingConfig, slug string, n int) {
	if err := os.MkdirAll(bk.CountDir, 0o700); err != nil {
		return
	}
	path := filepath.Join(bk.CountDir, slug)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(n)+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func appendVoluntary(bk BookkeepingConfig, result CheckResult, sid string) {
	if err := os.MkdirAll(filepath.Dir(bk.VoluntaryFile), 0o700); err != nil {
		return
	}
	now := bk.Now()
	line := fmt.Sprintf(
		`{"ts":"%s","epoch":%d,"slug":%s,"session_id":%s,"tier":%s,"ctx":%d}`+"\n",
		now.Format(time.RFC3339), now.Unix(), jsonString(result.Slug), jsonString(sid), jsonString(normTier(result.Tier)), result.Ctx)
	appendFile(bk.VoluntaryFile, line)
}

func logEvent(bk BookkeepingConfig, result CheckResult, sid string) {
	if err := os.MkdirAll(filepath.Dir(bk.EventsFile), 0o700); err != nil {
		return
	}
	now := bk.Now()
	line := fmt.Sprintf(
		`{"ts":"%s","event":"worker-token-wrap","payload":{"slug":%s,"session":%s,"ctx":%d,"tier":%s}}`+"\n",
		now.Format(time.RFC3339), jsonString(result.Slug), jsonString(sid), result.Ctx, jsonString(normTier(result.Tier)))
	appendFile(bk.EventsFile, line)
}

func appendFile(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func touch(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		f.Close()
	}
}

// AnnotateMessage prepends a "Voluntary wrap #N for slug=X (cumulative
// across resume cycles)." line to the wrap message, returning the
// augmented body. Callers pass it the result of Bookkeep when they
// want operators to see the per-slug tally in the wrap-up reminder.
func AnnotateMessage(message string, bk BookkeepingResult, slug string) string {
	if bk.Count <= 0 || slug == "" {
		return message
	}
	prefix := fmt.Sprintf("Voluntary wrap #%d for slug=%s (cumulative across resume cycles).\n", bk.Count, slug)
	if message == "" {
		return prefix
	}
	return prefix + message
}

func jsonString(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
