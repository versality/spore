// API-mode capture surface for spore budget.
//
// Anthropic API responses carry rolling rate-limit state on every
// response (`anthropic-ratelimit-*` headers). This file owns the
// on-disk spool that any caller appends to, and the readers that fold
// the latest line into state.json under api mode.
//
// Spool format (`$AGENT_BUDGET_STATE_DIR/api-headers.jsonl`, 0600):
//
//	{"ts":"2026-04-26T10:30:00Z","identity":"runner-a",
//	 "model":"claude-haiku-4-5",
//	 "headers":{"anthropic-ratelimit-tokens-remaining":"40000", ...}}
//
// One JSON object per line. Append-only via O_APPEND so that small
// writes are POSIX-atomic without explicit locking; size cap on a
// single header line keeps us under PIPE_BUF.
//
// Producers: any process making Anthropic API calls. The recommended
// integration is to write the line directly (no IPC) for negligible
// hot-path overhead. `spore budget capture` is provided as a stdin
// shim for shell-script callers and tests.

package budget

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	apiHeadersFilename  = "api-headers.jsonl"
	apiHeadersMode      = 0o600
	captureMaxLineSize  = 8 * 1024
	apiAutoDetectMaxAge = 30 * time.Minute
)

type headerLine struct {
	Timestamp time.Time         `json:"ts"`
	Identity  string            `json:"identity"`
	Model     string            `json:"model,omitempty"`
	Headers   map[string]string `json:"headers"`
}

// rateLimitHeaderPrefixes is the allow-list applied during capture.
// Anthropic exposes per-bucket counters under anthropic-ratelimit-*
// and per-tier flags under anthropic-priority-*; everything else is
// request metadata that doesn't belong in a budget spool.
var rateLimitHeaderPrefixes = []string{
	"anthropic-ratelimit-",
	"anthropic-priority-",
}

func apiHeadersPath() (string, error) {
	d, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, apiHeadersFilename), nil
}

func filterRateLimitHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		lk := strings.ToLower(k)
		for _, p := range rateLimitHeaderPrefixes {
			if strings.HasPrefix(lk, p) {
				out[lk] = v
				break
			}
		}
	}
	return out
}

// appendHeaderLine writes one filtered headerLine to the spool. The
// write fits in a single write(2) under captureMaxLineSize so O_APPEND
// gives atomicity for free.
func appendHeaderLine(line *headerLine) error {
	if line.Identity == "" {
		return errors.New("identity required")
	}
	if line.Timestamp.IsZero() {
		line.Timestamp = time.Now().UTC()
	} else {
		line.Timestamp = line.Timestamp.UTC()
	}
	line.Headers = filterRateLimitHeaders(line.Headers)
	if len(line.Headers) == 0 {
		return errors.New("no anthropic-ratelimit-* / anthropic-priority-* headers present")
	}

	b, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	b = append(b, '\n')
	if len(b) > captureMaxLineSize {
		return fmt.Errorf("encoded line %d bytes exceeds %d-byte cap", len(b), captureMaxLineSize)
	}

	d, err := stateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, stateDirMode); err != nil {
		return err
	}
	p := filepath.Join(d, apiHeadersFilename)

	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, apiHeadersMode)
	if err != nil {
		return fmt.Errorf("open spool: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("write spool: %w", err)
	}
	return nil
}

// readLatestHeaderLine returns the most recent header line in the
// spool, optionally filtered by identity. Returns (nil, nil) when the
// spool is missing or empty.
func readLatestHeaderLine(identity string) (*headerLine, error) {
	p, err := apiHeadersPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() == 0 {
		return nil, nil
	}

	const tailWindow int64 = 64 * 1024
	start := fi.Size() - tailWindow
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}

	for end := len(buf); end > 0; {
		nl := lastNewlineBefore(buf, end)
		lineStart := nl + 1
		raw := buf[lineStart:end]
		if len(strings.TrimSpace(string(raw))) > 0 {
			var hl headerLine
			if err := json.Unmarshal(raw, &hl); err == nil {
				if identity == "" || hl.Identity == identity {
					return &hl, nil
				}
			}
		}
		if nl < 0 {
			break
		}
		end = nl
	}
	return nil, nil
}

func lastNewlineBefore(b []byte, end int) int {
	for i := end - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

// rateLimitReading collapses the multi-bucket headers into one frac /
// reset pair: pick the bucket with the lowest remaining/limit ratio
// (the one closest to 429). The chosen bucket name lands in
// TokensBucket for diagnostics.
type rateLimitReading struct {
	Frac            float64
	TokensRemaining int64
	TokensLimit     int64
	Bucket          string
	ResetAt         *time.Time
}

var rateLimitBuckets = []string{
	"input-tokens",
	"output-tokens",
	"tokens",
	"requests",
}

func parseRateLimitReading(headers map[string]string) (rateLimitReading, bool) {
	best := rateLimitReading{Frac: -1}
	for _, bucket := range rateLimitBuckets {
		remStr, ok := headers["anthropic-ratelimit-"+bucket+"-remaining"]
		if !ok {
			continue
		}
		limStr, ok := headers["anthropic-ratelimit-"+bucket+"-limit"]
		if !ok {
			continue
		}
		rem, err := strconv.ParseInt(remStr, 10, 64)
		if err != nil {
			continue
		}
		lim, err := strconv.ParseInt(limStr, 10, 64)
		if err != nil || lim <= 0 {
			continue
		}
		frac := float64(lim-rem) / float64(lim)
		if frac < 0 {
			frac = 0
		}
		if frac > best.Frac {
			best = rateLimitReading{
				Frac:            frac,
				TokensRemaining: rem,
				TokensLimit:     lim,
				Bucket:          bucket,
			}
			if rs := headers["anthropic-ratelimit-"+bucket+"-reset"]; rs != "" {
				if t, err := time.Parse(time.RFC3339, rs); err == nil {
					t = t.UTC()
					best.ResetAt = &t
				}
			}
		}
	}
	if best.Frac < 0 {
		return rateLimitReading{}, false
	}
	return best, true
}

// resolveMode picks subscription vs api. Explicit env wins; otherwise
// auto-detect: if the spool has a header line within
// apiAutoDetectMaxAge, use api. Falls back to subscription.
func resolveMode(now time.Time) (string, error) {
	if m := strings.ToLower(os.Getenv("AGENT_BUDGET_MODE")); m != "" {
		switch m {
		case "api", "subscription":
			return m, nil
		default:
			return "", fmt.Errorf("AGENT_BUDGET_MODE=%q invalid (want api|subscription)", m)
		}
	}
	hl, err := readLatestHeaderLine(os.Getenv("AGENT_BUDGET_IDENTITY"))
	if err != nil {
		return "subscription", nil
	}
	if hl == nil {
		return "subscription", nil
	}
	if now.Sub(hl.Timestamp) <= apiAutoDetectMaxAge {
		return "api", nil
	}
	return "subscription", nil
}

// Capture reads one JSON headerLine from stdin and appends it to the
// spool. Convenience for shell-script callers and tests; in-process
// callers should prefer appendHeaderLine directly.
func Capture() error {
	var line headerLine
	dec := json.NewDecoder(io.LimitReader(os.Stdin, captureMaxLineSize))
	if err := dec.Decode(&line); err != nil {
		return fmt.Errorf("decode stdin: %w", err)
	}
	if line.Identity == "" {
		line.Identity = os.Getenv("AGENT_BUDGET_IDENTITY")
		if line.Identity == "" {
			line.Identity = os.Getenv("USER")
		}
	}
	return appendHeaderLine(&line)
}
