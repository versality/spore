// Package fleet implements the worker-fleet reconciler: a short-
// lived pass that brings tmux sessions in line with the on-disk
// task queue. For each tasks/<slug>.md with status=active that has
// no live session, Reconcile spawns one (worktree + branch + tmux);
// for each session whose task no longer has status=active, it kills
// the session. Idempotent and exit-on-done; no daemon main loop.
//
// A user-level kill switch lives at the FlagPath
// (~/.local/state/spore/fleet-enabled, honouring XDG_STATE_HOME).
// Empty/missing means paused: Reconcile returns immediately without
// touching tmux. Present (any contents) means enabled.
package fleet

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/task"
)

// DefaultMaxWorkers is the fallback concurrency cap when no override
// is set via flag, env, or spore.toml.
const DefaultMaxWorkers = 3

// Config drives a single reconcile pass.
type Config struct {
	TasksDir    string
	ProjectRoot string
	MaxWorkers  int
}

// Result is the outcome of one reconcile pass. Slug lists are
// sorted; sets are disjoint by construction (a slug appears in
// exactly one of Spawned / Kept / Skipped).
type Result struct {
	// Disabled is true when the kill-switch flag is missing and
	// Reconcile short-circuited. The slug lists are empty in that
	// case; the sessions already running (if any) are NOT reaped.
	Disabled bool

	Active  []string
	Spawned []string
	Reaped  []string
	Kept    []string
	Skipped []string
}

// Reconcile runs a single pass: list active tasks, list spore-prefix
// tmux sessions, reap stale sessions, then spawn missing ones up to
// the MaxWorkers cap. Honours the kill-switch flag at FlagPath. The
// singleton coordinator session is ensured alongside the worker fleet
// when the flag is on, and reaped when the flag goes off.
func Reconcile(cfg Config) (Result, error) {
	enabled, err := Enabled()
	if err != nil {
		return Result{}, err
	}
	if !enabled {
		// Worker sessions are kept alive on flag-disable so the
		// operator stays attached to in-flight work. The coordinator
		// is a kernel singleton with no operator-attached state worth
		// preserving, so we tear it down with the flag.
		ReapCoordinator(cfg.ProjectRoot)
		return Result{Disabled: true}, nil
	}

	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = DefaultMaxWorkers
	}

	if _, _, err := EnsureCoordinator(cfg.ProjectRoot); err != nil {
		return Result{}, fmt.Errorf("coordinator: %w", err)
	}

	metas, err := task.List(cfg.TasksDir)
	if err != nil {
		return Result{}, err
	}
	statusBySlug := map[string]string{}
	activeSet := map[string]bool{}
	for _, m := range metas {
		statusBySlug[m.Slug] = m.Status
		if m.Status == "active" {
			activeSet[m.Slug] = true
		}
	}

	running, err := task.SpawnedSlugs(cfg.ProjectRoot)
	if err != nil {
		return Result{}, err
	}
	// The coordinator shares the spore-session prefix but is not a
	// worker; filter it out before the reap loop and the cap math so
	// EnsureCoordinator above stays the sole owner of its lifecycle.
	var workerSlugs []string
	runningSet := map[string]bool{}
	for _, s := range running {
		if s == CoordinatorSlug {
			continue
		}
		workerSlugs = append(workerSlugs, s)
		runningSet[s] = true
	}

	res := Result{}

	// Reap first so freed slots count toward the cap on spawn.
	// Sessions for tasks the operator paused or blocked are kept
	// alive deliberately (the per-status semantics of pause/block).
	// Sessions whose task is done, missing, or in an unknown state
	// are reaped.
	for _, slug := range workerSlugs {
		switch statusBySlug[slug] {
		case "active", "paused", "blocked":
			res.Kept = append(res.Kept, slug)
			continue
		}
		if err := task.Reap(cfg.TasksDir, cfg.ProjectRoot, slug); err != nil {
			return res, fmt.Errorf("reap %s: %w", slug, err)
		}
		res.Reaped = append(res.Reaped, slug)
		delete(runningSet, slug)
	}

	// Stable iteration: sort active slugs before spawning so the
	// MaxWorkers cap picks the same prefix on every run.
	var actives []string
	for slug := range activeSet {
		actives = append(actives, slug)
	}
	sort.Strings(actives)
	res.Active = actives

	for _, slug := range actives {
		if runningSet[slug] {
			continue
		}
		if len(runningSet) >= cfg.MaxWorkers {
			res.Skipped = append(res.Skipped, slug)
			continue
		}
		if _, err := task.Ensure(cfg.TasksDir, slug); err != nil {
			return res, fmt.Errorf("ensure %s: %w", slug, err)
		}
		res.Spawned = append(res.Spawned, slug)
		runningSet[slug] = true
	}

	sort.Strings(res.Spawned)
	sort.Strings(res.Reaped)
	sort.Strings(res.Kept)
	sort.Strings(res.Skipped)
	return res, nil
}

// LoadMaxWorkers reads `[fleet] max_workers = N` from a spore.toml
// at projectRoot, falling back to DefaultMaxWorkers when missing.
// Mirrors the tiny TOML subset accepted by `internal/align`.
func LoadMaxWorkers(projectRoot string) (int, error) {
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultMaxWorkers, nil
		}
		return DefaultMaxWorkers, err
	}
	overrides, err := parseFleetTOML(string(b))
	if err != nil {
		return DefaultMaxWorkers, fmt.Errorf("fleet: parse %s: %w", tomlPath, err)
	}
	if v, ok := overrides["max_workers"]; ok {
		if v < 1 {
			return DefaultMaxWorkers, fmt.Errorf("fleet: max_workers must be >= 1, got %d", v)
		}
		return v, nil
	}
	return DefaultMaxWorkers, nil
}

func parseFleetTOML(content string) (map[string]int, error) {
	out := map[string]int{}
	inFleet := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inFleet = strings.TrimSpace(line[1:len(line)-1]) == "fleet"
			continue
		}
		if !inFleet {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if i := strings.IndexByte(val, '#'); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("line %d: key %q: want integer, got %q", lineNum, key, val)
		}
		out[key] = n
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// FlagPath returns the kill-switch path:
// `<XDG_STATE_HOME>/spore/fleet-enabled`, falling back to
// `$HOME/.local/state/spore/fleet-enabled` when XDG_STATE_HOME is
// unset. Reconcile short-circuits when this path is missing.
func FlagPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", errors.New("fleet: HOME and XDG_STATE_HOME both unset")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", "fleet-enabled"), nil
}

// Enable creates the kill-switch flag (along with parent dirs).
// Idempotent.
func Enable() error {
	p, err := FlagPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Disable removes the kill-switch flag. Idempotent (missing flag is
// a no-op).
func Disable() error {
	p, err := FlagPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Enabled reports whether the kill-switch flag is present.
func Enabled() (bool, error) {
	p, err := FlagPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
