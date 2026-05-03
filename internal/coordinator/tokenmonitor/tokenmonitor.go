// Package tokenmonitor wraps the budget short-window token context
// into a stop-hook shape for the coordinator. It reads the hook
// payload (session_id + transcript_path), sums input tokens from the
// latest assistant message's usage block, and fires soft/hard
// reminders when thresholds are crossed.
package tokenmonitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSoftCap = 150000
	DefaultHardCap = 190000
)

type Config struct {
	SoftCap    int
	HardCap    int
	StateDir   string
	LedgerFile string
	Inbox      string
}

type CheckResult struct {
	Ctx        int    `json:"ctx"`
	SoftCap    int    `json:"soft_cap"`
	HardCap    int    `json:"hard_cap"`
	Level      string `json:"level"`
	Message    string `json:"message,omitempty"`
	ShouldFire bool   `json:"should_fire"`
}

type HookPayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

func (c Config) Defaults() Config {
	if c.SoftCap <= 0 {
		c.SoftCap = DefaultSoftCap
	}
	if c.HardCap <= 0 {
		c.HardCap = DefaultHardCap
	}
	if c.StateDir == "" {
		c.StateDir = defaultStateDir()
	}
	if c.LedgerFile == "" {
		c.LedgerFile = filepath.Join(c.StateDir, "token-monitor.jsonl")
	}
	return c
}

// defaultStateDir resolves the coordinator state dir from the
// SPORE_COORDINATOR_STATE_DIR env var, falling back to
// $HOME/.local/state/spore/coordinator.
func defaultStateDir() string {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "spore", "coordinator")
}

// IsCoordinator returns true if the inbox path is under the state
// dir, matching the self-test logic from the bash version.
func (c Config) IsCoordinator() bool {
	if c.Inbox == "" {
		return false
	}
	stateRoot := strings.TrimRight(c.StateDir, "/")
	return c.Inbox == stateRoot || strings.HasPrefix(c.Inbox, stateRoot+"/")
}

// Check reads the transcript, sums context tokens, and determines
// whether a soft or hard cap has been crossed. The sessionID is used
// for the soft-marker (fire only once per session at soft cap).
func Check(cfg Config, payload HookPayload) CheckResult {
	cfg = cfg.Defaults()

	if !cfg.IsCoordinator() {
		return CheckResult{Level: "skip"}
	}

	transcript := payload.TranscriptPath
	if transcript == "" || !fileExists(transcript) {
		transcript = findFallbackTranscript()
	}
	if transcript == "" {
		return CheckResult{Level: "skip"}
	}

	ctx := sumContextTokens(transcript)
	if ctx <= 0 {
		return CheckResult{Level: "ok", Ctx: ctx, SoftCap: cfg.SoftCap, HardCap: cfg.HardCap}
	}

	markerDir := filepath.Join(cfg.StateDir, "token-monitor")
	os.MkdirAll(markerDir, 0o700)
	sid := payload.SessionID
	if sid == "" {
		sid = "unknown"
	}
	softMarker := filepath.Join(markerDir, sid+".soft")

	result := CheckResult{
		Ctx:     ctx,
		SoftCap: cfg.SoftCap,
		HardCap: cfg.HardCap,
	}

	if ctx >= cfg.HardCap {
		result.Level = "hard"
		result.ShouldFire = true
		result.Message = fmt.Sprintf(
			"COORDINATOR TOKEN MONITOR (hard): context %d tokens >= hard cap %d.\n"+
				"Wrap up NOW:\n"+
				"  1. Flush state.md so the next coordinator boots from it.\n"+
				"  2. Post a one-line summary to the operator if anything is still open.\n"+
				"  3. Run: tmux kill-session\n"+
				"The reconciler respawns a fresh coordinator from state.md.",
			ctx, cfg.HardCap)
		appendLedger(cfg, sid, ctx, false, true)
		return result
	}

	if ctx >= cfg.SoftCap && !fileExists(softMarker) {
		touch(softMarker)
		result.Level = "soft"
		result.ShouldFire = true
		result.Message = fmt.Sprintf(
			"COORDINATOR TOKEN MONITOR (soft): context %d tokens >= soft warn %d.\n"+
				"Wrap up at the next natural break: flush state.md, then run\n"+
				"  tmux kill-session\n"+
				"The reconciler respawns a fresh coordinator from state.md. Hard cap is %d;\n"+
				"crossing it forces a wrap-up reminder on every Stop.",
			ctx, cfg.SoftCap, cfg.HardCap)
		appendLedger(cfg, sid, ctx, true, false)
		return result
	}

	result.Level = "ok"
	appendLedger(cfg, sid, ctx, false, false)
	return result
}

// sumContextTokens reads the transcript JSONL and sums
// input_tokens + cache_creation_input_tokens + cache_read_input_tokens
// from the last assistant message's usage block.
func sumContextTokens(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"role":"assistant"`) {
			lastLine = line
		}
	}
	if lastLine == "" {
		return 0
	}

	return extractContextFromLine(lastLine)
}

func extractContextFromLine(line string) int {
	usageRE := regexp.MustCompile(`"usage"\s*:\s*\{`)
	locs := usageRE.FindAllStringIndex(line, -1)
	if len(locs) == 0 {
		return 0
	}
	lastLoc := locs[len(locs)-1]
	start := lastLoc[1] - 1

	depth := 0
	end := start
	for i := start; i < len(line); i++ {
		switch line[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				goto done
			}
		}
	}
done:
	if end <= start {
		return 0
	}

	block := line[start:end]

	var usage map[string]json.RawMessage
	if json.Unmarshal([]byte(block), &usage) != nil {
		return 0
	}

	sum := 0
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if raw, ok := usage[key]; ok {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				sum += n
			}
		}
	}
	return sum
}

func appendLedger(cfg Config, sessionID string, ctx int, softFired, hardFired bool) {
	os.MkdirAll(filepath.Dir(cfg.LedgerFile), 0o700)
	f, err := os.OpenFile(cfg.LedgerFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(f, `{"ts":"%s","session_id":"%s","ctx":%d,"soft_cap":%d,"hard_cap":%d,"soft_fired":%s,"hard_fired":%s}`+"\n",
		ts, sessionID, ctx, cfg.SoftCap, cfg.HardCap,
		strconv.FormatBool(softFired), strconv.FormatBool(hardFired))
}

func findFallbackTranscript() string {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	encoded := strings.ReplaceAll(cwd, "/", "-")
	home, _ := os.UserHomeDir()
	projDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}

	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || fi.ModTime().After(newestTime) {
			newest = filepath.Join(projDir, e.Name())
			newestTime = fi.ModTime()
		}
	}
	return newest
}

// LedgerVerdict reads the token-monitor ledger and returns whether
// the trailing N signal-bearing sessions are all "broken" (crossed soft
// cap but never fired soft or hard). Returns (broken, sessionIDs).
func LedgerVerdict(ledgerFile string, softCap int, threshold int) (bool, string) {
	f, err := os.Open(ledgerFile)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	type sessionInfo struct {
		peak    int
		cap     int
		anySoft bool
		anyHard bool
	}

	var order []string
	sessions := make(map[string]*sessionInfo)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		var row struct {
			SessionID string `json:"session_id"`
			Ctx       int    `json:"ctx"`
			SoftCap   int    `json:"soft_cap"`
			SoftFired bool   `json:"soft_fired"`
			HardFired bool   `json:"hard_fired"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.SessionID == "" {
			continue
		}
		info, exists := sessions[row.SessionID]
		if !exists {
			info = &sessionInfo{}
			sessions[row.SessionID] = info
			order = append(order, row.SessionID)
		}
		if row.Ctx > info.peak {
			info.peak = row.Ctx
		}
		if row.SoftCap > 0 {
			info.cap = row.SoftCap
		}
		if row.SoftFired {
			info.anySoft = true
		}
		if row.HardFired {
			info.anyHard = true
		}
	}

	var signal []string
	for _, sid := range order {
		info := sessions[sid]
		cap := info.cap
		if cap <= 0 {
			cap = softCap
		}
		if info.peak >= cap && cap > 0 {
			signal = append(signal, sid)
		}
	}

	run := 0
	for i := len(signal) - 1; i >= 0; i-- {
		info := sessions[signal[i]]
		if !info.anySoft && !info.anyHard {
			run++
		} else {
			break
		}
	}

	if run >= threshold {
		start := len(signal) - run
		broken := ""
		for i := start; i < len(signal); i++ {
			if broken != "" {
				broken += " "
			}
			broken += signal[i]
		}
		return true, broken
	}
	return false, ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func touch(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		f.Close()
	}
}
