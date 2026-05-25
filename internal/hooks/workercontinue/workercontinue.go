// Package workercontinue is the worker-side Stop-hook stage that
// refuses to let a worker idle when its task is still active and
// nothing blocks progress. It runs after plan-ready-mechanical
// (so a freshly-emitted "plan ready: <slug>" suppresses it via
// the plan-pending-ack check) and before watch-inbox (so a fire
// short-circuits the long inotify wait).
package workercontinue

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task/frontmatter"
)

// Config parameterizes a Check / Run call. Empty fields fall back
// to env-driven defaults; tests inject every field explicitly.
type Config struct {
	Slug          string
	Worktree      string
	Project       string
	WtStateDir    string
	LedgerDir     string
	TokenStateDir string
	FleetEnabled  func() (bool, error)
	Head          func() string
	Now           func() time.Time
}

// Result reports the verdict of one Stop-hook invocation. The
// caller maps ShouldFire to exit code 2; otherwise exit 0. Reason
// is one of: ok-fire, not-active, missing-task, parse-error,
// fleet-disabled, inbox-unread, plan-pending-ack, token-wrap-fired,
// already-nudged, noop-context.
type Result struct {
	ShouldFire bool
	Reason     string
	Message    string
}

const reminder = "WORKER STILL ACTIVE: tasks/%s.md is status=active and no blocker is recorded " +
	"(no unread inbox, no plan ack pending, no token wrap). " +
	"Resume work on the brief, or flip status to done/blocked with reason.\n"

// Check runs the decision logic without touching the ledger. The
// returned Result tells the caller whether to fire.
func Check(cfg Config) (Result, error) {
	cfg = cfg.defaults()
	if cfg.Slug == "" || cfg.Worktree == "" {
		return Result{Reason: "noop-context"}, nil
	}

	taskFile := filepath.Join(cfg.Worktree, "tasks", cfg.Slug+".md")
	raw, err := os.ReadFile(taskFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Reason: "missing-task"}, nil
		}
		return Result{}, fmt.Errorf("read task: %w", err)
	}
	meta, body, err := frontmatter.Parse(raw)
	if err != nil {
		return Result{Reason: "parse-error"}, nil
	}
	if meta.Status != "active" {
		return Result{Reason: "not-active"}, nil
	}
	if meta.Extra != nil && meta.Extra["worker-state"] == "awaiting-operator" {
		return Result{Reason: "awaiting-operator"}, nil
	}

	if cfg.FleetEnabled != nil {
		on, err := cfg.FleetEnabled()
		if err == nil && !on {
			return Result{Reason: "fleet-disabled"}, nil
		}
	}

	if cfg.WtStateDir != "" {
		inboxDir := filepath.Join(cfg.WtStateDir, cfg.Slug, "inbox")
		unread, err := hasUnreadInbox(inboxDir)
		if err != nil {
			return Result{}, fmt.Errorf("scan inbox: %w", err)
		}
		if unread {
			return Result{Reason: "inbox-unread"}, nil
		}
	}

	if cfg.Project != "" && bodyHasPlan(body) {
		pending, err := planPendingAck(cfg.coordinatorInbox(), cfg.Slug)
		if err != nil {
			return Result{}, fmt.Errorf("scan coord inbox: %w", err)
		}
		if pending {
			return Result{Reason: "plan-pending-ack"}, nil
		}
	}

	if tokenWrapFresh(cfg) {
		return Result{Reason: "token-wrap-fired"}, nil
	}

	fp := fingerprint(taskFile, cfg.Head)
	if alreadyNudged(cfg.ledgerPath(), fp) {
		return Result{Reason: "already-nudged"}, nil
	}

	return Result{
		ShouldFire: true,
		Reason:     "ok-fire",
		Message:    fmt.Sprintf(reminder, cfg.Slug),
	}, nil
}

// Run wraps Check and persists the ledger marker on a fire so the
// same fingerprint does not double-fire on the next Stop without
// worker progress.
func Run(cfg Config) (Result, error) {
	cfg = cfg.defaults()
	res, err := Check(cfg)
	if err != nil {
		return res, err
	}
	if !res.ShouldFire {
		return res, nil
	}
	taskFile := filepath.Join(cfg.Worktree, "tasks", cfg.Slug+".md")
	fp := fingerprint(taskFile, cfg.Head)
	if err := writeLedger(cfg.ledgerPath(), fp, cfg.Now()); err != nil {
		return res, fmt.Errorf("write ledger: %w", err)
	}
	return res, nil
}

// RunEnv reads the worker env and dispatches to Run. Silent no-op
// outside a worker context so the Stop chain stays clean.
func RunEnv() (Result, error) {
	slug := os.Getenv("SPORE_TASK_SLUG")
	project := os.Getenv("WT_PROJECT")
	if slug == "" {
		return Result{Reason: "noop-context"}, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return Result{Reason: "noop-context"}, nil
	}
	cfg := Config{
		Slug:          slug,
		Worktree:      wd,
		Project:       project,
		WtStateDir:    wtStateDir(),
		LedgerDir:     defaultLedgerDir(),
		TokenStateDir: os.Getenv("SPORE_WORKER_TOKEN_DIR"),
		FleetEnabled:  fleet.Enabled,
		Head:          func() string { return gitHead(wd) },
	}
	return Run(cfg)
}

func (c Config) defaults() Config {
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	return c
}

func (c Config) ledgerPath() string {
	if c.LedgerDir == "" || c.Slug == "" {
		return ""
	}
	return filepath.Join(c.LedgerDir, c.Slug+".json")
}

func (c Config) coordinatorInbox() string {
	base := os.Getenv("SPORE_COORDINATOR_STATE_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state", "spore", "coordinator")
	}
	return filepath.Join(base, c.Project, "inbox")
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

func defaultLedgerDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "spore", "worker-continue")
}

func hasUnreadInbox(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		return true, nil
	}
	return false, nil
}

func bodyHasPlan(body []byte) bool {
	inFence := false
	for _, ln := range bytes.Split(body, []byte("\n")) {
		trim := bytes.TrimSpace(ln)
		if bytes.HasPrefix(trim, []byte("```")) || bytes.HasPrefix(trim, []byte("~~~")) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if !bytes.HasPrefix(trim, []byte("##")) {
			continue
		}
		rest := bytes.TrimLeft(trim, "#")
		rest = bytes.TrimSpace(rest)
		if len(rest) < 4 {
			continue
		}
		if bytes.EqualFold(rest[:4], []byte("plan")) {
			if len(rest) == 4 || !isWord(rest[4]) {
				return true
			}
		}
	}
	return false
}

func isWord(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// planPendingAck returns true when a `plan ready: <slug>` tell sits
// in the coordinator inbox (top or read/) and no matching
// `plan ack: <slug>` reply has been recorded yet.
func planPendingAck(inbox, slug string) (bool, error) {
	if inbox == "" {
		return false, nil
	}
	readyPrefix := "plan ready: " + slug
	ackPrefix := "plan ack: " + slug
	ready, ack := false, false
	for _, sub := range []string{"", "read"} {
		dir := filepath.Join(inbox, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			body, ok := readTellBody(filepath.Join(dir, e.Name()))
			if !ok {
				continue
			}
			if strings.HasPrefix(body, readyPrefix) {
				ready = true
			}
			if strings.HasPrefix(body, ackPrefix) {
				ack = true
			}
		}
	}
	return ready && !ack, nil
}

func readTellBody(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var ev struct {
		Body string `json:"body"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &ev); err != nil {
		return "", false
	}
	if ev.Body != "" {
		return ev.Body, true
	}
	if ev.Msg != "" {
		return ev.Msg, true
	}
	return "", false
}

// tokenWrapFresh returns true when the worker token-monitor wrote a
// snapshot for this slug in the current Stop turn. We approximate
// "current turn" as "after the last ledger firing" (or, when no
// ledger yet, within the last 5 minutes).
func tokenWrapFresh(cfg Config) bool {
	if cfg.TokenStateDir == "" || cfg.Slug == "" {
		return false
	}
	snap := filepath.Join(cfg.TokenStateDir, cfg.Slug+".json")
	info, err := os.Stat(snap)
	if err != nil {
		return false
	}
	last := readLedgerFiredAt(cfg.ledgerPath())
	if last.IsZero() {
		last = cfg.Now().Add(-5 * time.Minute)
	}
	return info.ModTime().After(last)
}

type ledgerEntry struct {
	Fingerprint string    `json:"fingerprint"`
	FiredAt     time.Time `json:"fired_at"`
}

func alreadyNudged(path, fp string) bool {
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var e ledgerEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return false
	}
	return e.Fingerprint == fp
}

func readLedgerFiredAt(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	var e ledgerEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return time.Time{}
	}
	return e.FiredAt
}

func writeLedger(path, fp string, now time.Time) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(ledgerEntry{Fingerprint: fp, FiredAt: now})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fingerprint(taskFile string, head func() string) string {
	h := sha256.New()
	if info, err := os.Stat(taskFile); err == nil {
		fmt.Fprintf(h, "mtime=%d\nsize=%d\n", info.ModTime().UnixNano(), info.Size())
	} else {
		h.Write([]byte("missing\n"))
	}
	if head != nil {
		h.Write([]byte("head="))
		h.Write([]byte(head()))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func gitHead(worktree string) string {
	cmd := exec.Command("git", "-c", "safe.directory="+worktree, "rev-parse", "HEAD")
	cmd.Dir = worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
