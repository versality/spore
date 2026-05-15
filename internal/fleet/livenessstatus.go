package fleet

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
)

type workerRuntime struct {
	slug, agent, session, state, detail, projectRoot string
	unread                                           int
	wakePending                                      bool
	wakeStuck                                        bool
	wakePendingAge                                   string
	duplicates                                       []string
}

type runtimeStats struct {
	live, dead, zombie, unknown, duplicates, activeIdle, idleUnread, idleWakeStuck int
	details                                                                        []workerRuntime
}

// livenessEnv carries the dependency-injection seams. Production
// callers use defaultLivenessEnv(); tests fill in fakes.
type livenessEnv struct {
	hostname     func() string
	now          func() time.Time
	projectsFile string
	tmuxRunner   tmuxRunner
}

func defaultLivenessEnv() livenessEnv {
	return livenessEnv{
		hostname:     defaultHostname,
		now:          time.Now,
		projectsFile: projectsFilePath(),
		tmuxRunner:   realTmux{},
	}
}

func defaultHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	return h
}

// projectsFilePath returns $WT_CFG/projects (default
// ~/.config/wt/projects). Mirrors wt-go.
func projectsFilePath() string {
	if v := os.Getenv("WT_CFG"); v != "" {
		return filepath.Join(v, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "wt", "projects")
}

func readProjects(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, raw := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			raw = raw[:i]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}

// projectScanPaths returns the project roots from the projects file,
// deduplicated by realpath.
func projectScanPaths(projectsFile string) ([]string, error) {
	projects, err := readProjects(projectsFile)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		key := realpathOrClean(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out, nil
}

func realpathOrClean(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func matchingSessions(panes []paneInfo, project, slug string) []string {
	var out []string
	for _, p := range panes {
		if task.MatchSlug(p.session, project, slug) {
			out = appendUnique(out, p.session)
		}
	}
	return out
}

func appendUnique(in []string, v string) []string {
	if v == "" {
		return in
	}
	for _, existing := range in {
		if existing == v {
			return in
		}
	}
	return append(in, v)
}

func inspectRuntime(projectRoot, slug, agent, recordedSession string, tr tmuxRunner) workerRuntime {
	if agent == "" {
		agent = "claude"
	}
	project, err := task.ProjectName(projectRoot)
	if err != nil || project == "" {
		project = filepath.Base(projectRoot)
	}
	fallback := task.TaskTmuxSession(filepath.Join(projectRoot, "tasks"), projectRoot, slug)

	panesOut, _ := tr.listPanes()
	parsed := parsePanes(panesOut)
	matches := matchingSessions(parsed, project, slug)
	if recordedSession != "" {
		matches = appendUnique(matches, recordedSession)
	}
	sort.Strings(matches)

	session := recordedSession
	if session == "" && len(matches) == 1 {
		session = matches[0]
	}
	if session == "" {
		session = fallback
	}

	rt := workerRuntime{slug: slug, agent: agent, session: session, state: "unknown", projectRoot: projectRoot}
	if len(matches) > 1 {
		rt.duplicates = matches
	}

	if recordedSession == "" && len(matches) == 0 {
		rt.detail = "no recorded session"
		return rt
	}

	candidates := appendUnique(nil, recordedSession)
	for _, match := range matches {
		candidates = appendUnique(candidates, match)
	}
	candidates = appendUnique(candidates, fallback)

	var firstUnhealthy workerRuntime
	for i, candidate := range candidates {
		state, detail := inspectSession(candidate, agent, parsed, tr)
		if state != "dead" && state != "zombie" {
			rt.session = candidate
			rt.state = state
			rt.detail = detail
			if i > 0 && recordedSession != "" {
				if rt.detail != "" {
					rt.detail += "; "
				}
				rt.detail += "recorded session unhealthy"
			}
			return rt
		}
		if firstUnhealthy.state == "" {
			firstUnhealthy = workerRuntime{session: candidate, state: state, detail: detail}
		}
	}
	rt.session = firstUnhealthy.session
	rt.state = firstUnhealthy.state
	rt.detail = firstUnhealthy.detail
	return rt
}

func inspectSession(session, agent string, panes []paneInfo, tr tmuxRunner) (state, detail string) {
	if !tr.hasSession(session) {
		return "dead", "tmux session missing"
	}
	var windowPanes []paneInfo
	for _, p := range panes {
		if p.session == session && p.window == agent {
			windowPanes = append(windowPanes, p)
		}
	}
	if len(windowPanes) == 0 {
		return "zombie", "agent window missing"
	}
	for _, p := range windowPanes {
		if p.dead == "1" {
			if p.deadStatus != "" {
				return "dead", "pane dead status=" + p.deadStatus
			}
			return "dead", "pane dead"
		}
	}
	for _, p := range windowPanes {
		if agentShellCommands[p.command] {
			return "zombie", "agent pane is shell (" + p.command + ")"
		}
	}
	paneState, paneDetail := classifyAgentPane(tr, session+":"+agent, agent)
	if paneState == "dead" {
		return paneState, paneDetail
	}
	if agent == "codex" || agent == "claude" || agent == "opencode" {
		return paneState, paneDetail
	}
	return "running", ""
}

// scanActiveRuntimes walks every project listed in the projects file,
// reads each tasks/*.md, filters to status=active on the local host,
// inspects the tmux state, and returns aggregated counts plus
// per-worker details. Tasks without parseable frontmatter are skipped.
func scanActiveRuntimes(e livenessEnv) (runtimeStats, error) {
	var stats runtimeStats
	localHost := e.hostname()
	projects, err := projectScanPaths(e.projectsFile)
	if err != nil {
		return stats, err
	}
	for _, project := range projects {
		dir := filepath.Join(project, "tasks")
		metas, lerr := task.List(dir)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				continue
			}
			return stats, lerr
		}
		for _, m := range metas {
			if !task.IsActive(m.Status) {
				continue
			}
			if m.Host != "" && m.Host != localHost {
				continue
			}
			slug := m.Slug
			if slug == "" {
				continue
			}
			rt := inspectRuntime(project, slug, m.Agent, m.Session, e.tmuxRunner)
			if rt.state == "idle" {
				n, err := countUnreadAt(project, slug)
				if err != nil {
					return stats, err
				}
				rt.unread = n
				if n == 0 {
					clearWakePending(project, slug)
				}
				wake := wakePendingState(project, slug, e.now())
				rt.wakePending = n > 0 && wake.fresh
				rt.wakeStuck = n > 0 && wake.exists && !wake.fresh
				if rt.wakeStuck {
					rt.wakePendingAge = wake.age.Round(time.Second).String()
					stats.idleWakeStuck++
				} else if n > 0 && !rt.wakePending {
					stats.idleUnread++
				}
				stats.activeIdle++
			}
			stats.details = append(stats.details, rt)
			switch rt.state {
			case "dead":
				stats.dead++
			case "zombie":
				stats.zombie++
			case "unknown":
				stats.unknown++
				stats.live++
			default:
				stats.live++
			}
			if len(rt.duplicates) > 1 {
				stats.duplicates++
			}
		}
	}
	return stats, nil
}

// countUnreadAt mirrors task.CountUnreadInbox but for an arbitrary
// project root (the cwd-only helper does not fit the multi-project
// scan).
func countUnreadAt(projectRoot, slug string) (int, error) {
	dir, err := task.InboxDirForProject(projectRoot, slug)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		info, err := ent.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		n++
	}
	return n, nil
}

// RunStatus is the exported entry point for `spore fleet status`. It
// prints the wt-task-shape one-liner plus per-worker detail lines and
// returns 0 on a clean fleet, 2 when any worker needs attention
// (dead/zombie/duplicates/idle-unread/idle-wake-stuck).
func RunStatus(stdout, stderr io.Writer) (int, error) {
	return runStatus(stdout, stderr, defaultLivenessEnv())
}

func runStatus(stdout, stderr io.Writer, e livenessEnv) (int, error) {
	stats, err := scanActiveRuntimes(e)
	if err != nil {
		fmt.Fprintf(stderr, "spore: %v\n", err)
		return 1, err
	}
	sort.Slice(stats.details, func(i, j int) bool { return stats.details[i].slug < stats.details[j].slug })
	fmt.Fprintf(stdout, "fleet status: active-live=%d active-dead=%d active-zombie=%d active-unknown=%d duplicates=%d active-idle=%d idle-unread=%d idle-wake-stuck=%d\n",
		stats.live, stats.dead, stats.zombie, stats.unknown, stats.duplicates, stats.activeIdle, stats.idleUnread, stats.idleWakeStuck)
	for _, rt := range stats.details {
		switch rt.state {
		case "dead":
			fmt.Fprintf(stdout, "dead: %s (%s, session=%s", rt.slug, rt.agent, rt.session)
			if rt.detail != "" {
				fmt.Fprintf(stdout, ", %s", rt.detail)
			}
			fmt.Fprintln(stdout, ")")
		case "zombie":
			fmt.Fprintf(stdout, "zombie: %s (%s, session=%s", rt.slug, rt.agent, rt.session)
			if rt.detail != "" {
				fmt.Fprintf(stdout, ", %s", rt.detail)
			}
			fmt.Fprintln(stdout, ")")
		case "idle", "running":
			writeAgentDetail(stdout, rt)
		case "unknown":
			fmt.Fprintf(stdout, "unknown: %s (%s", rt.slug, rt.agent)
			if rt.detail != "" {
				fmt.Fprintf(stdout, ", %s", rt.detail)
			}
			fmt.Fprintln(stdout, ")")
		}
		if len(rt.duplicates) > 1 {
			fmt.Fprintf(stdout, "duplicate: %s sessions=%s\n", rt.slug, strings.Join(rt.duplicates, ","))
		}
	}
	if stats.dead > 0 || stats.zombie > 0 || stats.duplicates > 0 || stats.idleUnread > 0 || stats.idleWakeStuck > 0 {
		return 2, nil
	}
	return 0, nil
}

func writeAgentDetail(stdout io.Writer, rt workerRuntime) {
	switch rt.agent {
	case "codex":
		if rt.state == "idle" && rt.unread > 0 && rt.wakeStuck {
			fmt.Fprintf(stdout, "codex-idle-wake-stuck: %s (session=%s, unread-inbox=%d, wake-pending-age=%s)\n", rt.slug, rt.session, rt.unread, rt.wakePendingAge)
		} else if rt.state == "idle" && rt.unread > 0 && !rt.wakePending {
			fmt.Fprintf(stdout, "codex-idle-unread: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
		} else if rt.state == "idle" && rt.unread > 0 && rt.wakePending {
			fmt.Fprintf(stdout, "codex-idle-wake-pending: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
		} else {
			fmt.Fprintf(stdout, "codex-%s: %s (session=%s)\n", rt.state, rt.slug, rt.session)
		}
	case "claude":
		if rt.state == "idle" {
			if rt.unread > 0 && rt.wakeStuck {
				fmt.Fprintf(stdout, "claude-idle-wake-stuck: %s (session=%s, unread-inbox=%d, wake-pending-age=%s)\n", rt.slug, rt.session, rt.unread, rt.wakePendingAge)
			} else if rt.unread > 0 && !rt.wakePending {
				fmt.Fprintf(stdout, "claude-idle-unread: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
			} else if rt.unread > 0 && rt.wakePending {
				fmt.Fprintf(stdout, "claude-idle-wake-pending: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
			} else {
				fmt.Fprintf(stdout, "claude-idle: %s (session=%s)\n", rt.slug, rt.session)
			}
		}
	case "opencode":
		if rt.state == "idle" && rt.unread > 0 && rt.wakeStuck {
			fmt.Fprintf(stdout, "opencode-idle-wake-stuck: %s (session=%s, unread-inbox=%d, wake-pending-age=%s)\n", rt.slug, rt.session, rt.unread, rt.wakePendingAge)
		} else if rt.state == "idle" && rt.unread > 0 && !rt.wakePending {
			fmt.Fprintf(stdout, "opencode-idle-unread: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
		} else if rt.state == "idle" && rt.unread > 0 && rt.wakePending {
			fmt.Fprintf(stdout, "opencode-idle-wake-pending: %s (session=%s, unread-inbox=%d)\n", rt.slug, rt.session, rt.unread)
		} else {
			fmt.Fprintf(stdout, "opencode-%s: %s (session=%s)\n", rt.state, rt.slug, rt.session)
		}
	}
}
