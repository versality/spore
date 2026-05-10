// Package slascanner is the Go port of the bash skyhelm-sla-scanner.
// It walks state.md H2/H3 sections that aren't on the operator-owned
// ignore list and flags entries that are stale (no task pointer past
// the age threshold), orphaned (task slug not in the fleet), or done
// (task slug marked done either in the fleet or struck through).
//
// Findings format is deliberately kept byte-identical to the bash
// version (`done:` / `orphan:` / `stale:` lines) because wider tooling
// already pattern-matches on it.
package slascanner

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultAgeSeconds = 7200
	DefaultTaskBin    = "wt"
)

// Config is the runtime configuration for Scan. Defaults() fills the
// zero-value fields from environment and stdlib defaults.
type Config struct {
	StateDir   string
	StateFile  string
	AgeSeconds int64
	WtTaskBin  string
	Verbose    bool
	// Now overrides time.Now for tests; nil means use real wall clock.
	Now func() time.Time
	// TaskList overrides the call to `wt task ls`. Returns the raw
	// stdout. nil means shell out for real.
	TaskList func() (string, error)
}

// Finding is one SLA breach. Kind is "done", "orphan", or "stale".
type Finding struct {
	Kind string
	Line string
}

// Result is the verdict from Scan.
type Result struct {
	Findings []Finding
	// Trace is the per-entry verbose log (only populated when Verbose
	// is true). Each line matches the bash `--verbose` table.
	Trace []string
	// MissingState is true when StateFile does not exist; in that case
	// Findings is empty and the wrapper exits 0.
	MissingState bool
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
	if c.AgeSeconds == 0 {
		c.AgeSeconds = parseInt64Env("SKYHELM_SLA_AGE_SECONDS", DefaultAgeSeconds)
	}
	if c.WtTaskBin == "" {
		if v := os.Getenv("WT_TASK_BIN"); v != "" {
			c.WtTaskBin = v
		} else {
			c.WtTaskBin = DefaultTaskBin
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

func parseInt64Env(name string, fallback int64) int64 {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

// Scan reads the state file, classifies open entries, and returns the
// breach list. Missing state file is not an error: Result.MissingState
// is set and the caller can short-circuit to exit 0.
func Scan(cfg Config) (Result, error) {
	cfg = cfg.Defaults()

	body, err := os.ReadFile(cfg.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{MissingState: true}, nil
		}
		return Result{}, err
	}

	tasks, err := loadTaskMap(cfg)
	if err != nil {
		return Result{}, err
	}

	now := cfg.Now()
	thresholdEpoch := now.Unix() - cfg.AgeSeconds
	threshold := time.Unix(thresholdEpoch, 0).Format("2006-01-02T15:04")

	return scanContent(string(body), tasks, threshold, now.Unix(), cfg.Verbose), nil
}

// loadTaskMap returns slug -> status. Empty map is fine; the bash
// version swallows errors from `wt task ls` too.
func loadTaskMap(cfg Config) (map[string]string, error) {
	var raw string
	if cfg.TaskList != nil {
		out, err := cfg.TaskList()
		if err != nil {
			return map[string]string{}, nil
		}
		raw = out
	} else {
		out, err := exec.Command(cfg.WtTaskBin, "task", "ls").Output()
		if err != nil {
			return map[string]string{}, nil
		}
		raw = string(out)
	}
	return parseTaskList(raw), nil
}

// parseTaskList mirrors the bash:
//
//	wt task ls 2>/dev/null | awk 'NR>1 && $1 != "" {print $1 "\t" $2}'
//
// First line is a header, remaining rows have whitespace-separated
// columns; first is slug, second is status.
func parseTaskList(raw string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		slug := fields[0]
		status := ""
		if len(fields) >= 2 {
			status = fields[1]
		}
		out[slug] = status
	}
	return out
}

type entry struct {
	section  string
	text     string
	slug     string
	ts       string
	doneMark bool
}

// scanContent ports the awk body. threshold is the YYYY-MM-DDTHH:MM
// stale cutoff; nowEpoch is now in seconds for age math; verbose
// controls Trace population.
func scanContent(content string, tasks map[string]string, threshold string, nowEpoch int64, verbose bool) Result {
	var (
		entries     []entry
		secLatest   = map[string]string{}
		curSection  string
		recent      bool
		skip        = true
		tableRow    int
		pending     entry
		havePending bool
	)

	flush := func() {
		if !havePending || pending.text == "" {
			havePending = false
			pending = entry{}
			return
		}
		entries = append(entries, pending)
		havePending = false
		pending = entry{}
	}

	setPending := func(e entry) {
		flush()
		pending = e
		havePending = true
	}

	for _, line := range strings.Split(content, "\n") {
		if isHeading(line) {
			flush()
			h := stripHeading(line)
			recent = false
			tableRow = 0
			if ignoredSection(h) {
				skip = true
			} else {
				skip = false
				curSection = h
				recent = (h == "Recent events")
			}
			continue
		}
		if skip {
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "|") {
			if !durableRecentEvent(line, recent) {
				continue
			}
			check := stripTableSeparator(line)
			if check == "" {
				continue
			}
			tableRow++
			if tableRow == 1 {
				continue
			}
			text := trimTableCells(line)
			col := firstTableCell(line)
			rawCol := col
			col = strings.ReplaceAll(col, "~", "")
			e := entry{section: curSection, text: text}
			if slugRE.MatchString(col) {
				e.slug = col
				if strings.HasPrefix(rawCol, "~~") {
					e.doneMark = true
				}
			}
			e.ts = scanTS(line)
			if e.ts != "" {
				if cur, ok := secLatest[curSection]; !ok || e.ts > cur {
					secLatest[curSection] = e.ts
				}
			}
			setPending(e)
			continue
		}

		if !durableRecentEvent(line, recent) {
			continue
		}
		e := entry{section: curSection, text: line}
		if i := strings.Index(line, "task:"); i >= 0 {
			s := strings.TrimLeft(line[i+5:], " \t")
			if m := slugStartRE.FindString(s); m != "" {
				e.slug = m
			}
		}
		if e.slug == "" {
			if m := bulletSlugRE.FindStringSubmatch(line); m != nil {
				e.slug = m[1]
			}
		}
		e.ts = scanTS(line)
		if e.ts != "" {
			if cur, ok := secLatest[curSection]; !ok || e.ts > cur {
				secLatest[curSection] = e.ts
			}
		}
		setPending(e)
	}
	flush()

	var res Result
	for _, e := range entries {
		ts := e.ts
		if ts == "" {
			ts = secLatest[e.section]
		}
		disp := truncateDisp(e.text)

		if e.slug != "" {
			st, known := tasks[e.slug]
			switch {
			case st == "done" || e.doneMark:
				res.Findings = append(res.Findings, Finding{
					Kind: "done",
					Line: fmt.Sprintf("done: %s (task %s, remove)", disp, e.slug),
				})
				if verbose {
					res.Trace = append(res.Trace, fmt.Sprintf("%-8s [%s] %s", "done", e.slug, disp))
				}
			case !known || st == "":
				res.Findings = append(res.Findings, Finding{
					Kind: "orphan",
					Line: fmt.Sprintf("orphan: %s (task %s not in fleet)", disp, e.slug),
				})
				if verbose {
					res.Trace = append(res.Trace, fmt.Sprintf("%-8s [%s] %s", "orphan", e.slug, disp))
				}
			default:
				if verbose {
					res.Trace = append(res.Trace, fmt.Sprintf("%-8s [%s] %s", "ok", e.slug, disp))
				}
			}
			continue
		}

		if ts == "" || ts < threshold {
			ageStr := "no timestamp"
			if ts != "" {
				ep := tsToEpoch(ts)
				ageH := (nowEpoch - ep) / 3600
				ageStr = fmt.Sprintf("age %dh", ageH)
			}
			res.Findings = append(res.Findings, Finding{
				Kind: "stale",
				Line: fmt.Sprintf("stale: %s (no task, %s)", disp, ageStr),
			})
			if verbose {
				res.Trace = append(res.Trace, fmt.Sprintf("%-8s %s", "stale", disp))
			}
		} else if verbose {
			res.Trace = append(res.Trace, fmt.Sprintf("%-8s %s", "fresh", disp))
		}
	}
	return res
}

var (
	headingRE     = regexp.MustCompile(`^#{2,}\s+`)
	slugRE        = regexp.MustCompile(`^[a-z0-9][-a-z0-9]*$`)
	slugStartRE   = regexp.MustCompile(`^[a-z0-9][-a-z0-9]*`)
	bulletSlugRE  = regexp.MustCompile(`^-\s+([a-z0-9][-a-z0-9]*):`)
	tsRE          = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9]{2}:[0-9]{2})?`)
	directiveRE   = regexp.MustCompile(`(?i)operator (correction|directive|decision|explicit)`)
	decisionRE    = regexp.MustCompile(`(?i)decision:`)
	imperativeRE  = regexp.MustCompile(`(?i)(do not|must|should|require|requires|ban|forbid|future)`)
	separatorOnly = regexp.MustCompile(`^[-:]*$`)
)

func isHeading(line string) bool {
	return headingRE.MatchString(line)
}

func stripHeading(line string) string {
	out := headingRE.ReplaceAllString(line, "")
	return strings.TrimRight(out, " \t")
}

func ignoredSection(header string) bool {
	if strings.HasPrefix(header, "Active tasks") {
		rest := strings.TrimPrefix(header, "Active tasks")
		if rest == "" || strings.HasPrefix(rest, " ") {
			return true
		}
	}
	switch header {
	case "Operator ingress ledger",
		"Rules",
		"Operator-action-pending",
		"Roadmap",
		"Blocked",
		"Operator-bound",
		"Remote-host tasks",
		"Remote-project tasks":
		return true
	}
	return strings.Contains(strings.ToLower(header), "directives")
}

func durableRecentEvent(line string, recent bool) bool {
	if !recent {
		return true
	}
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "operator") {
		return false
	}
	if directiveRE.MatchString(lower) {
		return true
	}
	if decisionRE.MatchString(lower) {
		return true
	}
	if imperativeRE.MatchString(lower) {
		return true
	}
	return false
}

// scanTS returns the lexically greatest YYYY-MM-DD[THH:MM] match in
// the line, matching the awk loop that walks every match and keeps
// the largest.
func scanTS(line string) string {
	matches := tsRE.FindAllString(line, -1)
	best := ""
	for _, t := range matches {
		if t > best {
			best = t
		}
	}
	return best
}

// tsToEpoch parses YYYY-MM-DD or YYYY-MM-DDTHH:MM as local time, the
// way awk's mktime() does for the bash port.
func tsToEpoch(ts string) int64 {
	layouts := []string{"2006-01-02T15:04", "2006-01-02"}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, ts, time.Local); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// stripTableSeparator returns the row's content with whitespace and
// pipes squeezed out, then yields the empty string when only `-` and
// `:` characters remain (i.e. the markdown table header separator).
func stripTableSeparator(line string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '|':
			return -1
		}
		return r
	}, line)
	if separatorOnly.MatchString(cleaned) {
		return ""
	}
	return cleaned
}

// trimTableCells strips the leading `| ` and trailing ` |` so the
// remainder is the row body (cells joined by ` | `).
func trimTableCells(line string) string {
	out := strings.TrimPrefix(line, "|")
	out = strings.TrimLeft(out, " \t")
	if i := strings.LastIndex(out, "|"); i >= 0 {
		// Drop only the trailing pipe, mirroring `sub(/[[:space:]]*\|[[:space:]]*$/, "", e_text)`.
		trail := out[i+1:]
		if strings.TrimSpace(trail) == "" {
			out = strings.TrimRight(out[:i], " \t")
		}
	}
	return out
}

// firstTableCell returns the first cell of the row without trimming
// `~` markers; awk version: strips leading `| ` then everything from
// the next `|` onward.
func firstTableCell(line string) string {
	out := strings.TrimPrefix(line, "|")
	out = strings.TrimLeft(out, " \t")
	if i := strings.Index(out, "|"); i >= 0 {
		out = out[:i]
	}
	return strings.TrimRight(out, " \t")
}

func truncateDisp(s string) string {
	if len(s) <= 70 {
		return s
	}
	return s[:67] + "..."
}

// FormatFindings renders findings one per line, no trailing newline
// gymnastics: matches the bash `for (i=1;i<=n_bad;i++) print bad[i]`.
func FormatFindings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.Line)
		b.WriteByte('\n')
	}
	return b.String()
}
