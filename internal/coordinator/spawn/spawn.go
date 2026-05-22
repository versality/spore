// Package spawn implements the systemd-ExecStart lifecycle for the
// singleton coordinator: tier-gate, ensure-or-adopt, event-driven
// death-wait, and SIGTERM-clean teardown. The "spore coordinator
// spawn" command is the only production-time wrapper; "spore
// coordinator start" remains the dev-time CLI.
package spawn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/versality/spore/internal/budget"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/tmuxsess"
)

// HookSlot is the global session-closed hook index spawn uses to wait
// for the singleton session's death. A fixed slot lets us unhook on
// exit and lets a restarted spawn overwrite a stale hook rather than
// append a second one.
const HookSlot = 99

// Options configure a Run invocation. Empty fields fall back to
// production defaults; tests pass overrides.
type Options struct {
	// ProjectRoot is the directory whose coordinator session is
	// managed. Required.
	ProjectRoot string

	// Stderr receives one-line status messages. nil -> os.Stderr.
	Stderr io.Writer

	// TierLookup resolves the live account tier. nil -> budget.ActiveTierString.
	// Tests inject this to bypass real OAuth state.
	TierLookup func() (string, error)

	// HookSlot overrides the global session-closed hook index. Zero
	// -> HookSlot (99).
	HookSlot int

	// Signals selects which signals trigger a clean shutdown. nil ->
	// SIGTERM and SIGINT. Tests pass an empty slice to disable.
	Signals []os.Signal
}

// Run is the systemd-ExecStart entry point. It tier-gates, spawns or
// adopts the coordinator tmux session, then blocks until the session
// dies or a shutdown signal arrives. Returns nil on clean exit; an
// error on preflight failure so systemd Restart=on-success does not
// loop until the operator clears the underlying state.
func Run(opts Options) error {
	if opts.ProjectRoot == "" {
		return errors.New("spawn: ProjectRoot required")
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	tierLookup := opts.TierLookup
	if tierLookup == nil {
		tierLookup = budget.ActiveTierString
	}
	hookSlot := opts.HookSlot
	if hookSlot == 0 {
		hookSlot = HookSlot
	}
	sigs := opts.Signals
	if sigs == nil {
		sigs = []os.Signal{syscall.SIGTERM, syscall.SIGINT}
	}

	driver := ResolveDriver(opts.ProjectRoot)
	if err := enforceTier(driver, tierLookup); err != nil {
		return err
	}

	session, spawned, err := fleet.EnsureCoordinator(opts.ProjectRoot)
	if err != nil {
		return fmt.Errorf("ensure coordinator: %w", err)
	}
	if spawned {
		fmt.Fprintf(stderr, "[coordinator-spawn] spawned %s\n", session)
	} else {
		fmt.Fprintf(stderr, "[coordinator-spawn] adopted %s\n", session)
	}

	sigCh := make(chan os.Signal, 1)
	if len(sigs) > 0 {
		signal.Notify(sigCh, sigs...)
		defer signal.Stop(sigCh)
	}

	deathCh := make(chan error, 1)
	go func() { deathCh <- WaitForDeath(session, hookSlot) }()

	select {
	case sig := <-sigCh:
		fmt.Fprintf(stderr, "[coordinator-spawn] %s; killing %s\n", sig, session)
		tmuxsess.Kill(session)
		unhook(hookSlot)
		<-deathCh
		return nil
	case err := <-deathCh:
		if err != nil {
			fmt.Fprintf(stderr, "[coordinator-spawn] wait error: %v\n", err)
		}
		return nil
	}
}

// ResolveDriver returns the driver kind ("claude", "codex", ...) the
// coordinator session will run under. Mirrors fleet's internal
// precedence so the tier-gate engages on the same identity that
// EnsureCoordinator spawns. Falls back to the agent-binary basename
// so an explicit SPORE_AGENT_BINARY=sleep does not trip the gate.
func ResolveDriver(projectRoot string) string {
	if v := os.Getenv("SPORE_COORDINATOR_PROVIDER"); v != "" {
		return v
	}
	if cfg, err := fleet.LoadCoordinatorConfig(projectRoot); err == nil && cfg.Driver != "" {
		return cfg.Driver
	}
	if a := os.Getenv("SPORE_COORDINATOR_AGENT"); a != "" {
		return agentBasename(a)
	}
	if a := os.Getenv(task.AgentBinaryEnv); a != "" {
		return agentBasename(a)
	}
	return "claude"
}

func agentBasename(a string) string {
	fields := strings.Fields(a)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func enforceTier(driver string, lookup func() (string, error)) error {
	if driver != "claude" {
		return nil
	}
	tier, err := lookup()
	if err != nil {
		return fmt.Errorf("tier check failed: %w", err)
	}
	if tier != "max" {
		return fmt.Errorf("active claude account tier=%s, coordinator spawn requires max", tier)
	}
	return nil
}

// WaitForDeath blocks until the named tmux session is gone. Installs a
// global session-closed hook that signals a private wait-for channel
// when the session closes; re-checks has-session after install to
// short-circuit the race where the session died between the caller's
// check and the hook install. Unsets the hook on return. tmux 3.6's
// session-scoped hooks do not fire for session-closed (the session is
// gone before the hook resolves) so the hook must be global, filtered
// by hook_session_name.
func WaitForDeath(session string, hookSlot int) error {
	if !tmuxsess.Has(session) {
		return nil
	}
	channel := fmt.Sprintf("spore-coordinator-spawn-death-%d", os.Getpid())
	hookIdx := fmt.Sprintf("session-closed[%d]", hookSlot)
	hookCmd := fmt.Sprintf(
		"if-shell -F '#{==:#{hook_session_name},%s}' 'run-shell -b \"tmux wait-for -S %s\"'",
		session, channel,
	)
	if out, err := exec.Command("tmux", "set-hook", "-g", hookIdx, hookCmd).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux set-hook: %w: %s", err, strings.TrimSpace(string(out)))
	}
	defer unhook(hookSlot)
	if !tmuxsess.Has(session) {
		return nil
	}
	_ = exec.Command("tmux", "wait-for", channel).Run()
	return nil
}

func unhook(slot int) {
	_ = exec.Command("tmux", "set-hook", "-gu", fmt.Sprintf("session-closed[%d]", slot)).Run()
}
