package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// tmuxNewWindow launches command in a detached tmux window on the
// caller's current session. The window stays alive for postExit
// seconds after the command exits, so a fast crash is visible to the
// operator instead of vanishing.
func tmuxNewWindow(name, command string, postExitHold int) error {
	wrapped := fmt.Sprintf("%s; rc=$?; printf '\\n[rover exit %%d]\\n' $rc; sleep %d", command, postExitHold)
	cmd := exec.Command("tmux", "new-window", "-d", "-n", name, wrapped)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-window: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tmuxSendKeys feeds keystrokes to a named window. Each key is sent
// as a separate token; "Enter" submits.
func tmuxSendKeys(name string, keys ...string) error {
	args := append([]string{"send-keys", "-t", name}, keys...)
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux send-keys: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tmuxCapturePane returns the visible content of a named window's
// only pane.
func tmuxCapturePane(name string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", name, "-p")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// tmuxHasWindow reports whether a window of the given name exists in
// the current session.
func tmuxHasWindow(name string) (bool, error) {
	cmd := exec.Command("tmux", "list-windows", "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("tmux list-windows: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// tmuxKillWindow removes a window. Silent if it does not exist.
func tmuxKillWindow(name string) error {
	exists, err := tmuxHasWindow(name)
	if err != nil || !exists {
		return err
	}
	cmd := exec.Command("tmux", "kill-window", "-t", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-window: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
