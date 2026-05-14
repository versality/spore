package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/versality/spore/internal/transcript"
)

// PreToolUseConfig parameterizes the PreToolUse adapter. Gate is the
// same coordinator-session gate as Stop / SessionStart: workers and
// ad-hoc sessions skip the check.
type PreToolUseConfig struct {
	Inbox               string
	CoordinatorStateDir string
	Driver              string
}

// PreToolUsePayload is the subset of the Codex PreToolUse hook payload
// we read. Mirrors the Stop hook envelope shape.
type PreToolUsePayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
}

// PreToolUseResult is the adapter verdict.
type PreToolUseResult struct {
	ExitCode int
	Stderr   string
}

// PreToolUse refuses a new tool dispatch when the transcript still has
// at least one unfinalized tool call from a prior turn. The check
// re-parses the transcript live; the ledger is for observability only
// and is not consulted here.
//
// Exit codes:
//
//	0 - no prior unfinalized tool calls (or non-coordinator / non-codex)
//	2 - refuse: at least one prior call is unfinalized
//	1 - I/O error reading stdin
func PreToolUse(cfg PreToolUseConfig, r io.Reader) PreToolUseResult {
	body, err := io.ReadAll(r)
	if err != nil {
		return PreToolUseResult{ExitCode: 1, Stderr: fmt.Sprintf("read stdin: %v\n", err)}
	}
	var payload PreToolUsePayload
	_ = json.Unmarshal(body, &payload)

	if cfg.Driver != "codex" {
		return PreToolUseResult{}
	}
	if !inboxUnderRoot(cfg.Inbox, cfg.CoordinatorStateDir) {
		return PreToolUseResult{}
	}
	if payload.TranscriptPath == "" {
		return PreToolUseResult{}
	}
	if _, err := os.Stat(payload.TranscriptPath); err != nil {
		return PreToolUseResult{}
	}

	stuck, err := transcript.LastUnfinalizedToolCalls(payload.TranscriptPath)
	if err != nil || len(stuck) == 0 {
		return PreToolUseResult{}
	}

	parts := make([]string, 0, len(stuck))
	for _, c := range stuck {
		parts = append(parts, fmt.Sprintf("%s %s (call_id=%s)", c.Kind, c.Name, c.CallID))
	}
	tool := payload.ToolName
	if tool == "" {
		tool = "<unknown>"
	}
	msg := fmt.Sprintf(
		"codex-stuck-toolcall-prior: refusing %s: %d prior tool call(s) unfinalized: %s.\n"+
			"Emit the matching tool result(s) for the prior calls before dispatching a new tool.\n",
		tool, len(stuck), strings.Join(parts, ", "))
	return PreToolUseResult{ExitCode: 2, Stderr: msg}
}
