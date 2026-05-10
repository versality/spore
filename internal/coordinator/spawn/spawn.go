// Package spawn is the systemd-user ExecStart wrapper for the
// coordinator tmux session (skyhelm). It brings up the singleton
// session if not alive and blocks until it dies. The unit's
// Restart=on-success+RestartSec=1s turns "session died" into a fast
// respawn; preflight failures (missing brief, no project, wrong
// driver tier, bad effort) exit non-zero so on-success does NOT
// respawn (operator clears the underlying state, the reconciler
// restarts on the next chip-on tick).
//
// Why a wrapper at all: `tmux new-session -d` returns the moment
// the server registers the session, before the inner shell execs
// the agent. systemd needs a foreground process to stay up while
// the session is alive so "session died" maps cleanly to "unit
// exited 0". The wrapper installs a global session-closed[<slot>]
// hook filtered by session name, then blocks on `tmux wait-for`,
// which the hook signals when the session goes away.
//
// Session naming, env contract, hook slot, and SIGTERM semantics
// mirror the bash `skyhelm-spawn` verbatim so the systemd-user unit
// graph (skyhelm.service ExecStart, codex-skyhelm-inbox-watcher,
// `wt task skyhelm` operator commands) keeps working unchanged.
package spawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/versality/spore/claudepolicy"
	"github.com/versality/spore/codexpolicy"
)

// HookSlotDefault is the global session-closed hook slot. The slot
// number is fixed so the wrapper can clean it up on exit and so
// re-entry (after Restart) overwrites rather than appends.
const HookSlotDefault = "99"

// DefaultInnerCommand is the tmux session's first-window command.
// Kept as `wt-task launch-skyhelm` so the spore-side lift does not
// also have to port the per-driver agent argv (claude / codex
// invocation) in this commit.
const DefaultInnerCommand = "wt-task launch-skyhelm"

// Config is the runtime configuration for Run. Defaults() fills the
// zero-value fields from environment and stdlib defaults.
type Config struct {
	// SkyhelmDriver pins the driver. Empty falls back to
	// SKYHELM_DRIVER, then SKYHELM_DRIVER_FILE contents, then "claude".
	SkyhelmDriver string
	// SkyhelmDriverFile is read when SkyhelmDriver is empty.
	// Defaults to $SKYHELM_STATE_DIR/driver.
	SkyhelmDriverFile string
	// StateDir mirrors SKYHELM_STATE_DIR.
	StateDir string
	// Brief mirrors SKYHELM_BRIEF.
	Brief string
	// ProjectsFile mirrors $WT_CFG/projects (one absolute project
	// path per line; comments after `#`). The first usable line is
	// adopted as the spawn cwd.
	ProjectsFile string
	// Session is the tmux session name. Empty derives it from
	// driver + effort via skyhelm_<driver>_<effort>.
	Session string
	// HookSlot is the global session-closed hook slot. Defaults to
	// HookSlotDefault ("99"); SKYHELM_SPAWN_HOOK_SLOT overrides.
	HookSlot string
	// CodexEffort mirrors SKYHELM_CODEX_EFFORT (must normalize to
	// "high" per codex-effort-routing).
	CodexEffort string
	// ClaudeEffort mirrors SKYHELM_CLAUDE_EFFORT (any
	// claudepolicy-valid value, including empty).
	ClaudeEffort string
	// Model is SKYHELM_MODEL > WT_AGENT_MODEL > CodexModel for
	// codex; SKYHELM_MODEL > WT_AGENT_MODEL > "" for claude.
	Model string
	// CodexModel is SKYHELM_CODEX_MODEL.
	CodexModel string
	// InnerCommand is the tmux first-window command. Empty uses
	// DefaultInnerCommand.
	InnerCommand string
	// Tmux is the tmux runner. Tests inject a fake; production
	// uses ExecTmux against the real tmux binary.
	Tmux Tmux
	// TierLookup returns the live OAuth tier (max|pro|team|free).
	// Defaults to budget.LookupActiveTier; tests stub it. Only
	// consulted when driver==claude.
	TierLookup func() (string, error)
	// Stderr receives diagnostic logs (`[skyhelm-spawn] ...`).
	// Defaults to os.Stderr.
	Stderr io.Writer
}

// Tmux abstracts the tmux operations the spawn wrapper needs.
// ExecTmux is the production implementation; tests use a fake.
type Tmux interface {
	// Run invokes tmux with args and discards stdout. A non-zero
	// exit is returned as a non-nil error.
	Run(args ...string) error
	// Output invokes tmux with args and returns trimmed stdout.
	Output(args ...string) (string, error)
	// WaitFor blocks until tmux signals channel via `wait-for -S`.
	// Cancel ctx to abort the wait.
	WaitFor(ctx context.Context, channel string) error
}

// Defaults fills zero-value fields from env and stdlib defaults.
func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.SkyhelmDriverFile == "" {
		c.SkyhelmDriverFile = os.Getenv("SKYHELM_DRIVER_FILE")
	}
	if c.SkyhelmDriverFile == "" {
		c.SkyhelmDriverFile = filepath.Join(c.StateDir, "driver")
	}
	if c.SkyhelmDriver == "" {
		c.SkyhelmDriver = os.Getenv("SKYHELM_DRIVER")
	}
	if c.Brief == "" {
		c.Brief = os.Getenv("SKYHELM_BRIEF")
	}
	if c.Brief == "" {
		home, _ := os.UserHomeDir()
		c.Brief = filepath.Join(home, ".config", "skyhelm", "brief.md")
	}
	if c.ProjectsFile == "" {
		c.ProjectsFile = filepath.Join(wtCfgDir(), "projects")
	}
	if c.HookSlot == "" {
		c.HookSlot = os.Getenv("SKYHELM_SPAWN_HOOK_SLOT")
	}
	if c.HookSlot == "" {
		c.HookSlot = HookSlotDefault
	}
	if c.CodexEffort == "" {
		c.CodexEffort = os.Getenv("SKYHELM_CODEX_EFFORT")
	}
	if c.CodexEffort == "" {
		c.CodexEffort = "high"
	}
	if c.ClaudeEffort == "" {
		c.ClaudeEffort = os.Getenv("SKYHELM_CLAUDE_EFFORT")
	}
	if c.CodexModel == "" {
		c.CodexModel = os.Getenv("SKYHELM_CODEX_MODEL")
	}
	if c.CodexModel == "" {
		c.CodexModel = "gpt-5.5"
	}
	if c.InnerCommand == "" {
		c.InnerCommand = DefaultInnerCommand
	}
	if c.Stderr == nil {
		c.Stderr = os.Stderr
	}
	return c
}

func wtCfgDir() string {
	if v := os.Getenv("WT_CFG"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wt")
}

// Run executes the spawn flow against ctx. A nil return is normal
// exit (session died, or SIGTERM reaped it). Non-nil errors map to
// preflight or unrecoverable failures and should exit non-zero so
// systemd's on-success policy does NOT respawn.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.Defaults()
	if cfg.Tmux == nil {
		cfg.Tmux = ExecTmux{}
	}
	if cfg.TierLookup == nil {
		return errors.New("spawn: TierLookup not set; pass budget.LookupActiveTier")
	}

	if _, err := os.Stat(cfg.Brief); err != nil {
		return fmt.Errorf("brief missing at %s", cfg.Brief)
	}

	project, err := firstProject(cfg.ProjectsFile)
	if err != nil {
		return fmt.Errorf("no usable project (first=%s): %w", project, err)
	}
	if st, err := os.Stat(project); err != nil || !st.IsDir() {
		return fmt.Errorf("no usable project (first=%s)", project)
	}

	driver, err := resolveDriver(cfg)
	if err != nil {
		return err
	}

	if err := enforceDriverTier(driver, cfg); err != nil {
		return err
	}

	if driver == "codex" {
		eff, err := normalizeCodexEffort(cfg.CodexEffort)
		if err != nil {
			return err
		}
		cfg.CodexEffort = eff
	}

	// Resolve session name (may be overridden by survivor adoption).
	if cfg.Session == "" {
		cfg.Session = os.Getenv("SKYHELM_SESSION")
	}
	if cfg.Session == "" {
		cfg.Session = SessionName(driver, cfg)
	}
	if existing, ok := adoptSurvivor(cfg.Tmux); ok {
		cfg.Session = existing
	}

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", cfg.StateDir, err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "session"), []byte(cfg.Session+"\n"), 0o600); err != nil {
		return fmt.Errorf("write session marker: %w", err)
	}

	model := resolveModel(driver, cfg)
	display, err := displayName(driver, model, cfg)
	if err != nil {
		return err
	}

	// Install signal-driven shutdown: kill session, unwind hook,
	// return nil so the wrapper exits 0 (on-success respawn).
	shutdown := func() {
		fmt.Fprintln(cfg.Stderr, "[skyhelm-spawn] shutdown signal received; killing tmux session")
		_ = cfg.Tmux.Run("kill-session", "-t", cfg.Session)
		_ = cfg.Tmux.Run("set-hook", "-gu", "session-closed["+cfg.HookSlot+"]")
	}

	// Already alive (operator-launched, or survivor across a unit
	// restart): wait it out instead of double-spawning. Exit 0 so
	// systemd respawns and we mint fresh on next entry.
	if hasSession(cfg.Tmux, cfg.Session) {
		setDisplay(cfg.Tmux, cfg.Session, display)
		fmt.Fprintln(cfg.Stderr, "[skyhelm-spawn] skyhelm session already alive; waiting")
		return waitForSessionDeath(ctx, cfg, shutdown)
	}

	projectName := projectNameOf(project)
	inbox := filepath.Join(cfg.StateDir, projectName, "inbox")
	for _, d := range []string{cfg.StateDir, inbox, filepath.Join(inbox, ".tmp"), filepath.Join(inbox, "read")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	envArgs := []string{
		"-e", "SKYBOT_INBOX=" + inbox,
		"-e", "SPORE_TASK_INBOX=" + inbox,
		"-e", "WT_PROJECT=" + projectName,
		"-e", "SKYHELM_DRIVER=" + driver,
		"-e", "SKYHELM_SESSION=" + cfg.Session,
		"-e", "SKYHELM_CODEX_EFFORT=" + cfg.CodexEffort,
		"-e", "SKYHELM_REPO_ROOT=" + project,
		"-e", "SPORE_COORDINATOR_STATE_DIR=" + cfg.StateDir,
	}
	if model != "" {
		envArgs = append(envArgs, "-e", "SKYHELM_MODEL="+model)
	}
	if cfg.ClaudeEffort != "" {
		envArgs = append(envArgs, "-e", "SKYHELM_CLAUDE_EFFORT="+cfg.ClaudeEffort)
	}

	fmt.Fprintf(cfg.Stderr, "[skyhelm-spawn] spawning in %s (project=%s display=%s)\n",
		project, projectName, display)

	args := []string{
		"new-session", "-d",
		"-s", cfg.Session,
		"-n", display,
		"-c", project,
	}
	args = append(args, envArgs...)
	args = append(args, cfg.InnerCommand)
	if err := cfg.Tmux.Run(args...); err != nil {
		return fmt.Errorf("tmux new-session failed: %w", err)
	}
	setDisplay(cfg.Tmux, cfg.Session, display)
	return waitForSessionDeath(ctx, cfg, shutdown)
}

// SessionName mirrors wt_skyhelm_session_name: skyhelm_<driver>_<effort>.
// `_` is the separator (tmux parses `:` as session/window split).
func SessionName(driver string, cfg Config) string {
	effort := "high"
	switch driver {
	case "claude":
		if cfg.ClaudeEffort != "" {
			effort = cfg.ClaudeEffort
		}
	case "codex":
		if cfg.CodexEffort != "" {
			effort = cfg.CodexEffort
		}
	}
	return fmt.Sprintf("skyhelm_%s_%s", driver, effort)
}

// adoptSurvivor picks up a live "skyhelm" or "skyhelm_*" session if
// one exists. Spawn config (driver flip, effort change) can leave a
// live session unmatched; without adoption, the wrapper would
// double-spawn.
func adoptSurvivor(t Tmux) (string, bool) {
	out, err := t.Output("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "skyhelm" || strings.HasPrefix(name, "skyhelm_") {
			return name, true
		}
	}
	return "", false
}

func resolveDriver(cfg Config) (string, error) {
	driver := cfg.SkyhelmDriver
	if driver == "" && cfg.SkyhelmDriverFile != "" {
		if b, err := os.ReadFile(cfg.SkyhelmDriverFile); err == nil {
			driver = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
		}
	}
	if driver == "" {
		driver = "claude"
	}
	switch driver {
	case "claude", "codex":
		return driver, nil
	default:
		return "", fmt.Errorf("unknown skyhelm driver: %s", driver)
	}
}

func enforceDriverTier(driver string, cfg Config) error {
	if driver != "claude" {
		return nil
	}
	tier, err := cfg.TierLookup()
	if err != nil {
		// Bash version swallows lookup errors and reports the
		// resulting empty tier as "unknown"; mirror that so a
		// transient creds read failure still yields a clear
		// preflight message rather than a wrapped error.
		tier = ""
	}
	if tier != "max" {
		shown := tier
		if shown == "" {
			shown = "unknown"
		}
		return fmt.Errorf("active claude account tier=%s, claude skyhelm requires max", shown)
	}
	return nil
}

func normalizeCodexEffort(raw string) (string, error) {
	if raw == "" {
		return "high", nil
	}
	eff, err := codexpolicy.NormalizeEffort(raw, "high")
	if err != nil {
		return "", err
	}
	if eff != "high" {
		return "", fmt.Errorf("codex effort routing: SKYHELM_CODEX_EFFORT must be 'high' (got: %s). See nix/harness/claude/rules/codex-effort-routing.md", raw)
	}
	return "high", nil
}

func resolveModel(driver string, cfg Config) string {
	if cfg.Model != "" {
		return cfg.Model
	}
	if v := os.Getenv("SKYHELM_MODEL"); v != "" {
		return v
	}
	wt := os.Getenv("WT_AGENT_MODEL")
	switch driver {
	case "codex":
		if wt != "" {
			return wt
		}
		return cfg.CodexModel
	case "claude":
		return wt
	}
	return ""
}

func displayName(driver, model string, cfg Config) (string, error) {
	tag := driver
	switch driver {
	case "codex":
		eff, err := normalizeCodexEffort(cfg.CodexEffort)
		if err != nil {
			return "", err
		}
		tag = "codex-" + eff
	case "claude":
		if cfg.ClaudeEffort != "" {
			eff, err := claudepolicy.NormalizeEffort(cfg.ClaudeEffort, "")
			if err != nil {
				return "", err
			}
			tag = "claude:" + eff
		}
	}
	if model != "" {
		return fmt.Sprintf("skyhelm [%s %s]", tag, model), nil
	}
	return fmt.Sprintf("skyhelm [%s]", tag), nil
}

func setDisplay(t Tmux, session, display string) {
	_ = t.Run("rename-window", "-t", session, display)
	_ = t.Run("set-window-option", "-t", session, "automatic-rename", "off")
	_ = t.Run("select-pane", "-t", session, "-T", display)
	_ = t.Run("set-option", "-t", session, "@skyhelm_display", display)
}

func hasSession(t Tmux, name string) bool {
	return t.Run("has-session", "-t", name) == nil
}

// waitForSessionDeath blocks until the named tmux session closes,
// without polling. Installs a global session-closed hook filtered by
// session name; the hook signals our private channel via wait-for -S,
// which the body waits on. Session-scoped hooks (`-t <sess>`) do NOT
// fire for session-closed in tmux 3.6 (the session is gone before the
// hook resolves), hence -g.
func waitForSessionDeath(ctx context.Context, cfg Config, shutdown func()) error {
	t := cfg.Tmux
	if !hasSession(t, cfg.Session) {
		return nil
	}
	chan_ := fmt.Sprintf("skyhelm-spawn-death-%d", os.Getpid())
	body := fmt.Sprintf(
		`if-shell -F '#{==:#{hook_session_name},%s}' 'run-shell -b "tmux wait-for -S %s"'`,
		cfg.Session, chan_)
	_ = t.Run("set-hook", "-g", "session-closed["+cfg.HookSlot+"]", body)
	// Race: session may have died between has-session and set-hook.
	if !hasSession(t, cfg.Session) {
		_ = t.Run("set-hook", "-gu", "session-closed["+cfg.HookSlot+"]")
		return nil
	}

	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- t.WaitFor(waitCtx, chan_) }()

	select {
	case <-ctx.Done():
		shutdown()
		<-done
		return nil
	case <-done:
		_ = t.Run("set-hook", "-gu", "session-closed["+cfg.HookSlot+"]")
		return nil
	}
}

func projectNameOf(p string) string {
	if abs, err := filepath.EvalSymlinks(p); err == nil {
		p = abs
	}
	return filepath.Base(p)
}

// firstProject reads path and returns the first non-blank,
// non-comment line. Comments after `#` are stripped; surrounding
// whitespace is trimmed.
func firstProject(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
	return "", errors.New("projects file empty")
}
