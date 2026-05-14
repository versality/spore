package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// CodexToolCall summarises one unfinalized tool-call event from a
// Codex rollout JSONL file: an open envelope (function_call,
// custom_tool_call, ...) that never saw its matching *_output sibling
// before EOF.
type CodexToolCall struct {
	CallID  string
	Name    string
	Kind    string
	LineNum int
}

// LastUnfinalizedToolCalls scans the Codex rollout at path and returns
// every tool-call open whose matching *_output never arrived. The
// transcript is the source of truth; the function is order-tolerant
// (interleaved tools within a turn are fine) because pairs are matched
// purely by call_id.
//
// Open events are response_item lines whose payload.type ends in
// "_call". Close events are response_item lines whose payload.type ends
// in "_call_output" and carry the same payload.call_id. Lines that
// don't parse, payloads missing call_id, and *_output lines with an
// unknown call_id are all skipped silently - the function never
// returns a parse error short of a filesystem error.
//
// The returned slice is sorted by LineNum (oldest stuck call first) to
// make hook messages deterministic.
func LastUnfinalizedToolCalls(path string) ([]CodexToolCall, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	open := map[string]CodexToolCall{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Type != "response_item" || row.Payload.CallID == "" {
			continue
		}
		pt := row.Payload.Type
		switch {
		case strings.HasSuffix(pt, "_call_output"):
			delete(open, row.Payload.CallID)
		case strings.HasSuffix(pt, "_call"):
			open[row.Payload.CallID] = CodexToolCall{
				CallID:  row.Payload.CallID,
				Name:    row.Payload.Name,
				Kind:    pt,
				LineNum: line,
			}
		}
	}

	out := make([]CodexToolCall, 0, len(open))
	for _, c := range open {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LineNum < out[j].LineNum })
	return out, nil
}
