package coordinator

import (
	"os"
	"path/filepath"
)

// StateDir returns the host-wide coordinator root. Resolution order:
//  1. $SPORE_COORDINATOR_STATE_DIR (caller override)
//  2. $XDG_STATE_HOME/spore/coordinator
//  3. $HOME/.local/state/spore/coordinator
//
// Host-wide here means "shared across all projects on this host";
// per-project paths (state.md, inbox, ledgers) live one segment
// deeper at <StateDir>/<project>/ via the Project* helpers.
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

// StatePath joins name to StateDir for a host-wide primitive
// (worker-watch.json, loop-guard trip files). For per-project state
// use ProjectStateFile / ProjectInbox / ProjectLedger.
func StatePath(name string) string {
	return filepath.Join(StateDir(), name)
}

// ProjectDir is <StateDir>/<project>: the per-project coordinator
// directory that holds state.md, inbox/, and per-project ledgers.
func ProjectDir(project string) string {
	return filepath.Join(StateDir(), project)
}

// ProjectStateFile is <ProjectDir>/state.md.
func ProjectStateFile(project string) string {
	return filepath.Join(ProjectDir(project), "state.md")
}

// ProjectInbox is <ProjectDir>/inbox.
func ProjectInbox(project string) string {
	return filepath.Join(ProjectDir(project), "inbox")
}

// ProjectLedger is <ProjectDir>/<name>, for per-project jsonl ledgers
// (respawn-events.jsonl, codex-inbox-watcher.jsonl, token-monitor.jsonl).
func ProjectLedger(project, name string) string {
	return filepath.Join(ProjectDir(project), name)
}

// DefaultStateDir resolves the state directory most coordinator tools
// want by default: per-project when $WT_PROJECT is set, host-wide
// StateDir otherwise. This is the single entry point packages should
// use instead of re-rolling XDG / HOME fallbacks.
func DefaultStateDir() string {
	if p := os.Getenv("WT_PROJECT"); p != "" {
		return ProjectDir(p)
	}
	return StateDir()
}
