// Package fleetstop implements the opencode fleet-stop kill switch.
// It pauses every active opencode worker on the local host via
// `wt task pause <slug>`, then sweeps any orphan `opencode` process.
// Idempotent: zero workers + zero orphans -> exit 0 with a "none"
// summary; partial failures still kill orphans and report in the
// summary line.
//
// Used as a coordinator-callable kill switch when ollama serializes
// multiple opencode workers and completion latency climbs.
package fleetstop

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

// PostKillDelay is the beat we give pause/kill before scanning for
// orphan opencode processes. Exposed so tests can shrink it.
var PostKillDelay = 2 * time.Second

// Config is the fleet-stop runtime config. The zero value picks
// sensible defaults from the host environment.
type Config struct {
	// MainRoot is the canonical project root that owns tasks/.
	// Empty means resolve from the script-relative path used by
	// the original shell version (caller passes it explicitly here).
	MainRoot string
	// Host is the local host short-name; tasks with empty or
	// matching host: are in scope. Empty means $HOSTNAME-short.
	Host string
	// User is the orphan-sweep user filter; empty means $USER.
	User string
	// Pause runs `wt task pause <slug>`. Defaults to exec.
	Pause func(slug string) error
	// KillSession nukes a tmux session by name. Defaults to exec.
	KillSession func(session string) error
	// SessionName resolves a slug to a tmux session name (mirrors
	// `wt session-name <slug>`). Defaults to exec.
	SessionName func(root, slug string) (string, error)
	// FindOrphans returns PIDs of `opencode` processes belonging
	// to User. Defaults to pgrep.
	FindOrphans func(user string) ([]int, error)
	// Kill sends SIGTERM to a pid. Defaults to (*os.Process).Signal.
	Kill func(pid int) error
}

// Result is the structured outcome of a fleet-stop pass.
type Result struct {
	Active  []string // slugs we considered active
	Paused  []string // slugs whose `wt task pause` succeeded
	Killed  int      // orphan pids successfully signalled
	Orphans int      // orphan pids found (Killed <= Orphans)
}

// Summary returns the one-line summary the original shell script
// printed.
func (r Result) Summary() string {
	clause := "(none)"
	if len(r.Paused) > 0 {
		clause = "(slugs: " + strings.Join(r.Paused, ",") + ")"
	}
	return fmt.Sprintf("opencode-fleet-stop: paused %d workers %s killed %d orphan procs",
		len(r.Paused), clause, r.Killed)
}

// Run executes one fleet-stop pass and returns its Result.
func Run(cfg Config) (Result, error) {
	cfg = cfg.defaults()
	res := Result{}

	tasksDir := filepath.Join(cfg.MainRoot, "tasks")
	slugs, err := ListActiveSlugs(tasksDir, cfg.Host)
	if err != nil {
		return res, err
	}
	res.Active = slugs

	for _, slug := range slugs {
		if err := cfg.Pause(slug); err == nil {
			res.Paused = append(res.Paused, slug)
			continue
		}
		// Pause refused (likely unread inbox). Direct session
		// kill is the hatch's hatch.
		session, err := cfg.SessionName(cfg.MainRoot, slug)
		if err != nil || session == "" {
			continue
		}
		_ = cfg.KillSession(session)
	}

	if len(slugs) > 0 {
		time.Sleep(PostKillDelay)
	}

	pids, err := cfg.FindOrphans(cfg.User)
	if err != nil {
		return res, err
	}
	res.Orphans = len(pids)
	for _, pid := range pids {
		if err := cfg.Kill(pid); err == nil {
			res.Killed++
		}
	}
	return res, nil
}

// ListActiveSlugs scans tasksDir for active opencode workers whose
// host matches localHost (or is empty). Returns slugs in
// directory-sorted order.
func ListActiveSlugs(tasksDir, localHost string) ([]string, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(tasksDir, e.Name())
		raw, err := os.ReadFile(path)
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
		if m.Host != "" && m.Host != localHost {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs, nil
}

func (c Config) defaults() Config {
	if c.Host == "" {
		c.Host = shortHostname()
	}
	if c.User == "" {
		c.User = os.Getenv("USER")
	}
	if c.Pause == nil {
		c.Pause = pauseExec
	}
	if c.SessionName == nil {
		c.SessionName = sessionNameExec
	}
	if c.KillSession == nil {
		c.KillSession = killSessionExec
	}
	if c.FindOrphans == nil {
		c.FindOrphans = pgrepOpencode
	}
	if c.Kill == nil {
		c.Kill = killPid
	}
	return c
}

func shortHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
}

func pauseExec(slug string) error {
	cmd := exec.Command("wt", "task", "pause", slug)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func sessionNameExec(root, slug string) (string, error) {
	cmd := exec.Command("wt", "session-name", slug)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func killSessionExec(session string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", session)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func pgrepOpencode(user string) ([]int, error) {
	args := []string{"-x", "opencode"}
	if user != "" {
		args = append([]string{"-u", user}, args...)
	}
	cmd := exec.Command("pgrep", args...)
	out, err := cmd.Output()
	if err != nil {
		// pgrep exits 1 on no matches; treat as empty.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func killPid(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
