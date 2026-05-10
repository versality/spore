// Package liveness probes opencode rowers for stuckness.
//
// A rower is "stuck" when both signals fire:
//   - last assistant message in opencode's SQLite for this worktree's
//     sessions is older than StuckSeconds (default 10 min), AND
//   - wt/<slug> has zero commits beyond merge-base with main.
//
// Mid-stream rowers (any-message touched within GraceSeconds, default
// 60s) are excluded so a slow ollama turn isn't called dead.
//
// The probe is split into a pure Probe(...) function over an injected
// DB and Git interface; Run wires up the real opencode db CLI plus
// the local git binary.
package liveness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	DefaultGraceSeconds = 60
	DefaultStuckSeconds = 600
)

// Config tunes the probe thresholds. Zero values pick defaults.
type Config struct {
	GraceSeconds int
	StuckSeconds int
}

func (c Config) defaults() Config {
	if c.GraceSeconds <= 0 {
		c.GraceSeconds = DefaultGraceSeconds
	}
	if c.StuckSeconds <= 0 {
		c.StuckSeconds = DefaultStuckSeconds
	}
	return c
}

// SessionStats is the per-rower view of opencode's SQLite for one
// worktree directory. All timestamps are unix milliseconds (matching
// opencode's `time_updated` column); zero means "no row matched".
type SessionStats struct {
	LatestSession    string
	SessionCount     int
	MessagesInLatest int
	AnyMs            int64 // newest message of any role
	AsstMs           int64 // newest assistant message
}

// DB is the data source the probe queries for opencode session
// activity. Implementations may shell out to `opencode db` or fake
// the rows in a test.
type DB interface {
	// Available reports whether the opencode SQLite is present.
	// When false the probe short-circuits to "no rowers ever ran".
	Available() bool
	// Stats returns the session aggregate for wtDir or
	// (zero-value, nil) if no sessions match.
	Stats(wtDir string) (SessionStats, error)
}

// Git is the branch-progress probe; it returns the number of commits
// on wt/<slug> beyond merge-base with main. Missing branch / failed
// command -> 0 (treated as no progress).
type Git interface {
	CommitsAhead(slug string) int
}

// RowerStatus is the per-rower verdict.
type RowerStatus struct {
	Slug             string `json:"slug"`
	Stuck            bool   `json:"stuck"`
	IdleSeconds      int64  `json:"idle_seconds"`
	Reason           string `json:"reason"`
	Session          string `json:"session"`
	SessionsTotal    int    `json:"sessions_total"`
	MessagesInLatest int    `json:"messages_in_latest"`
}

// Report is the aggregate result.
type Report struct {
	Stuck    []RowerStatus `json:"stuck"`
	OkCount  int           `json:"ok_count"`
	Total    int           `json:"total"`
	Note     string        `json:"note,omitempty"`
	DBAbsent bool          `json:"-"`
}

// Probe is the pure verdict for one rower at a fixed wall-clock
// `now`. Returns (status, ok-flag); when okFlag is true the rower is
// counted under OkCount and Stuck is false.
func Probe(now time.Time, cfg Config, slug string, stats SessionStats, commitsAhead int) RowerStatus {
	cfg = cfg.defaults()
	nowS := now.Unix()
	rs := RowerStatus{
		Slug:             slug,
		Session:          stats.LatestSession,
		SessionsTotal:    stats.SessionCount,
		MessagesInLatest: stats.MessagesInLatest,
	}
	if rs.Session == "" {
		rs.Session = "(none)"
	}

	// Mid-stream grace: any message activity inside GraceSeconds.
	if stats.AnyMs > 0 && nowS-stats.AnyMs/1000 < int64(cfg.GraceSeconds) {
		rs.Stuck = false
		return rs
	}

	var idle int64
	if stats.AsstMs > 0 {
		idle = nowS - stats.AsstMs/1000
	} else {
		// No assistant turn ever -> idle since epoch.
		idle = nowS
	}
	rs.IdleSeconds = idle

	if idle > int64(cfg.StuckSeconds) && commitsAhead == 0 {
		rs.Stuck = true
		rs.Reason = fmt.Sprintf("last_assistant=%s ago, no commits since brief", FormatDuration(idle))
	}
	return rs
}

// ListRowers walks tasksDir and returns slugs that are (status=active,
// agent=opencode) AND have a worktree under mainRoot/.worktrees/<slug>
// whose .wt/agent file says "opencode".
func ListRowers(mainRoot string) ([]string, error) {
	tasksDir := filepath.Join(mainRoot, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		if name == "README.md" {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		raw, err := os.ReadFile(filepath.Join(tasksDir, name))
		if err != nil {
			continue
		}
		m, _, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if m.Status != "active" || m.Agent != "opencode" {
			continue
		}

		wtDir := filepath.Join(mainRoot, ".worktrees", slug)
		if !isDir(wtDir) {
			continue
		}
		agentFile := filepath.Join(wtDir, ".wt", "agent")
		body, err := os.ReadFile(agentFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(body)) != "opencode" {
			continue
		}
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs, nil
}

// Run is the high-level driver: list rowers, probe each, aggregate.
// db.Available() == false short-circuits to a zero report with note
// "db-missing".
func Run(now time.Time, cfg Config, mainRoot string, db DB, git Git) (Report, error) {
	if !db.Available() {
		return Report{Note: "db-missing", DBAbsent: true}, nil
	}
	slugs, err := ListRowers(mainRoot)
	if err != nil {
		return Report{}, err
	}
	rep := Report{}
	for _, slug := range slugs {
		wtDir := filepath.Join(mainRoot, ".worktrees", slug)
		stats, err := db.Stats(wtDir)
		if err != nil {
			return rep, fmt.Errorf("opencode db stats %s: %w", slug, err)
		}
		ahead := git.CommitsAhead(slug)
		rep.Total++
		st := Probe(now, cfg, slug, stats, ahead)
		if st.Stuck {
			rep.Stuck = append(rep.Stuck, st)
		} else {
			rep.OkCount++
		}
	}
	return rep, nil
}

// FormatDuration mirrors the original shell fmt_dur helper.
func FormatDuration(s int64) string {
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
}

// FormatText writes the human summary the original script emitted
// (one summary line plus an optional bullet list of stuck slugs).
func FormatText(w io.Writer, rep Report) {
	if rep.DBAbsent {
		fmt.Fprintln(w, "ok: opencode db not present (no rowers ever ran)")
		return
	}
	if rep.Total == 0 {
		fmt.Fprintln(w, "ok: no opencode rowers")
		return
	}
	if len(rep.Stuck) == 0 {
		if rep.Total == 1 {
			fmt.Fprintln(w, "ok: 1 opencode rower, making progress")
		} else {
			fmt.Fprintf(w, "ok: %d opencode rowers, all making progress\n", rep.Total)
		}
		return
	}
	var oldest int64
	for _, s := range rep.Stuck {
		if s.IdleSeconds > oldest {
			oldest = s.IdleSeconds
		}
	}
	oldestHuman := FormatDuration(oldest)
	if len(rep.Stuck) == 1 {
		fmt.Fprintf(w, "stuck: 1 opencode rower idle (no progress in %s)\n", oldestHuman)
	} else {
		fmt.Fprintf(w, "stuck: %d opencode rowers idle (oldest %s)\n", len(rep.Stuck), oldestHuman)
	}
	for _, s := range rep.Stuck {
		suggest := "poke or abandon"
		if oldest > 1800 {
			suggest = "abandon"
		}
		fmt.Fprintf(w, "- %s: %s, session=%s (%d msgs, %d sessions), suggest: %s\n",
			s.Slug, s.Reason, s.Session, s.MessagesInLatest, s.SessionsTotal, suggest)
	}
}

// FormatJSON writes the JSON shape the original script's --json mode
// produced.
func FormatJSON(w io.Writer, rep Report) error {
	type stuckOut struct {
		Slug             string `json:"slug"`
		IdleSeconds      int64  `json:"idle_seconds"`
		Reason           string `json:"reason"`
		Session          string `json:"session"`
		SessionsTotal    int    `json:"sessions_total"`
		MessagesInLatest int    `json:"messages_in_latest"`
	}
	type out struct {
		Stuck   []stuckOut `json:"stuck"`
		OkCount int        `json:"ok_count"`
		Total   int        `json:"total"`
		Note    string     `json:"note,omitempty"`
	}
	o := out{Stuck: []stuckOut{}, OkCount: rep.OkCount, Total: rep.Total, Note: rep.Note}
	for _, s := range rep.Stuck {
		o.Stuck = append(o.Stuck, stuckOut{
			Slug:             s.Slug,
			IdleSeconds:      s.IdleSeconds,
			Reason:           s.Reason,
			Session:          s.Session,
			SessionsTotal:    s.SessionsTotal,
			MessagesInLatest: s.MessagesInLatest,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}

// OpencodeDB shells out to the real `opencode db` CLI. Bin defaults
// to "opencode".
type OpencodeDB struct {
	Bin     string
	DBPath  string // resolved path or "" when unknown
	checked bool
}

// NewOpencodeDB resolves the SQLite path via `opencode db path` and
// returns a ready-to-use DB.
func NewOpencodeDB(bin string) *OpencodeDB {
	if bin == "" {
		bin = "opencode"
	}
	d := &OpencodeDB{Bin: bin}
	out, err := exec.Command(bin, "db", "path").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			path := strings.TrimSpace(lines[len(lines)-1])
			if _, err := os.Stat(path); err == nil {
				d.DBPath = path
			}
		}
	}
	d.checked = true
	return d
}

func (d *OpencodeDB) Available() bool {
	return d.DBPath != ""
}

func (d *OpencodeDB) Stats(wtDir string) (SessionStats, error) {
	dirLit := sqlLit(wtDir)
	q := `SELECT
		(SELECT slug FROM session WHERE directory = ` + dirLit + ` ORDER BY time_updated DESC LIMIT 1) AS latest_session,
		(SELECT COUNT(*) FROM session WHERE directory = ` + dirLit + `) AS session_count,
		(SELECT COUNT(*) FROM message WHERE session_id =
		  (SELECT id FROM session WHERE directory = ` + dirLit + ` ORDER BY time_updated DESC LIMIT 1)) AS latest_msgs,
		(SELECT COALESCE(MAX(m.time_updated), 0)
		 FROM message m JOIN session s ON s.id = m.session_id
		 WHERE s.directory = ` + dirLit + `) AS any_ms,
		(SELECT COALESCE(MAX(m.time_updated), 0)
		 FROM message m JOIN session s ON s.id = m.session_id
		 WHERE s.directory = ` + dirLit + `
		   AND json_extract(m.data, '$.role') = 'assistant') AS asst_ms`
	cmd := exec.Command(d.Bin, "db", "--format", "tsv", q)
	out, err := cmd.Output()
	if err != nil {
		return SessionStats{}, err
	}
	return parseStatsTSV(out)
}

func parseStatsTSV(buf []byte) (SessionStats, error) {
	scanner := bufio.NewScanner(bytes.NewReader(buf))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) < 2 {
		return SessionStats{}, nil
	}
	cols := strings.Split(lines[1], "\t")
	if len(cols) < 5 {
		return SessionStats{}, nil
	}
	st := SessionStats{LatestSession: cols[0]}
	st.SessionCount = atoiSafe(cols[1])
	st.MessagesInLatest = atoiSafe(cols[2])
	st.AnyMs = atoi64Safe(cols[3])
	if len(cols) >= 5 {
		st.AsstMs = atoi64Safe(cols[4])
	}
	return st, nil
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atoi64Safe(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func sqlLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// LocalGit runs `git rev-list --count main..wt/<slug>` from MainRoot.
type LocalGit struct {
	MainRoot string
}

func (g LocalGit) CommitsAhead(slug string) int {
	cmd := exec.Command("git", "-C", g.MainRoot, "rev-list", "--count", "main..wt/"+slug)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return atoiSafe(strings.TrimSpace(string(out)))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
