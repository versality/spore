// Package codex holds Stop / SessionStart hook adapters for Codex.
//
// Claude Code chains Stop hooks itself; Codex runs a single Stop hook
// per event, so these adapters bundle the pieces a coordinator session
// needs (context monitor, inbox drain, sub-hook chain) into one entry
// point. SessionStart injects the coordinator role brief on resume.
package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionStartConfig parameterizes the SessionStart adapter. All
// fields fall back to env / defaults when zero.
type SessionStartConfig struct {
	// Inbox is the coordinator-session inbox path ($SPORE_TASK_INBOX).
	// The adapter no-ops when this is empty or doesn't sit under
	// CoordinatorStateDir.
	Inbox string
	// CoordinatorStateDir gates the role-brief injection: only sessions
	// whose inbox lives under this dir are coordinator sessions.
	CoordinatorStateDir string
	// BriefPath is the role-brief markdown file. The adapter no-ops
	// when this is empty or unreadable.
	BriefPath string
	// LedgerFile is the JSONL ledger that records session-start events.
	// Defaults to <state>/codex-context-monitor.jsonl.
	LedgerFile string
	// Now is injected for tests; zero uses time.Now().UTC().
	Now func() time.Time
}

func (c SessionStartConfig) defaults() SessionStartConfig {
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.LedgerFile == "" && c.CoordinatorStateDir != "" {
		c.LedgerFile = filepath.Join(c.CoordinatorStateDir, "codex-context-monitor.jsonl")
	}
	return c
}

// SessionStartPayload is the subset of the Codex SessionStart hook
// payload we read.
type SessionStartPayload struct {
	SessionID string `json:"session_id"`
}

// SessionStartResult holds either the JSON document the adapter wrote
// to stdout (when it injects the brief) or empty (skip). Skipped is
// true when no output should be emitted.
type SessionStartResult struct {
	JSON    []byte
	Skipped bool
}

// SessionStart implements the Codex SessionStart hook adapter. It
// reads the JSON payload from r, validates this is a coordinator
// session (inbox under CoordinatorStateDir), appends a session-start
// event to the ledger, and returns the SessionStart hookSpecificOutput
// JSON document carrying the role brief as additionalContext.
//
// The adapter is best-effort: any I/O error on the ledger or brief is
// translated into a skip so it never blocks the session.
func SessionStart(cfg SessionStartConfig, r io.Reader) (SessionStartResult, error) {
	cfg = cfg.defaults()

	if cfg.Inbox == "" || !inboxUnderRoot(cfg.Inbox, cfg.CoordinatorStateDir) {
		return SessionStartResult{Skipped: true}, nil
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return SessionStartResult{Skipped: true}, err
	}
	var payload SessionStartPayload
	_ = json.Unmarshal(body, &payload)
	sid := payload.SessionID
	if sid == "" {
		sid = "unknown"
	}

	if cfg.BriefPath == "" {
		return SessionStartResult{Skipped: true}, nil
	}
	brief, err := os.ReadFile(cfg.BriefPath)
	if err != nil {
		return SessionStartResult{Skipped: true}, nil
	}

	appendSessionStartLedger(cfg, sid)

	out := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = "SessionStart"
	out.HookSpecificOutput.AdditionalContext = string(brief)
	doc, err := json.Marshal(out)
	if err != nil {
		return SessionStartResult{Skipped: true}, err
	}
	doc = append(doc, '\n')
	return SessionStartResult{JSON: doc}, nil
}

// inboxUnderRoot returns true when inbox is exactly root or a child of
// root. Both paths are compared as strings after stripping trailing
// slashes; we deliberately do not Stat or resolve symlinks since this
// is the same check the bash adapter applies.
func inboxUnderRoot(inbox, root string) bool {
	if inbox == "" || root == "" {
		return false
	}
	r := strings.TrimRight(root, "/")
	return inbox == r || strings.HasPrefix(inbox, r+"/")
}

func appendSessionStartLedger(cfg SessionStartConfig, sid string) {
	if cfg.LedgerFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LedgerFile), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(cfg.LedgerFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	ts := cfg.Now().Format(time.RFC3339)
	fmt.Fprintf(f, `{"ts":"%s","event":"session-start","session_id":%s,"source":"codex-session-start"}`+"\n",
		ts, jsonString(sid))
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
