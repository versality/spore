package hooks

import (
	"os"
)

// NotifyCoordinator writes a poke file into the coordinator's project
// inbox at $SPORE_COORDINATOR_STATE_DIR/<slug>/inbox/. The poke is a
// JSON file following the tell protocol ({ts, source, body}), written
// atomically via .tmp.
func NotifyCoordinator(slug string) error {
	return notifyCoordinatorAt(CoordinatorInbox(slug))
}

// NotifyCoordinatorEnv is the env-driven entry point for the
// Notification hook. It reads $WT_PROJECT to identify the target
// coordinator inbox, and $SPORE_TASK_INBOX to skip self-pokes when the
// firing session is the coordinator itself. Returns nil (no-op) when
// WT_PROJECT is unset or when SPORE_TASK_INBOX matches the target inbox.
func NotifyCoordinatorEnv() error {
	project := os.Getenv("WT_PROJECT")
	if project == "" {
		return nil
	}
	inbox := CoordinatorInbox(project)
	if os.Getenv("SPORE_TASK_INBOX") == inbox {
		return nil
	}
	return notifyCoordinatorAt(inbox)
}

func notifyCoordinatorAt(inbox string) error {
	return writeTellEnvelope(inbox, "notification", "poke", "1")
}
