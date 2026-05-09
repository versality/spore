// Package shrink computes harness-thinness metrics for a repo. The
// repo-shrink-probe daily user-timer (nix-config) walks a checkout
// once a day, hands the snapshot to the spore event bus, and the
// audit-refresh-cron consumer aggregates the resulting timeseries to
// surface drift.
package shrink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is the metric tuple emitted per probe. Field tags follow
// the schema in nix-config tasks/repo-shrink-probe-nixos.md.
type Snapshot struct {
	Repo         string    `json:"repo"`
	BashLoc      int       `json:"bash_loc"`
	BashFiles    int       `json:"bash_files"`
	WtgoLoc      int       `json:"wtgo_loc"`
	WtStateFiles int       `json:"wt_state_files"`
	HookCount    int       `json:"hook_count"`
	Ts           time.Time `json:"ts"`
}

// Options configures a Probe call.
type Options struct {
	// RepoRoot is the repo whose harness/ tree is measured. Required.
	RepoRoot string
	// WtStateDir is the wt task state dir. Empty means
	// $XDG_STATE_HOME/wt with ~/.local/state/wt as fallback.
	WtStateDir string
	// Now stamps Snapshot.Ts. nil means time.Now().UTC().
	Now func() time.Time
}

// Probe walks the repo and returns one Snapshot. Best-effort: a
// missing optional path (wt-go tree, wt state dir) maps to 0, never
// to an error. The repo root and harness/ tree are required.
func Probe(opts Options) (Snapshot, error) {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return Snapshot{}, errors.New("RepoRoot is required")
	}
	wtState := opts.WtStateDir
	if wtState == "" {
		wtState = defaultWtStateDir()
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	bashLoc, bashFiles, err := countLines(filepath.Join(opts.RepoRoot, "harness"), ".sh", true)
	if err != nil {
		return Snapshot{}, fmt.Errorf("harness bash: %w", err)
	}
	wtgoLoc, _, err := countLines(filepath.Join(opts.RepoRoot, "nix", "packages", "wt-go"), ".go", false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("wt-go: %w", err)
	}
	wtStateFiles, err := countFiles(wtState, false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("wt state: %w", err)
	}
	hookCount, err := countHooks(opts.RepoRoot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("hooks: %w", err)
	}

	return Snapshot{
		Repo:         opts.RepoRoot,
		BashLoc:      bashLoc,
		BashFiles:    bashFiles,
		WtgoLoc:      wtgoLoc,
		WtStateFiles: wtStateFiles,
		HookCount:    hookCount,
		Ts:           now().UTC(),
	}, nil
}

func defaultWtStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "wt")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "wt")
}

// countLines walks root recursively, summing line counts and file
// counts for files ending in suffix. When required is false, a
// missing root maps to (0, 0, nil); otherwise it is an error.
func countLines(root, suffix string, required bool) (int, int, error) {
	if root == "" {
		return 0, 0, nil
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) && !required {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	loc, files := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, suffix) {
			return nil
		}
		n, err := lineCount(path)
		if err != nil {
			return err
		}
		loc += n
		files++
		return nil
	})
	return loc, files, err
}

// lineCount returns the number of '\n' bytes in path. A file whose
// last line has no trailing newline still counts the bytes before it
// as one line so wc -l agrees on either shape.
func lineCount(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, nil
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n, nil
}

// countFiles walks root recursively and returns the regular-file
// count. When required is false, a missing root maps to (0, nil).
func countFiles(root string, required bool) (int, error) {
	if root == "" {
		return 0, nil
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) && !required {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	return count, err
}

// countHooks counts hook command leaves under
// configs/claude/{settings,settings-extras,hooks-config}.json. A
// "leaf" is any object with a string-valued "command" key. Missing
// files contribute 0.
func countHooks(repoRoot string) (int, error) {
	files := []string{
		filepath.Join(repoRoot, "configs", "claude", "settings.json"),
		filepath.Join(repoRoot, "configs", "claude", "settings-extras.json"),
		filepath.Join(repoRoot, "configs", "claude", "hooks-config.json"),
	}
	total := 0
	for _, p := range files {
		raw, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return 0, err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("parse %s: %w", p, err)
		}
		total += countCommands(v)
	}
	return total, nil
}

func countCommands(v any) int {
	switch x := v.(type) {
	case map[string]any:
		n := 0
		if cmd, ok := x["command"].(string); ok && strings.TrimSpace(cmd) != "" {
			n++
		}
		for _, child := range x {
			n += countCommands(child)
		}
		return n
	case []any:
		n := 0
		for _, child := range x {
			n += countCommands(child)
		}
		return n
	default:
		return 0
	}
}
