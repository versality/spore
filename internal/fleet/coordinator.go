package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/task"
)

// CoordinatorSlug is the reserved session slug for the singleton
// coordinator agent. Workers cannot use it; the fleet reconciler
// manages this session out-of-band from the per-task queue.
const CoordinatorSlug = "coordinator"

// CoordinatorRoleEnv overrides the role file path the reconciler
// hands to the coordinator session. Empty falls back to
// <projectRoot>/bootstrap/coordinator/role.md.
const CoordinatorRoleEnv = "SPORE_COORDINATOR_ROLE_FILE"

// CoordinatorAgentEnv selects the binary the coordinator session
// execs. Read before SPORE_AGENT_BINARY so operators can run a
// different agent (or a greet-and-shell wrapper) for the singleton
// coordinator without affecting per-task workers.
const CoordinatorAgentEnv = "SPORE_COORDINATOR_AGENT"

// CoordinatorSessionName returns the tmux session for the singleton
// coordinator: "spore/<project>/coordinator", parallel to worker
// session names.
func CoordinatorSessionName(projectRoot string) string {
	return fmt.Sprintf("spore/%s/%s", filepath.Base(projectRoot), CoordinatorSlug)
}

// CoordinatorRolePath returns the override path from
// SPORE_COORDINATOR_ROLE_FILE if set, else the in-tree default at
// <projectRoot>/bootstrap/coordinator/role.md.
func CoordinatorRolePath(projectRoot string) string {
	if p := os.Getenv(CoordinatorRoleEnv); p != "" {
		return p
	}
	return filepath.Join(projectRoot, "bootstrap", "coordinator", "role.md")
}

// EnsureCoordinator spawns the coordinator tmux session for projectRoot
// when it is not already alive. Idempotent: a live session is left
// alone. The session runs in projectRoot itself (no worktree) with
// SPORE_TASK_SLUG=coordinator and SPORE_COORDINATOR_ROLE=<path> in the
// session env. The session's command is a small shell snippet that
// passes the role file's contents as the agent's first positional
// arg when the file is readable and non-empty (so a default
// claude-code agent boots with the role as its first user message),
// and falls back to spawning the agent bare otherwise (so test agents
// like `sleep 30` and consumers without a role file installed are
// unaffected). Returns the session name and whether a spawn actually
// happened.
func EnsureCoordinator(projectRoot string) (string, bool, error) {
	session := CoordinatorSessionName(projectRoot)
	if hasSession(session) {
		return session, false, nil
	}

	agent := coordinatorAgent()
	rolePath := CoordinatorRolePath(projectRoot)
	project, err := task.ProjectName(projectRoot)
	if err != nil {
		return "", false, err
	}
	inbox, err := task.CoordinatorInboxDirForProject(projectRoot)
	if err != nil {
		return "", false, err
	}
	coordinatorState, err := task.CoordinatorStateDir()
	if err != nil {
		return "", false, err
	}

	cmd := coordinatorShellCommand(agent, rolePath)
	args := []string{
		"new-session", "-d",
		"-s", session,
		"-c", projectRoot,
		"-e", "SPORE_TASK_SLUG=" + CoordinatorSlug,
		"-e", "SPORE_COORDINATOR_ROLE=" + rolePath,
		"-e", "SPORE_PROJECT_ROOT=" + projectRoot,
		"-e", "WT_PROJECT=" + project,
		"-e", "SKYBOT_INBOX=" + inbox,
		"-e", "SPORE_COORDINATOR_STATE_DIR=" + coordinatorState,
		cmd,
	}
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return session, true, nil
}

// coordinatorAgent picks the binary the coordinator session execs.
// Precedence: SPORE_COORDINATOR_AGENT (lets operators run a different
// agent for the singleton coordinator) > SPORE_AGENT_BINARY (the same
// var workers honour) > "claude-code" (the kernel default).
func coordinatorAgent() string {
	if a := os.Getenv(CoordinatorAgentEnv); a != "" {
		return a
	}
	if a := os.Getenv(task.AgentBinaryEnv); a != "" {
		return a
	}
	return "claude-code"
}

// coordinatorShellCommand builds the shell snippet tmux runs for the
// coordinator session. tmux invokes its operator shell to parse this
// string; the agent token is intentionally left unquoted so callers
// can pass space-bearing values (e.g. SPORE_AGENT_BINARY="sleep 30")
// the same way worker spawn does.
func coordinatorShellCommand(agent, rolePath string) string {
	q := shellSingleQuote(rolePath)
	return fmt.Sprintf(
		`if [ -r %[1]s ] && [ -s %[1]s ]; then exec %[2]s "$(cat %[1]s)"; else exec %[2]s; fi`,
		q, agent,
	)
}

// shellSingleQuote returns s wrapped in single quotes, with embedded
// single quotes escaped, suitable for splicing into a shell-command
// string passed through tmux.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ReapCoordinator kills the coordinator tmux session for projectRoot.
// Idempotent: a missing session is not an error. Returns whether a
// kill was attempted.
func ReapCoordinator(projectRoot string) bool {
	session := CoordinatorSessionName(projectRoot)
	if !hasSession(session) {
		return false
	}
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	return true
}

func hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}
