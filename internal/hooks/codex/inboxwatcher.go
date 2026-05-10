package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectInbox is one inbox the watcher polls per pass.
type ProjectInbox struct {
	// Name is the project basename, used for ledger rows + state
	// subdir.
	Name string
	// Path is the inbox dir, typically
	// <CoordinatorStateDir>/<Name>/inbox.
	Path string
}

// InboxWatcherConfig parameterizes the watcher daemon. Only the
// state dir + projects list are required; the rest tune behavior.
type InboxWatcherConfig struct {
	// StateDir is the coordinator state root; per-project marker
	// dirs and the event ledger live here.
	StateDir string
	// Projects is the list of inboxes to poll per pass.
	Projects []ProjectInbox
	// LedgerFile overrides the JSONL event ledger path. Defaults to
	// <StateDir>/codex-inbox-watcher.jsonl.
	LedgerFile string

	// SessionName is the tmux session whose pane the watcher
	// respawns on a wake. The watcher exits when the session dies.
	SessionName string
	// PaneCmds is the set of pane current_command values that flag
	// the coordinator pane (e.g. "codex-raw", "claude"). The
	// watcher picks the first matching pane in the session.
	PaneCmds []string
	// WakeArgv is the command (with args) the watcher passes to
	// tmux respawn-pane. Empty disables waking entirely (mode falls
	// back to record-only).
	WakeArgv []string
	// WakeMode is respawn (default) or record-only.
	WakeMode WakeMode
	// WakePendingTTL caps how often the same project may be woken.
	// Zero defaults to 5 minutes.
	WakePendingTTL time.Duration

	// PollInterval is the loop cadence. Zero defaults to 5 seconds.
	PollInterval time.Duration
	// StartupWait is how long to wait for the tmux session before
	// giving up. Zero defaults to 30 seconds.
	StartupWait time.Duration
	// Once limits the watcher to a single pass for tests / smoke
	// checks.
	Once bool

	// Driver is the active coordinator driver. Only "codex" makes
	// the watcher actually wake; any other value idles until the
	// session dies.
	Driver string

	// Tmux is the tmux client; nil falls back to the os/exec-backed
	// implementation.
	Tmux TmuxClient
	// Now is injected for tests.
	Now func() time.Time
	// Sleep is injected for tests; nil uses time.Sleep.
	Sleep func(time.Duration)
}

// TmuxClient is the slice of tmux the watcher needs. The default
// implementation shells out via os/exec.
type TmuxClient interface {
	HasSession(name string) bool
	ResolvePane(session string, paneCmds []string) (string, bool)
	RespawnPane(paneID string, argv []string) error
}

func (c *InboxWatcherConfig) defaults() {
	if c.WakePendingTTL <= 0 {
		c.WakePendingTTL = 5 * time.Minute
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.StartupWait <= 0 {
		c.StartupWait = 30 * time.Second
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Sleep == nil {
		c.Sleep = time.Sleep
	}
	if c.LedgerFile == "" && c.StateDir != "" {
		c.LedgerFile = filepath.Join(c.StateDir, "codex-inbox-watcher.jsonl")
	}
	if c.Tmux == nil {
		c.Tmux = execTmux{}
	}
	if c.WakeMode == WakeModeRespawn && len(c.WakeArgv) == 0 {
		c.WakeMode = WakeModeRecordOnly
	}
}

// RunInboxWatcher is the daemon entry point. It waits for the tmux
// session to come up, then loops: poll inboxes -> plan -> execute
// (respawn pane, ledger writes) -> sleep. Exits cleanly when the
// session dies.
func RunInboxWatcher(ctx context.Context, cfg *InboxWatcherConfig) error {
	cfg.defaults()
	if cfg.SessionName == "" {
		return errors.New("inbox-watcher: SessionName required")
	}
	if len(cfg.Projects) == 0 {
		return errors.New("inbox-watcher: at least one project required")
	}

	if !waitForSession(ctx, cfg) {
		return errors.New("inbox-watcher: tmux session did not appear")
	}

	if cfg.Driver != "codex" {
		idleUntilSessionDies(ctx, cfg)
		return nil
	}

	for ctx.Err() == nil {
		if !cfg.Tmux.HasSession(cfg.SessionName) {
			return nil
		}
		runOnePass(cfg)
		if cfg.Once {
			return nil
		}
		if !sleepWithCtx(ctx, cfg.Sleep, cfg.PollInterval) {
			return nil
		}
	}
	return nil
}

// runOnePass walks every project, observes its inbox, plans an
// action via PlanInbox, then performs side-effects (state save,
// ledger append, optional wake).
func runOnePass(cfg *InboxWatcherConfig) {
	for _, p := range cfg.Projects {
		obs := observeInbox(p.Path)
		stateDir := filepath.Join(cfg.StateDir, p.Name)
		prior := LoadInboxState(stateDir)
		action := PlanInbox(prior, obs, cfg.WakeMode, cfg.WakePendingTTL, cfg.Now())
		applyAction(cfg, p, action)
	}
}

func applyAction(cfg *InboxWatcherConfig, p ProjectInbox, action InboxAction) {
	if action.Wake {
		paneID, ok := cfg.Tmux.ResolvePane(cfg.SessionName, cfg.PaneCmds)
		if !ok {
			action.Event = "wake-error"
			action.Wake = false
		} else if err := cfg.Tmux.RespawnPane(paneID, cfg.WakeArgv); err != nil {
			action.Event = "wake-error"
			action.Wake = false
		}
	}

	if action.Event != "" {
		appendInboxEvent(cfg, p.Name, action)
	}
	stateDir := filepath.Join(cfg.StateDir, p.Name)
	_ = SaveInboxState(stateDir, action.NewState)
}

// observeInbox snapshots one inbox dir: newest filename + count of
// pending *.json + the "source" string from the newest file's body
// (best-effort).
func observeInbox(inbox string) InboxObservation {
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o700); err != nil {
		return InboxObservation{}
	}
	entries, err := os.ReadDir(inbox)
	if err != nil {
		return InboxObservation{}
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return InboxObservation{}
	}
	sort.Strings(names)
	newest := names[len(names)-1]
	source := readEventSource(filepath.Join(inbox, newest))
	return InboxObservation{
		Newest:      newest,
		UnreadCount: len(names),
		Source:      source,
	}
}

func readEventSource(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	var ev struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.Source == "" {
		return "unknown"
	}
	return ev.Source
}

func appendInboxEvent(cfg *InboxWatcherConfig, project string, action InboxAction) {
	if cfg.LedgerFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LedgerFile), 0o700); err != nil {
		return
	}
	ts := cfg.Now().Format(time.RFC3339)
	source := action.Source
	if source == "" {
		source = "unknown"
	}
	row := fmt.Sprintf(
		`{"ts":"%s","project":%s,"unread":%d,"newest":%s,"status":%s,"source":%s}`+"\n",
		ts, jsonString(project), action.UnreadCount, jsonString(action.Newest), jsonString(action.Event), jsonString(source))
	f, err := os.OpenFile(cfg.LedgerFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(row)
}

func waitForSession(ctx context.Context, cfg *InboxWatcherConfig) bool {
	deadline := cfg.Now().Add(cfg.StartupWait)
	for cfg.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		if cfg.Tmux.HasSession(cfg.SessionName) {
			return true
		}
		if cfg.Once {
			return false
		}
		if !sleepWithCtx(ctx, cfg.Sleep, time.Second) {
			return false
		}
	}
	return cfg.Tmux.HasSession(cfg.SessionName)
}

func idleUntilSessionDies(ctx context.Context, cfg *InboxWatcherConfig) {
	for ctx.Err() == nil {
		if !cfg.Tmux.HasSession(cfg.SessionName) {
			return
		}
		if cfg.Once {
			return
		}
		if !sleepWithCtx(ctx, cfg.Sleep, cfg.PollInterval) {
			return
		}
	}
}

func sleepWithCtx(ctx context.Context, sleep func(time.Duration), d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	if ctx.Err() != nil {
		return false
	}
	sleep(d)
	return ctx.Err() == nil
}

// execTmux is the os/exec-backed TmuxClient.
type execTmux struct{}

func (execTmux) HasSession(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

func (execTmux) ResolvePane(session string, paneCmds []string) (string, bool) {
	cmd := exec.Command("tmux", "list-panes", "-t", session, "-F", "#{pane_id}\t#{pane_current_command}")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	want := make(map[string]bool, len(paneCmds))
	for _, c := range paneCmds {
		want[c] = true
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if want[parts[1]] {
			return parts[0], true
		}
	}
	return "", false
}

func (execTmux) RespawnPane(paneID string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("respawn-pane: empty argv")
	}
	args := []string{"respawn-pane", "-k", "-t", paneID}
	args = append(args, strings.Join(argv, " "))
	return exec.Command("tmux", args...).Run()
}
