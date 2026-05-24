// Package tmuxsess wraps the three tmux session calls every package
// in the harness needs: has-session, kill-session, list-sessions.
// Before this package they were inlined as one-line exec.Command
// pairs in internal/task, internal/fleet, internal/coordinator/spawn,
// internal/opencode/fleetstop, and the hook entrypoints.
//
// Inheriting the caller's TMUX_TMPDIR / TMUX env is intentional: the
// test suite swaps the socket via env, and the helpers must respect
// that so tests run against a private tmux server rather than the
// operator's live one.
package tmuxsess

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Has reports whether a tmux session with the given name exists.
// Returns false when tmux is not running.
func Has(name string) bool {
	return exec.Command("tmux", "has-session", "-t", exactTarget(name)).Run() == nil
}

// Kill kills a tmux session, ignoring the error from tmux. Use this
// in best-effort cleanup paths where a missing session is not a
// failure (already gone, never spawned, server down).
func Kill(name string) {
	cmd := exec.Command("tmux", "kill-session", "-t", exactTarget(name))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return
	}
	for range 20 {
		if !Has(name) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// KillErr kills a tmux session and surfaces the error to the caller
// with tmux's stderr appended. Use when the caller needs to log or
// react to the failure.
func KillErr(name string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", exactTarget(name))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("tmux kill-session %s: %w", name, err)
		}
		return fmt.Errorf("tmux kill-session %s: %w (%s)", name, err, msg)
	}
	return nil
}

func exactTarget(name string) string {
	if strings.HasPrefix(name, "=") {
		return name
	}
	return "=" + name
}

// List returns the names of every active tmux session in the order
// tmux reports them. When tmux is not running (or no sessions exist)
// the result is (nil, nil); only a genuine exec failure is surfaced.
func List() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// tmux exits non-zero with "no server running" when nothing
		// is up. That is not an error from the caller's perspective.
		if ee, ok := err.(*exec.ExitError); ok {
			if strings.Contains(string(ee.Stderr), "no server running") {
				return nil, nil
			}
		}
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}
