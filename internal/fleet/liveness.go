package fleet

import (
	"os/exec"
	"strings"

	"github.com/versality/spore/internal/agentpane"
)

// paneInfo is one row of `tmux list-panes -a -F "<session>\t<window>\t<dead>\t<dead_status>\t<command>\t<title>"`.
type paneInfo struct {
	session, window, dead, deadStatus, command, title string
}

// tmuxRunner is the seam tests fill with a fake tmux.
type tmuxRunner interface {
	listPanes() (string, error)
	capturePane(target string) (string, error)
	hasSession(name string) bool
}

type realTmux struct{}

func (realTmux) listPanes() (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{session_name}\t#{window_name}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_command}\t#{pane_title}").Output()
	return string(out), err
}

func (realTmux) capturePane(target string) (string, error) {
	return agentpane.RealCapture(target)
}

func (realTmux) hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func parsePanes(out string) []paneInfo {
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		for len(parts) < 6 {
			parts = append(parts, "")
		}
		panes = append(panes, paneInfo{
			session:    parts[0],
			window:     parts[1],
			dead:       parts[2],
			deadStatus: parts[3],
			command:    parts[4],
			title:      parts[5],
		})
	}
	return panes
}

// agentShellCommands are the foreground commands a tmux pane reports
// when the agent process has exited but the parent shell is still in
// the pane. None of the supported rower agents ever sit at one of
// these as their normal foreground command, so seeing one in an
// agent-named window is the zombie signal.
var agentShellCommands = map[string]bool{
	"bash": true,
	"sh":   true,
	"zsh":  true,
	"fish": true,
	"dash": true,
}

// classifyAgentPane delegates to internal/agentpane. Kept as a fleet-
// local wrapper so existing call sites in livenessstatus.go stay
// unchanged.
func classifyAgentPane(tr tmuxRunner, target, agent string) (string, string) {
	return agentpane.Classify(tr.capturePane, target, agent)
}
