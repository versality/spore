package task

import (
	"fmt"
	"os/exec"
	"strings"
)

func sessionHasDeadPane(session string) (bool, error) {
	out, err := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_dead}").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("tmux list-panes: %v: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "1" {
			return true, nil
		}
	}
	return false, nil
}
