// Package tokenmonitor is the worker-side claude-code Stop-hook helper
// that watches a worker's context budget. It reads the hook payload on
// stdin (session_id + transcript_path), parses the latest assistant
// message's usage block from the transcript, and on threshold crossing
// fires a wrap-up reminder so the worker can flush progress to its
// tasks/<slug>.md and kill its own driver process. The tmux session
// (and its scrollback) survives the driver kill via remain-on-exit, so
// the last-turn context stays readable for the operator and for the
// next reconciler-driven respawn.
//
// The threshold is tier-keyed: max-tier sessions wrap at 180k (20k
// headroom under the 200k quality cliff); sub-max sessions wrap at
// 120k to dodge the 150k hard block. Tier defaults to non-max so a
// session with an unknown tier wraps at the safer cap.
//
// The worker monitor skips any session whose inbox is under the
// coordinator state dir (those are owned by the coordinator monitor)
// and any session with no inbox set.
package tokenmonitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/coordinator"
	"github.com/versality/spore/internal/transcript"
)

const (
	DefaultWrapMax = 180000
	DefaultWrapSub = 120000
)

// WrapKillDriver is the wrap-action scope that tears down only the
// agent driver process (claude / codex / opencode) and leaves the
// surrounding tmux session and pane alive in remain-on-exit state so
// the last-turn scrollback survives for the operator and the next
// reconciler-driven respawn. It is the only scope Check emits; the
// legacy whole-session kill was retired because losing the scrollback
// strips every post-mortem signal a wrap fire would otherwise carry.
const WrapKillDriver = "kill-driver"

type Config struct {
	WrapMax             int
	WrapSub             int
	WrapOverride        int
	Tier                string
	Inbox               string
	CoordinatorStateDir string
}

type CheckResult struct {
	Ctx     int    `json:"ctx"`
	WrapCap int    `json:"wrap_cap"`
	Tier    string `json:"tier"`
	Slug    string `json:"slug,omitempty"`
	Level   string `json:"level"`
	// WrapAction is the kill scope the worker should apply on a wrap
	// fire. Always WrapKillDriver when Level == "wrap"; empty
	// otherwise.
	WrapAction string `json:"wrap_action,omitempty"`
	Message    string `json:"message,omitempty"`
	ShouldFire bool   `json:"should_fire"`
}

type HookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func (c Config) Defaults() Config {
	if c.WrapMax <= 0 {
		c.WrapMax = DefaultWrapMax
	}
	if c.WrapSub <= 0 {
		c.WrapSub = DefaultWrapSub
	}
	if c.CoordinatorStateDir == "" {
		c.CoordinatorStateDir = defaultCoordinatorStateDir()
	}
	return c
}

func defaultCoordinatorStateDir() string {
	return coordinator.StateDir()
}

// IsCoordinator returns true if the inbox is under the coordinator
// state dir; the coordinator token monitor handles those.
func (c Config) IsCoordinator() bool {
	if c.Inbox == "" {
		return false
	}
	stateRoot := strings.TrimRight(c.CoordinatorStateDir, "/")
	if stateRoot == "" {
		return false
	}
	return c.Inbox == stateRoot || strings.HasPrefix(c.Inbox, stateRoot+"/")
}

// WrapCap returns the effective wrap threshold given the configured
// override / tier. WrapOverride > 0 wins for any tier (test/debug);
// otherwise tier=="max" picks WrapMax; everything else picks WrapSub.
func (c Config) WrapCap() int {
	if c.WrapOverride > 0 {
		return c.WrapOverride
	}
	if c.Tier == "max" {
		return c.WrapMax
	}
	return c.WrapSub
}

// Slug returns the worker slug parsed from the inbox layout
// <state>/<slug>/inbox. Returns "" when the layout doesn't match.
func (c Config) Slug() string {
	if c.Inbox == "" {
		return ""
	}
	parent := filepath.Base(filepath.Dir(c.Inbox))
	if parent == "." || parent == "/" || parent == "" {
		return ""
	}
	return parent
}

// Check reads the transcript, sums context tokens, and decides whether
// the worker has crossed its wrap cap. When over the cap, ShouldFire is
// true, WrapAction is WrapKillDriver, and Message is the wrap-up
// reminder for the worker to flush to tasks/<slug>.md and kill its own
// driver process (keeping the tmux session and scrollback alive) so the
// reconciler respawns a fresh worker against the same worktree.
func Check(cfg Config, payload HookPayload) CheckResult {
	cfg = cfg.Defaults()

	if cfg.Inbox == "" || cfg.IsCoordinator() {
		return CheckResult{Level: "skip"}
	}

	slug := cfg.Slug()
	if slug == "" {
		return CheckResult{Level: "skip"}
	}

	tpath := payload.TranscriptPath
	if tpath == "" || !fileExists(tpath) {
		tpath = transcript.FindFallbackTranscript()
	}
	if tpath == "" {
		return CheckResult{Level: "skip", Slug: slug}
	}

	wrap := cfg.WrapCap()
	ctx := transcript.SumContextTokens(tpath)
	result := CheckResult{
		Ctx:     ctx,
		WrapCap: wrap,
		Tier:    cfg.Tier,
		Slug:    slug,
	}
	if ctx <= 0 {
		result.Level = "ok"
		return result
	}
	if ctx < wrap {
		result.Level = "ok"
		return result
	}

	result.Level = "wrap"
	result.ShouldFire = true
	result.WrapAction = WrapKillDriver
	var reason string
	if cfg.Tier == "max" {
		reason = fmt.Sprintf("Quality degrades past 200k on max; %d leaves 20k headroom to flush.", wrap)
	} else {
		reason = "Sub-max account; the 150k hard block is close."
	}
	result.Message = fmt.Sprintf(
		"WORKER TOKEN MONITOR (wrap): context %d tokens >= wrap cap %d on tier=%s.\n"+
			"%s Wrap up NOW:\n"+
			"  1. Flush in-flight progress to tasks/%s.md so the next worker boots from it.\n"+
			"  2. If you have a blocker for the coordinator, send it before exiting.\n"+
			"  3. Kill only the agent driver process; the tmux session and pane stay\n"+
			"     so the last-turn scrollback is readable on the next attach:\n"+
			"       %s\n"+
			"The fleet reconciler resumes a fresh worker against the same worktree on the next pass.",
		ctx, wrap, normTier(cfg.Tier), reason, slug, DriverKillCommand())
	return result
}

// DriverKillCommand returns the shell snippet a worker runs on a wrap
// fire to terminate the agent driver subtree without tearing down its
// tmux session. Resolving the pane's tty via tmux and pkilling every
// process attached to it kills the wrapper sh-c plus its agent child
// (claude / codex / opencode) without naming the binary, so the same
// snippet works across drivers. remain-on-exit (set by the worker
// session spawner) keeps the now-empty pane around as a dead pane so
// its scrollback survives until the next reconciler-driven respawn.
func DriverKillCommand() string {
	return `pkill -TERM -t "$(tmux display-message -p '#{pane_tty}' | sed 's,^/dev/,,')"`
}

func normTier(t string) string {
	if t == "" {
		return "unknown"
	}
	return t
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
