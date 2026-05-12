package fleet

import (
	"os/exec"
	"strings"
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
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p").Output()
	return string(out), err
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

func classifyAgentPane(tr tmuxRunner, target, agent string) (string, string) {
	out, err := tr.capturePane(target)
	if err != nil {
		return "running", ""
	}
	lines := tailNonEmptyLines(out, 12)
	joined := strings.Join(lines, "\n")
	switch agent {
	case "claude":
		return classifyClaudePane(lines, joined)
	case "opencode":
		return classifyOpenCodePane(lines, joined)
	}
	if paneLooksInterrupted(lines) {
		return "dead", agent + " conversation interrupted"
	}
	if agent != "codex" {
		return "running", ""
	}
	if strings.Contains(joined, "• Working (") ||
		strings.Contains(joined, "• Waiting for background terminal") ||
		strings.Contains(joined, "• Running ") {
		return "running", ""
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "› ") || strings.HasPrefix(line, "> ") {
			return "idle", ""
		}
	}
	return "running", ""
}

// classifyClaudePane tells running from idle for a Claude Code rower.
// The TUI keeps the input box and mode bar visible even when no turn
// is in flight, so pane process liveness alone is not enough: a
// rower at an empty prompt, a "Interrupted ·" banner, or a
// pasted-but-unsubmitted brief in the input area all look "alive" to
// the pane but are idle to the operator. The rule is:
//
//	busy markers (esc-to-interrupt, "Cogitating… (Ns ...)") -> running.
//	mode line present + not busy -> idle (covers empty prompt,
//	  interrupted banner, pasted-brief in input, anything else
//	  parked at the input).
//	otherwise -> running (TUI pre-init: pane alive, mode line not
//	  yet rendered).
func classifyClaudePane(lines []string, joined string) (string, string) {
	if claudePaneBusy(joined, lines) {
		return "running", ""
	}
	if claudeHasModeLine(lines) {
		return "idle", ""
	}
	return "running", ""
}

func claudePaneBusy(joined string, lines []string) bool {
	if strings.Contains(joined, "esc to interrupt") {
		return true
	}
	for _, line := range lines {
		if claudeStatusLine(line) {
			return true
		}
	}
	return false
}

// claudeStatusLine matches Claude's "<glyph> <Word>… (Ns · ...)" status
// indicator (e.g. "✶ Cogitating… (53s · ↓ 2.2k tokens · thought for
// 4s)"). Approximates by looking for "… (" followed by a digit-letter
// duration like "53s" or "2m".
func claudeStatusLine(line string) bool {
	i := strings.Index(line, "… (")
	if i < 0 {
		return false
	}
	rest := line[i+len("… ("):]
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(rest) {
		return false
	}
	return rest[digits] == 's' || rest[digits] == 'm'
}

func claudeHasModeLine(lines []string) bool {
	for _, line := range lines {
		if strings.Contains(line, "-- INSERT --") || strings.Contains(line, "-- NORMAL --") {
			return true
		}
	}
	return false
}

func classifyOpenCodePane(lines []string, joined string) (string, string) {
	if paneLooksInterrupted(lines) {
		return "dead", "opencode conversation interrupted"
	}
	for _, marker := range []string{
		"esc to interrupt",
		"ctrl+c to interrupt",
		"Thinking",
		"Working",
		"Running",
	} {
		if strings.Contains(joined, marker) {
			return "running", ""
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == ">" || strings.HasPrefix(trimmed, "> ") || strings.HasPrefix(trimmed, "│ >") {
			return "idle", ""
		}
	}
	return "running", ""
}

// paneLooksInterrupted reports whether the captured tail ends in an
// interrupted banner with no subsequent bullet-line resumption.
func paneLooksInterrupted(lines []string) bool {
	lastInterrupted := -1
	for i, line := range lines {
		for _, marker := range []string{
			"Conversation interrupted",
			"Interrupted by user",
			"aborted by user",
		} {
			if strings.Contains(line, marker) {
				lastInterrupted = i
				break
			}
		}
	}
	if lastInterrupted < 0 {
		return false
	}
	for _, line := range lines[lastInterrupted+1:] {
		if strings.HasPrefix(line, "• ") {
			return false
		}
	}
	return true
}

func tailNonEmptyLines(s string, n int) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}
