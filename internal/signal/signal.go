// Package signal records a one-line signal per warning / error
// surfaced by a wrapped command (or a pre-captured output file). The
// state dir under $XDG_STATE_HOME/spore-command-signals holds three
// files: seen.tsv (the dedup ledger keyed by signal hash), events.jsonl
// (every recording, append-only), and minted.tsv (auto-mint memo so a
// repeat ticket-candidate does not re-fire).
//
// This package is the lifted equivalent of the legacy
// nix-config/harness/capture-command-signal.sh; the digest, ledger
// schema, and severity ladder are stable across the Go port so a
// freshly-rotated state dir matches downstream tooling.
package signal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Env names the package reads. The lift renames the legacy HARNESS_*
// variants from the shell script.
const (
	EnvTicketAfter = "SPORE_SIGNAL_TICKET_AFTER"
	EnvAutoMint    = "SPORE_SIGNAL_AUTO_MINT"
	EnvParentTool  = "SPORE_SIGNAL_PARENT_TOOL"
)

// DefaultStateDirName is the per-user subdir under XDG_STATE_HOME.
const DefaultStateDirName = "spore-command-signals"

// DefaultTicketAfter is the count at which a repeat warning crosses
// from log to ticket-candidate.
const DefaultTicketAfter = 3

// Severity is the recorded signal class.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Action is the recorded handling tier for a signal at recording time.
type Action string

const (
	ActionBlock           Action = "block"
	ActionTicketCandidate Action = "ticket-candidate"
	ActionLog             Action = "log"
)

// Signal is one recorded line, post-recording.
type Signal struct {
	Hash     string
	Severity Severity
	Action   Action
	Summary  string
	Tool     string
	Owner    string
	Count    int // count after this recording
}

// Result is the output of Process.
type Result struct {
	ExitCode int
	Signals  []Signal
}

// Config is the static input for Process.
type Config struct {
	StateDir    string
	Tool        string
	Owner       string
	TicketAfter int
	DryRun      bool
	AutoMint    bool
	// Now lets tests pin the recorded timestamp.
	Now func() time.Time
	// Logger receives the human-readable [signal:hash] line per
	// recording. Defaults to os.Stderr.
	Logger io.Writer
	// Mint is invoked once per ticket-candidate that has not been
	// minted before. A nil Mint disables auto-mint regardless of
	// AutoMint. The default `spore signal capture` wiring uses
	// MintViaWT.
	Mint MintFunc
}

// MintFunc is the auto-mint hook. Implementations must be safe to
// call from a single goroutine at a time.
type MintFunc func(hash, title, body string) error

// DefaultStateDir is $XDG_STATE_HOME/spore-command-signals (with the
// XDG fallback to $HOME/.local/state).
func DefaultStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, DefaultStateDirName)
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", DefaultStateDirName)
	}
	return DefaultStateDirName
}

// Capture runs args under exec.CommandContext and returns the merged
// output (stdout + stderr) suitable for scanning, plus the inner exit
// code. With preserveStreams=false the wrapper tees the merged stream
// to userOut while the command runs (live for long-running daemons).
// With preserveStreams=true the wrapper buffers stdout and stderr
// separately, then replays stdout to userOut and stderr to userErr
// after the inner command exits; this matches the Stop-hook contract
// where Claude reads stderr on exit-2.
func Capture(ctx context.Context, args []string, preserveStreams bool, parentTool string, userOut, userErr io.Writer) ([]byte, int, error) {
	if len(args) == 0 {
		return nil, 0, errors.New("signal: empty command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), EnvParentTool+"="+parentTool)

	var captured bytes.Buffer
	if preserveStreams {
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		rc, runErr := runCmd(cmd)
		_, _ = userOut.Write(outBuf.Bytes())
		_, _ = userErr.Write(errBuf.Bytes())
		captured.Write(outBuf.Bytes())
		captured.Write(errBuf.Bytes())
		return captured.Bytes(), rc, runErr
	}
	// Merged tee: stdout and stderr both flow to userOut and the
	// scan buffer. Use a mutex so concurrent writes from cmd's two
	// fds do not interleave at byte granularity.
	mw := newSafeMultiWriter(userOut, &captured)
	cmd.Stdout = mw
	cmd.Stderr = mw
	rc, runErr := runCmd(cmd)
	return captured.Bytes(), rc, runErr
}

// runCmd starts and waits for cmd, returning the exit code. A nonzero
// exit from the wrapped command is *not* an error from this function;
// only setup / IO failures bubble up.
func runCmd(cmd *exec.Cmd) (int, error) {
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// Process records signals from output (and, if exitCode != 0, an
// "exit=N cmd=..." error signal). cmdDisplay is only used to enrich
// the error-signal summary; pass "" for scan-file mode.
func Process(cfg Config, output []byte, cmdDisplay string, exitCode int) (Result, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = os.Stderr
	}
	if cfg.TicketAfter <= 0 {
		cfg.TicketAfter = DefaultTicketAfter
	}
	if cfg.Tool == "" {
		return Result{ExitCode: exitCode}, errors.New("signal: tool is empty")
	}
	if cfg.Owner == "" {
		cfg.Owner = "unowned"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir()
	}

	now := cfg.Now().UTC().Format("2006-01-02T15:04:05Z")
	res := Result{ExitCode: exitCode}

	type pending struct {
		hash     string
		severity Severity
		action   Action
		summary  string
	}
	var queue []pending
	seen := map[string]bool{}

	if exitCode != 0 {
		summary := "exit=" + strconv.Itoa(exitCode)
		if cmdDisplay != "" {
			summary += " cmd=" + cmdDisplay
		}
		summary = NormalizeLine(summary)
		hash := HashSignal(SeverityError, cfg.Tool, summary)
		count, err := existingCount(cfg.StateDir, hash)
		if err != nil {
			return res, err
		}
		queue = append(queue, pending{
			hash:     hash,
			severity: SeverityError,
			action:   actionFor(SeverityError, count, cfg.TicketAfter),
			summary:  summary,
		})
		seen[hash] = true
	}

	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !matchesSignal(line) {
			continue
		}
		summary := NormalizeLine(strings.ReplaceAll(line, "\t", " "))
		if summary == "" {
			continue
		}
		hash := HashSignal(SeverityWarning, cfg.Tool, summary)
		if seen[hash] {
			continue
		}
		seen[hash] = true
		count, err := existingCount(cfg.StateDir, hash)
		if err != nil {
			return res, err
		}
		queue = append(queue, pending{
			hash:     hash,
			severity: SeverityWarning,
			action:   actionFor(SeverityWarning, count, cfg.TicketAfter),
			summary:  summary,
		})
	}
	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("signal: scan output: %w", err)
	}

	for _, p := range queue {
		count, err := existingCount(cfg.StateDir, p.hash)
		if err != nil {
			return res, err
		}
		newCount, err := record(cfg, now, p.hash, p.severity, p.action, p.summary, count)
		if err != nil {
			return res, err
		}
		res.Signals = append(res.Signals, Signal{
			Hash:     p.hash,
			Severity: p.severity,
			Action:   p.action,
			Summary:  p.summary,
			Tool:     cfg.Tool,
			Owner:    cfg.Owner,
			Count:    newCount,
		})
	}

	if cfg.AutoMint && !cfg.DryRun && cfg.Mint != nil {
		for _, s := range res.Signals {
			if s.Action != ActionTicketCandidate {
				continue
			}
			minted, err := alreadyMinted(cfg.StateDir, s.Hash)
			if err != nil {
				return res, err
			}
			if minted {
				fmt.Fprintf(cfg.Logger, "[signal:%s] auto-mint skipped: already minted\n", s.Hash)
				continue
			}
			title := fmt.Sprintf("harness signal: %s %s", cfg.Tool, s.Hash)
			body := mintBody(s, cfg.Tool, cfg.Owner)
			if err := cfg.Mint(s.Hash, title, body); err != nil {
				fmt.Fprintf(cfg.Logger, "spore signal: auto-mint failed for %s: %v\n", s.Hash, err)
				continue
			}
			if err := markMinted(cfg.StateDir, s.Hash, now, title); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func mintBody(s Signal, tool, owner string) string {
	return fmt.Sprintf(`Signal: %s
Tool: %s
Owner: %s
Hash: %s
Summary: %s

This draft was minted by spore signal capture after the same
warning/error signature crossed SPORE_SIGNAL_TICKET_AFTER.`,
		s.Severity, tool, owner, s.Hash, s.Summary)
}

// signalRegex matches lines containing a warning / error keyword
// bordered by non-letter characters. Mirrors the awk pattern in the
// shell script. The leading `[ok]` skip is handled separately.
var signalRegex = regexp.MustCompile(`(?i)(^|[^[:alpha:]])(warn|warning|deprecated|deprecation|will be removed|error|failed|failure)([^[:alpha:]]|$)`)

// okPrefix lines (e.g. `[ok] migrated database`) are skipped before
// the signal regex even runs.
var okPrefix = regexp.MustCompile(`^\[ok\][[:space:]]`)

func matchesSignal(line string) bool {
	if okPrefix.MatchString(line) {
		return false
	}
	return signalRegex.MatchString(line)
}

// HashSignal returns the 12-char prefix of sha256(severity\ttool\tsummary\n).
// The trailing newline is part of the input so the digest matches the
// `printf '...\n' | sha256sum` recipe used by the legacy shell script.
func HashSignal(severity Severity, tool, summary string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\t%s\t%s\n", severity, tool, summary)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// NormalizeLine redacts volatile tokens (tmp paths, ISO timestamps,
// and 4+-digit runs in non-path tokens) so a recurring warning hashes
// to the same dedup key across runs.
func NormalizeLine(in string) string {
	in = strings.ReplaceAll(in, "\t", " ")
	in = tmpPathRe.ReplaceAllString(in, "/tmp/...")
	in = timestampRe.ReplaceAllString(in, "<timestamp>")
	tokens := strings.Fields(in)
	for i, tok := range tokens {
		if strings.ContainsRune(tok, '/') {
			continue
		}
		tokens[i] = digitRunRe.ReplaceAllString(tok, "<n>")
	}
	return strings.Join(tokens, " ")
}

var (
	tmpPathRe   = regexp.MustCompile(`/tmp/[^[:space:]]+`)
	timestampRe = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.\-]+Z`)
	digitRunRe  = regexp.MustCompile(`[0-9]{4,}`)
)

// existingCount reads the prior count for hash from seen.tsv. Returns
// 0 when the ledger does not exist or the hash is new.
func existingCount(stateDir, hash string) (int, error) {
	path := filepath.Join(stateDir, "seen.tsv")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for s.Scan() {
		fields := strings.SplitN(s.Text(), "\t", 9)
		if len(fields) < 4 {
			continue
		}
		if fields[0] != hash {
			continue
		}
		n, err := strconv.Atoi(fields[3])
		if err != nil {
			return 0, fmt.Errorf("ledger: parse count for %s: %w", hash, err)
		}
		return n, nil
	}
	return 0, s.Err()
}

func actionFor(sev Severity, count, ticketAfter int) Action {
	if sev == SeverityError {
		return ActionBlock
	}
	if count+1 >= ticketAfter {
		return ActionTicketCandidate
	}
	return ActionLog
}

// record updates the ledger row for hash, appends a JSONL event, and
// emits the human-readable [signal:hash] line. Returns the new count.
// Dry-run only emits the [signal:hash] line.
func record(cfg Config, now, hash string, sev Severity, action Action, summary string, prevCount int) (int, error) {
	newCount := prevCount + 1
	if cfg.DryRun {
		fmt.Fprintf(cfg.Logger,
			"[signal:%s] dry-run severity=%s action=%s count=%d owner=%s tool=%s summary=%s\n",
			hash, sev, action, newCount, cfg.Owner, cfg.Tool, summary)
		return newCount, nil
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return 0, err
	}
	if err := upsertLedger(cfg.StateDir, hash, now, newCount, sev, cfg.Tool, cfg.Owner, action, summary); err != nil {
		return 0, err
	}
	if err := appendEvent(cfg.StateDir, now, hash, sev, action, newCount, cfg.Owner, cfg.Tool, summary); err != nil {
		return 0, err
	}
	fmt.Fprintf(cfg.Logger,
		"[signal:%s] severity=%s action=%s count=%d owner=%s tool=%s summary=%s\n",
		hash, sev, action, newCount, cfg.Owner, cfg.Tool, summary)
	return newCount, nil
}

// upsertLedger rewrites seen.tsv: if hash exists, update its row
// in-place (keeping first_seen); else append a new row.
func upsertLedger(stateDir, hash, now string, count int, sev Severity, tool, owner string, action Action, summary string) error {
	path := filepath.Join(stateDir, "seen.tsv")
	row := func(firstSeen string) string {
		return strings.Join([]string{
			hash,
			firstSeen,
			now,
			strconv.Itoa(count),
			string(sev),
			tool,
			owner,
			string(action),
			summary,
		}, "\t") + "\n"
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var buf bytes.Buffer
	updated := false
	if len(existing) > 0 {
		for _, ln := range strings.SplitAfter(string(existing), "\n") {
			if ln == "" {
				continue
			}
			fields := strings.SplitN(strings.TrimRight(ln, "\n"), "\t", 9)
			if len(fields) >= 1 && fields[0] == hash {
				firstSeen := now
				if len(fields) >= 2 && fields[1] != "" {
					firstSeen = fields[1]
				}
				buf.WriteString(row(firstSeen))
				updated = true
				continue
			}
			buf.WriteString(ln)
		}
	}
	if !updated {
		buf.WriteString(row(now))
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendEvent(stateDir, now, hash string, sev Severity, action Action, count int, owner, tool, summary string) error {
	path := filepath.Join(stateDir, "events.jsonl")
	rec := struct {
		TS       string `json:"ts"`
		Hash     string `json:"hash"`
		Severity string `json:"severity"`
		Action   string `json:"action"`
		Count    int    `json:"count"`
		Owner    string `json:"owner"`
		Tool     string `json:"tool"`
		Summary  string `json:"summary"`
	}{now, hash, string(sev), string(action), count, owner, tool, summary}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b = append(b, '\n')
	_, err = f.Write(b)
	return err
}

func alreadyMinted(stateDir, hash string) (bool, error) {
	path := filepath.Join(stateDir, "minted.tsv")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.SplitN(s.Text(), "\t", 3)
		if len(fields) >= 1 && fields[0] == hash {
			return true, nil
		}
	}
	return false, s.Err()
}

func markMinted(stateDir, hash, now, title string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(stateDir, "minted.tsv")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\n", hash, now, title)
	return err
}

// MintViaWT shells out to `wt task new --draft <title> --body-stdin
// --no-edit`, piping body on stdin. It is the production wiring for
// Config.Mint. Tests inject their own MintFunc.
func MintViaWT(hash, title, body string) error {
	if _, err := exec.LookPath("wt"); err != nil {
		return fmt.Errorf("wt: %w", err)
	}
	cmd := exec.Command("wt", "task", "new", "--draft", title, "--body-stdin", "--no-edit")
	cmd.Stdin = strings.NewReader(body)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// ShellQuote returns a POSIX-shell-safe rendering of args joined by
// spaces. Used to build the cmd= portion of an exit-error summary.
func ShellQuote(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shellQuoteOne(a)
	}
	return strings.Join(out, " ")
}

func shellQuoteOne(a string) string {
	if a == "" {
		return "''"
	}
	for _, r := range a {
		if !shellSafeRune(r) {
			return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return a
}

func shellSafeRune(r rune) bool {
	if r > unicode.MaxASCII {
		return false
	}
	switch {
	case r >= '0' && r <= '9':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	}
	switch r {
	case '_', '-', '.', '/', ':', '+', '=', '@':
		return true
	}
	return false
}

// safeMultiWriter is io.MultiWriter with a mutex, so concurrent writes
// from the wrapped command's stdout and stderr fds do not interleave
// at byte granularity inside one call to Write. (io.MultiWriter alone
// has no such guarantee.)
type safeMultiWriter struct {
	mu sync.Mutex
	ws []io.Writer
}

func newSafeMultiWriter(ws ...io.Writer) *safeMultiWriter {
	return &safeMultiWriter{ws: ws}
}

func (w *safeMultiWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, dst := range w.ws {
		if _, err := dst.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// MatchesSignal is exported for use by callers (and tests) that want
// the same warn/deprecated/error/etc. boundary check applied
// elsewhere.
func MatchesSignal(line string) bool { return matchesSignal(line) }
