package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/versality/spore/internal/coordinator"
	"github.com/versality/spore/internal/hooks/codex"
)

const hooksCodexUsage = `spore hooks codex - codex hook adapters

Usage:
  spore hooks codex <event>

Events:
  session-start   Codex SessionStart hook: inject the coordinator role
                  brief on resume. No-ops outside coordinator sessions.
                  Reads $SPORE_TASK_INBOX (gate: under
                  $SPORE_COORDINATOR_STATE_DIR), $SPORE_COORDINATOR_BRIEF
                  (default $HOME/.config/spore/coordinator/brief.md).
                  Appends a session-start event to the codex context
                  monitor ledger under
                  $SPORE_COORDINATOR_STATE_DIR/codex-context-monitor.jsonl.
  inbox-watcher   Long-running daemon: poll project inboxes under
                  $SPORE_COORDINATOR_STATE_DIR/<project>/inbox and
                  respawn the coordinator pane when a fresh message
                  arrives. Reads $SPORE_PROJECTS_FILE (one absolute
                  project path per line, # comments allowed),
                  $SPORE_COORDINATOR_SESSION (default "coordinator"),
                  $SPORE_INBOX_WATCHER_PANE_CMDS (colon-separated tmux
                  pane current_command values; default "codex-raw"),
                  $SPORE_INBOX_WATCHER_WAKE_CMD (argv as one shell-style
                  string). Exits when the tmux session dies. Pure
                  record-only mode: leave WAKE_CMD empty.
  stop            Codex Stop hook: bundles the codex context monitor
                  (coordinator only), worker inbox drain, and a
                  configurable sub-hook chain into one entry point
                  Codex calls per Stop. Reads $SPORE_DRIVER for the
                  context-monitor gate, $SPORE_TASK_INBOX,
                  $SPORE_COORDINATOR_STATE_DIR, $WT_STATE,
                  $SPORE_CODEX_STOP_CHAIN (JSON file mapping argv lists
                  to the post-monitor sub-hook chain),
                  $SPORE_CODEX_STOP_TIMEOUT (per-hook seconds, default 8).
`

func runHooksCodex(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, hooksCodexUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(hooksCodexUsage)
		return 0
	case "session-start":
		return runHooksCodexSessionStart(rest)
	case "stop":
		return runHooksCodexStop(rest)
	case "inbox-watcher":
		return runHooksCodexInboxWatcher(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore hooks codex: unknown event %q\n\n%s", sub, hooksCodexUsage)
		return 2
	}
}

func runHooksCodexSessionStart(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks codex session-start: takes no args")
		return 2
	}
	cfg := codex.SessionStartConfig{
		Inbox:               os.Getenv("SPORE_TASK_INBOX"),
		CoordinatorStateDir: defaultCoordinatorStateDirEnv(),
		BriefPath:           defaultCoordinatorBriefEnv(),
	}
	res, err := codex.SessionStart(cfg, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks codex session-start:", err)
		return 1
	}
	if res.Skipped {
		return 0
	}
	if _, err := os.Stdout.Write(res.JSON); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks codex session-start: write:", err)
		return 1
	}
	return 0
}

func runHooksCodexStop(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks codex stop: takes no args")
		return 2
	}
	cfg := codex.StopConfig{
		Inbox:               os.Getenv("SPORE_TASK_INBOX"),
		CoordinatorStateDir: defaultCoordinatorStateDirEnv(),
		WorkerStateDir:      wtStateDirEnv(),
		Driver:              os.Getenv("SPORE_DRIVER"),
		SoftCap:             envInt("SPORE_COORDINATOR_TOKEN_SOFT"),
		HardCap:             envInt("SPORE_COORDINATOR_TOKEN_HARD"),
	}
	if v := envInt("SPORE_CODEX_STOP_TIMEOUT"); v > 0 {
		cfg.CommandTimeout = time.Duration(v) * time.Second
	}
	if chain, err := loadStopChainFromEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks codex stop:", err)
		return 1
	} else {
		cfg.Chain = chain
	}

	res := codex.Stop(cfg, os.Stdin)
	if res.Stderr != "" {
		io.WriteString(os.Stderr, res.Stderr)
	}
	return res.ExitCode
}

// loadStopChainFromEnv reads $SPORE_CODEX_STOP_CHAIN. The value is a
// path to a JSON file with shape `[{"argv":["spore","worker","token-monitor"]}, ...]`.
// Empty / missing env collapses to no chain (the codex-stop adapter
// becomes a pure context monitor + inbox drain).
func loadStopChainFromEnv() ([]codex.ChainHook, error) {
	path := os.Getenv("SPORE_CODEX_STOP_CHAIN")
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read chain %s: %w", path, err)
	}
	var rows []struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse chain %s: %w", path, err)
	}
	out := make([]codex.ChainHook, 0, len(rows))
	for _, r := range rows {
		if len(r.Argv) == 0 {
			continue
		}
		out = append(out, codex.ChainHook{Argv: r.Argv})
	}
	return out, nil
}

func runHooksCodexInboxWatcher(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks codex inbox-watcher: takes no args")
		return 2
	}
	stateDir := defaultCoordinatorStateDirEnv()
	projectsFile := os.Getenv("SPORE_PROJECTS_FILE")
	if projectsFile == "" {
		home, _ := os.UserHomeDir()
		projectsFile = filepath.Join(home, ".config", "spore", "projects")
	}
	projects, err := readProjectsFile(projectsFile, stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks codex inbox-watcher:", err)
		return 1
	}
	if len(projects) == 0 {
		fmt.Fprintln(os.Stderr, "spore hooks codex inbox-watcher: no projects configured")
		return 1
	}

	session := os.Getenv("SPORE_COORDINATOR_SESSION")
	if session == "" {
		session = "coordinator"
	}
	paneCmds := []string{"codex-raw"}
	if v := os.Getenv("SPORE_INBOX_WATCHER_PANE_CMDS"); v != "" {
		paneCmds = paneCmds[:0]
		for _, p := range strings.Split(v, ":") {
			if p = strings.TrimSpace(p); p != "" {
				paneCmds = append(paneCmds, p)
			}
		}
	}
	wakeArgv := splitShellArgs(os.Getenv("SPORE_INBOX_WATCHER_WAKE_CMD"))

	driver := os.Getenv("SPORE_DRIVER")
	if driver == "" {
		driver = "codex"
	}

	cfg := &codex.InboxWatcherConfig{
		StateDir:       stateDir,
		Projects:       projects,
		SessionName:    session,
		PaneCmds:       paneCmds,
		WakeArgv:       wakeArgv,
		WakePendingTTL: time.Duration(envIntDefault("SPORE_INBOX_WATCHER_WAKE_TTL", 300)) * time.Second,
		PollInterval:   time.Duration(envIntDefault("SPORE_INBOX_WATCHER_POLL_SEC", 5)) * time.Second,
		StartupWait:    time.Duration(envIntDefault("SPORE_INBOX_WATCHER_STARTUP_WAIT", 30)) * time.Second,
		Driver:         driver,
		Once:           os.Getenv("SPORE_INBOX_WATCHER_ONCE") == "1",
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := codex.RunInboxWatcher(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks codex inbox-watcher:", err)
		return 1
	}
	return 0
}

// readProjectsFile parses one absolute project path per line; lines
// starting with # and blank lines are ignored. Returns the
// ProjectInbox list with Path = <stateDir>/<basename>/inbox.
func readProjectsFile(path, stateDir string) ([]codex.ProjectInbox, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []codex.ProjectInbox
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		out = append(out, codex.ProjectInbox{
			Name: base,
			Path: filepath.Join(stateDir, base, "inbox"),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
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

func envIntDefault(name string, def int) int {
	if v := envInt(name); v > 0 {
		return v
	}
	return def
}

func wtStateDirEnv() string {
	if d := os.Getenv("WT_STATE"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "wt")
}

func defaultCoordinatorStateDirEnv() string {
	return coordinator.StateDir()
}

func defaultCoordinatorBriefEnv() string {
	if p := os.Getenv("SPORE_COORDINATOR_BRIEF"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "spore", "coordinator", "brief.md")
}
