// Package bootaudit reads a Claude Code session.jsonl and emits a
// cold-boot quality profile: turn-1 input-token cost, ToolSearch
// round-trips before first useful work, MCP servers whose tools the
// worker actually invoked, boot-time tool errors, and operator-brief
// size.
//
// It is read-only and pattern-based. Inputs are the exact records
// Claude Code writes to ~/.claude/projects/<project>/<session>.jsonl,
// one JSON object per line. Records the analyzer doesn't recognize
// (custom-title, agent-name, file-history-snapshot, etc.) are
// silently skipped: the boot profile only depends on user/assistant
// records.
//
// Note on MCP coverage: the jsonl persistence layer does NOT preserve
// the session-start system-reminders that announce "still connecting"
// servers, so a connect-surface count from the transcript is
// structurally undercounted. The probe instead reports MCP servers
// whose tools the worker actually called during the session - that is
// recoverable from tool_use record names and tells the operator
// whether the connect overhead was paid for or not.
//
// Findings shape mirrors coordinator/slascan: structured one-line
// entries on stderr when a threshold is breached, exit 2 to signal.
package bootaudit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

// DefaultBootTurns is how many assistant turns count as "boot" for
// per-turn metrics like tool-error count. Five matches the brief's
// "first 5 turns" framing.
const DefaultBootTurns = 5

// Config drives Audit.
type Config struct {
	SessionPath string
	BootTurns   int
}

// Profile is the audit's verdict.
type Profile struct {
	SessionPath        string
	Turn1CacheCreation int
	Turn1CacheRead     int
	Turn1InputTokens   int
	BootToolErrors     int
	ToolSearchRounds   int
	MCPServersUsed     []string
	BriefBytes         int
	AssistantTurns     int
	Findings           []string
}

// Total returns the turn-1 input-token cost (cache_creation +
// cache_read + input_tokens). This is what the model "loaded" before
// emitting its first output.
func (p Profile) Total() int {
	return p.Turn1CacheCreation + p.Turn1CacheRead + p.Turn1InputTokens
}

type rawRecord struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type rawContentItem struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

var mcpToolNameRE = regexp.MustCompile(`^mcp__([a-zA-Z0-9_-]+)__`)

// Audit reads cfg.SessionPath as a Claude Code session.jsonl and
// builds a Profile. Returns an error only on I/O failure or malformed
// envelope JSON; per-record decode errors are silently skipped to
// match the lenient bash audit pattern.
func Audit(cfg Config) (Profile, error) {
	if cfg.BootTurns == 0 {
		cfg.BootTurns = DefaultBootTurns
	}
	f, err := os.Open(cfg.SessionPath)
	if err != nil {
		return Profile{}, err
	}
	defer f.Close()

	prof := Profile{SessionPath: cfg.SessionPath}
	mcpSet := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	gotFirstAssistantUsage := false
	gotFirstUserBrief := false

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}
		var msg rawMessage
		if err := json.Unmarshal(rec.Message, &msg); err != nil {
			continue
		}

		switch rec.Type {
		case "assistant":
			prof.AssistantTurns++
			if !gotFirstAssistantUsage && msg.Usage != nil {
				prof.Turn1CacheCreation = msg.Usage.CacheCreationInputTokens
				prof.Turn1CacheRead = msg.Usage.CacheReadInputTokens
				prof.Turn1InputTokens = msg.Usage.InputTokens
				gotFirstAssistantUsage = true
			}
			items := decodeContent(msg.Content)
			withinBoot := prof.AssistantTurns <= cfg.BootTurns
			for _, it := range items {
				if it.Type != "tool_use" {
					continue
				}
				if it.Name == "ToolSearch" && withinBoot {
					prof.ToolSearchRounds++
				}
				if m := mcpToolNameRE.FindStringSubmatch(it.Name); m != nil {
					mcpSet[m[1]] = struct{}{}
				}
			}
		case "user":
			items := decodeContent(msg.Content)
			if len(items) == 0 {
				// content was a bare string, not an array
				if !gotFirstUserBrief {
					var s string
					if err := json.Unmarshal(msg.Content, &s); err == nil {
						prof.BriefBytes = len(s)
						gotFirstUserBrief = true
					}
				}
				continue
			}
			for _, it := range items {
				switch it.Type {
				case "tool_result":
					if it.IsError && prof.AssistantTurns <= cfg.BootTurns {
						prof.BootToolErrors++
					}
				case "text":
					if !gotFirstUserBrief {
						prof.BriefBytes = len(it.Text)
						gotFirstUserBrief = true
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return prof, err
	}

	prof.MCPServersUsed = make([]string, 0, len(mcpSet))
	for s := range mcpSet {
		prof.MCPServersUsed = append(prof.MCPServersUsed, s)
	}
	sort.Strings(prof.MCPServersUsed)

	if prof.BootToolErrors > 0 {
		prof.Findings = append(prof.Findings,
			fmt.Sprintf("boot-tool-errors: %d in turns 1-%d", prof.BootToolErrors, cfg.BootTurns))
	}
	return prof, nil
}

func decodeContent(raw json.RawMessage) []rawContentItem {
	if len(raw) == 0 {
		return nil
	}
	var items []rawContentItem
	if err := json.Unmarshal(raw, &items); err == nil {
		return items
	}
	return nil
}

// Format renders the profile as one structured line per metric, in
// the byte-shape downstream wrappers can pattern-match on.
func Format(p Profile, w io.Writer) {
	fmt.Fprintf(w, "boot-audit session=%s\n", p.SessionPath)
	fmt.Fprintf(w, "turn1-tokens: %d (cc=%d cr=%d ic=%d)\n",
		p.Total(), p.Turn1CacheCreation, p.Turn1CacheRead, p.Turn1InputTokens)
	fmt.Fprintf(w, "boot-tool-errors: %d\n", p.BootToolErrors)
	fmt.Fprintf(w, "toolsearch-rounds: %d\n", p.ToolSearchRounds)
	if len(p.MCPServersUsed) > 0 {
		fmt.Fprintf(w, "mcp-servers-used: %d (%s)\n",
			len(p.MCPServersUsed), strings.Join(p.MCPServersUsed, ","))
	} else {
		fmt.Fprintf(w, "mcp-servers-used: 0\n")
	}
	fmt.Fprintf(w, "brief-bytes: %d\n", p.BriefBytes)
	fmt.Fprintf(w, "assistant-turns: %d\n", p.AssistantTurns)
}
