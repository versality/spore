// Package align tracks pilot-agent alignment for a project: a list
// of observed pilot preferences, how many of them have been promoted
// to rule-pool entries, and an operator-set "flipped" sentinel that
// turns alignment mode off once trust is established.
//
// State lives under "$XDG_STATE_HOME/spore/<project>/" (default
// "$HOME/.local/state/spore/<project>/"):
//
//	alignment.md  Markdown bullet log. Lines starting with
//	              "- [promoted]" count as promoted preferences.
//	              Other "- " lines count as plain notes.
//	flipped       Sentinel file. Existence means alignment off.
//
// Per-project defaults can be overridden in a top-level "spore.toml"
// with an [align] section: required_notes, required_promoted (ints).
package align

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/versality/spore/internal/sporetoml"
	"github.com/versality/spore/internal/task"
)

// Default exit criteria. Overridable per-project via spore.toml.
const (
	DefaultRequiredNotes    = 10
	DefaultRequiredPromoted = 3
)

// Criteria captures the per-project exit thresholds.
type Criteria struct {
	RequiredNotes    int
	RequiredPromoted int
}

// DefaultCriteria returns the built-in defaults.
func DefaultCriteria() Criteria {
	return Criteria{
		RequiredNotes:    DefaultRequiredNotes,
		RequiredPromoted: DefaultRequiredPromoted,
	}
}

// Status is a snapshot of alignment progress for a project.
type Status struct {
	Notes    int
	Promoted int
	Flipped  bool
	Criteria Criteria
}

// Met reports whether all three exit criteria are satisfied.
func (s Status) Met() bool {
	return s.Notes >= s.Criteria.RequiredNotes &&
		s.Promoted >= s.Criteria.RequiredPromoted &&
		s.Flipped
}

// Active reports whether alignment mode is currently on. Alignment
// mode stays on until the operator runs `spore align flip`, which
// drops the sentinel into place.
func (s Status) Active() bool { return !s.Flipped }

// Paths bundles the resolved on-disk locations for one project's
// alignment state. Project is the basename of the git toplevel from
// projectRoot, or basename of projectRoot if not a git repo.
type Paths struct {
	Project       string
	StateDir      string
	AlignmentFile string
	FlippedFile   string
}

// Resolve picks the state directory for projectRoot, honouring
// XDG_STATE_HOME, falling back to HOME/.local/state.
func Resolve(projectRoot string) (Paths, error) {
	project, err := task.ProjectName(projectRoot)
	if err != nil {
		return Paths{}, err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return Paths{}, errors.New("align: HOME and XDG_STATE_HOME both unset")
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "spore", project)
	return Paths{
		Project:       project,
		StateDir:      dir,
		AlignmentFile: filepath.Join(dir, "alignment.md"),
		FlippedFile:   filepath.Join(dir, "flipped"),
	}, nil
}

// Note appends a single observation to alignment.md. Whitespace is
// trimmed; empty lines are rejected. The first character is set to
// "- " if the caller did not already provide a list marker.
func Note(p Paths, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return errors.New("align: note line must not be empty")
	}
	if !strings.HasPrefix(line, "- ") {
		line = "- " + line
	}
	if err := os.MkdirAll(p.StateDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p.AlignmentFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

// Flip drops the sentinel file that turns alignment mode off.
// Repeat calls are idempotent; the file's mtime updates each time.
func Flip(p Paths) error {
	if err := os.MkdirAll(p.StateDir, 0o755); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	return os.WriteFile(p.FlippedFile, []byte(stamp+"\n"), 0o644)
}

// Read returns a Status snapshot. Missing alignment.md / flipped is
// treated as zero state, not an error.
func Read(p Paths, c Criteria) (Status, error) {
	notes, promoted, err := countNotes(p.AlignmentFile)
	if err != nil {
		return Status{}, err
	}
	flipped, err := exists(p.FlippedFile)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Notes:    notes,
		Promoted: promoted,
		Flipped:  flipped,
		Criteria: c,
	}, nil
}

// Active is a convenience wrapper for callers that only need the
// boolean and prefer not to thread a Criteria through (composer).
// Returns true (alignment on) when the project has no state yet, so
// freshly bootstrapped trees pick up the rule by default.
func Active(projectRoot string) (bool, error) {
	p, err := Resolve(projectRoot)
	if err != nil {
		return false, err
	}
	flipped, err := exists(p.FlippedFile)
	if err != nil {
		return false, err
	}
	return !flipped, nil
}

// LoadCriteria reads a spore.toml at projectRoot if present and
// merges its [align] section over DefaultCriteria. Missing file or
// missing keys fall through to defaults.
func LoadCriteria(projectRoot string) (Criteria, error) {
	c := DefaultCriteria()
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	overrides, err := parseAlignTOML(string(b))
	if err != nil {
		return c, fmt.Errorf("align: parse %s: %w", tomlPath, err)
	}
	if v, ok := overrides["required_notes"]; ok {
		c.RequiredNotes = v
	}
	if v, ok := overrides["required_promoted"]; ok {
		c.RequiredPromoted = v
	}
	return c, nil
}

func countNotes(path string) (notes, promoted int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		notes++
		body := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if strings.HasPrefix(body, "[promoted]") {
			promoted++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return notes, promoted, nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// parseAlignTOML reads a tiny TOML subset: only the [align] section,
// only `key = N` integer scalars. Lines outside [align], blanks,
// comments (`#`), and unknown keys are ignored. Returns a map of the
// integer keys it understood. Anything malformed inside [align] is an
// error so misconfiguration surfaces loudly.
func parseAlignTOML(content string) (map[string]int, error) {
	out := make(map[string]int)
	err := sporetoml.ScanSections(content, func(l sporetoml.Line) error {
		if l.Section != "align" {
			return nil
		}
		key, val, ok := sporetoml.SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: malformed entry %q", l.LineNum, l.Text)
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("line %d: key %q: want integer, got %q", l.LineNum, key, val)
		}
		out[key] = n
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
