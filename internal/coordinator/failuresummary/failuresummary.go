// Package failuresummary aggregates recurring failure signals from
// the JSONL ledgers spore already writes (context-wraps, respawns,
// codex inbox wake-errors) plus a fleet-floor probe, and emits one
// delta-only block plus a list of safe recovery commands the operator
// can run. No tmux input, no auto-execution: metadata reads and
// command guidance only.
//
// Inputs (all optional; missing files count as zero):
//
//	$WT_STATE/worker-voluntary-events.jsonl              context-wrap events (epoch, tier, slug)
//	$SPORE_COORDINATOR_STATE_DIR/respawn-events.jsonl    coordinator respawns (ts)
//	$SPORE_COORDINATOR_STATE_DIR/codex-inbox-watcher.jsonl  wake-errors (ts, status, source)
//	spore fleet status                                   active-live floor signal
//
// Two upstream ledgers from the original bash port have no spore
// producer yet (stuck-worker transitions, worker pane-death reaps).
// Both are reported as dark signals so the operator can see the
// missing input; producers land in a follow-up port.
//
// Exit semantics on the CLI wrapper: 2 when Summary.Actions is
// non-empty, 0 otherwise.
package failuresummary

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/coordinator"
	"github.com/versality/spore/internal/fleet"
)

const (
	DefaultWindowSecs = 86400
	DefaultStuckSecs  = 1800
	DefaultFloor      = 6

	envWorkerStateDir = "WT_STATE"
	envWindowSecs     = "SPORE_FAILURE_WINDOW_SECS"
	envStuckSecs      = "SPORE_FAILURE_STUCK_SECS"
	envFloor          = "SPORE_FLEET_FLOOR"
)

// Config is the runtime configuration for Summarize. Defaults() fills
// zero-value fields from env vars + stdlib defaults.
type Config struct {
	CoordinatorStateDir string
	WorkerStateDir      string
	WindowSecs          int64
	StuckSecs           int64
	Floor               int
	Quiet               bool
	// Now overrides time.Now for tests; nil means real wall clock.
	Now func() time.Time
	// FleetStatus overrides the fleet.RunStatus call for tests. It
	// returns the raw stdout block. nil means run the real fleet
	// scan.
	FleetStatus func() (string, error)
}

// Defaults returns a copy of c with zero-value fields filled from env
// + stdlib defaults. Caller-supplied non-zero fields win.
func (c Config) Defaults() Config {
	if c.CoordinatorStateDir == "" {
		c.CoordinatorStateDir = coordinator.DefaultStateDir()
	}
	if c.WorkerStateDir == "" {
		c.WorkerStateDir = os.Getenv(envWorkerStateDir)
	}
	if c.WorkerStateDir == "" {
		home, _ := os.UserHomeDir()
		c.WorkerStateDir = filepath.Join(home, ".local", "state", "wt")
	}
	if c.WindowSecs == 0 {
		c.WindowSecs = parseInt64Env(envWindowSecs, DefaultWindowSecs)
	}
	if c.StuckSecs == 0 {
		c.StuckSecs = parseInt64Env(envStuckSecs, DefaultStuckSecs)
	}
	if c.Floor == 0 {
		c.Floor = int(parseInt64Env(envFloor, int64(DefaultFloor)))
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

func parseInt64Env(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n < 0 {
		return fallback
	}
	return n
}

// Counts mirrors the per-signal counts in the rendered block.
type Counts struct {
	Wraps         int
	Respawns      int
	WakeErrors    int
	StuckWorkers  int
	PaneDeaths    int
	TierBreakdown map[string]int
	TopSlugs      []SlugCount
}

type SlugCount struct {
	Slug  string
	Count int
}

// Summary is the verdict from Summarize. Actions non-empty means
// exit 2 on the wrapper.
type Summary struct {
	WindowHours int
	StateDir    string
	Counts      Counts
	Actions     []string
	// DarkSignals lists input ledgers that have no spore producer
	// yet; the count is zero by construction. Surface them so the
	// operator knows the signal is dark, not green.
	DarkSignals []string
}

// Summarize runs the scan and returns the structured Summary.
func Summarize(cfg Config) Summary {
	cfg = cfg.Defaults()

	now := cfg.Now().Unix()
	threshold := now - cfg.WindowSecs

	wraps := loadJSONL(filepath.Join(cfg.WorkerStateDir, "worker-voluntary-events.jsonl"))
	wrapsCount := countByEpoch(wraps, threshold)
	tierMap := tierBreakdownByEpoch(wraps, threshold)
	topSlugs := topSlugsByEpoch(wraps, threshold, 3, 2)

	respawns := loadJSONL(filepath.Join(cfg.CoordinatorStateDir, "respawn-events.jsonl"))
	respawnCount := countByTS(respawns, threshold, nil)

	codex := loadJSONL(filepath.Join(cfg.CoordinatorStateDir, "codex-inbox-watcher.jsonl"))
	wakeErrCount := countByTS(codex, threshold, func(r jsonRow) bool {
		return r.str("status") == "wake-error"
	})

	var actions []string
	if al, ok := activeLiveCount(cfg); ok && al < cfg.Floor {
		actions = append(actions, fmt.Sprintf(
			"active-live=%d below floor=%d -> mint a draft via spore task new --draft '<title>' or unblock a blocked slug",
			al, cfg.Floor))
	}
	if wakes := wakeErrorSources(codex, threshold, 3); len(wakes) > 0 {
		actions = append(actions, fmt.Sprintf(
			"codex inbox wake-errors recur for: %s -> inspect inbox dirs + restart the codex inbox watcher (no tmux input)",
			formatSlugCounts(wakes)))
	}

	windowH := int(cfg.WindowSecs / 3600)
	if windowH < 1 {
		windowH = 1
	}

	return Summary{
		WindowHours: windowH,
		StateDir:    cfg.CoordinatorStateDir,
		Counts: Counts{
			Wraps:         wrapsCount,
			Respawns:      respawnCount,
			WakeErrors:    wakeErrCount,
			StuckWorkers:  0,
			PaneDeaths:    0,
			TierBreakdown: tierMap,
			TopSlugs:      topSlugs,
		},
		Actions:     actions,
		DarkSignals: []string{"stuck-workers", "pane-deaths"},
	}
}

// Format renders the block. Quiet drops the header + counts and the
// "actionable: (none)" trailer; only the bulleted action lines remain.
func (s Summary) Format(quiet bool) string {
	var b strings.Builder
	if !quiet {
		fmt.Fprintf(&b, "[spore-failure-summary] window=%dh state_dir=%s\n",
			s.WindowHours, s.StateDir)
		fmt.Fprintf(&b, "counts: wraps=%d", s.Counts.Wraps)
		if tier := formatTierBreakdown(s.Counts.TierBreakdown); tier != "" {
			fmt.Fprintf(&b, " (%s)", tier)
		}
		if top := formatTopSlugs(s.Counts.TopSlugs); top != "" {
			fmt.Fprintf(&b, " top: %s", top)
		}
		fmt.Fprintf(&b, " respawns=%d wake-errors=%d stuck-workers=%d pane-deaths=%d\n",
			s.Counts.Respawns, s.Counts.WakeErrors, s.Counts.StuckWorkers, s.Counts.PaneDeaths)
		if len(s.DarkSignals) > 0 {
			fmt.Fprintf(&b, "dark-signals: %s (no producer yet)\n",
				strings.Join(s.DarkSignals, " "))
		}
	}
	if len(s.Actions) > 0 {
		if !quiet {
			b.WriteString("actionable:\n")
		}
		for _, a := range s.Actions {
			fmt.Fprintf(&b, "  - %s\n", a)
		}
		return b.String()
	}
	if !quiet {
		b.WriteString("actionable: (none)\n")
	}
	return b.String()
}

// jsonRow keeps raw bytes so each accessor can apply its own
// type-tolerant decode (jq-style field access).
type jsonRow map[string]json.RawMessage

func (r jsonRow) str(key string) string {
	raw, ok := r[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func (r jsonRow) num(key string) (float64, bool) {
	raw, ok := r[key]
	if !ok {
		return 0, false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	return 0, false
}

func loadJSONL(path string) []jsonRow {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var rows []jsonRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r jsonRow
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			rows = append(rows, r)
		}
	}
	return rows
}

func countByEpoch(rows []jsonRow, threshold int64) int {
	n := 0
	for _, r := range rows {
		ep, ok := r.num("epoch")
		if !ok || int64(ep) < threshold {
			continue
		}
		n++
	}
	return n
}

func countByTS(rows []jsonRow, threshold int64, extra func(jsonRow) bool) int {
	n := 0
	for _, r := range rows {
		ts := r.str("ts")
		if ts == "" {
			continue
		}
		ep, ok := parseTS(ts)
		if !ok || ep < threshold {
			continue
		}
		if extra != nil && !extra(r) {
			continue
		}
		n++
	}
	return n
}

// parseTS handles RFC3339 with or without fractional seconds and with
// any offset. Returns false on malformed input.
func parseTS(ts string) (int64, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

func tierBreakdownByEpoch(rows []jsonRow, threshold int64) map[string]int {
	m := map[string]int{}
	for _, r := range rows {
		ep, ok := r.num("epoch")
		if !ok || int64(ep) < threshold {
			continue
		}
		tier := r.str("tier")
		if tier == "" {
			tier = "unknown"
		}
		m[tier]++
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func topSlugsByEpoch(rows []jsonRow, threshold int64, limit, minCount int) []SlugCount {
	counts := map[string]int{}
	for _, r := range rows {
		ep, ok := r.num("epoch")
		if !ok || int64(ep) < threshold {
			continue
		}
		slug := r.str("slug")
		if slug == "" {
			continue
		}
		counts[slug]++
	}
	return topN(counts, limit, minCount)
}

func wakeErrorSources(rows []jsonRow, threshold int64, minCount int) []SlugCount {
	counts := map[string]int{}
	for _, r := range rows {
		if r.str("status") != "wake-error" {
			continue
		}
		ep, ok := parseTS(r.str("ts"))
		if !ok || ep < threshold {
			continue
		}
		src := r.str("source")
		if src == "" {
			continue
		}
		counts[src]++
	}
	return topN(counts, 0, minCount)
}

func topN(counts map[string]int, limit, minCount int) []SlugCount {
	out := make([]SlugCount, 0, len(counts))
	for k, v := range counts {
		if v < minCount {
			continue
		}
		out = append(out, SlugCount{Slug: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Slug < out[j].Slug
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

var activeLiveRE = regexp.MustCompile(`active-live=(\d+)`)

func activeLiveCount(cfg Config) (int, bool) {
	var raw string
	var err error
	if cfg.FleetStatus != nil {
		raw, err = cfg.FleetStatus()
	} else {
		var buf bytes.Buffer
		_, err = fleet.RunStatus(&buf, io.Discard)
		raw = buf.String()
	}
	if err != nil || raw == "" {
		return 0, false
	}
	m := activeLiveRE.FindStringSubmatch(raw)
	if m == nil {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// formatTierBreakdown renders the tier map as `tier:count` pairs
// joined by single spaces, sorted by tier name for stable output.
func formatTierBreakdown(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func formatTopSlugs(s []SlugCount) string {
	if len(s) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s))
	for _, sc := range s {
		parts = append(parts, fmt.Sprintf("%s(%d)", sc.Slug, sc.Count))
	}
	return strings.Join(parts, " ")
}

func formatSlugCounts(s []SlugCount) string {
	return formatTopSlugs(s)
}
