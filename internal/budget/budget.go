// Package budget aggregates Anthropic spend across all claude-code
// sessions for the current user on this host into rolling short (5h)
// and long (7d) windows.
//
// Two collection modes:
//
//   subscription  primary signal is Anthropic's OAuth /usage endpoint
//                 (account-wide, sees every host's claude-code activity).
//                 Falls back to ~/.claude/projects/*/*.jsonl cost-weighted
//                 transcript aggregation when /usage is unreachable.
//   api           short window read from response-header spool
//                 ($AGENT_BUDGET_STATE_DIR/api-headers.jsonl); long
//                 window falls back to transcript-est until Anthropic
//                 exposes a weekly header.
//
// Mode is picked from $AGENT_BUDGET_MODE or auto-detected (recent
// api-headers line wins). State at $AGENT_BUDGET_STATE_DIR/state.json
// is byte-compatible with the basecamp agent-budget binary so the two
// can shadow-soak against the same file.
package budget

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	shortWindow      = 5 * time.Hour
	longWindow       = 7 * 24 * time.Hour
	defaultShortCap  = 250.0
	defaultLongCap   = 2000.0
	tightenShortFrac = 0.8
	tightenLongFrac  = 0.8
	rationShortFrac  = 0.9
	rationLongFrac   = 0.8
	stateFileMode    = 0o600
	stateDirMode     = 0o700
)

type message struct {
	RequestID string    `json:"request_id"`
	Timestamp time.Time `json:"ts"`
	Model     string    `json:"model"`
	CostUSD   float64   `json:"cost_usd"`
	Tokens    int64     `json:"tokens"`
}

type fileEntry struct {
	SizeBytes int64     `json:"size"`
	MtimeNs   int64     `json:"mtime_ns"`
	Messages  []message `json:"messages"`
}

type windowState struct {
	DurationSeconds int        `json:"duration_seconds"`
	CostUSD         float64    `json:"cost_usd"`
	CapUSD          float64    `json:"cap_usd"`
	Frac            float64    `json:"frac"`
	OldestEventAt   *time.Time `json:"oldest_event_at,omitempty"`
	ResetAt         *time.Time `json:"reset_at,omitempty"`
	MessageCount    int        `json:"message_count"`
	Source          string     `json:"source,omitempty"`
	TokensRemaining *int64     `json:"tokens_remaining,omitempty"`
	TokensLimit     *int64     `json:"tokens_limit,omitempty"`
	TokensBucket    string     `json:"tokens_bucket,omitempty"`
}

type state struct {
	Mode          string                `json:"mode"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Short         windowState           `json:"short"`
	Long          windowState           `json:"long"`
	Advice        string                `json:"advice"`
	Cache         map[string]*fileEntry `json:"cache"`
	UsageSnapshot *usageSnapshot        `json:"usage_snapshot,omitempty"`
}

func stateDir() (string, error) {
	if d := os.Getenv("AGENT_BUDGET_STATE_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "agent-budget"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agent-budget"), nil
}

func projectsDir() (string, error) {
	if d := os.Getenv("AGENT_BUDGET_PROJECTS"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

func capUSD(env string, def float64) float64 {
	v := os.Getenv(env)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

func statePath() (string, error) {
	d, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.json"), nil
}

func loadState() (*state, error) {
	p, err := statePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &state{Cache: map[string]*fileEntry{}}, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	s := &state{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if s.Cache == nil {
		s.Cache = map[string]*fileEntry{}
	}
	return s, nil
}

func writeState(s *state) error {
	d, err := stateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, stateDirMode); err != nil {
		return err
	}
	p := filepath.Join(d, "state.json")
	tmp := p + ".tmp"
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(tmp, b, stateFileMode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Refresh recomputes state.json from the configured collection mode.
// In subscription mode it polls /usage (subject to a freshness gate)
// and walks ~/.claude/projects/*/*.jsonl as a transcript fallback. In
// api mode it reads the most recent api-headers.jsonl line.
func Refresh() error {
	s, err := loadState()
	if err != nil {
		return err
	}
	root, err := projectsDir()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	cutoff := now.Add(-longWindow - time.Hour)

	live := map[string]bool{}

	if _, err := os.Stat(root); err == nil {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			if fi.ModTime().Before(cutoff) {
				return nil
			}
			live[path] = true
			cached, ok := s.Cache[path]
			if ok && cached.SizeBytes == fi.Size() && cached.MtimeNs == fi.ModTime().UnixNano() {
				return nil
			}
			msgs, perr := parseTranscript(path)
			if perr != nil {
				fmt.Fprintf(os.Stderr, "spore budget: parse %s: %v\n", path, perr)
				return nil
			}
			s.Cache[path] = &fileEntry{
				SizeBytes: fi.Size(),
				MtimeNs:   fi.ModTime().UnixNano(),
				Messages:  msgs,
			}
			return nil
		})
		if err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	for path := range s.Cache {
		if !live[path] {
			delete(s.Cache, path)
		}
	}

	refreshUsageSnapshot(s, now)
	computeAggregates(s, now)
	return writeState(s)
}

// usageMinInterval is the minimum gap between successive /usage hits.
// The Stop hook fires per consumer turn (could be many per minute);
// dispatcher band decisions tolerate ~minute-old data, and Anthropic
// rate-limits /usage with multi-minute retry-after (observed 429s with
// retry-after ~280s). Tunable via $AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC
// for test or operator override.
const usageMinInterval = 60 * time.Second

func refreshUsageSnapshot(s *state, now time.Time) {
	mode, err := resolveMode(now)
	if err != nil {
		mode = "subscription"
	}
	if mode != "subscription" {
		return
	}
	if s.UsageSnapshot != nil && !s.UsageSnapshot.Stale {
		minInterval := usageMinInterval
		if v := os.Getenv("AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				minInterval = time.Duration(n) * time.Second
			}
		}
		if now.Sub(s.UsageSnapshot.FetchedAt) < minInterval {
			return
		}
	}
	ur, ferr := fetchUsage(context.Background(), now)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "spore budget: /usage unavailable (%v); transcript fallback in effect\n", ferr)
		if s.UsageSnapshot != nil {
			s.UsageSnapshot.Stale = true
		}
		return
	}
	s.UsageSnapshot = &usageSnapshot{
		FetchedAt: now,
		Short:     ur.FiveHour,
		Long:      ur.SevenDay,
	}
}

// Query prints state.json with a fresh advice band to stdout.
func Query() error {
	s, err := loadState()
	if err != nil {
		return err
	}
	computeAggregates(s, time.Now().UTC())
	out := map[string]any{
		"mode":       s.Mode,
		"updated_at": s.UpdatedAt,
		"short":      s.Short,
		"long":       s.Long,
		"advice":     s.Advice,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// Summary prints a one-line human-readable summary to stdout.
func Summary() error {
	s, err := loadState()
	if err != nil {
		return err
	}
	computeAggregates(s, time.Now().UTC())
	fmt.Println(formatSummary(s))
	return nil
}

func formatSummary(s *state) string {
	return fmt.Sprintf("short: %d%% (%s) | long: %d%% (%s) | advice: %s",
		pct(s.Short.Frac), windowResetHint(s.Short, shortWindow),
		pct(s.Long.Frac), windowResetHint(s.Long, longWindow),
		s.Advice,
	)
}

func pct(frac float64) int {
	if frac < 0 {
		frac = 0
	}
	return int(frac*100 + 0.5)
}

func windowResetHint(w windowState, dur time.Duration) string {
	if w.ResetAt != nil {
		left := time.Until(*w.ResetAt)
		if left < 0 {
			left = 0
		}
		return formatDuration(left)
	}
	if w.OldestEventAt == nil {
		return formatDuration(dur)
	}
	left := time.Until(w.OldestEventAt.Add(dur))
	if left < 0 {
		left = 0
	}
	return formatDuration(left)
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60
	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// computeAggregates fills s.Mode / s.Short / s.Long / s.Advice. Mode is
// resolved per call so the same on-disk cache supports both
// subscription (transcript-cost-weighted) and api (header-driven short
// window + transcript-est long window) consumers without a separate
// state file. Idempotent; safe to run on each query.
func computeAggregates(s *state, now time.Time) {
	short, long := transcriptWindows(s, now)

	mode, err := resolveMode(now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore budget: %v; falling back to subscription\n", err)
		mode = "subscription"
	}

	s.UpdatedAt = now
	s.Mode = mode

	switch mode {
	case "api":
		shortAPI, ok := apiShortWindow(now)
		if ok {
			s.Short = shortAPI
		} else {
			s.Short = short.finalize("transcript-est")
		}
		s.Long = long.finalize("transcript-est")
	default:
		if s.UsageSnapshot != nil {
			s.Short = usageWindowState(s.UsageSnapshot.Short, shortWindow, s.UsageSnapshot.Stale)
			s.Long = usageWindowState(s.UsageSnapshot.Long, longWindow, s.UsageSnapshot.Stale)
		} else {
			s.Short = short.finalize("transcript")
			s.Long = long.finalize("transcript")
		}
	}
	s.Advice = adviceFor(s.Short.Frac, s.Long.Frac)
}

func transcriptWindows(s *state, now time.Time) (windowAccum, windowAccum) {
	short := windowAccum{cap: capUSD("AGENT_BUDGET_SHORT_CAP", defaultShortCap), dur: shortWindow}
	long := windowAccum{cap: capUSD("AGENT_BUDGET_LONG_CAP", defaultLongCap), dur: longWindow}

	seen := make(map[string]struct{}, 1024)
	for _, fe := range s.Cache {
		for _, m := range fe.Messages {
			key := m.RequestID
			if key == "" {
				key = m.Timestamp.Format(time.RFC3339Nano) + "|" + m.Model
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			age := now.Sub(m.Timestamp)
			if age >= 0 && age <= short.dur {
				short.add(m)
			}
			if age >= 0 && age <= long.dur {
				long.add(m)
			}
		}
	}
	return short, long
}

func apiShortWindow(now time.Time) (windowState, bool) {
	hl, err := readLatestHeaderLine(os.Getenv("AGENT_BUDGET_IDENTITY"))
	if err != nil || hl == nil {
		return windowState{}, false
	}
	r, ok := parseRateLimitReading(hl.Headers)
	if !ok {
		return windowState{}, false
	}
	dur := shortWindow
	if r.ResetAt != nil {
		left := r.ResetAt.Sub(now)
		if left > 0 {
			dur = left
		}
	}
	rem := r.TokensRemaining
	lim := r.TokensLimit
	return windowState{
		DurationSeconds: int(dur.Seconds()),
		Frac:            r.Frac,
		ResetAt:         r.ResetAt,
		Source:          "api-headers",
		TokensRemaining: &rem,
		TokensLimit:     &lim,
		TokensBucket:    r.Bucket,
	}, true
}

type windowAccum struct {
	cap    float64
	dur    time.Duration
	cost   float64
	count  int
	oldest *time.Time
}

func (a *windowAccum) add(m message) {
	a.cost += m.CostUSD
	a.count++
	if a.oldest == nil || m.Timestamp.Before(*a.oldest) {
		t := m.Timestamp
		a.oldest = &t
	}
}

func (a *windowAccum) finalize(source string) windowState {
	frac := 0.0
	if a.cap > 0 {
		frac = a.cost / a.cap
	}
	w := windowState{
		DurationSeconds: int(a.dur.Seconds()),
		CostUSD:         a.cost,
		CapUSD:          a.cap,
		Frac:            frac,
		MessageCount:    a.count,
		OldestEventAt:   a.oldest,
		Source:          source,
	}
	if a.oldest != nil {
		r := a.oldest.Add(a.dur)
		w.ResetAt = &r
	}
	return w
}

func adviceFor(shortFrac, longFrac float64) string {
	if shortFrac >= rationShortFrac || longFrac >= rationLongFrac {
		return "ration"
	}
	if shortFrac >= tightenShortFrac || longFrac >= tightenLongFrac {
		return "tighten"
	}
	return "ok"
}

// parseTranscript reads a claude-code JSONL transcript and returns one
// message per unique request_id with cost-weighted spend. Lines that
// aren't `assistant` records or that lack a `usage` block are skipped;
// malformed JSON lines are tolerated (transcripts are append-only and
// the last line may be partial during a live session).
func parseTranscript(path string) ([]message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	seen := make(map[string]struct{})
	var out []message

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" || rec.Message == nil || rec.Message.Usage == nil {
			continue
		}
		key := rec.RequestID
		if key == "" {
			key = rec.Message.ID
		}
		if key != "" {
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		ts := rec.Timestamp
		if ts.IsZero() {
			continue
		}
		cost, tokens := costForUsage(rec.Message.Model, rec.Message.Usage)
		out = append(out, message{
			RequestID: key,
			Timestamp: ts.UTC(),
			Model:     rec.Message.Model,
			CostUSD:   cost,
			Tokens:    tokens,
		})
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

type transcriptRecord struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	Timestamp time.Time       `json:"timestamp"`
	Message   *messagePayload `json:"message,omitempty"`
}

type messagePayload struct {
	ID    string      `json:"id"`
	Model string      `json:"model"`
	Usage *usageBlock `json:"usage,omitempty"`
}

type usageBlock struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func costForUsage(model string, u *usageBlock) (cost float64, totalTokens int64) {
	p := PricingFor(model)
	cost += float64(u.InputTokens) * p.InputPerMTok / 1e6
	cost += float64(u.OutputTokens) * p.OutputPerMTok / 1e6
	cost += float64(u.CacheReadInputTokens) * p.CacheReadPerMTok / 1e6
	cost += float64(u.CacheCreationInputTokens) * p.CacheCreatePerMTok / 1e6
	totalTokens = u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return cost, totalTokens
}

// bandFor maps a single window's frac to its band. Mirrors the
// OR-of-windows logic in adviceFor but applied per window so the
// stop-hook can detect per-window crossings independently.
func bandFor(frac, tighten, ration float64) string {
	switch {
	case frac >= ration:
		return "ration"
	case frac >= tighten:
		return "tighten"
	default:
		return "ok"
	}
}

func markersDir() (string, error) {
	d, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "markers"), nil
}

// updateMarkers reconciles the on-disk per-window-per-band markers
// with the current band and reports whether this is a fresh crossing
// for window. Marker invariants:
//   - band == "ok":      both tighten + ration markers absent.
//   - band == "tighten": tighten marker present, ration marker absent.
//   - band == "ration":  both markers present (so a future drop to
//     tighten does not re-fire the tighten reminder).
//
// "Fresh" = the marker for the current band did not exist on entry.
func updateMarkers(dir, window, band string) (bool, error) {
	tighten := filepath.Join(dir, window+"-tighten")
	ration := filepath.Join(dir, window+"-ration")
	switch band {
	case "ok":
		_ = os.Remove(tighten)
		_ = os.Remove(ration)
		return false, nil
	case "tighten":
		_ = os.Remove(ration)
		return createMarker(tighten)
	case "ration":
		if _, err := createMarker(tighten); err != nil {
			return false, err
		}
		return createMarker(ration)
	}
	return false, nil
}

func createMarker(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, stateFileMode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	_ = f.Close()
	return true, nil
}

func reminderTextFor(s *state, band string) string {
	tail := map[string]string{
		"tighten": "Defer non-urgent runner starts. Route lightweight turns through a cheaper model. Reserve top-tier models for tool-use loops and code edits.",
		"ration":  "Stop spawning runners this window. Only spend top-tier model time on turns that genuinely need it. Post the blocker and idle until reset otherwise.",
	}
	short := windowFragment("short", s.Short, shortWindow, band, tightenShortFrac, rationShortFrac)
	long := windowFragment("long", s.Long, longWindow, band, tightenLongFrac, rationLongFrac)
	return fmt.Sprintf("AGENT BUDGET (%s): %s, %s.\n%s", band, short, long, tail[band])
}

// windowFragment renders one window's slice of the reminder, appending
// "(resets in ...)" only when this window is the one that triggered
// band. Without that gate every reminder would carry two reset hints;
// the brief prefers the binding window only.
func windowFragment(label string, w windowState, dur time.Duration, band string, tighten, ration float64) string {
	binding := false
	switch band {
	case "ration":
		binding = w.Frac >= ration
	case "tighten":
		binding = w.Frac >= tighten
	}
	if binding {
		return fmt.Sprintf("%s=%d%% (resets in %s)", label, pct(w.Frac), windowResetHint(w, dur))
	}
	return fmt.Sprintf("%s=%d%%", label, pct(w.Frac))
}

// StopHook is the spore-budget Stop-hook entry. It refreshes state,
// recomputes bands, and returns the desired process exit code:
//   - 0 silent (no fresh band crossing or band == ok).
//   - 2 with a reminder line on stderr (fresh crossing into tighten or
//     ration).
//
// Spore does not gate this on an orchestrator-identity env: consumers
// wire the hook into settings.json only for the agents they want
// monitored. Any orchestrator-shape gating belongs in the consumer's
// hook config, not in this binary.
func StopHook() int {
	if err := Refresh(); err != nil {
		fmt.Fprintf(os.Stderr, "spore budget stop-hook: refresh: %v\n", err)
		return 0
	}
	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore budget stop-hook: load: %v\n", err)
		return 0
	}
	computeAggregates(s, time.Now().UTC())

	dir, err := markersDir()
	if err != nil {
		return 0
	}
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return 0
	}

	shortBand := bandFor(s.Short.Frac, tightenShortFrac, rationShortFrac)
	longBand := bandFor(s.Long.Frac, tightenLongFrac, rationLongFrac)

	freshShort, _ := updateMarkers(dir, "short", shortBand)
	freshLong, _ := updateMarkers(dir, "long", longBand)
	if !freshShort && !freshLong {
		return 0
	}
	band := s.Advice
	if band == "ok" {
		return 0
	}
	fmt.Fprintln(os.Stderr, reminderTextFor(s, band))
	return 2
}
