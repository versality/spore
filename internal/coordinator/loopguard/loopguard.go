// Package loopguard implements a circuit breaker on coordinator
// respawn rate. When the coordinator cycles too frequently (context
// wrap, crash, voluntary restart), the loop guard trips and blocks
// further respawns until a cooldown period elapses or the operator
// resets it.
package loopguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultMaxRespawns = 5
	DefaultWindow      = 10 * time.Minute
	DefaultCooldown    = 5 * time.Minute
	ledgerName         = "respawn-events.jsonl"
	tripMarkerName     = "loopguard-tripped"
)

type Config struct {
	StateDir    string
	MaxRespawns int
	Window      time.Duration
	Cooldown    time.Duration
}

type RespawnEvent struct {
	Timestamp time.Time `json:"ts"`
	SessionID string    `json:"session_id,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}

type Status struct {
	Tripped       bool      `json:"tripped"`
	TrippedAt     time.Time `json:"tripped_at,omitempty"`
	RecentCount   int       `json:"recent_count"`
	WindowSeconds int       `json:"window_seconds"`
	MaxRespawns   int       `json:"max_respawns"`
	CooldownLeft  string    `json:"cooldown_left,omitempty"`
}

func (c Config) defaults() Config {
	if c.MaxRespawns <= 0 {
		c.MaxRespawns = DefaultMaxRespawns
	}
	if c.Window <= 0 {
		c.Window = DefaultWindow
	}
	if c.Cooldown <= 0 {
		c.Cooldown = DefaultCooldown
	}
	return c
}

func ledgerPath(stateDir string) string {
	return filepath.Join(stateDir, ledgerName)
}

func tripMarkerPath(stateDir string) string {
	return filepath.Join(stateDir, tripMarkerName)
}

// Record appends a respawn event to the ledger.
func Record(cfg Config, ev RespawnEvent) error {
	cfg = cfg.defaults()
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(ledgerPath(cfg.StateDir), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b = append(b, '\n')
	_, err = f.Write(b)
	return err
}

// Check reads recent respawn events and returns whether the circuit
// is tripped. When tripped, the caller should refuse to spawn. When
// the cooldown period has elapsed since the trip, the marker is
// automatically cleared.
func Check(cfg Config) (Status, error) {
	cfg = cfg.defaults()
	now := time.Now().UTC()

	marker := tripMarkerPath(cfg.StateDir)
	if fi, err := os.Stat(marker); err == nil {
		trippedAt := fi.ModTime()
		elapsed := now.Sub(trippedAt)
		if elapsed < cfg.Cooldown {
			left := cfg.Cooldown - elapsed
			return Status{
				Tripped:       true,
				TrippedAt:     trippedAt,
				WindowSeconds: int(cfg.Window.Seconds()),
				MaxRespawns:   cfg.MaxRespawns,
				CooldownLeft:  fmt.Sprintf("%ds", int(left.Seconds())),
			}, nil
		}
		os.Remove(marker)
	}

	events, err := readRecentEvents(cfg.StateDir, now.Add(-cfg.Window))
	if err != nil {
		return Status{}, err
	}

	s := Status{
		RecentCount:   len(events),
		WindowSeconds: int(cfg.Window.Seconds()),
		MaxRespawns:   cfg.MaxRespawns,
	}

	if len(events) >= cfg.MaxRespawns {
		s.Tripped = true
		s.TrippedAt = now
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return s, err
		}
		f.Close()
		s.CooldownLeft = fmt.Sprintf("%ds", int(cfg.Cooldown.Seconds()))
	}

	return s, nil
}

// Reset clears the trip marker, allowing respawns to resume.
func Reset(stateDir string) error {
	return os.Remove(tripMarkerPath(stateDir))
}

func readRecentEvents(stateDir string, since time.Time) ([]RespawnEvent, error) {
	f, err := os.Open(ledgerPath(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []RespawnEvent
	dec := json.NewDecoder(f)
	for dec.More() {
		var ev RespawnEvent
		if err := dec.Decode(&ev); err != nil {
			continue
		}
		if !ev.Timestamp.Before(since) {
			events = append(events, ev)
		}
	}
	return events, nil
}
