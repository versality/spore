package transcript

import (
	"bufio"
	"encoding/json"
	"os"
)

// Codex transcripts are JSONL with a different shape than claude-code.
// Each event line carries a "type" plus a payload. Two types matter for
// the coordinator stop-hook: "session_meta" (carries payload.id, the
// codex-side session ID), and "token_count" (carries last_token_usage
// with the running total).

// CodexSessionID returns the session ID from the first session_meta
// event in path, or "" with ok=false if none was found / readable.
func CodexSessionID(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				ID string `json:"id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Type == "session_meta" && row.Payload.ID != "" {
			return row.Payload.ID, true
		}
	}
	return "", false
}

// CodexLastTokenCount returns the total_tokens from the last
// token_count event's last_token_usage block. Returns 0 with ok=false
// if no token_count event is present.
func CodexLastTokenCount(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	last := 0
	found := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var row struct {
			Type           string `json:"type"`
			LastTokenUsage *struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"last_token_usage"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if row.Type != "token_count" || row.LastTokenUsage == nil {
			continue
		}
		last = row.LastTokenUsage.TotalTokens
		found = true
	}
	if !found {
		return 0, false
	}
	return last, true
}
