package codex

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Env var names that tune InboxWatcherConfig from the spore CLI. The
// daemon itself does not read environment - the CLI wrapper calls
// ApplyEnv on a partially-built config to lift these values onto the
// struct before handing it to RunInboxWatcher.
const (
	EnvPaneCmds    = "SPORE_INBOX_WATCHER_PANE_CMDS"
	EnvWakeCmd     = "SPORE_INBOX_WATCHER_WAKE_CMD"
	EnvWakeTTL     = "SPORE_INBOX_WATCHER_WAKE_TTL"
	EnvPollSec     = "SPORE_INBOX_WATCHER_POLL_SEC"
	EnvStartupWait = "SPORE_INBOX_WATCHER_STARTUP_WAIT"
	EnvOnce        = "SPORE_INBOX_WATCHER_ONCE"
)

// ApplyEnv populates cfg from the SPORE_INBOX_WATCHER_* env vars.
// Empty / unset env vars leave the corresponding field untouched so
// the caller may seed defaults first. PaneCmds is appended to (with
// the env value taking precedence over any caller-supplied list) so
// that an empty env value preserves caller defaults.
func (c *InboxWatcherConfig) ApplyEnv() {
	if v := os.Getenv(EnvPaneCmds); v != "" {
		c.PaneCmds = c.PaneCmds[:0]
		for _, p := range strings.Split(v, ":") {
			if p = strings.TrimSpace(p); p != "" {
				c.PaneCmds = append(c.PaneCmds, p)
			}
		}
	}
	if v := os.Getenv(EnvWakeCmd); v != "" {
		c.WakeArgv = splitShellArgs(v)
	}
	if d := envSeconds(EnvWakeTTL); d > 0 {
		c.WakePendingTTL = d
	}
	if d := envSeconds(EnvPollSec); d > 0 {
		c.PollInterval = d
	}
	if d := envSeconds(EnvStartupWait); d > 0 {
		c.StartupWait = d
	}
	if os.Getenv(EnvOnce) == "1" {
		c.Once = true
	}
}

func envSeconds(name string) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// splitShellArgs is a minimal whitespace-and-quote splitter for the
// wake command env var. Single + double quotes group tokens; no
// escape handling beyond that, which is plenty for typical wake
// commands like `wt-task launch-coordinator`.
func splitShellArgs(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
