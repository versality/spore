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
