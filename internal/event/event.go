// Package event is the canonical fleet event bus. Every consumer
// (skyhelm, wt-task, repo probes, future agents) publishes through
// the same jsonl stream so tools can tail, filter, and react across
// the whole fleet without per-source schemas.
//
// Storage is append-only at $XDG_STATE_HOME/spore/events.jsonl
// (default ~/.local/state/spore/events.jsonl). The file rotates to
// events-<rfc3339>.jsonl when it exceeds $SPORE_EVENT_MAX_BYTES
// (default 64 MiB); readers merge rotated files in chronological order.
package event

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// LevelInfo is the lowest severity. Routine signals.
	LevelInfo = "info"
	// LevelWarn is for degraded but not failing conditions.
	LevelWarn = "warn"
	// LevelError is for failures that need attention.
	LevelError = "error"
)

const (
	// DefaultMaxBytes is the rotation threshold when
	// $SPORE_EVENT_MAX_BYTES is unset.
	DefaultMaxBytes int64 = 64 * 1024 * 1024
	currentName           = "events.jsonl"
	rotatedPrefix         = "events-"
	rotatedSuffix         = ".jsonl"
	dirMode               = 0o700
	fileMode              = 0o600
)

// Event is the wire and storage format. Required: Ts, Source, Level,
// Kind, Message. Optional: Slug, Data. Data is a raw JSON value so
// publishers don't have to agree on a Go shape.
type Event struct {
	Ts      time.Time       `json:"ts"`
	Source  string          `json:"source"`
	Level   string          `json:"level"`
	Kind    string          `json:"kind"`
	Slug    string          `json:"slug,omitempty"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Validate enforces the closed level enum and non-empty required fields.
// Ts may be zero on the way in; Append will stamp it if so.
func (e *Event) Validate() error {
	switch e.Level {
	case LevelInfo, LevelWarn, LevelError:
	case "":
		return errors.New("level required")
	default:
		return fmt.Errorf("level %q invalid: want info|warn|error", e.Level)
	}
	if strings.TrimSpace(e.Source) == "" {
		return errors.New("source required")
	}
	if strings.TrimSpace(e.Kind) == "" {
		return errors.New("kind required")
	}
	if strings.TrimSpace(e.Message) == "" {
		return errors.New("message required")
	}
	if len(e.Data) > 0 && !json.Valid(e.Data) {
		return errors.New("data is not valid JSON")
	}
	return nil
}

// Dir returns the spore event directory, honoring $SPORE_EVENT_DIR
// (test-only override) then $XDG_STATE_HOME, then ~/.local/state.
func Dir() (string, error) {
	if d := os.Getenv("SPORE_EVENT_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "spore"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "spore"), nil
}

// CurrentPath is the active jsonl file, the one publishers append to.
func CurrentPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, currentName), nil
}

// MaxBytes resolves $SPORE_EVENT_MAX_BYTES with DefaultMaxBytes as the
// fallback. Zero or negative values mean "never rotate".
func MaxBytes() int64 {
	v := os.Getenv("SPORE_EVENT_MAX_BYTES")
	if v == "" {
		return DefaultMaxBytes
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return DefaultMaxBytes
	}
	return n
}

// Append validates ev, stamps Ts when zero, rotates the current file
// if it would exceed MaxBytes, then appends one jsonl line.
func Append(ev *Event) error {
	if ev.Ts.IsZero() {
		ev.Ts = time.Now().UTC()
	} else {
		ev.Ts = ev.Ts.UTC()
	}
	if err := ev.Validate(); err != nil {
		return err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	d, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, dirMode); err != nil {
		return err
	}
	cur := filepath.Join(d, currentName)

	if max := MaxBytes(); max > 0 {
		if st, err := os.Stat(cur); err == nil && st.Size()+int64(len(line)) > max {
			rotated := filepath.Join(d, rotatedPrefix+ev.Ts.Format("20060102T150405.000000000Z")+rotatedSuffix)
			if err := os.Rename(cur, rotated); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}

	f, err := os.OpenFile(cur, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// Files returns the chronologically ordered set of event files: all
// rotated events-*.jsonl first (oldest to newest by name), then the
// current events.jsonl if it exists. Missing dir returns an empty list.
func Files() ([]string, error) {
	d, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var rotated []string
	hasCurrent := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == currentName {
			hasCurrent = true
			continue
		}
		if strings.HasPrefix(name, rotatedPrefix) && strings.HasSuffix(name, rotatedSuffix) {
			rotated = append(rotated, filepath.Join(d, name))
		}
	}
	sort.Strings(rotated)
	if hasCurrent {
		rotated = append(rotated, filepath.Join(d, currentName))
	}
	return rotated, nil
}

// Filter narrows a stream by AND-composed predicates. Zero-value fields
// are ignored. Since is "events at or after t"; a zero Since means
// "no lower bound".
type Filter struct {
	Since  time.Time
	Level  string
	Source string
	Kind   string
	Slug   string
}

// Match reports whether ev passes every set predicate.
func (f Filter) Match(ev *Event) bool {
	if !f.Since.IsZero() && ev.Ts.Before(f.Since) {
		return false
	}
	if f.Level != "" && ev.Level != f.Level {
		return false
	}
	if f.Source != "" && ev.Source != f.Source {
		return false
	}
	if f.Kind != "" && ev.Kind != f.Kind {
		return false
	}
	if f.Slug != "" && ev.Slug != f.Slug {
		return false
	}
	return true
}

// Read returns up to limit events matching f across all rotations,
// oldest first. limit <= 0 means "no cap". Malformed lines are
// skipped silently so a stray write can't poison the stream.
func Read(f Filter, limit int) ([]Event, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	var all []Event
	for _, p := range files {
		evs, err := readFile(p, f)
		if err != nil {
			return nil, err
		}
		all = append(all, evs...)
	}
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

func readFile(path string, f Filter) ([]Event, error) {
	fh, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer fh.Close()
	return decodeStream(fh, f)
}

func decodeStream(r io.Reader, f Filter) ([]Event, error) {
	var out []Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if !f.Match(&ev) {
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Follow opens the current jsonl and emits matching events to out as
// they're appended. It returns when ctx (passed via stop) closes or
// when the file is removed and not recreated.
//
// poll is the wake interval between read attempts; <= 0 means 200ms.
// If the file rotates (truncates or shrinks), Follow seeks to the new
// head so it can keep reading without producing duplicates from the
// rotated tail.
func Follow(stop <-chan struct{}, poll time.Duration, f Filter, emit func(Event)) error {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	cur, err := CurrentPath()
	if err != nil {
		return err
	}

	var (
		fh     *os.File
		offset int64
		buf    []byte
	)
	openIfPresent := func() error {
		if fh != nil {
			return nil
		}
		h, err := os.Open(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		fh = h
		offset = 0
		return nil
	}
	defer func() {
		if fh != nil {
			fh.Close()
		}
	}()

	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		if err := openIfPresent(); err != nil {
			return err
		}
		if fh != nil {
			st, err := fh.Stat()
			if err != nil {
				return err
			}
			if st.Size() < offset {
				// file was rotated/truncated under us; reopen.
				fh.Close()
				fh = nil
				offset = 0
				continue
			}
			if st.Size() > offset {
				if _, err := fh.Seek(offset, io.SeekStart); err != nil {
					return err
				}
				chunk, err := io.ReadAll(fh)
				if err != nil {
					return err
				}
				offset += int64(len(chunk))
				buf = append(buf, chunk...)
				for {
					nl := bytes.IndexByte(buf, '\n')
					if nl < 0 {
						break
					}
					line := buf[:nl]
					buf = buf[nl+1:]
					if len(line) == 0 {
						continue
					}
					var ev Event
					if err := json.Unmarshal(line, &ev); err != nil {
						continue
					}
					if f.Match(&ev) {
						emit(ev)
					}
				}
			}
		}
		select {
		case <-stop:
			return nil
		case <-t.C:
		}
	}
}
