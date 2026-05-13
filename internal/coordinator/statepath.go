package coordinator

import (
	"os"
	"path/filepath"
)

// StateDir returns the coordinator state directory. Resolution order:
//  1. $SPORE_COORDINATOR_STATE_DIR (caller override)
//  2. $XDG_STATE_HOME/spore/coordinator
//  3. $HOME/.local/state/spore/coordinator
func StateDir() string {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "spore", "coordinator")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "spore", "coordinator")
}

// StatePath returns a path inside StateDir for the named primitive.
// Pass the bare filename (e.g. "rower-watch.json"); StatePath does
// not create the directory.
func StatePath(name string) string {
	return filepath.Join(StateDir(), name)
}
