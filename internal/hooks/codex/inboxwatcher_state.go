package codex

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InboxState is the per-project marker set the watcher persists
// across passes. Each marker is a file in the project's state subdir
// holding a single filename (or empty).
type InboxState struct {
	// Last is the newest inbox filename we've already classified.
	Last string
	// Dedupe is the filename for which we've already emitted a
	// wake-deduped event (so re-firing on the same file doesn't
	// spam the ledger).
	Dedupe string
	// Drained is the filename whose drain we've already recorded.
	Drained string
	// WakePending is the filename for which a wake-respawn is in
	// flight; the TTL prevents back-to-back respawns.
	WakePending string
	// WakePendingMTime is the modification time of the WakePending
	// marker, used to age the throttle.
	WakePendingMTime time.Time
}

// InboxObservation is what the watcher saw for one project on a pass.
type InboxObservation struct {
	// Newest is the lexicographically-greatest filename in inbox/.
	Newest string
	// UnreadCount is the *.json count at the inbox root.
	UnreadCount int
	// Source is the "source" field extracted from the newest file's
	// JSON, used in the event ledger.
	Source string
}

// WakeMode controls what action the watcher performs on a fresh
// inbox file. RecordOnly logs an event but does not wake; Respawn
// triggers a tmux pane respawn.
type WakeMode int

const (
	WakeModeRespawn WakeMode = iota
	WakeModeRecordOnly
)

// InboxAction is what the watcher should do for one project after
// reconciling state and observation. The pure planner returns this;
// the daemon does the side-effects.
type InboxAction struct {
	// Event names the ledger row to write. Empty means no event.
	// Possible values: "processed/drained", "wake-deduped", "wake-pending",
	// "wake-sent", "recorded-only".
	Event string
	// Wake is true when the daemon should perform the wake action
	// (e.g. tmux respawn).
	Wake bool
	// NewState is the marker set the daemon should persist.
	NewState InboxState
	// Newest carries through for ledger writes.
	Newest string
	// UnreadCount carries through for ledger writes.
	UnreadCount int
	// Source carries through for ledger writes.
	Source string
}

// PlanInbox decides what the watcher should do for one project this
// pass. It is a pure function of the prior state, the current
// observation, the wake mode, and the wake-pending TTL.
//
// Behavior matches the bash adapter:
//
//   - inbox is now empty: if Last was set, record a "processed/drained"
//     event once; clear WakePending.
//   - newest matches Last: record a "wake-deduped" event once per
//     filename to avoid spam.
//   - newest is new: clear Dedupe / Drained, then either:
//     record-only mode -> log "recorded-only"
//     wake pending within TTL -> log "wake-pending", refresh marker
//     wake-mode respawn -> request a wake; the daemon writes
//     "wake-sent" or "wake-error" depending on the side-effect.
func PlanInbox(prior InboxState, obs InboxObservation, mode WakeMode, ttl time.Duration, now time.Time) InboxAction {
	out := InboxAction{
		NewState:    prior,
		Newest:      obs.Newest,
		UnreadCount: obs.UnreadCount,
		Source:      obs.Source,
	}

	if obs.Newest == "" || obs.UnreadCount == 0 {
		if prior.Last != "" && prior.Drained != prior.Last {
			out.Event = "processed/drained"
			out.NewState.Drained = prior.Last
			out.NewState.WakePending = ""
			out.UnreadCount = 0
			out.Newest = prior.Last
			out.Source = "coordinator"
		}
		return out
	}

	if obs.Newest == prior.Last {
		if prior.Dedupe != obs.Newest {
			out.Event = "wake-deduped"
			out.NewState.Dedupe = obs.Newest
		}
		return out
	}

	out.NewState.Last = obs.Newest
	out.NewState.Dedupe = ""
	out.NewState.Drained = ""
	if mode == WakeModeRecordOnly {
		out.Event = "recorded-only"
		return out
	}
	if prior.WakePending != "" && ttl > 0 && now.Sub(prior.WakePendingMTime) < ttl {
		out.Event = "wake-pending"
		out.NewState.WakePending = obs.Newest
		out.NewState.WakePendingMTime = now
		return out
	}
	out.Wake = true
	out.NewState.WakePending = obs.Newest
	out.NewState.WakePendingMTime = now
	out.Event = "wake-sent"
	return out
}

// LoadInboxState reads the four marker files plus the wake-pending
// mtime from disk. Missing markers map to empty strings.
func LoadInboxState(stateDir string) InboxState {
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(stateDir, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(body))
	}
	s := InboxState{
		Last:        read("codex-inbox-watcher.last"),
		Dedupe:      read("codex-inbox-watcher.deduped"),
		Drained:     read("codex-inbox-watcher.drained"),
		WakePending: read("codex-inbox-watcher.wake-pending"),
	}
	if info, err := os.Stat(filepath.Join(stateDir, "codex-inbox-watcher.wake-pending")); err == nil {
		s.WakePendingMTime = info.ModTime()
	}
	return s
}

// SaveInboxState persists the marker set. Each file holds the bare
// filename plus a trailing newline; missing values clear the file.
func SaveInboxState(stateDir string, s InboxState) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	type pair struct {
		name string
		val  string
	}
	for _, p := range []pair{
		{"codex-inbox-watcher.last", s.Last},
		{"codex-inbox-watcher.deduped", s.Dedupe},
		{"codex-inbox-watcher.drained", s.Drained},
		{"codex-inbox-watcher.wake-pending", s.WakePending},
	} {
		path := filepath.Join(stateDir, p.name)
		if p.val == "" {
			_ = os.Remove(path)
			continue
		}
		if err := os.WriteFile(path, []byte(p.val+"\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}
