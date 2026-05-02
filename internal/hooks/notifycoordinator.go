package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NotifyCoordinator writes a poke file into the coordinator's project
// inbox at $SPORE_COORDINATOR_STATE_DIR/<slug>/inbox/. The poke is a
// JSON file following the tell protocol ({ts, source, body}), written
// atomically via .tmp.
func NotifyCoordinator(slug string) error {
	inbox := coordinatorInbox(slug)
	if err := ensureInbox(inbox); err != nil {
		return fmt.Errorf("notify-coordinator: ensure inbox: %w", err)
	}

	poke := tellEvent{
		Ts:     time.Now().Format("2006-01-02T15:04:05-07:00"),
		Source: "notification",
		Body:   "poke",
	}
	b, err := json.Marshal(poke)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	name := fmt.Sprintf("%d-%d-1.json", time.Now().UnixMilli(), os.Getpid())
	tmp := filepath.Join(inbox, ".tmp", name)
	dst := filepath.Join(inbox, name)

	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("notify-coordinator: write tmp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("notify-coordinator: rename: %w", err)
	}
	return nil
}

type tellEvent struct {
	Ts     string `json:"ts"`
	Source string `json:"source"`
	Body   string `json:"body"`
}

func coordinatorInbox(slug string) string {
	root := os.Getenv("SPORE_COORDINATOR_STATE_DIR")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".local", "state", "spore", "coordinator")
		}
	}
	return filepath.Join(root, slug, "inbox")
}
