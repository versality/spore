// Package agentpane classifies an agent tmux pane as running, idle,
// or dead from its captured output. It is leaf-shaped: no fleet,
// task, or hook dependency. Callers pass a CaptureFunc plus the pane
// target and the driver name; the package handles the per-driver TUI
// heuristics.
//
// The classifier was historically embedded in internal/fleet; lifting
// it lets internal/hooks/wtmergemechanical reuse the same decision
// without dragging in fleet's coordinator surface.
package agentpane

import (
	"os/exec"
	"strings"
)

// CaptureFunc returns the tmux capture-pane output for target. fleet's
// tmuxRunner.capturePane and a real tmux invocation both satisfy it.
type CaptureFunc func(target string) (string, error)

// RealCapture runs `tmux capture-pane -t <target> -p`.
func RealCapture(target string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p").Output()
	return string(out), err
}

// Classify reads the pane at target and returns ("running"|"idle"|"dead", detail).
// Unrecognised agents default to "running" so the caller's existing
// behavior is not perturbed.
func Classify(capture CaptureFunc, target, agent string) (string, string) {
	out, err := capture(target)
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
		strings.Contains(joined, "esc to interrupt") {
		return "running", ""
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "› ") || strings.HasPrefix(line, "> ") {
			return "idle", ""
		}
	}
	if stopHookChainVisible(joined) {
		return "idle", ""
	}
	return "running", ""
}

// classifyClaudePane tells running from idle for a Claude Code worker.
// The TUI keeps the input box and mode bar visible even when no turn
// is in flight, so pane process liveness alone is not enough: a
// worker at an empty prompt, a "Interrupted ·" banner, or a
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
	// Claude hides the mode line while its Stop-hook chain runs ("•
	// Running Stop hook: ..." / "• Running N Stop hooks"). The agent's
	// turn is logically over; the prompt returns once the chain ends.
	// Treat the chain-visible tail as idle so fleet wake can re-mint
	// instead of skipping the worker as busy.
	if stopHookChainVisible(joined) {
		return "idle", ""
	}
	return "running", ""
}

// stopHookChainVisible reports whether the captured tail shows a
// Stop-hook chain bullet without any busy spinner alongside it. Both
// "• Running Stop hook: ..." and "• Running N Stop hooks" qualify;
// "esc to interrupt" anywhere in the tail keeps the agent in the
// running state (the chain bullet may be old scrollback above a fresh
// turn). Shared between claude and codex.
func stopHookChainVisible(joined string) bool {
	if strings.Contains(joined, "esc to interrupt") {
		return false
	}
	if !strings.Contains(joined, "Stop hook") && !strings.Contains(joined, "Stop hooks") {
		return false
	}
	return strings.Contains(joined, "• Running ")
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
