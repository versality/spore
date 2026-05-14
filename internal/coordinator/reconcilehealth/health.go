// Package reconcilehealth consumes the reconciler's health snapshot
// at $WT_STATE/reconcile-health.json. The reconciler (wt-go) writes
// this file on every tick; spore reads it from `spore coordinator
// monitor` and the boot probe to surface dirty-main wedges,
// mis-projected slugs, and replenish-skip backlogs that today only
// land in journal output.
//
// Verdict() is pure given (Health, now): callers pass a parsed file
// plus a clock and get back a slice of one-line findings + an exit
// code. Read() handles the I/O seam so tests can construct Health
// directly.
package reconcilehealth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// DefaultStaleAfter is the grace window before a stale snapshot
// fires as a finding. The reconciler ticks every 60s, so 5 minutes
// covers a few missed runs without false-positiving on a slow tick.
const DefaultStaleAfter = 5 * time.Minute

// Health mirrors the on-disk JSON shape the reconciler writes.
// FleetDisabled, when true, suppresses dirty/mis-projected findings
// because skip-on-paused is the expected behaviour.
type Health struct {
	TS            string                   `json:"ts"`
	Version       int                      `json:"version"`
	Projects      map[string]ProjectHealth `json:"projects"`
	MisProjected  []MisProjection          `json:"mis_projected,omitempty"`
	LastReplenish *ReplenishSummary        `json:"last_replenish,omitempty"`
	FleetDisabled bool                     `json:"fleet_disabled,omitempty"`
}

type ProjectHealth struct {
	Status       string   `json:"status"`
	DirtyFiles   []string `json:"dirty_files,omitempty"`
	SkippedSlugs []string `json:"skipped_slugs,omitempty"`
}

type MisProjection struct {
	Slug            string `json:"slug"`
	ExpectedProject string `json:"expected_project"`
	FoundProject    string `json:"found_project"`
}

type ReplenishSummary struct {
	Floor        int  `json:"floor"`
	Was          int  `json:"was"`
	Promoted     int  `json:"promoted"`
	QueueEmpty   bool `json:"queue_empty"`
	SkippedCount int  `json:"skipped_count"`
}

// DefaultPath returns the canonical health file path, honouring the
// same $WT_STATE / $HOME fallback chain the reconciler uses.
func DefaultPath() string {
	return filepath.Join(wtStateDir(), "reconcile-health.json")
}

func wtStateDir() string {
	if v := os.Getenv("WT_STATE"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "wt")
	}
	return ""
}

// Read parses the health file. Missing returns (nil, nil) so callers
// can distinguish "reconciler hasn't run yet" from a real parse
// failure.
func Read(path string) (*Health, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var h Health
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("reconcile-health: parse %s: %w", path, err)
	}
	return &h, nil
}

// Verdict renders the surface for monitor/boot. Returns (findings,
// rc). rc=0 means no incident; rc=2 means the operator needs to act.
//
// Contract:
//   - h == nil (file missing): informational only at rc=0.
//   - h.FleetDisabled: rc=0 with a single paused-fleet line. The
//     existing fleet-disabled banner owns that surface; we suppress
//     dirt/mis-projection findings so paused mode reads as expected.
//   - stale (now - h.TS > staleAfter): rc=2 stale finding; other
//     findings still emitted so a stale-but-broken file reports
//     both signals.
//   - dirty-main, mis-projected, replenish.SkippedCount > 0: rc=2
//     each with one finding line.
func Verdict(h *Health, now time.Time, staleAfter time.Duration) ([]string, int) {
	if h == nil {
		return []string{"reconcile-health: unwritten (reconciler not run yet)"}, 0
	}
	if h.FleetDisabled {
		return []string{"reconcile-health: paused (fleet disabled)"}, 0
	}

	var findings []string
	rc := 0

	if stale, age := isStale(h.TS, now, staleAfter); stale {
		findings = append(findings,
			fmt.Sprintf("reconcile-health: stale (last write %s ago)", formatAge(age)))
		rc = 2
	}

	for _, name := range sortedProjects(h.Projects) {
		p := h.Projects[name]
		switch p.Status {
		case "", "ok":
			continue
		case "dirty-main":
			line := fmt.Sprintf("reconcile-health: dirty-main %s (%d files)",
				name, len(p.DirtyFiles))
			if n := len(p.SkippedSlugs); n > 0 {
				line += fmt.Sprintf(", %d slug(s) blocked", n)
			}
			findings = append(findings, line)
			rc = 2
		default:
			findings = append(findings,
				fmt.Sprintf("reconcile-health: %s %s", p.Status, name))
			rc = 2
		}
	}

	for _, m := range h.MisProjected {
		findings = append(findings,
			fmt.Sprintf("reconcile-health: mis-projected %s (expected %s, found %s)",
				m.Slug, m.ExpectedProject, m.FoundProject))
		rc = 2
	}

	if h.LastReplenish != nil && h.LastReplenish.SkippedCount > 0 {
		findings = append(findings,
			fmt.Sprintf("reconcile-health: last_replenish skipped=%d",
				h.LastReplenish.SkippedCount))
		rc = 2
	}

	if len(findings) == 0 {
		return nil, 0
	}
	return findings, rc
}

func sortedProjects(m map[string]ProjectHealth) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// isStale returns (true, age) when ts parses and is older than
// staleAfter. An unparseable or empty ts is treated as stale with a
// sentinel age so the operator gets a finding instead of silent
// drift.
func isStale(ts string, now time.Time, staleAfter time.Duration) (bool, time.Duration) {
	if ts == "" {
		return true, 0
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return true, 0
	}
	age := now.Sub(t)
	return age > staleAfter, age
}

func formatAge(d time.Duration) string {
	if d <= 0 {
		return "unknown"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
