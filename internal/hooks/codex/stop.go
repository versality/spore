package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/coordinator/state"
	"github.com/versality/spore/internal/transcript"
)

// StopConfig parameterizes the Codex Stop adapter. Empty fields fall
// back to the corresponding env vars / defaults at call time.
type StopConfig struct {
	// Inbox is the session inbox path ($SPORE_TASK_INBOX). Used to
	// classify the session as coordinator / worker / ad-hoc.
	Inbox string
	// CoordinatorStateDir gates the codex context monitor.
	CoordinatorStateDir string
	// WorkerStateDir is the parent dir for worker slugs ($WT_STATE).
	// Used to classify a worker inbox for the inbox drain.
	WorkerStateDir string
	// Driver is the coordinator agent driver ("codex" or "claude").
	// The codex context monitor only runs when this is "codex".
	Driver string
	// SoftCap / HardCap define context-budget thresholds for the
	// coordinator. Zero falls back to DefaultCoordSoftCap /
	// DefaultCoordHardCap.
	SoftCap int
	HardCap int
	// Chain is the ordered sub-hook chain run after the context
	// monitor + inbox drain. Each hook receives the original payload
	// on stdin. Exit 2 from any hook propagates and terminates the
	// chain.
	Chain []ChainHook
	// CommandTimeout is the per-sub-hook timeout. Zero defaults to
	// DefaultChainTimeout.
	CommandTimeout time.Duration
	// Now is injected for tests.
	Now func() time.Time
}

// ChainHook is one step of the post-monitor sub-hook chain. Argv
// is the command + args; stdin is piped from the original payload.
type ChainHook struct {
	Argv    []string
	Timeout time.Duration
}

const (
	// DefaultChainTimeout matches the bash adapter's 8s default.
	DefaultChainTimeout = 8 * time.Second
)

// StopPayload is the subset of the Codex Stop hook payload we read.
type StopPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// StopResult is the adapter's verdict for the caller. ExitCode is the
// process exit to return; Stderr is the text the caller should write
// to its stderr.
type StopResult struct {
	ExitCode int
	Stderr   string
}

// Stop is the Codex Stop hook adapter entry point. It reads the
// payload from r, runs the codex context monitor (when this is a
// coordinator session), drains the worker inbox (when this is a
// worker session), and runs the sub-hook chain. Exit-2 conditions at
// any stage stop further processing and propagate.
func Stop(cfg StopConfig, r io.Reader) StopResult {
	cfg = cfg.stopDefaults()

	body, err := io.ReadAll(r)
	if err != nil {
		return StopResult{ExitCode: 1, Stderr: fmt.Sprintf("read stdin: %v\n", err)}
	}
	var payload StopPayload
	_ = json.Unmarshal(body, &payload)

	if res := codexContextMonitor(cfg, payload); res.ShouldExit2 {
		return StopResult{ExitCode: 2, Stderr: res.Message}
	}

	if res := codexStuckToolcallCheck(cfg, payload); res.ShouldExit2 {
		return StopResult{ExitCode: 2, Stderr: res.Message}
	}

	if drained := drainWorkerInbox(cfg); drained.Count > 0 {
		msg := fmt.Sprintf(
			"CODEX WORKER INBOX: %d message(s) moved to %s/read. Read the JSON files there, handle them, then continue.\n",
			drained.Count, drained.Dir)
		return StopResult{ExitCode: 2, Stderr: msg}
	}

	chainRes := runChain(cfg, body)
	if chainRes.ExitCode == 2 {
		return StopResult{ExitCode: 2, Stderr: chainRes.Stderr}
	}

	return StopResult{ExitCode: 0, Stderr: chainRes.Stderr}
}

func (c StopConfig) stopDefaults() StopConfig {
	if c.SoftCap <= 0 {
		c.SoftCap = DefaultCoordSoftCap
	}
	if c.HardCap <= 0 {
		c.HardCap = DefaultCoordHardCap
	}
	if c.CommandTimeout <= 0 {
		c.CommandTimeout = DefaultChainTimeout
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	return c
}

// DefaultCoordSoftCap / DefaultCoordHardCap mirror the bash adapter's
// defaults.
const (
	DefaultCoordSoftCap = 150000
	DefaultCoordHardCap = 190000
)

// contextResult captures whether the codex context monitor wants to
// trip the Stop hook (exit 2) and the human-readable message it
// emitted to do so.
type contextResult struct {
	ShouldExit2 bool
	Level       string
	Ctx         int
	Message     string
}

// codexContextMonitor inspects the codex transcript's session_meta /
// token_count events, validates the session matches, logs a ledger
// entry, and on a soft / hard cap crossing returns ShouldExit2 with
// the wrap-up message. Skipped sessions log a "skipped" reason and
// return ShouldExit2=false.
func codexContextMonitor(cfg StopConfig, payload StopPayload) contextResult {
	if cfg.Driver != "codex" {
		return contextResult{}
	}
	if !inboxUnderRoot(cfg.Inbox, cfg.CoordinatorStateDir) {
		return contextResult{}
	}

	sid := payload.SessionID
	if sid == "" {
		sid = "unknown"
	}

	tpath := payload.TranscriptPath
	if tpath == "" {
		logCodexContextSkip(cfg, sid, "missing-transcript-path", "")
		return contextResult{}
	}
	info, err := os.Stat(tpath)
	if err != nil {
		logCodexContextSkip(cfg, sid, "transcript-not-found", tpath)
		return contextResult{}
	}
	if info.Mode().Perm()&0o400 == 0 {
		logCodexContextSkip(cfg, sid, "transcript-not-readable", tpath)
		return contextResult{}
	}

	transcriptSession, ok := transcript.CodexSessionID(tpath)
	if !ok {
		logCodexContextSkip(cfg, sid, "missing-session-meta", tpath)
		return contextResult{}
	}
	if sid != transcriptSession {
		logCodexContextSkip(cfg, sid, "session-mismatch", tpath)
		return contextResult{}
	}
	ctx, ok := transcript.CodexLastTokenCount(tpath)
	if !ok || ctx <= 0 {
		logCodexContextSkip(cfg, sid, "missing-token-count", tpath)
		return contextResult{}
	}
	source := "codex-last-token-count"
	bytes := info.Size()

	if ctx >= cfg.HardCap {
		snapshotStateBeforeWrap(cfg, "hard", ctx, source)
		appendCodexContextLedger(cfg, sid, ctx, source, bytes, false, true)
		return contextResult{
			ShouldExit2: true,
			Level:       "hard",
			Ctx:         ctx,
			Message: fmt.Sprintf(
				"CODEX COORDINATOR CONTEXT MONITOR (hard): context %d tokens (%s) >= hard cap %d.\n"+
					"Wrap up NOW: flush state.md and post any needed operator summary, then run `spore coordinator restart` to respawn the coordinator session.\n",
				ctx, source, cfg.HardCap),
		}
	}

	markerDir := filepath.Join(cfg.CoordinatorStateDir, "codex-context-monitor")
	marker := filepath.Join(markerDir, sid+".soft")
	if ctx >= cfg.SoftCap {
		if !fileExists(marker) {
			os.MkdirAll(markerDir, 0o700)
			touch(marker)
			snapshotStateBeforeWrap(cfg, "soft", ctx, source)
			appendCodexContextLedger(cfg, sid, ctx, source, bytes, true, false)
			return contextResult{
				ShouldExit2: true,
				Level:       "soft",
				Ctx:         ctx,
				Message: fmt.Sprintf(
					"CODEX COORDINATOR CONTEXT MONITOR (soft): context %d tokens (%s) >= soft warn %d.\n"+
						"Wrap at the next natural break: flush state.md and post any needed operator summary, then run `spore coordinator restart` to respawn the coordinator session.\n",
					ctx, source, cfg.SoftCap),
			}
		}
	}

	appendCodexContextLedger(cfg, sid, ctx, source, bytes, false, false)
	return contextResult{Level: "ok", Ctx: ctx}
}

// drainResult is the outcome of the worker-inbox drain.
type drainResult struct {
	Count int
	Dir   string
}

// drainWorkerInbox moves *.json files at the top of the worker inbox
// into inbox/read/. It only runs when the configured inbox looks like
// a worker inbox (basename "inbox" with grandparent matching
// WorkerStateDir).
func drainWorkerInbox(cfg StopConfig) drainResult {
	if !isWorkerInbox(cfg) {
		return drainResult{}
	}
	inbox := cfg.Inbox
	readDir := filepath.Join(inbox, "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		return drainResult{}
	}

	entries, err := os.ReadDir(inbox)
	if err != nil {
		return drainResult{}
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		src := filepath.Join(inbox, e.Name())
		dst := filepath.Join(readDir, e.Name())
		if err := os.Rename(src, dst); err == nil {
			count++
		}
	}
	return drainResult{Count: count, Dir: inbox}
}

func isWorkerInbox(cfg StopConfig) bool {
	if cfg.Inbox == "" {
		return false
	}
	if filepath.Base(cfg.Inbox) != "inbox" {
		return false
	}
	if cfg.WorkerStateDir == "" {
		return false
	}
	parent := filepath.Dir(cfg.Inbox)
	gp := filepath.Dir(parent)
	root, err := filepath.Abs(strings.TrimRight(cfg.WorkerStateDir, "/"))
	if err != nil {
		return false
	}
	got, err := filepath.Abs(gp)
	if err != nil {
		return false
	}
	return got == root
}

// chainResult bundles the chain's exit verdict + accumulated stderr.
type chainResult struct {
	ExitCode int
	Stderr   string
}

// runChain runs cfg.Chain sequentially, piping payload to each on
// stdin. Any child failure stops the chain and returns exit 2 with a
// continuation prompt so Codex surfaces the lifecycle failure instead of
// treating stderr on an exit-0 adapter as ignorable output.
func runChain(cfg StopConfig, payload []byte) chainResult {
	var stderr strings.Builder
	for _, h := range cfg.Chain {
		if len(h.Argv) == 0 {
			continue
		}
		timeout := h.Timeout
		if timeout <= 0 {
			timeout = cfg.CommandTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, h.Argv[0], h.Argv[1:]...)
		cmd.Stdin = strings.NewReader(string(payload))
		var combined strings.Builder
		cmd.Stdout = &combined
		cmd.Stderr = &combined
		err := cmd.Run()
		cancel()

		timedOut := ctx.Err() == context.DeadlineExceeded
		if err == nil {
			continue
		}
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			fmt.Fprintf(&stderr, "spore hooks codex stop: chain %v: %v\n", h.Argv, err)
			appendWorkerStopError(cfg, "spawn-error", h.Argv, -1, fmt.Sprintf("%v: %s", err, combined.String()))
			return chainResult{ExitCode: 2, Stderr: stderr.String()}
		}
		rc := exitErr.ExitCode()
		switch {
		case rc == 2:
			stderr.WriteString(combined.String())
			return chainResult{ExitCode: 2, Stderr: stderr.String()}
		case timedOut || rc == 124 || rc == 137:
			fmt.Fprintf(&stderr, "spore hooks codex stop: timed out after %s: %v\n", timeout, h.Argv)
			appendWorkerStopError(cfg, "timeout", h.Argv, rc, combined.String())
			return chainResult{ExitCode: 2, Stderr: stderr.String()}
		default:
			fmt.Fprintf(&stderr, "spore hooks codex stop: %v exited %d\n", h.Argv, rc)
			appendWorkerStopError(cfg, "exit", h.Argv, rc, combined.String())
			return chainResult{ExitCode: 2, Stderr: stderr.String()}
		}
	}
	return chainResult{ExitCode: 0, Stderr: stderr.String()}
}

// snapshotStateBeforeWrap merges a recent-events bullet into state.md
// before the wrap reminder fires, so a fresh coordinator booted from
// state.md keeps a pointer to the most recent context-monitor event.
// Existing sections (operator-questions, Active tasks, Rules, prior
// Recent-events bullets, etc.) are preserved. Best-effort; any failure
// here is silent (the wrap message is the primary signal).
func snapshotStateBeforeWrap(cfg StopConfig, level string, ctx int, source string) {
	if cfg.CoordinatorStateDir == "" {
		return
	}
	if err := os.MkdirAll(cfg.CoordinatorStateDir, 0o700); err != nil {
		return
	}
	stateFile := filepath.Join(cfg.CoordinatorStateDir, "state.md")
	now := cfg.Now().Format(time.RFC3339)
	bullet := fmt.Sprintf("- %s codex-context-monitor: auto-snapshotted state before %s wrap prompt; ctx=%d source=%s",
		now, level, ctx, source)

	existing, _ := os.ReadFile(stateFile)
	var body []byte
	if len(strings.TrimSpace(string(existing))) == 0 {
		body = []byte(fmt.Sprintf(
			"# coordinator state - last updated %s\n\n## Recent events\n\n%s\n",
			now, bullet))
	} else {
		body = mergeRecentEvent(existing, bullet, now)
	}

	tmp := stateFile + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, stateFile)
}

// mergeRecentEvent appends a new Recent-events bullet to state.md while
// preserving all other sections. If `## Recent events` is missing, it
// is inserted at the end of the document. The leading
// `# coordinator state - last updated <ts>` line, if present, has its
// timestamp refreshed; otherwise the document body is left as-is.
func mergeRecentEvent(existing []byte, bullet, now string) []byte {
	doc := state.Parse(existing)
	if len(doc.Sections) == 0 {
		return []byte(fmt.Sprintf(
			"# coordinator state - last updated %s\n\n## Recent events\n\n%s\n",
			now, bullet))
	}

	if doc.Sections[0].Level == 0 {
		doc.Sections[0].Body = refreshCoordinatorHeading(doc.Sections[0].Body, now)
	}

	if sec := doc.FindSection("Recent events"); sec != nil {
		if sec.Body == "" {
			sec.Body = bullet
		} else {
			sec.Body = strings.TrimRight(sec.Body, "\n") + "\n" + bullet
		}
	} else {
		doc.Sections = append(doc.Sections, state.Section{
			Level:   2,
			Heading: "Recent events",
			Body:    bullet,
		})
	}

	return state.Write(doc)
}

func refreshCoordinatorHeading(body, now string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# coordinator state") {
			lines[i] = "# coordinator state - last updated " + now
			return strings.Join(lines, "\n")
		}
	}
	return body
}

// appendWorkerStopError writes one line to worker-stop-errors.jsonl so
// the coordinator boot probe surfaces unresolved sub-hook failures.
// kind is "timeout" | "exit" | "spawn-error". rc is the child's exit
// code (-1 for spawn errors). stderr is truncated to 4 KiB.
func appendWorkerStopError(cfg StopConfig, kind string, argv []string, rc int, childStderr string) {
	dir := cfg.WorkerStateDir
	if dir == "" {
		dir = cfg.CoordinatorStateDir
	}
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	const maxStderr = 4096
	if len(childStderr) > maxStderr {
		childStderr = childStderr[len(childStderr)-maxStderr:]
	}
	row := struct {
		TS     string   `json:"ts"`
		Slug   string   `json:"slug"`
		Kind   string   `json:"kind"`
		RC     int      `json:"rc"`
		Argv   []string `json:"argv"`
		Stderr string   `json:"stderr,omitempty"`
	}{
		TS:     cfg.Now().Format(time.RFC3339),
		Slug:   stopSlug(cfg),
		Kind:   kind,
		RC:     rc,
		Argv:   argv,
		Stderr: childStderr,
	}
	line, err := json.Marshal(&row)
	if err != nil {
		return
	}
	appendFile(filepath.Join(dir, "worker-stop-errors.jsonl"), string(line)+"\n")
}

// stopSlug derives a short identifier for the current Stop-hook
// context. For worker inboxes laid out as <WorkerStateDir>/<slug>/inbox
// it returns the slug. Coordinator sessions return "coordinator".
// Anything else falls back to the inbox's parent basename or "".
func stopSlug(cfg StopConfig) string {
	if isWorkerInbox(cfg) {
		return filepath.Base(filepath.Dir(cfg.Inbox))
	}
	if cfg.CoordinatorStateDir != "" && inboxUnderRoot(cfg.Inbox, cfg.CoordinatorStateDir) {
		return "coordinator"
	}
	if cfg.Inbox != "" {
		return filepath.Base(filepath.Dir(cfg.Inbox))
	}
	return ""
}

func appendCodexContextLedger(cfg StopConfig, sid string, ctx int, source string, sizeBytes int64, soft, hard bool) {
	if cfg.CoordinatorStateDir == "" {
		return
	}
	if err := os.MkdirAll(cfg.CoordinatorStateDir, 0o700); err != nil {
		return
	}
	ts := cfg.Now().Format(time.RFC3339)
	line := fmt.Sprintf(
		`{"ts":"%s","session_id":%s,"ctx":%d,"soft_cap":%d,"hard_cap":%d,"soft_fired":%t,"hard_fired":%t,"source":%s,"bytes":%d}`+"\n",
		ts, jsonString(sid), ctx, cfg.SoftCap, cfg.HardCap, soft, hard, jsonString(source), sizeBytes)
	for _, name := range []string{"codex-context-monitor.jsonl", "token-monitor.jsonl"} {
		path := filepath.Join(cfg.CoordinatorStateDir, name)
		appendFile(path, line)
	}
}

func logCodexContextSkip(cfg StopConfig, sid, reason, transcriptPath string) {
	if cfg.CoordinatorStateDir == "" {
		return
	}
	if err := os.MkdirAll(cfg.CoordinatorStateDir, 0o700); err != nil {
		return
	}
	ts := cfg.Now().Format(time.RFC3339)
	line := fmt.Sprintf(
		`{"ts":"%s","session_id":%s,"event":"skipped","reason":%s,"transcript_path":%s}`+"\n",
		ts, jsonString(sid), jsonString(reason), jsonString(transcriptPath))
	appendFile(filepath.Join(cfg.CoordinatorStateDir, "codex-context-monitor.jsonl"), line)
}

func appendFile(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func touch(p string) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		f.Close()
	}
}
