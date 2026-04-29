// Package bootstrap drives the stage-gated walk from a fresh repo to
// a worker-ready one. The default sequence is repo-mapped,
// info-gathered, tests-pass, creds-wired, readme-followed,
// validation-green, pilot-aligned, worker-fleet-ready.
//
// Each stage has a Detect function that returns a notes string and a
// nil error when the gate is satisfied. A non-nil error is rendered
// as the blocker reason; the stage is recorded as failed but not
// advanced. Once a stage is recorded as completed or skipped, later
// runs do not re-fire it (additive semantics) until `Reset` wipes the
// state file.
//
// State lives at "<state-dir>/bootstrap.json" with one record per
// stage: status, started_at, completed_at, notes. The pilot-aligned
// stage uses internal/align to verify the operator has run through
// the alignment period and flipped out.
package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Stage statuses recorded in the per-stage StageRecord. Pending is the
// implicit default for stages with no record yet.
const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
)

// Stage is one named gate in the bootstrap sequence.
type Stage struct {
	Name string
	// Detect returns notes (free-form, recorded with the stage record)
	// and nil when the gate is satisfied. A non-nil error is rendered
	// as the blocker reason and the stage is recorded as failed.
	Detect func(root string) (string, error)
}

// StageRecord is the persisted outcome of one stage.
type StageRecord struct {
	Status      string `json:"status,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

// State is the on-disk view of bootstrap progress.
type State struct {
	Stages map[string]StageRecord `json:"stages,omitempty"`
}

// Result captures one Run outcome.
type Result struct {
	Current  string
	Advanced []string
	Skipped  []string
	Blocker  string
	Done     bool
}

// Options tunes a Run.
type Options struct {
	// Skip names stages to mark as skipped rather than detect. Skipping
	// a stage that is already completed is a no-op.
	Skip []string
	// Now overrides the timestamp source. nil means time.Now().UTC().
	Now func() time.Time
}

// DefaultStages returns the kernel's default stage sequence with real
// detectors wired up.
func DefaultStages() []Stage {
	return []Stage{
		{Name: "repo-mapped", Detect: detectRepoMapped},
		{Name: "info-gathered", Detect: detectInfoGathered},
		{Name: "tests-pass", Detect: detectTestsPass},
		{Name: "creds-wired", Detect: detectCredsWired},
		{Name: "readme-followed", Detect: detectReadmeFollowed},
		{Name: "validation-green", Detect: detectValidationGreen},
		{Name: "pilot-aligned", Detect: detectPilotAligned},
		{Name: "worker-fleet-ready", Detect: detectWorkerFleetReady},
	}
}

// Run advances the bootstrap cursor for projectRoot through stages,
// persisting state under stateDir. Returns a Result describing what
// changed and the blocker (if any).
func Run(projectRoot, stateDir string, stages []Stage, opts Options) (Result, error) {
	if len(stages) == 0 {
		return Result{}, errors.New("bootstrap: empty stage list")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	skipSet := map[string]bool{}
	for _, s := range opts.Skip {
		skipSet[s] = true
	}
	for name := range skipSet {
		if indexOf(stages, name) < 0 {
			return Result{}, fmt.Errorf("bootstrap: --skip %q does not name a known stage", name)
		}
	}

	state, err := loadState(stateDir)
	if err != nil {
		return Result{}, err
	}
	if state.Stages == nil {
		state.Stages = map[string]StageRecord{}
	}

	res := Result{}
	for i, st := range stages {
		rec := state.Stages[st.Name]
		if rec.Status == StatusCompleted || rec.Status == StatusSkipped {
			res.Current = st.Name
			if i == len(stages)-1 {
				res.Done = true
			}
			continue
		}
		if skipSet[st.Name] {
			rec = StageRecord{
				Status:      StatusSkipped,
				StartedAt:   timestamp(now),
				CompletedAt: timestamp(now),
				Notes:       "skipped via --skip",
			}
			state.Stages[st.Name] = rec
			res.Skipped = append(res.Skipped, st.Name)
			res.Current = st.Name
			if i == len(stages)-1 {
				res.Done = true
			}
			continue
		}
		rec.Status = StatusPending
		rec.StartedAt = timestamp(now)
		state.Stages[st.Name] = rec
		notes, derr := runDetect(st, projectRoot)
		if derr != nil {
			rec.Status = StatusFailed
			rec.Notes = derr.Error()
			rec.CompletedAt = ""
			state.Stages[st.Name] = rec
			res.Current = st.Name
			res.Blocker = derr.Error()
			if err := saveState(stateDir, state); err != nil {
				return res, err
			}
			return res, nil
		}
		rec.Status = StatusCompleted
		rec.Notes = notes
		rec.CompletedAt = timestamp(now)
		state.Stages[st.Name] = rec
		res.Advanced = append(res.Advanced, st.Name)
		res.Current = st.Name
		if i == len(stages)-1 {
			res.Done = true
		}
	}
	if err := saveState(stateDir, state); err != nil {
		return res, err
	}
	return res, nil
}

// Status returns the per-stage records for stages, in stage order.
// Stages with no record yet appear as pending.
func Status(stateDir string, stages []Stage) ([]NamedRecord, error) {
	state, err := loadState(stateDir)
	if err != nil {
		return nil, err
	}
	out := make([]NamedRecord, 0, len(stages))
	for _, st := range stages {
		rec := state.Stages[st.Name]
		if rec.Status == "" {
			rec.Status = StatusPending
		}
		out = append(out, NamedRecord{Name: st.Name, Record: rec})
	}
	return out, nil
}

// NamedRecord pairs a stage name with its record for callers that want
// an ordered slice without re-keying maps.
type NamedRecord struct {
	Name   string
	Record StageRecord
}

// Reset wipes the bootstrap state file. Returns nil when the file is
// already absent.
func Reset(stateDir string) error {
	path := filepath.Join(stateDir, "bootstrap.json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func runDetect(st Stage, root string) (string, error) {
	if st.Detect == nil {
		return "", nil
	}
	return st.Detect(root)
}

func loadState(stateDir string) (State, error) {
	path := filepath.Join(stateDir, "bootstrap.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{Stages: map[string]StageRecord{}}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("bootstrap: parse %s: %w", path, err)
	}
	if s.Stages == nil {
		s.Stages = map[string]StageRecord{}
	}
	return s, nil
}

func saveState(stateDir string, s State) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	if s.Stages == nil {
		s.Stages = map[string]StageRecord{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, "bootstrap.json"), append(b, '\n'), 0o644)
}

func timestamp(now func() time.Time) string {
	return now().UTC().Format(time.RFC3339)
}

func indexOf(stages []Stage, name string) int {
	for i, s := range stages {
		if s.Name == name {
			return i
		}
	}
	return -1
}
