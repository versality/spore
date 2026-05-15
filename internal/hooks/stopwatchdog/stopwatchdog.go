// Package stopwatchdog implements the 5s post-Stop watchdog hook.
// The parent hook spawns a detached tick child and exits 0 so the
// Stop chain proceeds. The tick child sleeps WAIT_SECONDS, captures
// the agent's tmux pane, classifies it, and if the pane is still
// running (chain has not released to idle TUI) appends a row to
// worker-stop-errors.jsonl and drops a synthetic tell into the worker
// inbox that explains the timeout. The tell file shape mirrors a
// normal wt-task tell so watchinbox's drain claims it on its next
// pass; the body is the force-release signal the operator and the
// agent can both read.
package stopwatchdog

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/versality/spore/internal/agentpane"
)

// DefaultWait is the post-Stop deadline before the watchdog declares
// the chain hung. Configurable via $SPORE_STOP_WATCHDOG_SECONDS so
// hosts on slow disks can extend it without a rebuild.
const DefaultWait = 5 * time.Second

// Config carries the watchdog's dependencies. Production wires
// DefaultConfig; tests fill in fakes.
type Config struct {
	Inbox      string
	WorkerDir  string
	Agent      string
	Wait       time.Duration
	Now        func() time.Time
	Sleep      func(time.Duration)
	TmuxTarget func() (string, error)
	Capture    agentpane.CaptureFunc
}

// DefaultConfig returns the production wiring: env-derived paths,
// real tmux probes, real captures. SPORE_TASK_INBOX, WT_STATE, and
// SPORE_AGENT_DRIVER must be set by the harness; missing values yield
// a no-op tick (the Stop chain still runs, the watchdog just skips).
func DefaultConfig() Config {
	return Config{
		Inbox:      os.Getenv("SPORE_TASK_INBOX"),
		WorkerDir:  os.Getenv("WT_STATE"),
		Agent:      os.Getenv("SPORE_AGENT_DRIVER"),
		Wait:       envWait(),
		Now:        func() time.Time { return time.Now().UTC() },
		Sleep:      time.Sleep,
		TmuxTarget: tmuxCurrentTarget,
		Capture:    agentpane.RealCapture,
	}
}

func envWait() time.Duration {
	if v := os.Getenv("SPORE_STOP_WATCHDOG_SECONDS"); v != "" {
		var n int
		if _, err := fmt.Sscan(v, &n); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return DefaultWait
}

// Spawn forks the tick child (`<self> hooks stop-watchdog-tick`) in a
// new session so it survives the Stop chain's exit. The parent hook
// returns 0 immediately. If exec.LookPath fails or the env lacks an
// inbox to write into, the spawn is skipped: the chain still works
// without a watchdog and the operator gets no spurious force-release
// tells.
func Spawn() error {
	if os.Getenv("SPORE_TASK_INBOX") == "" {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "hooks", "stop-watchdog-tick")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Env = os.Environ()
	return cmd.Start()
}

// Tick is the child entry point. It sleeps Wait, probes the agent
// pane, and if the chain has not released to idle TUI, logs and drops
// a force-release tell. Always returns nil: a watchdog failure must
// not cascade into a chain failure on the next Stop.
func Tick(cfg Config) error {
	if cfg.Inbox == "" {
		return nil
	}
	if cfg.Wait <= 0 {
		cfg.Wait = DefaultWait
	}
	cfg.Sleep(cfg.Wait)

	target, err := cfg.TmuxTarget()
	if err != nil || target == "" {
		return nil
	}
	state, _ := agentpane.Classify(cfg.Capture, target, cfg.Agent)
	if state == "idle" || state == "dead" {
		return nil
	}

	cfg.appendError(state)
	cfg.dropForceRelease(state)
	return nil
}

func (c Config) appendError(state string) {
	dir := c.WorkerDir
	if dir == "" {
		dir = filepath.Dir(c.Inbox)
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	row := struct {
		TS    string `json:"ts"`
		Slug  string `json:"slug"`
		Kind  string `json:"kind"`
		Wait  string `json:"wait"`
		State string `json:"state"`
		Agent string `json:"agent,omitempty"`
	}{
		TS:    c.Now().Format(time.RFC3339),
		Slug:  inboxSlug(c.Inbox),
		Kind:  "slow-stop-chain",
		Wait:  c.Wait.String(),
		State: state,
		Agent: c.Agent,
	}
	line, err := json.Marshal(&row)
	if err != nil {
		return
	}
	appendFile(filepath.Join(dir, "worker-stop-errors.jsonl"), string(line)+"\n")
}

func (c Config) dropForceRelease(state string) {
	if err := os.MkdirAll(c.Inbox, 0o700); err != nil {
		return
	}
	ts := c.Now()
	name := fmt.Sprintf("%d-stop-watchdog-%s.json", ts.UnixNano(), state)
	body := fmt.Sprintf(
		"stop-watchdog: Stop chain did not release to idle TUI within %s (pane classified %q). "+
			"Force-release: investigate worker-stop-errors.jsonl for the slow-stop-chain row and check the "+
			"sync hooks ahead of watch-inbox; a hung hook starves the asyncRewake watcher.",
		c.Wait.String(), state)
	payload := map[string]string{
		"ts":     ts.Format(time.RFC3339),
		"source": "stop-watchdog",
		"body":   body,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	tmp := filepath.Join(c.Inbox, ".tmp", name)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o700); err != nil {
		return
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(c.Inbox, name))
}

// inboxSlug extracts the slug segment from a worker inbox path of
// shape <WorkerStateDir>/<slug>/inbox. Coordinator inboxes return
// "coordinator"; ad-hoc paths return the parent basename.
func inboxSlug(inbox string) string {
	if inbox == "" {
		return ""
	}
	clean := filepath.Clean(inbox)
	parent := filepath.Dir(clean)
	if filepath.Base(clean) == "inbox" {
		return filepath.Base(parent)
	}
	return filepath.Base(parent)
}

func appendFile(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func tmuxCurrentTarget() (string, error) {
	if os.Getenv("TMUX") == "" {
		return "", nil
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#S:#W").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
