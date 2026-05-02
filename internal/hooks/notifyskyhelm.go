package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NotifyCoordinator writes a poke file into coordinator's project inbox at
// $SKYHELM_STATE_DIR/<slug>/inbox/. The poke is a JSON file following
// the tell protocol ({ts, source, body}), written atomically via .tmp.
func NotifyCoordinator(slug string) error {
	return notifyCoordinatorAt(coordinatorInbox(slug))
}

// NotifyCoordinatorEnv is the env-driven entry point for the Notification
// hook. It reads $WT_PROJECT to identify the target coordinator inbox, and
// $SKYBOT_INBOX to skip self-pokes when the firing session is the
// project's coordinator itself. Returns nil (no-op) when WT_PROJECT is
// unset (ad-hoc claude session outside a configured project) or when
// the firing session is the target coordinator.
func NotifyCoordinatorEnv() error {
	project := os.Getenv("WT_PROJECT")
	if project == "" {
		return nil
	}
	inbox := coordinatorInbox(project)
	if isCoordinatorSession(inbox) {
		return nil
	}
	return notifyCoordinatorAt(inbox)
}

// isCoordinatorSession reports whether the firing session is the coordinator
// for inbox. Mirrors the bash self_id check: SKYBOT_INBOX equal to the
// coordinator inbox path means we are coordinator and pokes would self-wake.
func isCoordinatorSession(inbox string) bool {
	self := os.Getenv("SKYBOT_INBOX")
	if self == "" {
		return false
	}
	return self == inbox
}

func notifyCoordinatorAt(inbox string) error {
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
	root := os.Getenv("SKYHELM_STATE_DIR")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".local", "state", "coordinator")
		}
	}
	return filepath.Join(root, slug, "inbox")
}
