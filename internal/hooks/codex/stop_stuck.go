package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/transcript"
)

// stuckResult captures whether the stuck-toolcall check wants to trip
// the Stop hook (exit 2) and the human-readable message it emitted to
// do so.
type stuckResult struct {
	ShouldExit2 bool
	Message     string
}

// codexStuckToolcallCheck inspects the codex transcript for any tool
// call (function_call, custom_tool_call, ...) opened in the latest
// turn that never saw its matching _output sibling before EOF. Each
// stuck call is appended to the codex-stuck-toolcalls.jsonl ledger;
// when at least one is present we return ShouldExit2 with a reminder
// listing them.
//
// Gate matches the context monitor: only coordinator sessions on the
// codex driver run this check. Widening to workers is a future call
// pending first-telemetry-pass evidence.
func codexStuckToolcallCheck(cfg StopConfig, payload StopPayload) stuckResult {
	if cfg.Driver != "codex" {
		return stuckResult{}
	}
	if !inboxUnderRoot(cfg.Inbox, cfg.CoordinatorStateDir) {
		return stuckResult{}
	}

	tpath := payload.TranscriptPath
	if tpath == "" {
		return stuckResult{}
	}
	if _, err := os.Stat(tpath); err != nil {
		return stuckResult{}
	}

	stuck, err := transcript.LastUnfinalizedToolCalls(tpath)
	if err != nil || len(stuck) == 0 {
		return stuckResult{}
	}

	sid := payload.SessionID
	if sid == "" {
		sid = "unknown"
	}
	for _, c := range stuck {
		appendCodexStuckToolcallsLedger(cfg, sid, tpath, c)
	}

	parts := make([]string, 0, len(stuck))
	for _, c := range stuck {
		parts = append(parts, fmt.Sprintf("%s %s (call_id=%s)", c.Kind, c.Name, c.CallID))
	}
	msg := fmt.Sprintf(
		"CODEX STUCK TOOLCALL: %d unfinalized tool call(s) in transcript: %s.\n"+
			"Emit the matching tool result(s) before stopping; new tool dispatch will be refused until prior calls finalize.\n",
		len(stuck), strings.Join(parts, ", "))
	return stuckResult{ShouldExit2: true, Message: msg}
}

func appendCodexStuckToolcallsLedger(cfg StopConfig, sid, transcriptPath string, c transcript.CodexToolCall) {
	if cfg.CoordinatorStateDir == "" {
		return
	}
	if err := os.MkdirAll(cfg.CoordinatorStateDir, 0o700); err != nil {
		return
	}
	ts := cfg.Now().Format(time.RFC3339)
	line := fmt.Sprintf(
		`{"ts":"%s","event":"stuck-toolcall","session_id":%s,"call_id":%s,"tool_name":%s,"kind":%s,"transcript_path":%s,"line_num":%d}`+"\n",
		ts,
		jsonString(sid),
		jsonString(c.CallID),
		jsonString(c.Name),
		jsonString(c.Kind),
		jsonString(transcriptPath),
		c.LineNum,
	)
	appendFile(filepath.Join(cfg.CoordinatorStateDir, "codex-stuck-toolcalls.jsonl"), line)
}
