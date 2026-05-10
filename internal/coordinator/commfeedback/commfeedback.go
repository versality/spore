// Package commfeedback implements the claude-code UserPromptSubmit hook
// that catalogues operator feedback. When the operator's prompt ends in
// `+++` (liked) or `---` (hard) after trimming trailing whitespace, the
// hook appends one JSONL row to the comm-feedback ledger tagging the
// previous assistant reply. Lifts the manual scan in state.md to a hook
// so the cataloguing is free (zero inference) and survives respawns.
//
// Self-gates by SKYBOT_INBOX: any agent whose inbox is not under
// SKYHELM_STATE_DIR returns Skipped, so the binary is safe to wire as a
// global UserPromptSubmit hook for rowers, wingbot, skyler, and the
// operator's interactive shell.
//
// Never blocks the turn: persistence failures surface as Warning on the
// returned Result; the caller prints them to stderr and exits 0.
package commfeedback

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const (
	DefaultThreshold        = 100
	DefaultPromptTailMax    = 200
	DefaultAssistantTailMax = 600
)

// Config is the runtime configuration for Run. Defaults() fills the
// zero-value fields from environment and stdlib defaults.
type Config struct {
	StateDir         string
	LedgerFile       string
	ReadyFile        string
	Threshold        int
	Inbox            string
	PromptTailMax    int
	AssistantTailMax int
	// Now overrides time.Now for tests; nil means use real wall clock.
	Now func() time.Time
}

// HookPayload is the subset of the claude-code UserPromptSubmit
// payload that this hook needs.
type HookPayload struct {
	Prompt         string `json:"prompt"`
	TranscriptPath string `json:"transcript_path"`
}

// Result is the verdict from Run. Skipped means the gate fired or the
// prompt did not end in +++/---. Warning carries a one-line message
// the caller should print to stderr; the caller always exits 0.
type Result struct {
	Skipped   bool
	Recorded  bool
	Sentiment string
	Warning   string
}

// Defaults returns a copy of c with zero-value fields populated from
// the SKYHELM_* env vars and stdlib defaults. Caller-supplied values
// take precedence.
func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.LedgerFile == "" {
		c.LedgerFile = os.Getenv("SKYHELM_COMM_FEEDBACK_FILE")
	}
	if c.LedgerFile == "" {
		c.LedgerFile = filepath.Join(c.StateDir, "comm-feedback.jsonl")
	}
	if c.ReadyFile == "" {
		c.ReadyFile = os.Getenv("SKYHELM_COMM_FEEDBACK_READY")
	}
	if c.ReadyFile == "" {
		c.ReadyFile = filepath.Join(c.StateDir, "comm-feedback.ready")
	}
	if c.Threshold == 0 {
		if v := os.Getenv("SKYHELM_COMM_FEEDBACK_THRESHOLD"); v != "" {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
				c.Threshold = n
			}
		}
	}
	if c.Threshold == 0 {
		c.Threshold = DefaultThreshold
	}
	if c.Inbox == "" {
		c.Inbox = os.Getenv("SKYBOT_INBOX")
	}
	if c.PromptTailMax == 0 {
		c.PromptTailMax = DefaultPromptTailMax
	}
	if c.AssistantTailMax == 0 {
		c.AssistantTailMax = DefaultAssistantTailMax
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// IsCoordinator reports whether the configured Inbox sits under the
// configured StateDir. Mirrors the bash gate verbatim: empty inbox
// means "not coordinator", otherwise the inbox must equal the state
// root or be a child of it.
func (c Config) IsCoordinator() bool {
	if c.Inbox == "" {
		return false
	}
	root := strings.TrimRight(c.StateDir, "/")
	return c.Inbox == root || strings.HasPrefix(c.Inbox, root+"/")
}

// timeFormat matches `date -Iseconds`: seconds-precision RFC3339 with
// a numeric timezone offset (no Z).
const timeFormat = "2006-01-02T15:04:05-07:00"

// Run executes the hook against the supplied payload bytes and the
// already-read transcript bytes (or nil if the transcript file is
// absent). The caller is responsible for resolving transcript_path
// from the payload and reading the file; this keeps the package
// trivially testable without touching disk for the transcript.
func Run(cfg Config, payload []byte, transcript []byte) Result {
	cfg = cfg.Defaults()

	if !cfg.IsCoordinator() {
		return Result{Skipped: true}
	}
	if len(payload) == 0 {
		return Result{Skipped: true}
	}

	var hp HookPayload
	if err := json.Unmarshal(payload, &hp); err != nil {
		return Result{Skipped: true}
	}
	if hp.Prompt == "" {
		return Result{Skipped: true}
	}

	trimmed := strings.TrimRightFunc(hp.Prompt, unicode.IsSpace)
	sentiment := detectSentiment(trimmed)
	if sentiment == "" {
		return Result{Skipped: true}
	}

	body := strings.TrimRightFunc(trimmed[:len(trimmed)-3], unicode.IsSpace)
	promptTail := body
	if len(promptTail) > cfg.PromptTailMax {
		promptTail = promptTail[len(promptTail)-cfg.PromptTailMax:]
	}

	assistantTail := extractAssistantTail(transcript, cfg.AssistantTailMax)

	ts := cfg.Now().Format(timeFormat)

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return Result{Warning: fmt.Sprintf("mkdir %s failed", cfg.StateDir)}
	}

	row := struct {
		TS            string `json:"ts"`
		Sentiment     string `json:"sentiment"`
		PromptTail    string `json:"prompt_tail"`
		AssistantTail string `json:"assistant_tail"`
	}{ts, sentiment, promptTail, assistantTail}
	line, err := json.Marshal(row)
	if err != nil {
		return Result{Warning: "marshal row failed"}
	}

	if err := appendLedgerRow(cfg.LedgerFile, append(line, '\n')); err != nil {
		return Result{Warning: fmt.Sprintf("append to %s failed", cfg.LedgerFile)}
	}

	count, err := countLines(cfg.LedgerFile)
	if err != nil {
		return Result{Recorded: true, Sentiment: sentiment}
	}
	if count >= cfg.Threshold {
		if _, err := os.Stat(cfg.ReadyFile); os.IsNotExist(err) {
			body := fmt.Sprintf(`{"ts":"%s","count":%d}`+"\n", ts, count)
			if err := writeReadyMarker(cfg.ReadyFile, []byte(body)); err != nil {
				return Result{Recorded: true, Sentiment: sentiment, Warning: "ready marker write failed"}
			}
		}
	}
	return Result{Recorded: true, Sentiment: sentiment}
}

func detectSentiment(trimmed string) string {
	if len(trimmed) < 3 {
		return ""
	}
	switch trimmed[len(trimmed)-3:] {
	case "+++":
		return "liked"
	case "---":
		return "hard"
	}
	return ""
}

// appendLedgerRow creates the ledger file with mode 0600 if missing,
// then appends one row. Mirrors the bash `umask 077 && : >file` followed
// by the `>>file` append.
func appendLedgerRow(path string, row []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(row)
	return err
}

// writeReadyMarker creates the ready marker exclusively (mode 0600),
// matching the bash `umask 077 && printf ... >file`. A concurrent
// creator wins; we treat that as success.
func writeReadyMarker(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.Write(body)
	return err
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	count := 0
	for s.Scan() {
		count++
	}
	return count, s.Err()
}

// extractAssistantTail mirrors the awk in the bash version: find the
// last transcript line carrying both "role":"assistant" and
// "type":"text", concatenate every {"type":"text","text":"..."} chunk
// on that line, then tail-clip to maxBytes. The chunk parser decodes
// \n \t \\ \" and passes any other backslash escape through as the
// next byte; that matches claude-code's plain-text output and keeps us
// out of full JSON parsing for what is a noisy log.
func extractAssistantTail(transcript []byte, maxBytes int) string {
	if len(transcript) == 0 {
		return ""
	}
	var lastLine []byte
	s := bufio.NewScanner(bytes.NewReader(transcript))
	s.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for s.Scan() {
		line := s.Bytes()
		if bytes.Contains(line, []byte(`"role":"assistant"`)) &&
			bytes.Contains(line, []byte(`"type":"text"`)) {
			lastLine = append(lastLine[:0], line...)
		}
	}
	if len(lastLine) == 0 {
		return ""
	}

	marker := []byte(`"type":"text","text":"`)
	var out strings.Builder
	rest := lastLine
	for {
		idx := bytes.Index(rest, marker)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(marker):]
		chunk, after := decodeChunk(rest)
		out.WriteString(chunk)
		rest = after
	}
	res := out.String()
	if len(res) > maxBytes {
		res = res[len(res)-maxBytes:]
	}
	return res
}

// decodeChunk reads bytes up to the next unescaped `"`, decoding the
// four escapes the awk handled (\n \t \\ \"). Any other `\x` produces
// `x` verbatim. Returns the decoded chunk and the bytes after the
// closing quote (or nil if the closing quote was missing).
func decodeChunk(s []byte) (string, []byte) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '"' {
			return b.String(), s[i+1:]
		}
		if c == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}
