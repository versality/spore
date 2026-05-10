// Package proactiveloop is the Go port of the bash
// skyhelm-proactive-loop systemd-user 5m driver. Tick(cfg) gathers
// fleet status, idle-watchdog findings, queue-classifier verdicts, and
// state.md drift, then decides whether to send a wake message into the
// skyhelm inbox. Concurrent ticks are skipped via flock; stable exit
// codes drive the systemd unit.
package proactiveloop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Result is the outcome of one Tick. Stdout/Stderr mirror the bash
// echos; ExitCode mirrors the script exit (0 ok, 1 inbox write failed,
// 2 unresolved coordinator work after fail-on-stall).
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	// Skipped is set when the lock was already held; ExitCode 0.
	Skipped bool
}

// Runner shells out to a configured binary. stdout is captured; non-zero
// exit codes are returned in code (errors from spawn return -1).
type Runner func(name string, args ...string) (stdout string, code int)

// Config is the runtime configuration for Tick. Defaults() fills the
// zero-value fields from environment variables and stdlib defaults.
type Config struct {
	StateDir   string
	StateFile  string
	LoopState  string
	LoopEvents string
	LockFile   string

	WtCfg        string
	ProjectsFile string

	WtTaskBin     string
	BudgetBin     string
	WatchdogBin   string
	ClassifierBin string

	Floor       int
	RepeatAfter time.Duration
	FailOnStall bool
	DryRun      bool

	LocalHost string

	Now    func() time.Time
	Exec   Runner
	Random func() string

	// BudgetAdvice overrides the BudgetBin call (mirrors
	// SKYHELM_PROACTIVE_LOOP_BUDGET_ADVICE).
	BudgetAdvice string
}

// Defaults returns a copy of c with zero-value fields populated from
// the environment and stdlib defaults.
func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = envOr("SKYHELM_STATE_DIR", "")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.StateFile == "" {
		c.StateFile = envOr("SKYHELM_STATE_FILE", filepath.Join(c.StateDir, "state.md"))
	}
	if c.LoopState == "" {
		c.LoopState = envOr("SKYHELM_PROACTIVE_LOOP_STATE", filepath.Join(c.StateDir, "proactive-loop.last"))
	}
	if c.LoopEvents == "" {
		c.LoopEvents = envOr("SKYHELM_PROACTIVE_LOOP_EVENTS", filepath.Join(c.StateDir, "proactive-loop.jsonl"))
	}
	if c.LockFile == "" {
		c.LockFile = envOr("SKYHELM_PROACTIVE_LOOP_LOCK", filepath.Join(c.StateDir, "proactive-loop.lock"))
	}
	if c.WtCfg == "" {
		home, _ := os.UserHomeDir()
		c.WtCfg = envOr("WT_CFG", filepath.Join(home, ".config", "wt"))
	}
	if c.ProjectsFile == "" {
		c.ProjectsFile = envOr("WT_PROJECTS_FILE", filepath.Join(c.WtCfg, "projects"))
	}
	if c.WtTaskBin == "" {
		c.WtTaskBin = envOr("WT_TASK_BIN", "wt-task")
	}
	if c.BudgetBin == "" {
		c.BudgetBin = envOr("SKYHELM_BUDGET_BIN", "skyhelm-budget")
	}
	if c.WatchdogBin == "" {
		c.WatchdogBin = envOr("SKYHELM_IDLE_WATCHDOG_BIN", "skyhelm-idle-watchdog")
	}
	if c.ClassifierBin == "" {
		c.ClassifierBin = envOr("SKYHELM_QUEUE_CLASSIFIER_BIN", "skyhelm-queue-classifier")
	}
	if c.Floor == 0 {
		c.Floor = envInt("WT_FLEET_FLOOR", 5)
		if c.Floor < 1 {
			c.Floor = 5
		}
	}
	if c.RepeatAfter == 0 {
		secs := envInt("SKYHELM_PROACTIVE_LOOP_REPEAT_AFTER", 0)
		c.RepeatAfter = time.Duration(secs) * time.Second
	}
	if c.LocalHost == "" {
		c.LocalHost = envOr("SKYHELM_LOCAL_HOST", "")
	}
	if c.LocalHost == "" {
		host, err := os.Hostname()
		if err == nil {
			if i := strings.IndexByte(host, '.'); i > 0 {
				host = host[:i]
			}
			c.LocalHost = host
		} else {
			c.LocalHost = "unknown"
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Exec == nil {
		c.Exec = systemRunner
	}
	if c.Random == nil {
		c.Random = defaultRandom
	}
	if c.BudgetAdvice == "" {
		c.BudgetAdvice = os.Getenv("SKYHELM_PROACTIVE_LOOP_BUDGET_ADVICE")
	}
	return c
}

// Tick runs one pass of the proactive loop. It is safe to call
// concurrently: a flock on cfg.LockFile causes overlapping calls to
// return Skipped without touching state.
func Tick(cfg Config) Result {
	cfg = cfg.Defaults()
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return Result{ExitCode: 1, Stderr: fmt.Sprintf("skyhelm-proactive-loop: mkdir %s: %v\n", cfg.StateDir, err)}
	}

	lock, err := os.OpenFile(cfg.LockFile, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return Result{ExitCode: 1, Stderr: fmt.Sprintf("skyhelm-proactive-loop: open lock: %v\n", err)}
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return Result{ExitCode: 0, Stdout: "skyhelm-proactive-loop: ok (already running)\n", Skipped: true}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	return runUnderLock(cfg)
}

func runUnderLock(cfg Config) Result {
	projects := readProjects(cfg)

	tasks := loadTasks(projects, cfg.LocalHost)
	if len(tasks.local) == 0 {
		return Result{ExitCode: 0, Stdout: "skyhelm-proactive-loop: ok (no tasks)\n"}
	}

	fleetOut, fleetRC := cfg.Exec(cfg.WtTaskBin, "fleet", "status")
	activeLive := parseActiveLive(fleetOut)

	advice := budgetAdvice(cfg)
	replenishFloor := cfg.Floor
	if advice == "tighten" {
		replenishFloor = cfg.Floor / 2
	}

	watchdogOut, watchdogRC := cfg.Exec(cfg.WatchdogBin)

	var replenishOut string
	var replenishRC int
	var stdout strings.Builder
	if advice == "ration" || replenishFloor < 1 {
		fmt.Fprintf(&stdout, "skyhelm-proactive-loop: %s; skipping replenish\n", advice)
		stdout.WriteString("replenish: skipped (budget=" + advice + ")\n")
		replenishOut = "replenish: skipped (budget=" + advice + ")"
	} else {
		replenishOut, replenishRC = cfg.Exec(cfg.WtTaskBin,
			"fleet", "replenish", "--floor", strconv.Itoa(replenishFloor), "--dry-run")
		_ = replenishRC
	}

	startable, parked, paused, blocked, invalid := classify(cfg, projects, replenishFloor, activeLive)
	stale := staleStateRows(cfg.StateFile, tasks)

	unread, unreadNewest := countUnreadInboxes(cfg, projects)

	actions := buildActions(actionsInput{
		advice:         advice,
		replenishFloor: replenishFloor,
		activeLive:     activeLive,
		startable:      startable,
		parked:         parked,
		paused:         paused,
		blocked:        blocked,
		invalid:        invalid,
		stale:          stale,
		unread:         unread,
		unreadNewest:   unreadNewest,
		watchdogRC:     watchdogRC,
		watchdogOut:    watchdogOut,
		replenishRC:    replenishRC,
		replenishOut:   replenishOut,
		fleetRC:        fleetRC,
		fleetOut:       fleetOut,
	})

	if len(actions) == 0 {
		clearLoopState(cfg)
		appendEvent(cfg, "ok", "", "ok")
		stdout.WriteString("skyhelm-proactive-loop: ok\n")
		return Result{ExitCode: 0, Stdout: stdout.String()}
	}

	if mechanicalReconcile(cfg, replenishOut, replenishFloor, &stdout) {
		clearLoopState(cfg)
		stdout.WriteString("skyhelm-proactive-loop: ok (mechanical reconcile)\n")
		return Result{ExitCode: 0, Stdout: stdout.String()}
	}

	message := buildMessage(activeLive, replenishFloor, actions)
	fingerprint := fingerprintOf(message)
	priorFP, priorSent := readLoopState(cfg.LoopState)
	now := cfg.Now()

	if priorFP == fingerprint {
		if cfg.RepeatAfter == 0 || now.Sub(priorSent) < cfg.RepeatAfter {
			appendEvent(cfg, "recorded-silent", fingerprint, message)
			stdout.WriteString("skyhelm-proactive-loop: recorded unchanged state: " + message + "\n")
			return Result{ExitCode: 0, Stdout: stdout.String()}
		}
	}

	if err := writeInbox(cfg, projects, message); err != nil {
		appendEvent(cfg, "error", fingerprint, message)
		return Result{
			ExitCode: 1,
			Stdout:   stdout.String(),
			Stderr:   "skyhelm-proactive-loop: failed to write skyhelm inbox\n",
		}
	}
	if !cfg.DryRun {
		writeLoopState(cfg.LoopState, fingerprint, now)
	}
	appendEvent(cfg, "sent", fingerprint, message)
	stdout.WriteString("skyhelm-proactive-loop: sent wake: " + message + "\n")
	if !cfg.DryRun && cfg.FailOnStall {
		return Result{
			ExitCode: 2,
			Stdout:   stdout.String(),
			Stderr:   "skyhelm-proactive-loop: unresolved coordinator work remains after mechanical reconcile\n",
		}
	}
	return Result{ExitCode: 0, Stdout: stdout.String()}
}

// budgetAdvice mirrors the bash budget_advice: env override wins,
// otherwise call the budget bin and parse the .advice JSON field. Any
// unrecognised result clamps to "ration".
func budgetAdvice(cfg Config) string {
	if cfg.BudgetAdvice != "" {
		return clampAdvice(cfg.BudgetAdvice)
	}
	out, code := cfg.Exec(cfg.BudgetBin, "query")
	if code != 0 {
		return "ration"
	}
	advice := extractJSONField(out, "advice")
	return clampAdvice(advice)
}

func clampAdvice(s string) string {
	switch s {
	case "ok", "tighten", "ration":
		return s
	default:
		return "ration"
	}
}

// fingerprintOf returns the sha256 hex of message + "\n" (matches bash
// `printf '%s\n' "$message" | sha256sum`).
func fingerprintOf(message string) string {
	sum := sha256.Sum256([]byte(message + "\n"))
	return hex.EncodeToString(sum[:])
}

func buildMessage(activeLive, floor int, actions []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "skyhelm proactive loop: active-live=%d floor=%d", activeLive, floor)
	for _, a := range actions {
		b.WriteString("; ")
		b.WriteString(a)
	}
	return b.String()
}

// mechanicalReconcile runs replenish + reap when the dry-run output
// indicates we would promote drafts and agents are on. Returns true on
// successful promotion (caller treats the tick as resolved).
func mechanicalReconcile(cfg Config, replenishOut string, floor int, stdout *strings.Builder) bool {
	if cfg.DryRun {
		return false
	}
	if !strings.Contains(replenishOut, "would-promote=") {
		return false
	}
	if !agentsOn(cfg) {
		appendEvent(cfg, "mechanical-skipped", "agents-off", "agents flag off; not starting drafts")
		return false
	}
	out, rc := cfg.Exec(cfg.WtTaskBin, "fleet", "replenish", "--floor", strconv.Itoa(floor))
	reapOut, reapRC := cfg.Exec(cfg.WtTaskBin, "fleet", "reap")
	appendEvent(cfg, "mechanical", "",
		fmt.Sprintf("replenish_rc=%d reap_rc=%d replenish=%s reap=%s", rc, reapRC, out, reapOut))
	fmt.Fprintf(stdout, "skyhelm-proactive-loop: mechanical reconcile: %s\n", out)
	return promotedAtLeastOne(out)
}

func agentsOn(cfg Config) bool {
	out, _ := cfg.Exec(cfg.WtTaskBin, "agents", "status")
	return strings.TrimSpace(out) == "on"
}

func promotedAtLeastOne(out string) bool {
	for _, tok := range strings.Fields(out) {
		const pfx = "promoted="
		if !strings.HasPrefix(tok, pfx) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(tok[len(pfx):]))
		if err == nil && n >= 1 {
			return true
		}
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
