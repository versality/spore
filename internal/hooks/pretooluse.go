package hooks

import (
	"encoding/json"
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
// claude-code should receive. Non-Bash tools are always allowed (this
// scaffold only knows about shell). Bash commands are checked against
// the forbidden set; the first match denies with that pattern's
// reason.
func PreToolUse(req Request, forbidden []ForbiddenPattern) Response {
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
