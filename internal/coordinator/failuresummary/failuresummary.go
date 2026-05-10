// Package failuresummary aggregates recurring failures from the
// state files skyhelm + wt already keep, and emits one delta-only
// summary plus a list of safe `wt task ...` commands the operator
// (or skyhelm itself) can run to recover. No tmux input, no
// auto-execution: only metadata reads and command guidance.
//
// Mirrors the bash skyhelm-failure-summary port. Inputs (all
// optional, missing files count as zero):
//
//	$WT_STATE/rower-voluntary-events.jsonl       rower context-wraps by tier+slug (epoch field)
//	$WT_STATE/rower-pane-death.jsonl             reaper-detected dead-pane reaps (ts field)
//	$SKYHELM_STATE_DIR/respawn-events.jsonl      skyhelm respawns (epoch field)
//	$SKYHELM_STATE_DIR/codex-inbox-watcher.jsonl codex inbox watcher errors (ts field)
//	$SKYHELM_STATE_DIR/rower-watch.json          NDJSON rower transition state
//	tasks/<slug>.md frontmatter                  status, trigger, needs, scheduler
//	wt-task fleet status                         active-live floor signal
//
// Exit semantics on the wrapper: 2 when Result.Actions is non-empty,
// 0 otherwise.
package failuresummary

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultWindowSecs = 86400
	DefaultStuckSecs  = 1800
	DefaultFloor      = 6
	DefaultTaskBin    = "wt-task"
)

// Config is the runtime configuration for Summarize. Defaults() fills
// the zero-value fields from environment and stdlib defaults.
type Config struct {
	StateDir   string
	WtState    string
	WtCfg      string
	WtTaskBin  string
	WindowSecs int64
	StuckSecs  int64
	Floor      int
	Quiet      bool
	// Now overrides time.Now for tests; nil means use real wall clock.
	Now func() time.Time
	// FleetStatus overrides the call to `wt-task fleet status` for
	// tests. Returns the raw stdout. nil means shell out for real.
	FleetStatus func() (string, error)
	// ProjectRoots overrides project root discovery for tests. nil
	// means use the bash-equivalent discovery (WT_CFG/projects file
	// or git common dir).
	ProjectRoots func() []string
}

// Defaults returns a copy of c with zero-value fields populated from
// the env vars and stdlib defaults. Caller-supplied values take
// precedence.
func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.WtState == "" {
		c.WtState = os.Getenv("WT_STATE")
	}
	if c.WtState == "" {
		home, _ := os.UserHomeDir()
		c.WtState = filepath.Join(home, ".local", "state", "wt")
	}
	if c.WtCfg == "" {
		c.WtCfg = os.Getenv("WT_CFG")
	}
	if c.WtCfg == "" {
		home, _ := os.UserHomeDir()
		c.WtCfg = filepath.Join(home, ".config", "wt")
	}
	if c.WtTaskBin == "" {
		if v := os.Getenv("WT_TASK_BIN"); v != "" {
			c.WtTaskBin = v
		} else {
			c.WtTaskBin = DefaultTaskBin
		}
	}
	if c.WindowSecs == 0 {
		c.WindowSecs = parseInt64Env("SKYHELM_FAILURE_WINDOW_SECS", DefaultWindowSecs)
	}
	if c.StuckSecs == 0 {
		c.StuckSecs = parseInt64Env("SKYHELM_FAILURE_STUCK_SECS", DefaultStuckSecs)
	}
	if c.Floor == 0 {
		c.Floor = int(parseInt64Env("WT_FLEET_FLOOR", int64(DefaultFloor)))
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

// Counts is the structured count output, matching the bash counts
// line one-for-one.
type Counts struct {
	Wraps       int
	Respawns    int
	WakeErrors  int
	StuckRowers int
	PaneDeaths  int
	// TierBreakdown maps tier -> count for wraps inside the window,
	// emitted as "tier:count" pairs in the bash output.
	TierBreakdown map[string]int
	// TopSlugs is the up-to-three slug names with >= 2 wraps in the
	// window, ordered by count desc.
	TopSlugs []SlugCount
}

// SlugCount is one (slug, count) pair.
type SlugCount struct {
	Slug  string
	Count int
}

// Summary is the verdict from Summarize. Actions is the actionable
// recovery commands; non-empty means exit 2.
type Summary struct {
	WindowHours int
	StateDir    string
	Counts      Counts
	Actions     []string
}

// Summarize runs the scan and returns the structured Summary.
func Summarize(cfg Config) Summary {
	cfg = cfg.Defaults()

	now := cfg.Now().Unix()
	threshold := now - cfg.WindowSecs

	wt := cfg.WtState
	st := cfg.StateDir

	wraps := loadJSONL(filepath.Join(wt, "rower-voluntary-events.jsonl"))
	wrapsCount := countByEpoch(wraps, threshold, nil)
	tierMap := tierBreakdownByEpoch(wraps, threshold)
	topSlugs := topSlugsByEpoch(wraps, threshold, 3, 2)

	respawns := loadJSONL(filepath.Join(st, "respawn-events.jsonl"))
	respawnCount := countByEpoch(respawns, threshold, nil)

	codex := loadJSONL(filepath.Join(st, "codex-inbox-watcher.jsonl"))
	wakeErrCount := countByTS(codex, threshold, func(r jsonRow) bool {
		return r.str("status") == "wake-error"
	})

	paneDeaths := loadJSONL(filepath.Join(wt, "rower-pane-death.jsonl"))
	paneDeathCount := countByTS(paneDeaths, threshold, func(r jsonRow) bool {
		return r.str("event") == "rower-pane-death"
	})

	stuckRows := stuckRowerLines(filepath.Join(st, "rower-watch.json"), cfg.StuckSecs)

	var actions []string
	for _, r := range stuckRows {
		if r.Status == "active" && r.IdleSecs > cfg.StuckSecs {
			actions = append(actions, fmt.Sprintf(
				"rower %s status=active idle_secs=%d (agent=%s) -> wt task pause %s   # status drift, frontmatter masks idle",
				r.Slug, r.IdleSecs, r.Agent, r.Slug))
		} else if r.Status == "active" {
			actions = append(actions, fmt.Sprintf(
				"rower %s stuck status=active (agent=%s) -> wt task tell %s 'still running?' or wt task pause %s",
				r.Slug, r.Agent, r.Slug, r.Slug))
		}
	}

	for _, b := range blockedWithoutTrigger(cfg) {
		actions = append(actions, fmt.Sprintf(
			"task %s blocked without trigger/needs/scheduler -> wt task tell %s 'add trigger:' or wt task done %s if obsolete   # repo=%s",
			b.Slug, b.Slug, b.Slug, b.Root))
	}

	if al, ok := activeLiveCount(cfg); ok && al < cfg.Floor {
		actions = append(actions, fmt.Sprintf(
			"active-live=%d below floor=%d -> mint a draft via wt task new --draft '<title>' or resume a paused slug",
			al, cfg.Floor))
	}

	if wakes := wakeErrorSlugs(codex, threshold, 3); len(wakes) > 0 {
		actions = append(actions, fmt.Sprintf(
			"codex inbox wake-errors recur for: %s -> ls $WT_STATE/<slug>/inbox; systemctl --user restart codex-skyhelm-inbox-watcher (no tmux input)",
			formatSlugCounts(wakes)))
	}

	windowH := int(cfg.WindowSecs / 3600)
	if windowH < 1 {
		windowH = 1
	}

	return Summary{
		WindowHours: windowH,
		StateDir:    cfg.StateDir,
		Counts: Counts{
			Wraps:         wrapsCount,
			Respawns:      respawnCount,
			WakeErrors:    wakeErrCount,
			StuckRowers:   len(stuckRows),
			PaneDeaths:    paneDeathCount,
			TierBreakdown: tierMap,
			TopSlugs:      topSlugs,
		},
		Actions: actions,
	}
}

// Format renders the bash-equivalent stdout for s. Quiet suppresses
// the header + counts line and the "actionable: (none)" line, matching
// the bash --quiet flag.
func (s Summary) Format(quiet bool) string {
	var b strings.Builder
	if !quiet {
		fmt.Fprintf(&b, "[skyhelm-failure-summary] window=%dh state_dir=%s\n",
			s.WindowHours, s.StateDir)
		fmt.Fprintf(&b, "counts: wraps=%d", s.Counts.Wraps)
		if tier := formatTierBreakdown(s.Counts.TierBreakdown); tier != "" {
			fmt.Fprintf(&b, " (%s)", tier)
		}
		if top := formatTopSlugs(s.Counts.TopSlugs); top != "" {
			fmt.Fprintf(&b, " top: %s", top)
		}
		fmt.Fprintf(&b, " respawns=%d wake-errors=%d stuck-rowers=%d pane-deaths=%d\n",
			s.Counts.Respawns, s.Counts.WakeErrors, s.Counts.StuckRowers, s.Counts.PaneDeaths)
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

// jsonRow is one parsed JSONL row, kept as raw JSON for
// type-tolerant field access matching jq's `.field` semantics.
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

func (r jsonRow) bool_(key string) bool {
	raw, ok := r[key]
	if !ok {
		return false
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return false
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

func countByEpoch(rows []jsonRow, threshold int64, extra func(jsonRow) bool) int {
	n := 0
	for _, r := range rows {
		ep, ok := r.num("epoch")
		if !ok || int64(ep) < threshold {
			continue
		}
		if extra != nil && !extra(r) {
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

// parseTS mirrors the jq `(.ts | sub("\\+.*";"Z") | fromdateiso8601)`:
// strip any +offset suffix, treat the remainder as UTC, then fall
// back to Go's native RFC3339 parser for negative-offset and
// fractional-second cases that jq's fromdateiso8601 also accepts.
// Returns false on malformed input.
func parseTS(ts string) (int64, bool) {
	stripped := ts
	if i := strings.Index(stripped, "+"); i >= 0 {
		stripped = stripped[:i] + "Z"
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05.000000Z",
	} {
		if t, err := time.Parse(layout, stripped); err == nil {
			return t.Unix(), true
		}
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
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

func topSlugsByEpoch(rows []jsonRow, threshold int64, n, minCount int) []SlugCount {
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
	return topN(counts, n, minCount)
}

func wakeErrorSlugs(rows []jsonRow, threshold int64, minCount int) []SlugCount {
	counts := map[string]int{}
	for _, r := range rows {
		if r.str("status") != "wake-error" {
			continue
		}
		ts := r.str("ts")
		ep, ok := parseTS(ts)
		if !ok || ep < threshold {
			continue
		}
		src := r.str("source")
		if src == "" {
			continue
		}
		counts[src]++
	}
	// Bash uses no top-N cap on wake errors, only the minCount filter.
	return topN(counts, len(counts), minCount)
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

type stuckRow struct {
	Slug     string
	Status   string
	Agent    string
	IdleSecs int64
}

func stuckRowerLines(path string, stuckSecs int64) []stuckRow {
	rows := loadJSONL(path)
	var out []stuckRow
	for _, r := range rows {
		idle, ok := r.num("idle_secs")
		stuck := r.bool_("stuck")
		if !stuck && !(ok && int64(idle) > stuckSecs) {
			continue
		}
		out = append(out, stuckRow{
			Slug:     r.str("slug"),
			Status:   r.str("status"),
			Agent:    r.str("agent"),
			IdleSecs: int64(idle),
		})
	}
	return out
}

type blockedTask struct {
	Slug string
	Root string
}

func blockedWithoutTrigger(cfg Config) []blockedTask {
	var roots []string
	if cfg.ProjectRoots != nil {
		roots = cfg.ProjectRoots()
	} else {
		roots = discoverProjectRoots(cfg.WtCfg)
	}
	var out []blockedTask
	for _, root := range roots {
		tasksDir := filepath.Join(root, "tasks")
		entries, err := os.ReadDir(tasksDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(tasksDir, e.Name())
			fm := readFrontmatter(path)
			if fm["status"] != "blocked" {
				continue
			}
			if fm["trigger"] != "" || fm["needs"] != "" || fm["scheduler"] != "" {
				continue
			}
			slug := strings.TrimSuffix(e.Name(), ".md")
			out = append(out, blockedTask{Slug: slug, Root: root})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root != out[j].Root {
			return out[i].Root < out[j].Root
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

func discoverProjectRoots(wtCfg string) []string {
	projectsFile := filepath.Join(wtCfg, "projects")
	if body, err := os.ReadFile(projectsFile); err == nil {
		var roots []string
		for _, line := range strings.Split(string(body), "\n") {
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			info, err := os.Stat(filepath.Join(line, "tasks"))
			if err != nil || !info.IsDir() {
				continue
			}
			roots = append(roots, line)
		}
		return roots
	}
	out, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return nil
	}
	gitCommon := strings.TrimSpace(string(out))
	if gitCommon == "" {
		return nil
	}
	root, err := filepath.Abs(filepath.Join(gitCommon, ".."))
	if err != nil {
		return nil
	}
	if info, err := os.Stat(filepath.Join(root, "tasks")); err == nil && info.IsDir() {
		return []string{root}
	}
	return nil
}

var fmFenceRE = regexp.MustCompile(`(?m)^---[ \t]*$`)

// readFrontmatter parses the YAML frontmatter at the top of path and
// returns a flat key -> trimmed-value map. The bash equivalent uses
// awk to read the first "key: value" line per key inside the first
// `---`-fenced block, so this returns the first value for each key.
func readFrontmatter(path string) map[string]string {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	lines := strings.Split(string(body), "\n")
	inFM := false
	for _, line := range lines {
		if fmFenceRE.MatchString(line) {
			if inFM {
				return out
			}
			inFM = true
			continue
		}
		if !inFM {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		key := line[:i]
		val := strings.TrimRight(strings.TrimLeft(line[i+1:], " \t"), " \t")
		if _, exists := out[key]; !exists {
			out[key] = val
		}
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
		var out []byte
		out, err = exec.Command(cfg.WtTaskBin, "fleet", "status").Output()
		raw = string(out)
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

// formatTierBreakdown renders the tier map as the bash does:
// `tier:count tier:count` joined by single spaces, sorted by tier
// name for stable output (the bash relies on `sort | uniq -c | awk`,
// which yields tier-sorted output).
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
