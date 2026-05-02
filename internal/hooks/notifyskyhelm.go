package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NotifySkyhelm writes a poke file into skyhelm's project inbox at
// $SKYHELM_STATE_DIR/<slug>/inbox/. The poke is a JSON file following
// the tell protocol ({ts, source, body}), written atomically via .tmp.
func NotifySkyhelm(slug string) error {
	return notifySkyhelmAt(skyhelmInbox(slug))
}

// NotifySkyhelmEnv is the env-driven entry point for the Notification
// hook. It reads $WT_PROJECT to identify the target skyhelm inbox, and
// $SKYBOT_INBOX to skip self-pokes when the firing session is the
// project's skyhelm itself. Returns nil (no-op) when WT_PROJECT is
// unset (ad-hoc claude session outside a configured project) or when
// the firing session is the target skyhelm.
func NotifySkyhelmEnv() error {
	project := os.Getenv("WT_PROJECT")
	if project == "" {
		return nil
	}
	inbox := skyhelmInbox(project)
	if isSkyhelmSession(inbox) {
		return nil
	}
	return notifySkyhelmAt(inbox)
}

// isSkyhelmSession reports whether the firing session is the skyhelm
// for inbox. Mirrors the bash self_id check: SKYBOT_INBOX equal to the
// skyhelm inbox path means we are skyhelm and pokes would self-wake.
func isSkyhelmSession(inbox string) bool {
	self := os.Getenv("SKYBOT_INBOX")
	if self == "" {
		return false
	}
	return self == inbox
}

func notifySkyhelmAt(inbox string) error {
	if err := ensureInbox(inbox); err != nil {
		return fmt.Errorf("notify-skyhelm: ensure inbox: %w", err)
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
		return fmt.Errorf("notify-skyhelm: write tmp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("notify-skyhelm: rename: %w", err)
	}
	return nil
}

type tellEvent struct {
	Ts     string `json:"ts"`
	Source string `json:"source"`
	Body   string `json:"body"`
}

func skyhelmInbox(slug string) string {
	root := os.Getenv("SKYHELM_STATE_DIR")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".local", "state", "skyhelm")
		}
	}
	return filepath.Join(root, slug, "inbox")
}
