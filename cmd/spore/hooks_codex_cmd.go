package main

import (
	"bufio"
	"context"
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
	"github.com/versality/spore/internal/lifecyclehooks"
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
                  $SPORE_CODEX_STOP_TIMEOUT (per-hook seconds, default 8).
  pre-tool-use    Codex PreToolUse hook: refuse a new tool dispatch
                  when the transcript still has at least one prior
                  tool call without a matching *_output. Re-parses the
                  transcript live; never consults the ledger. Reads
                  $SPORE_DRIVER, $SPORE_TASK_INBOX, and
                  $SPORE_COORDINATOR_STATE_DIR for the coordinator-session
                  gate. Exits 2 with codex-stuck-toolcall-prior when
                  prior is unfinalized; 0 otherwise.
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
	case "pre-tool-use":
		return runHooksCodexPreToolUse(rest)
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
	if chain, ok := codexStopChainFromRegistry(); ok {
		cfg.Chain = chain
	} else {
		return 0
	}

	res := codex.Stop(cfg, os.Stdin)
	if res.Stderr != "" {
		io.WriteString(os.Stderr, res.Stderr)
	}
	return res.ExitCode
}

func codexStopChainFromRegistry() ([]codex.ChainHook, bool) {
	root := os.Getenv("SPORE_PROJECT_ROOT")
	if root == "" {
		var ok bool
		root, ok = findSporeRoot(cwdOrDot())
		if !ok {
			return nil, false
		}
	}
	if _, err := os.Stat(filepath.Join(root, "spore.toml")); err != nil {
		return nil, false
	}
	kind := os.Getenv("WT_SESSION_KIND")
	if kind == "" {
		return nil, false
	}
	var out []codex.ChainHook
	for _, hook := range lifecyclehooks.ForDriver(lifecyclehooks.DriverCodex) {
		if hook.Event != "Stop" || hook.Command == "spore hooks codex stop" {
			continue
		}
		if !hookMatchesKind(hook.Kinds, kind) {
			continue
		}
		argv := strings.Fields(hook.Command)
		if len(argv) == 0 {
			continue
		}
		out = append(out, codex.ChainHook{
			Argv:    argv,
			Timeout: time.Duration(hook.Timeout) * time.Second,
			Async:   hook.Async,
		})
	}
	return out, true
}

func findSporeRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "spore.toml")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func cwdOrDot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func hookMatchesKind(kinds []string, kind string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func runHooksCodexPreToolUse(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks codex pre-tool-use: takes no args")
		return 2
	}
	cfg := codex.PreToolUseConfig{
		Inbox:               os.Getenv("SPORE_TASK_INBOX"),
		CoordinatorStateDir: defaultCoordinatorStateDirEnv(),
		Driver:              os.Getenv("SPORE_DRIVER"),
	}
	res := codex.PreToolUse(cfg, os.Stdin)
	if res.Stderr != "" {
		io.WriteString(os.Stderr, res.Stderr)
	}
	return res.ExitCode
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
	driver := os.Getenv("SPORE_DRIVER")
	if driver == "" {
		driver = "codex"
	}

	cfg := &codex.InboxWatcherConfig{
		StateDir:    stateDir,
		Projects:    projects,
		SessionName: session,
		PaneCmds:    []string{"codex-raw"},
		Driver:      driver,
	}
	cfg.ApplyEnv()

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
