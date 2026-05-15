package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AgentKillSwitches flags unsafe agent-lifecycle primitives in shell,
// nix, and doc sources: literal self-kill of a named tmux session,
// any tmux kill-session lacking an explicit -t target, restricted
// systemctl --user (restart|stop) of the agent's user service outside
// a small allowlist of authoritative files, and broad process killers
// (pkill, killall) naming agent processes.
//
// All consumer-specific names ship empty; without configuration only
// the universal "tmux kill-session without -t" check fires. Wire via
// spore.toml [lint.agent-kill-switches]:
//
//	agent_session         = "<tmux session name>"
//	agent_service         = "<name>.service"
//	agent_service_allow   = ["path/one", "path/two"]
//	agent_processes       = ["procA", "procB"]
//	scan_dirs             = ["harness", "docs"]
//	skip_path             = ["harness/test-*"]
type AgentKillSwitches struct {
	ScanDirs          []string
	SkipPath          []string
	AgentSession      string
	AgentService      string
	AgentServiceAllow []string
	AgentProcesses    []string
}

func (AgentKillSwitches) Name() string { return "agent-kill-switches" }

var (
	aksKillSession       = regexp.MustCompile(`tmux[\t ]+kill-session\b`)
	aksKillSessionTarget = regexp.MustCompile(`tmux[\t ]+kill-session[\t ]+-t[\t ]+`)
	aksBroadKillers      = regexp.MustCompile(`\b(pkill|killall)\b`)
)

const (
	aksMsgSelfKill        = "unsafe agent lifecycle: tmux kill-session targeting the agent session"
	aksMsgBroadKill       = "unsafe agent lifecycle: tmux kill-session without explicit -t target"
	aksMsgServiceRestart  = "unsafe agent lifecycle: agent service stop/restart outside the operator lifecycle allowlist"
	aksMsgBroadProcKiller = "unsafe agent lifecycle: broad process killer naming an agent process"
)

var aksTextExts = map[string]bool{
	".sh":   true,
	".bash": true,
	".nix":  true,
	".md":   true,
}

func (l AgentKillSwitches) Run(root string) ([]Issue, error) {
	files, err := listFiles(root, aksTextExts)
	if err != nil {
		return nil, err
	}

	selfKillRe := l.selfKillRegexp()
	serviceRe := l.serviceRestartRegexp()
	procKillerRe := l.procKillerRegexp()
	allow := l.allowSet()

	var issues []Issue
	for _, rel := range files {
		if skipPath(rel, l.SkipPath) {
			continue
		}
		if l.scanDirsConfigured() && !l.inScanDirs(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, fmt.Errorf("agent-kill-switches: read %s: %w", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			lineNo := i + 1
			if selfKillRe != nil && selfKillRe.MatchString(line) {
				issues = append(issues, Issue{Path: rel, Line: lineNo, Message: aksMsgSelfKill})
			}
			if aksKillSession.MatchString(line) && !aksKillSessionTarget.MatchString(line) {
				issues = append(issues, Issue{Path: rel, Line: lineNo, Message: aksMsgBroadKill})
			}
			if serviceRe != nil && !allow[rel] && serviceRe.MatchString(line) {
				issues = append(issues, Issue{Path: rel, Line: lineNo, Message: aksMsgServiceRestart})
			}
			if procKillerRe != nil && procKillerRe.MatchString(line) && aksBroadKillers.MatchString(line) {
				issues = append(issues, Issue{Path: rel, Line: lineNo, Message: aksMsgBroadProcKiller})
			}
		}
	}
	return issues, nil
}

func (l AgentKillSwitches) selfKillRegexp() *regexp.Regexp {
	name := strings.TrimSpace(l.AgentSession)
	if name == "" {
		return nil
	}
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`tmux[\t ]+kill-session[\t ]+-t[\t ]+["']?` + q + `["']?([^A-Za-z0-9_-]|$)`)
}

func (l AgentKillSwitches) serviceRestartRegexp() *regexp.Regexp {
	svc := strings.TrimSpace(l.AgentService)
	if svc == "" {
		return nil
	}
	return regexp.MustCompile(`systemctl[\t ]+--user[\t ]+(restart|stop)[\t ]+` + regexp.QuoteMeta(svc) + `\b`)
}

func (l AgentKillSwitches) procKillerRegexp() *regexp.Regexp {
	var names []string
	for _, p := range l.AgentProcesses {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, regexp.QuoteMeta(p))
		}
	}
	if len(names) == 0 {
		return nil
	}
	return regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\b`)
}

func (l AgentKillSwitches) allowSet() map[string]bool {
	out := map[string]bool{}
	for _, p := range l.AgentServiceAllow {
		p = strings.TrimSpace(filepath.ToSlash(p))
		if p != "" {
			out[p] = true
		}
	}
	return out
}

func (l AgentKillSwitches) scanDirsConfigured() bool {
	for _, d := range l.ScanDirs {
		if s := strings.TrimSpace(d); s != "" && s != "." {
			return true
		}
	}
	return false
}

func (l AgentKillSwitches) inScanDirs(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range l.ScanDirs {
		d = strings.TrimSpace(filepath.ToSlash(d))
		if d == "" || d == "." {
			return true
		}
		d = strings.TrimSuffix(d, "/")
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}
