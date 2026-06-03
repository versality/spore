package hooks

import (
	"os"
	"path/filepath"
)

// WtStateDir resolves the wt state root: $WT_STATE when set, else
// ~/.local/state/wt. Returns "" when neither is resolvable.
func WtStateDir() string {
	if v := os.Getenv("WT_STATE"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "wt")
	}
	return ""
}

// CoordinatorInbox resolves the coordinator inbox for key (a project
// or slug name): <root>/<key>/inbox, where root is
// $SPORE_COORDINATOR_STATE_DIR or ~/.local/state/spore/coordinator.
func CoordinatorInbox(key string) string {
	root := os.Getenv("SPORE_COORDINATOR_STATE_DIR")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil {
			root = filepath.Join(home, ".local", "state", "spore", "coordinator")
		}
	}
	return filepath.Join(root, key, "inbox")
}
