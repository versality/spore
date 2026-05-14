package hooks

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/versality/spore/internal/sessionkind"
)

// ErrGateMiss signals that the runtime kind did not match any allowed
// kind. Callers exit 0 silently so claude-code treats the hook as a
// no-op.
var ErrGateMiss = errors.New("session-kind mismatch")

// ErrGateUsage signals bad args (no kinds, missing `--`, empty
// command). Callers exit 2.
var ErrGateUsage = errors.New("gate-kind usage")

// GateKindExitError carries the inner command's exit code. Callers
// mirror that exit code so claude-code sees the hook's real result.
type GateKindExitError struct{ Code int }

func (e *GateKindExitError) Error() string {
	return fmt.Sprintf("gate-kind: inner command exited %d", e.Code)
}

// GateKind reads sessionkind.Env, compares it against the kind
// list in args, and either runs the wrapped command (stdin/stdout/
// stderr inherited) or returns ErrGateMiss. An empty session kind
// never matches, so operator-interactive sessions always skip.
//
// args is the slice after the "gate-kind" subcommand name; must
// contain at least one kind, then "--", then the command and its
// args. On a successful run that exits zero, GateKind returns nil.
// On a non-zero inner exit, it returns *GateKindExitError so the CLI
// can propagate the code.
func GateKind(args []string, getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}
	if dashIdx < 1 {
		return fmt.Errorf("%w: spore hooks gate-kind <kind...> -- <cmd> [args]", ErrGateUsage)
	}
	if dashIdx == len(args)-1 {
		return fmt.Errorf("%w: missing command after `--`", ErrGateUsage)
	}
	kinds := args[:dashIdx]
	cmdline := args[dashIdx+1:]

	have := getenv(sessionkind.Env)
	if have == "" {
		return ErrGateMiss
	}
	matched := false
	for _, k := range kinds {
		if k == have {
			matched = true
			break
		}
	}
	if !matched {
		return ErrGateMiss
	}

	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &GateKindExitError{Code: exitErr.ExitCode()}
		}
		return fmt.Errorf("gate-kind: %w", err)
	}
	return nil
}
