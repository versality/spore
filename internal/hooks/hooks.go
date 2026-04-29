// Package hooks holds spore's Claude-Code hook entry points. Each
// event (PreToolUse, Stop, ...) maps to a Go function that takes the
// hook request, evaluates whatever policy applies, and returns a
// decision the harness writes back to claude-code on stdout.
//
// The kernel ships JSON-protocol types, a PreToolUse decider that
// blocks forbidden bash patterns, and a no-op Stop. Wiring them as
// actual claude-code hooks is up to the consumer (settings.json
// hooks block); the kernel just provides the implementations.
package hooks

import "encoding/json"

// Request is the JSON envelope claude-code sends on every hook
// invocation. ToolInput's shape depends on ToolName (for Bash it has
// `command` and `description`); the kernel keeps it as raw JSON and
// lets each hook unmarshal what it needs.
type Request struct {
	HookEventName  string          `json:"hook_event_name"`
	SessionID      string          `json:"session_id,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	CWD            string          `json:"cwd,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
}

// Response is the JSON document claude-code reads from the hook's
// stdout. PermissionDecision drives the PreToolUse / PostToolUse
// allow / deny / ask flow; SystemMessage surfaces a short status
// string in the transcript regardless of decision.
type Response struct {
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
	SystemMessage      string              `json:"systemMessage,omitempty"`
}

// HookSpecificOutput carries event-typed extra fields. Today only the
// PermissionDecision pair is used; other shapes can be added without
// breaking older consumers because the field is optional.
type HookSpecificOutput struct {
	HookEventName            string `json:"hookEventName,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// Allow / Deny / Ask are the valid PermissionDecision values.
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)
