package task

import "github.com/versality/spore/internal/sessionkind"

// SessionKindEnv, SessionKindCoordinator, and SessionKindWorker are
// kept as aliases of the leaf `internal/sessionkind` constants so
// existing call sites in task and fleet keep compiling. New code
// should import sessionkind directly.
const (
	SessionKindEnv         = sessionkind.Env
	SessionKindCoordinator = sessionkind.Coordinator
	SessionKindWorker      = sessionkind.Worker
)
