// Package sessionkind hosts the env-var contract that spore's tmux
// spawners use to tag each claude-code session by role (coordinator,
// worker) and that hooks consult to scope their behaviour. The
// contract is a leaf package so the hook-render/inject layer can
// import it without dragging the spawner-side `internal/task`
// package into a cycle (task -> hooks/inject -> hooks -> sessionkind).
package sessionkind

// Env is the env var spore's tmux spawners set so hooks, the
// gate-kind helper, and consumer wiring can tell a coordinator
// session from a worker session from an operator-interactive one
// (env absent). Stable across drivers (claude, codex, opencode).
const Env = "WT_SESSION_KIND"

// Coordinator marks the singleton coordinator session. Set by
// EnsureCoordinator in internal/fleet.
const Coordinator = "coordinator"

// Worker marks a per-task worker session. Set by ensureSession in
// internal/task.
const Worker = "worker"
