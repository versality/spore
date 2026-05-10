// Package contexttee writes a per-session JSON token-usage snapshot
// at end-of-turn so external surfaces (claude-code statusLine, tmux
// status-right) can render context-budget without re-walking the
// transcript or shelling spore. Never gates the Stop chain.
//
// Identity matches the wrap monitors: an inbox under the coordinator
// state dir is a coordinator session; any other inbox under a
// per-slug parent is a worker session; an empty inbox is ad-hoc and
// skipped.
package contexttee

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/transcript"
)

const (
	DefaultCoordSoftCap  = 150000
	DefaultCoordHardCap  = 190000
	DefaultWorkerWrapMax = 180000
	DefaultWorkerWrapSub = 120000
)

// Config parameterizes the tee. Path resolution and cap selection are
// pure functions of these fields plus the inbox layout.
type Config struct {
	// Inbox is the session inbox ($SPORE_TASK_INBOX). Empty inbox
	// no-ops.
	Inbox string
	// CoordinatorStateDir gates the coordinator/worker decision.
	CoordinatorStateDir string
	// CoordTokenFile is the coordinator tee path. Defaults to
	// <CoordinatorStateDir>/token.json when zero.
	CoordTokenFile string
	// WorkerTokenDir is the parent dir for per-slug worker tees.
	// Defaults to $HOME/.local/state/spore/worker-token.
	WorkerTokenDir string
	// Tier is the account tier ($SPORE_ACCOUNT_TIER). "max" picks the
	// max-tier worker cap; everything else picks the sub-max cap.
	Tier string
	// Caps. Zero falls back to the Default* constants.
	CoordSoftCap  int
	CoordHardCap  int
	WorkerWrapMax int
	WorkerWrapSub int
	// WorkerWrapOverride forces a worker wrap cap regardless of tier
	// (test/debug, mirroring SPORE_WORKER_TOKEN_WRAP).
	WorkerWrapOverride int
	// Now is injected for tests.
	Now func() time.Time
}

func (c Config) defaults() Config {
	if c.CoordSoftCap <= 0 {
		c.CoordSoftCap = DefaultCoordSoftCap
	}
	if c.CoordHardCap <= 0 {
		c.CoordHardCap = DefaultCoordHardCap
	}
	if c.WorkerWrapMax <= 0 {
		c.WorkerWrapMax = DefaultWorkerWrapMax
	}
	if c.WorkerWrapSub <= 0 {
		c.WorkerWrapSub = DefaultWorkerWrapSub
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.WorkerTokenDir == "" {
		home, _ := os.UserHomeDir()
		c.WorkerTokenDir = filepath.Join(home, ".local", "state", "spore", "worker-token")
	}
	return c
}

type Payload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// TokenJSON is the on-disk schema. Always present; empty values
// reflect missing inputs (e.g. ctx=0 when no transcript was found).
type TokenJSON struct {
	Ts        string `json:"ts"`
	Slug      string `json:"slug"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Tier      string `json:"tier"`
	Ctx       int    `json:"ctx"`
	CapSoft   int    `json:"cap_soft"`
	CapHard   int    `json:"cap_hard"`
	Pct       int    `json:"pct"`
}

// Result reports what the tee did. Skipped is true when there was no
// inbox, no transcript, or any unrecoverable I/O error.
type Result struct {
	Skipped bool
	Path    string
	JSON    TokenJSON
}

// Run reads the payload, resolves role/cap, sums context tokens, and
// writes the JSON tee. Failure modes (no inbox, missing transcript,
// disk write error) all collapse into Skipped=true so the Stop chain
// keeps moving.
func Run(cfg Config, r io.Reader) (Result, error) {
	cfg = cfg.defaults()

	role, slug, outFile := resolve(cfg)
	if role == "" {
		return Result{Skipped: true}, nil
	}

	body, err := io.ReadAll(r)
	if err != nil || len(body) == 0 {
		return Result{Skipped: true}, err
	}
	var p Payload
	_ = json.Unmarshal(body, &p)

	tpath := p.TranscriptPath
	if tpath == "" || !fileExists(tpath) {
		tpath = transcript.FindFallbackTranscript()
	}
	if tpath == "" {
		return Result{Skipped: true}, nil
	}

	ctx := transcript.SumContextTokens(tpath)
	capSoft, capHard := caps(cfg, role)
	pct := 0
	if capHard > 0 {
		pct = (ctx * 100) / capHard
	}
	sid := p.SessionID
	if sid == "" {
		sid = "unknown"
	}
	tier := cfg.Tier
	if tier == "" {
		tier = "unknown"
	}
	out := TokenJSON{
		Ts:        cfg.Now().Format(time.RFC3339),
		Slug:      slug,
		SessionID: sid,
		Role:      role,
		Tier:      tier,
		Ctx:       ctx,
		CapSoft:   capSoft,
		CapHard:   capHard,
		Pct:       pct,
	}
	if err := writeAtomic(outFile, out); err != nil {
		return Result{Skipped: true}, err
	}
	return Result{Path: outFile, JSON: out}, nil
}

// resolve returns (role, slug, outFile) for the configured inbox, or
// ("", "", "") to skip.
func resolve(cfg Config) (string, string, string) {
	if cfg.Inbox == "" {
		return "", "", ""
	}
	if cfg.CoordinatorStateDir != "" {
		root := strings.TrimRight(cfg.CoordinatorStateDir, "/")
		if cfg.Inbox == root || strings.HasPrefix(cfg.Inbox, root+"/") {
			out := cfg.CoordTokenFile
			if out == "" {
				out = filepath.Join(cfg.CoordinatorStateDir, "token.json")
			}
			return "coordinator", "coordinator", out
		}
	}
	parent := filepath.Base(filepath.Dir(cfg.Inbox))
	if parent == "" || parent == "." || parent == "/" {
		return "", "", ""
	}
	out := filepath.Join(cfg.WorkerTokenDir, parent+".json")
	return "worker", parent, out
}

func caps(cfg Config, role string) (int, int) {
	if role == "coordinator" {
		return cfg.CoordSoftCap, cfg.CoordHardCap
	}
	cap := cfg.WorkerWrapSub
	if cfg.WorkerWrapOverride > 0 {
		cap = cfg.WorkerWrapOverride
	} else if cfg.Tier == "max" {
		cap = cfg.WorkerWrapMax
	}
	return cap, cap
}

func writeAtomic(path string, v TokenJSON) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	old := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	f, err := os.OpenFile(tmp, old, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
