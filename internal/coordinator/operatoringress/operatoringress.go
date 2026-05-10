// Package operatoringress persists a coordinator operator prompt to
// the state.md ledger before the model can act on it. It mirrors the
// claude-code UserPromptSubmit hook contract: stdin carries the JSON
// payload, exit 2 with stderr blocks the prompt, exit 0 lets it
// through.
//
// Self-gates by SKYBOT_INBOX: any agent whose inbox is not under
// SKYHELM_STATE_DIR returns Skipped (exit 0 on the wrapper side), so
// the binary is safe to wire as a global UserPromptSubmit hook for
// rowers, wingbot, and the operator's interactive shell.
package operatoringress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const DefaultMaxExcerpt = 180

// Config is the runtime configuration for Run. Defaults() fills the
// zero-value fields from environment and stdlib defaults.
type Config struct {
	StateDir   string
	StateFile  string
	Inbox      string
	MaxExcerpt int
	// Now overrides time.Now for tests; nil means use real wall clock.
	Now func() time.Time
}

// HookPayload is the subset of the claude-code UserPromptSubmit
// payload that this hook needs. Other fields are ignored.
type HookPayload struct {
	Prompt string `json:"prompt"`
}

// Result is the verdict from Run. Skipped means the gate fired (no
// state changed). Failed means persistence failed and the caller
// should exit 2 with ErrorMsg on stderr; otherwise the caller exits
// 0.
type Result struct {
	Skipped  bool
	Failed   bool
	ErrorMsg string
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
	if c.StateFile == "" {
		c.StateFile = os.Getenv("SKYHELM_STATE_FILE")
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(c.StateDir, "state.md")
	}
	if c.Inbox == "" {
		c.Inbox = os.Getenv("SKYBOT_INBOX")
	}
	if c.MaxExcerpt == 0 {
		if v := os.Getenv("SKYHELM_OPERATOR_INGRESS_MAX_EXCERPT"); v != "" {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
				c.MaxExcerpt = n
			}
		}
	}
	if c.MaxExcerpt == 0 {
		c.MaxExcerpt = DefaultMaxExcerpt
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

// Run executes the hook against the supplied payload bytes. Callers
// are expected to read stdin themselves and pass the bytes in.
func Run(cfg Config, payload []byte) Result {
	cfg = cfg.Defaults()

	if !cfg.IsCoordinator() {
		return Result{Skipped: true}
	}
	if len(payload) == 0 {
		return Result{Failed: true, ErrorMsg: "missing hook payload; cannot persist operator prompt"}
	}

	var hp HookPayload
	if err := json.Unmarshal(payload, &hp); err != nil || hp.Prompt == "" {
		return Result{Failed: true, ErrorMsg: "payload missing prompt; cannot persist operator prompt"}
	}

	sum := sha256.Sum256([]byte(hp.Prompt))
	hash := hex.EncodeToString(sum[:])
	chars := len(hp.Prompt)
	excerpt := buildExcerpt(hp.Prompt, cfg.MaxExcerpt)
	ts := cfg.Now().Format(timeFormat)

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return Result{Failed: true, ErrorMsg: fmt.Sprintf(
			"mkdir %s failed; operator prompt sha256=%s not persisted", cfg.StateDir, hash)}
	}

	if err := ensureStateFile(cfg.StateFile); err != nil {
		return Result{Failed: true, ErrorMsg: fmt.Sprintf(
			"create %s failed; operator prompt sha256=%s not persisted", cfg.StateFile, hash)}
	}

	if err := appendUnderLock(cfg, hash, chars, excerpt, ts); err != nil {
		return Result{Failed: true, ErrorMsg: fmt.Sprintf(
			"%s; operator prompt sha256=%s not persisted", err.Error(), hash)}
	}

	return Result{}
}

// timeFormat matches `date -Iseconds`: seconds-precision RFC3339 with
// a numeric timezone offset (no Z).
const timeFormat = "2006-01-02T15:04:05-07:00"

func ensureStateFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	return f.Close()
}

// appendUnderLock takes an exclusive flock on a sibling lock file,
// then ensures the ledger header exists and appends one row. The lock
// scope matches the bash version: header check + append happen inside
// the critical section so concurrent runs cannot duplicate the
// header.
func appendUnderLock(cfg Config, hash string, chars int, excerpt, ts string) error {
	lockPath := filepath.Join(cfg.StateDir, "operator-ingress.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open lock %s failed", lockPath)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock %s failed", lockPath)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	hasHeader, err := hasLedgerHeader(cfg.StateFile)
	if err != nil {
		return fmt.Errorf("read %s failed", cfg.StateFile)
	}

	out, err := os.OpenFile(cfg.StateFile, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s for append failed", cfg.StateFile)
	}
	defer out.Close()

	if !hasHeader {
		if _, err := out.WriteString("\n## Operator ingress ledger\n"); err != nil {
			return fmt.Errorf("write ledger header failed")
		}
	}
	row := fmt.Sprintf(
		"- %s operator-prompt status=pending sha256=%s chars=%d excerpt=%s next=\"process before probes dispatch edits\"\n",
		ts, hash, chars, excerpt)
	if _, err := out.WriteString(row); err != nil {
		return fmt.Errorf("append ingress row failed")
	}
	return nil
}

func hasLedgerHeader(path string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return ledgerHeaderRE.Match(body), nil
}

var ledgerHeaderRE = regexp.MustCompile(`(?m)^## Operator ingress ledger$`)

// buildExcerpt produces the excerpt field. Order mirrors the bash:
// sensitivity check first, then empty/long checks, then redaction.
func buildExcerpt(prompt string, maxExcerpt int) string {
	if looksSensitive(prompt) {
		return "[redacted-sensitive-prompt]"
	}
	first := firstNonBlankLine(prompt)
	first = sanitizeFirstLine(first)
	if first == "" {
		return "[empty-prompt]"
	}
	if len(first) > maxExcerpt {
		return "[redacted-long-line]"
	}
	return redactExcerpt(first)
}

// firstNonBlankLine returns the first line of s that has at least one
// non-whitespace character (`awk 'NF { print; exit }'`).
func firstNonBlankLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimFunc(line, unicode.IsSpace) != "" {
			return line
		}
	}
	return ""
}

// sanitizeFirstLine collapses \r and \t to single spaces, matching
// `tr '\r\t' '  '`.
func sanitizeFirstLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

var (
	sensitiveOnce sync.Once
	sensitiveRE   *regexp.Regexp
	redactRE1     *regexp.Regexp
	redactRE2     *regexp.Regexp
	redactRE3     *regexp.Regexp
)

func compileExcerptREs() {
	sensitiveRE = regexp.MustCompile(
		`(?i)(password|passwd|token|secret|api[ _-]?key|authorization|bearer|private[ _-]?key|BEGIN OPENSSH|BEGIN RSA|age1[0-9a-z]+|sk-[A-Za-z0-9_-]{16,})`)
	redactRE1 = regexp.MustCompile(
		`(?i)((password|passwd|token|secret|api[ _-]?key)[\s]*[:=][\s]*)[^\s,;]+`)
	redactRE2 = regexp.MustCompile(
		`(?i)(Authorization:[\s]*Bearer[\s]+)\S+`)
	redactRE3 = regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`)
}

func looksSensitive(s string) bool {
	sensitiveOnce.Do(compileExcerptREs)
	return sensitiveRE.MatchString(s)
}

// redactExcerpt mirrors the bash `redact_excerpt`: three sed
// substitutions, then a control-char-to-space pass.
func redactExcerpt(s string) string {
	sensitiveOnce.Do(compileExcerptREs)
	s = redactRE1.ReplaceAllString(s, "${1}[REDACTED]")
	s = redactRE2.ReplaceAllString(s, "${1}[REDACTED]")
	s = redactRE3.ReplaceAllString(s, "sk-[REDACTED]")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
