// Package idlewatchdog ports the bash skyhelm-idle-watchdog quiet-idle
// gate. Two modes: standalone (proactive-loop driver) re-emits every
// finding on each call; --hook (Stop-hook) self-gates by SKYBOT_INBOX
// vs SKYHELM_STATE_DIR and dedups against the prior call's
// fingerprint, staying quiet when nothing changed.
//
// Findings come from three sources: wt-task fleet status + ls (dead
// rowers, duplicates, idle-with-unread), the queue classifier
// (promotable drafts, satisfied parked/paused, blocked-without-owner,
// invalid-needs), and a state.md walk (stale Active rows, open
// Roadmap items, coast candidates under a budget gate). On findings
// the gate writes the fingerprint set, optionally notifies via
// harness-notify-attention, and after EscalateAfter seconds escalates
// via the configured bin (skywarden / ntfy-push / generic).
package idlewatchdog

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultEscalateAfter         = 300
	DefaultFloor                 = 6
	DefaultSkywardenStartTimeout = 5
)

// Runner executes an external command. Tests inject a recorder. The
// returned exitCode is the process exit code; runErr is non-nil only
// on a failure to spawn / wait.
type Runner func(name string, args ...string) (stdout string, exitCode int, runErr error)

// Notifier shims harness-notify-attention. Args is the full arg list
// the production caller would pass to the bin.
type Notifier func(bin string, args []string)

// Escalator shims the escalation bin. Production dispatches based on
// the bin's basename (skywarden / ntfy-push / etc).
type Escalator func(bin, summary, body string, escalateAfter, startTimeout int)

// Config drives Check. Defaults() fills zero-value fields from the
// SKYHELM_* / WT_* / HARNESS_* env vars and stdlib helpers.
type Config struct {
	StateDir    string
	StateFile   string
	StatePath   string // SKYHELM_IDLE_WATCHDOG_STATE
	HookState   string // SKYHELM_IDLE_WATCHDOG_HOOK_STATE
	FirstSeen   string // SKYHELM_IDLE_WATCHDOG_FIRST_SEEN
	Escalated   string // SKYHELM_IDLE_WATCHDOG_ESCALATED
	WTTaskBin   string
	Classifier  string
	NotifyBin   string
	EscalateBin string

	Notify        bool
	Escalate      bool
	EscalateAfter int
	StartTimeout  int

	Floor      int
	AgentsFlag string
	WTState    string

	HookMode bool
	Inbox    string

	LocalHost     string
	ProjectRoot   string // override; empty -> auto-detect via git rev-parse
	BudgetAdvice  string // empty -> SKYHELM_QUEUE_BUDGET_ADVICE / skyhelm-budget query / "ok"
	NotifyThreads string // HARNESS_NOTIFY_THREAD_FILE

	Now       func() time.Time
	Run       Runner
	Notifier  Notifier
	Escalator Escalator
	LookPath  func(string) (string, error)
	Hostname  func() (string, error)
}

// Result carries the verdict for the caller. The cmd wrapper renders
// Stdout verbatim, prints Stderr if non-empty, and exits with
// ExitCode. Findings is exposed for tests and external consumers.
type Result struct {
	Findings        []string
	CoastCandidates []string
	ExitCode        int
	Stdout          string
	Stderr          string

	NewSinceState int
	NewSinceHook  int
	FirstSeenAt   int64
	Notified      bool
	Escalated     bool
}

// Defaults populates zero-value fields from env / stdlib defaults.
// Caller-supplied values take precedence.
func (c Config) Defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.StateFile == "" {
		c.StateFile = envOr("SKYHELM_STATE_FILE", filepath.Join(c.StateDir, "state.md"))
	}
	if c.StatePath == "" {
		c.StatePath = envOr("SKYHELM_IDLE_WATCHDOG_STATE", filepath.Join(c.StateDir, "idle-watchdog.last"))
	}
	if c.HookState == "" {
		c.HookState = envOr("SKYHELM_IDLE_WATCHDOG_HOOK_STATE", filepath.Join(c.StateDir, "idle-watchdog-hook.last"))
	}
	if c.FirstSeen == "" {
		c.FirstSeen = envOr("SKYHELM_IDLE_WATCHDOG_FIRST_SEEN", filepath.Join(c.StateDir, "idle-watchdog.first-seen"))
	}
	if c.Escalated == "" {
		c.Escalated = envOr("SKYHELM_IDLE_WATCHDOG_ESCALATED", filepath.Join(c.StateDir, "idle-watchdog.escalated"))
	}
	if c.WTTaskBin == "" {
		c.WTTaskBin = envOr("WT_TASK_BIN", "wt-task")
	}
	if c.Classifier == "" {
		c.Classifier = envOr("SKYHELM_QUEUE_CLASSIFIER_BIN", "skyhelm-queue-classifier")
	}
	if c.NotifyBin == "" {
		c.NotifyBin = envOr("HARNESS_NOTIFY_ATTENTION_BIN", "harness-notify-attention")
	}
	if c.EscalateBin == "" {
		c.EscalateBin = os.Getenv("SKYHELM_IDLE_WATCHDOG_ESCALATE_BIN")
	}
	if !c.Notify {
		c.Notify = envBool("SKYHELM_IDLE_WATCHDOG_NOTIFY", true)
	}
	if !c.Escalate {
		c.Escalate = envBool("SKYHELM_IDLE_WATCHDOG_ESCALATE", true)
	}
	if c.EscalateAfter == 0 {
		c.EscalateAfter = envInt("SKYHELM_IDLE_WATCHDOG_ESCALATE_AFTER", DefaultEscalateAfter)
	}
	if c.StartTimeout == 0 {
		c.StartTimeout = envInt("SKYHELM_IDLE_WATCHDOG_SKYWARDEN_START_TIMEOUT", DefaultSkywardenStartTimeout)
	}
	if c.Floor == 0 {
		c.Floor = envInt("WT_FLEET_FLOOR", DefaultFloor)
	}
	if c.WTState == "" {
		home, _ := os.UserHomeDir()
		c.WTState = envOr("WT_STATE", filepath.Join(home, ".local", "state", "wt"))
	}
	if c.AgentsFlag == "" {
		c.AgentsFlag = envOr("WT_AGENTS_FLAG", filepath.Join(c.WTState, "agents-enabled"))
	}
	if c.NotifyThreads == "" {
		c.NotifyThreads = envOr("HARNESS_NOTIFY_THREAD_FILE", filepath.Join(c.WTState, "harness-notify-threads.jsonl"))
	}
	if c.Inbox == "" {
		c.Inbox = os.Getenv("SKYBOT_INBOX")
	}
	if c.LocalHost == "" {
		c.LocalHost = os.Getenv("SKYHELM_LOCAL_HOST")
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.LookPath == nil {
		c.LookPath = exec.LookPath
	}
	if c.Hostname == nil {
		c.Hostname = os.Hostname
	}
	if c.Run == nil {
		c.Run = defaultRunner
	}
	if c.Notifier == nil {
		c.Notifier = defaultNotifier(c.LookPath)
	}
	if c.Escalator == nil {
		c.Escalator = defaultEscalator
	}
	return c
}

// hookGateOK reports whether the hook-mode self-gate passes. Empty
// inbox or inbox not under StateDir means the gate fired (caller exits
// 0 silently).
func (c Config) hookGateOK() bool {
	if c.Inbox == "" {
		return false
	}
	root := strings.TrimRight(c.StateDir, "/")
	return c.Inbox == root || strings.HasPrefix(c.Inbox, root+"/")
}

// Check executes the watchdog against cfg. The returned Result carries
// the rendered Stdout / Stderr and the desired ExitCode; the cmd
// wrapper writes them out and exits.
func Check(cfg Config) Result {
	cfg = cfg.Defaults()

	if cfg.HookMode && !cfg.hookGateOK() {
		return Result{ExitCode: 0}
	}

	root := cfg.ProjectRoot
	if root == "" {
		root = detectGitRoot()
	}
	if root == "" {
		return okResult("ok (outside git repo)")
	}
	tasksDir := filepath.Join(root, "tasks")
	if !isDir(tasksDir) {
		return okResult("ok (no tasks dir)")
	}

	statusBySlug, remoteHostSlug := scanTasks(tasksDir, cfg.localHost())

	fleetOut, fleetRC := runCapture(cfg.Run, cfg.WTTaskBin, "fleet", "status")
	lsOut, lsRC := runCapture(cfg.Run, cfg.WTTaskBin, "ls")
	counts := parseFleetCounts(fleetOut)

	if cfg.Floor < 1 {
		cfg.Floor = DefaultFloor
	}
	budget := cfg.BudgetAdvice
	if budget == "" {
		budget = resolveBudgetAdvice(cfg)
	}
	agentsEnabled := fileExists(cfg.AgentsFlag)

	var findings []string
	var coastCandidates []string

	if fleetRC != 0 && fleetRC != 2 {
		findings = append(findings, "fleet-status-error: "+firstLine(fleetOut))
	}
	if lsRC != 0 {
		findings = append(findings, "task-ls-error: "+firstLine(lsOut))
	}
	if counts.activeDead > 0 {
		findings = append(findings, fmt.Sprintf("dead-active-rowers: %d", counts.activeDead))
	}
	if counts.activeUnknown > 0 {
		findings = append(findings, fmt.Sprintf("unknown-active-rowers: %d", counts.activeUnknown))
	}
	if counts.duplicates > 0 {
		findings = append(findings, fmt.Sprintf("duplicate-rowers: %d", counts.duplicates))
	}
	if counts.idleUnread > 0 {
		findings = append(findings, fmt.Sprintf("idle-rowers-with-unread-inbox: %d", counts.idleUnread))
	}
	if counts.idleWakeStuck > 0 {
		findings = append(findings, fmt.Sprintf("idle-rowers-with-stuck-wake: %d", counts.idleWakeStuck))
	}
	if counts.activeLive > 0 && counts.activeIdle == counts.activeLive {
		findings = append(findings, fmt.Sprintf("all-active-rowers-idle: %d", counts.activeIdle))
	}
	if cfg.HookMode && cfg.Inbox != "" {
		if n := countInboxJSON(cfg.Inbox); n > 0 {
			findings = append(findings, fmt.Sprintf("unread-inbox: %d at %s", n, cfg.Inbox))
		}
	}

	classifierOut, classifierRC := runCapture(cfg.Run,
		cfg.Classifier,
		"--project", root,
		"--state", cfg.StateFile,
		"--active-live", strconv.Itoa(counts.activeLive),
		"--floor", strconv.Itoa(cfg.Floor),
		"--budget-advice", budget,
	)
	stateBody := readFile(cfg.StateFile)
	if classifierRC != 0 {
		findings = append(findings, "queue-classifier-error: "+firstLine(classifierOut))
	} else {
		for _, row := range parseClassifierRows(classifierOut) {
			findings, coastCandidates = applyClassifierRow(
				findings, coastCandidates, row, agentsEnabled, cfg, stateBody, budget)
		}
	}

	for _, slug := range activeStateSlugs(stateBody) {
		if remoteHostSlug[slug] {
			continue
		}
		st := statusBySlug[slug]
		if st == "active" {
			continue
		}
		if st == "" {
			st = "missing"
		}
		findings = append(findings, "stale-state-active-row: "+slug+" status="+st)
	}
	for _, line := range openRoadmapItems(stateBody) {
		if len(line) > 120 {
			line = line[:120]
		}
		findings = append(findings, "roadmap-open: "+line)
	}

	if len(findings) == 0 && len(coastCandidates) > 0 {
		for _, slug := range coastCandidates {
			findings = append(findings, "coast-detected: "+slug)
		}
		coastCandidates = nil
	}

	if len(findings) == 0 {
		clearStateFiles(cfg.StatePath, cfg.HookState, cfg.FirstSeen, cfg.Escalated)
		return okResult("ok")
	}

	res := Result{
		Findings:        findings,
		CoastCandidates: coastCandidates,
		ExitCode:        2,
	}

	fpList := fingerprintList(findings)
	aggregate := aggregateHash(fpList)

	if cfg.HookMode {
		newViaHook := newCountVs(cfg.HookState, fpList)
		writeFingerprintSet(cfg.HookState, fpList)
		res.NewSinceHook = newViaHook
		if newViaHook == 0 && countPrefix(findings, "unread-inbox:") == 0 {
			return okResult("ok (unchanged coordinator state recorded)")
		}
	}

	summary := "coordinator action needed: " + findings[0]
	res.Stdout = renderStdout(cfg.HookMode, findings)

	priorAggregate := aggregateOfFile(cfg.StatePath)
	newViaState := newCountVs(cfg.StatePath, fpList)
	res.NewSinceState = newViaState
	writeFingerprintSet(cfg.StatePath, fpList)
	now := cfg.Now().Unix()
	firstSeen := now
	if priorAggregate == aggregate {
		if v := readInt64File(cfg.FirstSeen); v > 0 {
			firstSeen = v
		}
	} else {
		_ = os.Remove(cfg.Escalated)
	}
	_ = os.WriteFile(cfg.FirstSeen, []byte(strconv.FormatInt(firstSeen, 10)+"\n"), 0o600)
	res.FirstSeenAt = firstSeen

	if cfg.Notify && newViaState > 0 {
		cfg.Notifier(cfg.NotifyBin, []string{
			"--kind", "skyhelm-idle-watchdog",
			"--slug", "skyhelm",
			"--summary", summary,
			"--look", "run skyhelm-idle-watchdog, reconcile state.md, then unblock or park explicitly",
		})
		res.Notified = true
	}

	if cfg.Escalate && cfg.EscalateAfter > 0 &&
		now-firstSeen >= int64(cfg.EscalateAfter) &&
		!fileExists(cfg.Escalated) {
		body := summary + ". Attach to skyhelm and handle the watchdog findings."
		bin := cfg.EscalateBin
		if bin == "" {
			bin = pickEscalateBin(cfg.LookPath)
		}
		if bin != "" {
			cfg.Escalator(bin, summary, body, cfg.EscalateAfter, cfg.StartTimeout)
			_ = os.WriteFile(cfg.Escalated, []byte(strconv.FormatInt(now, 10)+"\n"), 0o600)
			res.Escalated = true
		}
	}

	return res
}

// applyClassifierRow folds one classifier TSV row into the running
// findings + coast-candidate slices.
func applyClassifierRow(findings, coast []string, row classifierRow,
	agents bool, cfg Config, stateBody, budget string) ([]string, []string) {
	switch row.class + ":" + row.status {
	case "runnable-promote:draft":
		if agents {
			findings = append(findings, "promotable-draft: "+row.slug)
		}
	case "runnable-promote:parked":
		if agents {
			findings = append(findings, "satisfied-parked: "+row.slug)
		}
	case "resume:paused":
		if agents {
			findings = append(findings, "satisfied-paused: "+row.slug)
		}
	case "waiting-trigger:paused":
		if agents && row.reason == "scheduler-pending" &&
			coastCandidateAllowed(cfg, stateBody, budget, row.slug) {
			coast = append(coast, row.slug)
		}
	default:
		if row.class == "invalid-needs-reclassify" {
			if row.status == "blocked" && row.reason == "blocked-without-operator-owner" {
				findings = append(findings, "blocked-without-operator-question: "+row.slug)
			} else {
				findings = append(findings, "invalid-needs-reclassify: "+row.slug+" "+row.reason)
			}
		}
	}
	return findings, coast
}

// okResult returns a Result that prints "skyhelm-idle-watchdog: <msg>"
// and exits 0.
func okResult(msg string) Result {
	return Result{ExitCode: 0, Stdout: "skyhelm-idle-watchdog: " + msg + "\n"}
}

// renderStdout produces the bash printout for findings. Standalone:
// header line + every finding. Hook: header + findings=N details=...
// + per-prefix count/sample rows + any coast-detected lines verbatim.
func renderStdout(hook bool, findings []string) string {
	var b strings.Builder
	b.WriteString("skyhelm-idle-watchdog: coordinator action needed\n")
	if !hook {
		for _, f := range findings {
			b.WriteString(f)
			b.WriteString("\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b,
		"findings=%d details=run skyhelm-idle-watchdog or inspect skyhelm-proactive-loop/state\n",
		len(findings))
	for _, prefix := range hookPrefixes {
		count := countPrefix(findings, prefix)
		if count == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s count=%d sample=%s\n",
			strings.TrimSuffix(prefix, ":"), count, firstWithPrefix(findings, prefix))
	}
	for _, f := range findings {
		if strings.HasPrefix(f, "coast-detected:") {
			b.WriteString(f)
			b.WriteString("\n")
		}
	}
	return b.String()
}

var hookPrefixes = []string{
	"fleet-status-error:",
	"task-ls-error:",
	"dead-active-rowers:",
	"unknown-active-rowers:",
	"duplicate-rowers:",
	"idle-rowers-with-unread-inbox:",
	"all-active-rowers-idle:",
	"unread-inbox:",
	"blocked-without-operator-question:",
	"stale-state-active-row:",
	"roadmap-open:",
	"satisfied-parked:",
	"satisfied-paused:",
	"coast-detected:",
	"promotable-draft:",
	"reclassify-promote-parked:",
	"reclassify-resume-paused:",
}

func countPrefix(findings []string, prefix string) int {
	n := 0
	for _, f := range findings {
		if strings.HasPrefix(f, prefix) {
			n++
		}
	}
	return n
}

func firstWithPrefix(findings []string, prefix string) string {
	for _, f := range findings {
		if strings.HasPrefix(f, prefix) {
			return f
		}
	}
	return ""
}

// localHost returns the configured LocalHost or the short hostname
// (`hostname -s` with `hostname` fallback).
func (c Config) localHost() string {
	if c.LocalHost != "" {
		return c.LocalHost
	}
	if h, err := c.Hostname(); err == nil && h != "" {
		if i := strings.IndexByte(h, '.'); i > 0 {
			return h[:i]
		}
		return h
	}
	return "unknown"
}

// fleetCounts holds the integers parsed out of `wt-task fleet status`.
type fleetCounts struct {
	activeLive    int
	activeDead    int
	activeUnknown int
	duplicates    int
	activeIdle    int
	idleUnread    int
	idleWakeStuck int
}

var fleetKeyRE = regexp.MustCompile(`([a-z-]+)=([0-9]+)`)

func parseFleetCounts(out string) fleetCounts {
	var c fleetCounts
	for _, m := range fleetKeyRE.FindAllStringSubmatch(out, -1) {
		v, _ := strconv.Atoi(m[2])
		switch m[1] {
		case "active-live":
			c.activeLive = v
		case "active-dead":
			c.activeDead = v
		case "active-unknown":
			c.activeUnknown = v
		case "duplicates":
			c.duplicates = v
		case "active-idle":
			c.activeIdle = v
		case "idle-unread":
			c.idleUnread = v
		case "idle-wake-stuck":
			c.idleWakeStuck = v
		}
	}
	return c
}

// classifierRow is one TSV row from the queue-classifier:
// class \t slug \t status \t reason.
type classifierRow struct {
	class, slug, status, reason string
}

func parseClassifierRows(out string) []classifierRow {
	var rows []classifierRow
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		row := classifierRow{class: parts[0], slug: parts[1]}
		if len(parts) >= 3 {
			row.status = parts[2]
		}
		if len(parts) >= 4 {
			row.reason = parts[3]
		}
		if row.class == "" || row.slug == "" {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func clearStateFiles(paths ...string) {
	for _, p := range paths {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

// envBool defaults to fallback when unset; explicit "0" -> false.
// Mirrors bash `[[ "$X" == 1 ]]` with X defaulting to "1".
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v != "0"
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func readFile(path string) string {
	if path == "" {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(body)
}

func readInt64File(path string) int64 {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func countInboxJSON(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.Mode()&fs.ModeType != 0 {
			continue
		}
		n++
	}
	return n
}
