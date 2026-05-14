// Package wtcheck implements `spore wt-check`: the Go replacement
// for the per-project `.wt/check.sh` step that wt-go's `wt merge`
// runs after rebasing the rower branch onto main. The wrapper exists
// so projects can keep their lint+test gate in Go instead of shipping
// a bash trampoline; net-bash stays flat under the bash-net-positive
// merge lint.
//
// The check itself is opinionated: `nix develop -c just check` from
// the repo root. The shell that opens is the contract surface; spore
// does not pre-validate `flake.nix` or `justfile` presence.
package wtcheck

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Config is the runtime input.
type Config struct {
	// Root is the directory `nix develop -c just check` runs in.
	// Required.
	Root string
}

// Runner executes one external command. Implementations either shell
// out (LocalRunner) or record calls (tests). It returns the process
// exit code on a clean run (possibly non-zero) plus a nil err, or
// (0, err) when the runner itself failed (e.g. binary not found).
type Runner func(dir, name string, args []string, stdout, stderr io.Writer) (int, error)

// Run executes `nix develop -c just check` from cfg.Root and returns
// the exit code wt-go should propagate. Wrapper-level errors (nix
// missing, bad runner) collapse to 2 with a diagnostic on stderr.
func Run(cfg Config, runner Runner, stdout, stderr io.Writer) int {
	if cfg.Root == "" {
		fmt.Fprintln(stderr, "spore wt-check: root is empty")
		return 2
	}
	code, err := runner(cfg.Root, "nix", []string{"develop", "-c", "just", "check"}, stdout, stderr)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			fmt.Fprintln(stderr, "spore wt-check: nix not on PATH")
			return 2
		}
		fmt.Fprintln(stderr, "spore wt-check:", err)
		return 2
	}
	return code
}

// LocalRunner shells out to the real binary, streaming stdout/stderr.
// A non-zero exit returns (code, nil); only infrastructure errors
// (binary missing, fork failure) come back as a non-nil err.
func LocalRunner(dir, name string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// GitTopLevel runs `git rev-parse --show-toplevel` from dir and
// returns the trimmed result. Used by the cmd layer when no --root
// override is passed.
func GitTopLevel(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
