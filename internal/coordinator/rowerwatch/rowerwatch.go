// Package rowerwatch surfaces rower-state transitions to the next
// coordinator turn. It is the Stop-hook helper sibling of
// tokenmonitor: silent on no transition, otherwise emits a one-line
// stderr reminder per transition (APPEARED / DISAPPEARED / STUCK /
// UNSTUCK / HEAD-MOVED) and asks the caller to exit 2.
//
// Self-gates by SKYBOT_INBOX vs SKYHELM_STATE_DIR so the binary is
// safe as a global Stop hook (no-op for any non-coordinator session).
//
// State at $SKYHELM_ROWER_WATCH_FILE is NDJSON (one rower per line,
// schema {"slug","status","branch","head_sha","agent","idle_secs",
// "stuck","flap","last_seen"}), atomic-rename on write.
package rowerwatch

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

const (
	DefaultStuckOpencode = 600
	DefaultStuckClaude   = 900
	DefaultDebounceFlaps = 2
)

// Config is the runtime knobs. Defaults() fills zero-value fields
// from environment and stdlib defaults.
type Config struct {
	StateDir      string
	StateFile     string
	Inbox         string
	ProjectsFile  string
	MainRoot      string
	StuckOpencode int
	StuckClaude   int
	DebounceFlaps int
	HeadMovedOn   bool
	Now           func() time.Time
	Probe         Probe
}

// Result is the verdict from Watch. Skipped means the inbox gate
// fired; Transitions is the (possibly empty) ordered list of stderr
// reminder lines. Caller exits 2 iff Transitions is non-empty.
type Result struct {
	Skipped     bool
	Transitions []string
}

func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.StateFile == "" {
		c.StateFile = os.Getenv("SKYHELM_ROWER_WATCH_FILE")
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(c.StateDir, "rower-watch.json")
	}
	if c.Inbox == "" {
		c.Inbox = os.Getenv("SKYBOT_INBOX")
	}
	if c.ProjectsFile == "" {
		if v := os.Getenv("WT_CFG"); v != "" {
			c.ProjectsFile = filepath.Join(v, "projects")
		} else if home, _ := os.UserHomeDir(); home != "" {
			c.ProjectsFile = filepath.Join(home, ".config", "wt", "projects")
		}
	}
	if c.StuckOpencode == 0 {
		c.StuckOpencode = envInt("WATCH_STUCK_OPENCODE_SECS", DefaultStuckOpencode)
	}
	if c.StuckClaude == 0 {
		c.StuckClaude = envInt("WATCH_STUCK_CLAUDE_SECS", DefaultStuckClaude)
	}
	if c.DebounceFlaps == 0 {
		c.DebounceFlaps = envInt("WATCH_DEBOUNCE_FLAPS", DefaultDebounceFlaps)
	}
	if !c.HeadMovedOn {
		if v := os.Getenv("WATCH_HEAD_MOVED"); v != "" && v != "0" {
			c.HeadMovedOn = true
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Probe == nil {
		c.Probe = DefaultProbe{}
	}
	return c
}

func (c Config) IsCoordinator() bool {
	if c.Inbox == "" {
		return false
	}
	root := strings.TrimRight(c.StateDir, "/")
	return c.Inbox == root || strings.HasPrefix(c.Inbox, root+"/")
}

// Watch runs one observation pass.
func Watch(cfg Config) Result {
	cfg = cfg.Defaults()
	if !cfg.IsCoordinator() {
		return Result{Skipped: true}
	}

	mainRoot := cfg.MainRoot
	if mainRoot == "" {
		mainRoot = cfg.Probe.GitMainRoot()
	}
	if mainRoot == "" {
		return Result{}
	}

	projectRoots := readProjectRoots(cfg.ProjectsFile)
	if len(projectRoots) == 0 {
		projectRoots = []string{mainRoot}
	}

	rootByProject := make(map[string]string, len(projectRoots))
	for _, p := range projectRoots {
		rootByProject[projectName(p)] = p
	}

	now := cfg.Now()
	prior, _ := readState(cfg.StateFile)
	current := observeAll(cfg, projectRoots, now)
	transitions := computeTransitions(cfg, prior, current, rootByProject, mainRoot)

	_ = writeState(cfg.StateFile, cfg.StateDir, current, now)

	return Result{Transitions: transitions}
}

// Format renders the transition block exactly as the bash script
// emits it.
func (r Result) Format() string {
	if len(r.Transitions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SKYHELM ROWER WATCH:\n")
	for _, t := range r.Transitions {
		b.WriteString("  ")
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return b.String()
}

// rower captures the in-memory view of one task. slug is the
// possibly project-prefixed display key; baseSlug is the on-disk
// file basename (no prefix). newStuck/newFlap are the debounced
// values writeState persists.
type rower struct {
	slug     string
	baseSlug string
	status   string
	headSHA  string
	agent    string
	idleSecs int
	stuckRaw bool
	newStuck bool
	newFlap  int
}

func observeAll(cfg Config, projectRoots []string, now time.Time) []*rower {
	multiProject := len(projectRoots) > 1
	fleetOut := cfg.Probe.FleetStatus()

	var out []*rower
	for _, projectRoot := range projectRoots {
		tasksDir := filepath.Join(projectRoot, "tasks")
		entries, _ := filepath.Glob(filepath.Join(tasksDir, "*.md"))
		sort.Strings(entries)
		projectN := projectName(projectRoot)
		for _, taskFile := range entries {
			base := strings.TrimSuffix(filepath.Base(taskFile), ".md")
			if base == "README" {
				continue
			}
			meta, ok := readFrontmatter(taskFile)
			if !ok || meta.Status != "active" {
				continue
			}
			agent := meta.Agent
			if agent == "" {
				agent = "claude"
			}
			displaySlug := base
			if multiProject {
				displaySlug = projectN + "/" + base
			}
			wtDir := filepath.Join(projectRoot, ".worktrees", base)
			headSHA := ""
			if dirExists(wtDir) {
				headSHA = cfg.Probe.GitHeadSHA(projectRoot, "wt/"+base)
			}
			idle := -1
			stuckRaw := false
			if dirExists(wtDir) {
				switch agent {
				case "opencode":
					if v, okp := cfg.Probe.OpencodeIdleSecs(wtDir, now); okp {
						idle = v
						if idle > cfg.StuckOpencode {
							stuckRaw = true
						}
					}
				default:
					if v, okp := cfg.Probe.ClaudeIdleSecs(wtDir, now); okp {
						idle = v
						if idle > cfg.StuckClaude {
							stuckRaw = true
						}
					}
				}
			}
			line := fleetRuntimeLine(fleetOut, base)
			if strings.Contains(line, "-running: ") || strings.Contains(line, "-idle-wake-pending: ") {
				stuckRaw = false
			}
			out = append(out, &rower{
				slug:     displaySlug,
				baseSlug: base,
				status:   meta.Status,
				headSHA:  headSHA,
				agent:    agent,
				idleSecs: idle,
				stuckRaw: stuckRaw,
			})
		}
	}
	return out
}

func computeTransitions(cfg Config, prior map[string]Entry, current []*rower,
	rootByProject map[string]string, mainRoot string) []string {

	var lines []string
	currentBySlug := make(map[string]*rower, len(current))

	for _, r := range current {
		currentBySlug[r.slug] = r
		p, seen := prior[r.slug]
		if !seen {
			parts := r.status + ", " + r.agent
			if r.idleSecs >= 0 {
				parts += ", idle=" + fmtDur(r.idleSecs)
			}
			lines = append(lines, "APPEARED "+r.slug+" ("+parts+")")
			r.newStuck = false
			r.newFlap = 0
			continue
		}

		stuckNow := r.stuckRaw
		priorStuck := p.Stuck
		priorFlap := p.Flap

		var ns bool
		var nf int
		if stuckNow == priorStuck {
			ns = priorStuck
			nf = 0
		} else {
			nf = priorFlap + 1
			if nf >= cfg.DebounceFlaps {
				ns = stuckNow
				nf = 0
				idleH := fmtDur(r.idleSecs)
				if ns {
					lines = append(lines, "STUCK "+r.slug+" ("+r.agent+", idle="+idleH+")")
				} else {
					lines = append(lines, "UNSTUCK "+r.slug+" ("+r.agent+", idle="+idleH+")")
				}
			} else {
				ns = priorStuck
			}
		}
		r.newStuck = ns
		r.newFlap = nf

		if cfg.HeadMovedOn && r.headSHA != "" && p.HeadSHA != "" &&
			r.headSHA != p.HeadSHA && r.status == p.Status {
			lines = append(lines, "HEAD-MOVED "+r.slug+" ("+p.HeadSHA+" -> "+r.headSHA+")")
		}
	}

	var gone []string
	for slug := range prior {
		if _, here := currentBySlug[slug]; !here {
			gone = append(gone, slug)
		}
	}
	sort.Strings(gone)
	for _, slug := range gone {
		baseSlug := slug
		projectRoot := mainRoot
		if i := strings.Index(slug, "/"); i >= 0 {
			projectRoot = rootByProject[slug[:i]]
			if projectRoot == "" {
				projectRoot = mainRoot
			}
			baseSlug = slug[i+1:]
		}
		newFile := filepath.Join(projectRoot, "tasks", baseSlug+".md")
		newStatus := "missing"
		if data, err := os.ReadFile(newFile); err == nil {
			if m, _, err := frontmatter.Parse(data); err == nil && m.Status != "" {
				newStatus = m.Status
			} else {
				newStatus = "?"
			}
		}
		verdict := ""
		if newStatus == "done" {
			verdict = cfg.Probe.DoneVerdict(baseSlug)
		}
		line := "DISAPPEARED " + slug + " (" + prior[slug].Status + " -> " + newStatus
		if verdict != "" {
			line += ", verdict=" + verdict
		}
		line += ")"
		lines = append(lines, line)
	}

	return lines
}

func fmtDur(s int) string {
	if s < 60 {
		return strconv.Itoa(s) + "s"
	}
	if s < 3600 {
		return strconv.Itoa(s/60) + "m"
	}
	return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
}

// fleetRuntimeLine returns the first fleet-status line that names
// baseSlug, matching the bash awk: contains ": <slug> " or ": <slug> (".
func fleetRuntimeLine(fleetOut, baseSlug string) string {
	scan := bufio.NewScanner(strings.NewReader(fleetOut))
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	needleA := ": " + baseSlug + " "
	needleB := ": " + baseSlug + " ("
	for scan.Scan() {
		ln := scan.Text()
		if strings.Contains(ln, needleA) || strings.Contains(ln, needleB) {
			return ln
		}
	}
	return ""
}

func readProjectRoots(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !dirExists(filepath.Join(line, "tasks")) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func projectName(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return filepath.Base(path)
}

func readFrontmatter(path string) (frontmatter.Meta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return frontmatter.Meta{}, false
	}
	m, _, err := frontmatter.Parse(data)
	if err != nil {
		return frontmatter.Meta{}, false
	}
	return m, true
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
