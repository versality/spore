package hooks

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// BashInput is the shape claude-code passes as ToolInput for the Bash
// tool. Other tools use different shapes; PreToolUse only inspects
// Bash.
type BashInput struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

// ForbiddenPattern is one rule the PreToolUse decider checks against
// the Bash command line. Reason is surfaced verbatim in the deny
// response, so write it as a sentence the operator can act on.
type ForbiddenPattern struct {
	Re     *regexp.Regexp
	Reason string
}

// DefaultForbidden is the starter set of bash patterns spore blocks
// out of the box. Downstream projects override with their own set
// (e.g. nixos-rebuild for a NixOS host repo, terraform apply for an
// infra repo). Keep the kernel set small and obviously universal.
func DefaultForbidden() []ForbiddenPattern {
	return []ForbiddenPattern{
		{
			Re:     regexp.MustCompile(`(?m)^[[:space:]]*sudo([[:space:]]|$)`),
			Reason: "sudo: ask the operator instead of escalating from a hook context",
		},
		{
			Re:     regexp.MustCompile(`\brm[[:space:]]+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)[[:space:]]+/(\s|$)`),
			Reason: "rm -rf /: refusing root-tree wipe",
		},
		{
			Re:     regexp.MustCompile(`\bgit[[:space:]]+push[[:space:]]+(--force|-f)\b`),
			Reason: "git push --force: confirm with the operator before force-pushing",
		},
	}
}

// PreToolUse evaluates a PreToolUse request and returns the response
// claude-code should receive. Bash commands are checked against the
// forbidden set; the first match denies with that pattern's reason.
// AskUserQuestion is denied unconditionally when the request's cwd
// looks like a worker worktree (.worktrees/<slug>): there is no
// operator on the other end of a worker turn, so the agent must act
// on a stated assumption instead.
func PreToolUse(req Request, forbidden []ForbiddenPattern) Response {
	if req.ToolName == "AskUserQuestion" && isWorktreeCWD(req.CWD) {
		return Response{
			HookSpecificOutput: &HookSpecificOutput{
				HookEventName:            "PreToolUse",
				PermissionDecision:       Deny,
				PermissionDecisionReason: "AskUserQuestion: workers run autonomously; no operator is reading. State your assumption in the task file's plan section and act on it. Flip the task to status=blocked only after exhausting alternatives.",
			},
		}
	}
	if req.ToolName != "Bash" {
		return Response{}
	}
	var in BashInput
	if err := json.Unmarshal(req.ToolInput, &in); err != nil {
		return Response{}
	}
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return Response{}
	}
	for _, p := range forbidden {
		if p.Re.MatchString(cmd) {
			return Response{
				HookSpecificOutput: &HookSpecificOutput{
					HookEventName:            "PreToolUse",
					PermissionDecision:       Deny,
					PermissionDecisionReason: p.Reason,
				},
			}
		}
	}
	return Response{}
}

// isWorktreeCWD reports whether path sits inside a `.worktrees/`
// directory, the layout `wt` uses for worker checkouts. An empty cwd
// (older harnesses that did not forward it) returns false so the
// AskUserQuestion block stays opt-in by location.
func isWorktreeCWD(cwd string) bool {
	if cwd == "" {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(cwd))
	if strings.Contains(clean, "/.worktrees/") {
		return true
	}
	return strings.HasSuffix(clean, "/.worktrees") || clean == ".worktrees"
}
