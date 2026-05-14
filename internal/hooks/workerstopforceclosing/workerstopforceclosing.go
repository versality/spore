// Package workerstopforceclosing is the worker-side Stop-hook stage
// that refuses to let the worker end a turn without taking a closing
// move (ship/done/block-with-question/edit).
//
// Decision boundary (any signal -> exit 0; otherwise exit 2):
//
//  1. State file missing (first turn since the hook landed): noop.
//  2. tasks/<slug>.md status != "active": closing move (done/blocked).
//  3. Status flipped since last Stop (active -> done/blocked): closing move.
//  4. wt/<slug> HEAD advanced since last Stop: a commit shipped.
//  5. Transcript shows Edit / Write / MultiEdit tool_use since last Stop.
//
// The hook updates its state file on every invocation so a subsequent
// idle turn re-fires (no fingerprint dedup; see brief).
package workerstopforceclosing

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

// Result is the hook's verdict.
type Result struct {
	ExitCode int
	Stderr   string
	Reason   string
}

// Config parameterizes one Run call. Empty fields fall back to env
// defaults via RunEnv; tests set every field explicitly.
type Config struct {
	Slug           string
	Worktree       string
	StateDir       string
	TranscriptPath string
	Now            func() time.Time
	Head           func(worktree string) string
	ToolUsesSince  func(transcriptPath string, since time.Time) []string
}

type state struct {
	LastStopAt time.Time `json:"last_stop_at"`
	LastHead   string    `json:"last_head"`
	LastStatus string    `json:"last_status"`
}

const reminder = "worker idle without closing move; this turn produced no ship/done/block-with-question/edit on %s. " +
	"Either commit + wt merge, run wt task done, run wt task block + wt task tell coordinator \"<question>\", " +
	"or make a decision and act. Do NOT end this turn idle.\n"

// Run evaluates the decision boundary and returns the Result.
func Run(cfg Config) (Result, error) {
	cfg = cfg.defaults()
	if cfg.Slug == "" || cfg.Worktree == "" {
		return Result{Reason: "noop-context"}, nil
	}

	taskFile := filepath.Join(cfg.Worktree, "tasks", cfg.Slug+".md")
	raw, err := os.ReadFile(taskFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{Reason: "missing-task"}, nil
		}
		return Result{}, fmt.Errorf("read task: %w", err)
	}
	meta, _, err := frontmatter.Parse(raw)
	if err != nil {
		return Result{Reason: "parse-error"}, nil
	}

	head := cfg.Head(cfg.Worktree)

	statePath := cfg.statePath()
	prev, hasPrev := readState(statePath)

	newState := state{
		LastStopAt: cfg.Now(),
		LastHead:   head,
		LastStatus: meta.Status,
	}

	res := decide(cfg, meta.Status, head, prev, hasPrev)

	if err := writeState(statePath, newState); err != nil {
		// State write failure is not fatal: log via stderr but keep the
		// computed verdict so we don't mask a legitimate fire.
		fmt.Fprintln(os.Stderr, "spore hooks worker-stop-force-closing: write state:", err)
	}
	return res, nil
}

func decide(cfg Config, status, head string, prev state, hasPrev bool) Result {
	if !hasPrev {
		return Result{Reason: "first-turn"}
	}
	if status != "active" {
		return Result{Reason: "not-active"}
	}
	if prev.LastStatus != "" && prev.LastStatus != status {
		return Result{Reason: "status-flipped"}
	}
	if head != "" && prev.LastHead != "" && head != prev.LastHead {
		return Result{Reason: "head-advanced"}
	}
	if cfg.TranscriptPath != "" && cfg.ToolUsesSince != nil {
		for _, k := range cfg.ToolUsesSince(cfg.TranscriptPath, prev.LastStopAt) {
			switch k {
			case "Edit", "Write", "MultiEdit", "NotebookEdit":
				return Result{Reason: "edit-in-transcript"}
			}
		}
	}
	branch := "wt/" + cfg.Slug
	return Result{
		ExitCode: 2,
		Stderr:   fmt.Sprintf(reminder, branch),
		Reason:   "fire",
	}
}

func (c Config) defaults() Config {
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Head == nil {
		c.Head = gitHead
	}
	if c.ToolUsesSince == nil {
		c.ToolUsesSince = ScanClaudeToolUses
	}
	return c
}

func (c Config) statePath() string {
	if c.StateDir == "" || c.Slug == "" {
		return ""
	}
	return filepath.Join(c.StateDir, c.Slug+".json")
}

func readState(path string) (state, bool) {
	if path == "" {
		return state{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return state{}, false
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return state{}, false
	}
	return s, true
}

func writeState(path string, s state) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func gitHead(worktree string) string {
	cmd := exec.Command("git", "-c", "safe.directory="+worktree, "rev-parse", "HEAD")
	cmd.Dir = worktree
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// ScanClaudeToolUses walks a claude-code JSONL transcript and returns
// the names of tool_use entries with a timestamp strictly newer than
// `since`. Lines without parseable timestamps are included (worst case:
// a Edit/Write counts as closing-move, which is the safe direction).
func ScanClaudeToolUses(path string, since time.Time) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var out []string
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"tool_use"`)) {
			continue
		}
		var row struct {
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		if row.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, row.Timestamp); err == nil {
				if !ts.After(since) {
					continue
				}
			}
		}
		for _, c := range row.Message.Content {
			if c.Type == "tool_use" && c.Name != "" {
				out = append(out, c.Name)
			}
		}
	}
	return out
}

// RunEnv reads worker env and dispatches to Run. Silent no-op outside
// a worker context so the Stop chain stays clean.
func RunEnv(transcriptPath string) (Result, error) {
	slug := os.Getenv("SPORE_TASK_SLUG")
	if slug == "" {
		return Result{Reason: "noop-context"}, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return Result{Reason: "noop-context"}, nil
	}
	return Run(Config{
		Slug:           slug,
		Worktree:       wd,
		StateDir:       defaultStateDir(),
		TranscriptPath: transcriptPath,
	})
}

func defaultStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "spore", "worker-stop-force-closing")
}
