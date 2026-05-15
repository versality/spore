package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SkyhelmTmuxInputBan flags production code that types into another
// agent's TUI - the very-bad pattern of `tmux send-keys` (or paste-
// buffer / load-buffer) aimed at a session running an LLM. It mirrors
// the bash prior art at nix-config/harness/check-skyhelm-tmux-input-
// ban.sh and folds two scans into one lint:
//
//  1. Roots: any line invoking tmux send-keys / paste-buffer whose
//     target literal names a known agent session (skyhelm / codex /
//     opencode / claude). Default Roots match the nix-config layout.
//
//  2. ControlRoots: agent-fleet control-plane code, where ANY tmux
//     send-keys / paste-buffer / load-buffer is banned regardless of
//     target. Catches the indirect form where the session name is
//     built from a variable.
//
// SkipPath allowlists files that legitimately type into themselves
// (a worker's own loop driving its own pane, an agent's self-restart).
// Commented-out lines (first non-blank char is # or //) never match;
// neither does the bash prior art itself (allowlisted by default).
type SkyhelmTmuxInputBan struct {
	Roots        []string
	ControlRoots []string
	SkipPath     []string
	AgentNames   []string
}

func (SkyhelmTmuxInputBan) Name() string { return "skyhelm-tmux-input-ban" }

var (
	defaultBanRoots = []string{
		"bash/",
		"configs/",
		"harness/",
		"nix/features/",
		"nix/packages/",
	}
	defaultBanControlRoots = []string{
		"nix/packages/wt/",
		"nix/packages/wt-go/",
	}
	defaultBanAgentNames = []string{"skyhelm", "codex", "opencode", "claude"}
	defaultBanSelfSkip   = []string{
		"harness/check-skyhelm-tmux-input-ban.sh",
	}
)

func (l SkyhelmTmuxInputBan) Run(root string) ([]Issue, error) {
	roots := l.Roots
	if roots == nil {
		roots = defaultBanRoots
	}
	controlRoots := l.ControlRoots
	if controlRoots == nil {
		controlRoots = defaultBanControlRoots
	}
	agents := l.AgentNames
	if len(agents) == 0 {
		agents = defaultBanAgentNames
	}
	userSkips := append([]string{}, defaultBanSelfSkip...)
	userSkips = append(userSkips, l.SkipPath...)

	files, err := listFiles(root, nil)
	if err != nil {
		return nil, err
	}

	agentAlt := strings.Join(agents, "|")
	reLit := regexp.MustCompile(`(?i)\btmux\s+(?:send-keys|paste-buffer)\b[^\n]*\b(?:` + agentAlt + `)\b`)
	rePaste := regexp.MustCompile(`(?i)\bpaste-buffer\b[^\n]*\b(?:` + agentAlt + `)\b`)
	reCtl := regexp.MustCompile(`\btmux\s+(?:send-keys|paste-buffer|load-buffer)(?:\s|$)`)
	reCtlGo := regexp.MustCompile(`exec\.Command\("tmux"[^\n]*"(?:send-keys|paste-buffer|load-buffer)"`)

	var issues []Issue
	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		if skipPath(rel, userSkips) {
			continue
		}
		inCtl := hasAnyPrefix(rel, controlRoots)
		inRoot := hasAnyPrefix(rel, roots) && !inCtl
		if !inCtl && !inRoot {
			continue
		}
		if inCtl {
			if strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "/testdata/") {
				continue
			}
		}
		if inRoot {
			if strings.HasPrefix(rel, "harness/test-") {
				continue
			}
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("skyhelm-tmux-input-ban: read %s: %w", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "//") {
				continue
			}
			switch {
			case inCtl && (reCtl.MatchString(line) || reCtlGo.MatchString(line)):
				issues = append(issues, Issue{
					Path:    rel,
					Line:    i + 1,
					Message: "agent control plane must not invoke tmux send-keys/paste-buffer/load-buffer: " + trim,
				})
			case inRoot && (reLit.MatchString(line) || rePaste.MatchString(line)):
				issues = append(issues, Issue{
					Path:    rel,
					Line:    i + 1,
					Message: "production code must not type into agent TUI: " + trim,
				})
			}
		}
	}
	return issues, nil
}

func hasAnyPrefix(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}
