// Command spore is the kernel CLI: compose CLAUDE.md from a rule
// pool, drive the bootstrap stage gates, manage tasks, run lints and
// hooks, and wrap nixos-anywhere for fresh-server installs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/align"
	"github.com/versality/spore/internal/composer"
	"github.com/versality/spore/internal/infect"
)

const usage = `spore - LLM-coding-agent harness kernel

Usage:
  spore <command> [flags]

Commands:
  compose    Render a CLAUDE.md from a consumer's rule list.
  task       Manage tasks (new, ls, start, pause, block, done, tell).
  fleet      Run the worker fleet against the task queue (up/down/status).
  align      Track and exit the pilot-agent alignment period.
  bootstrap  Walk a fresh project through the stage gates.
  install    Drop the spore skills into a project's .claude/skills/.
  infect     Bootstrap a fresh server with NixOS via nixos-anywhere.
  lint       Run portable lints over the working tree.
  hooks      Install or run claude-code / git hooks.
  budget     Track rolling 5h + 7d Anthropic spend; gate Stop on cap crossings.
  coordinator  Coordinator support (state-debt, verify-done, loop-guard).
`

const lintUsage = `spore lint - run portable lints over the working tree

Usage:
  spore lint [--root <path>]

Flags:
  --root   Repo root to lint. Defaults to the current directory.

Exits non-zero when any lint reports an issue.
`

const hooksUsage = `spore hooks - install or run kernel hooks

Usage:
  spore hooks <subcommand> [args]

Subcommands:
  install                Wire core.hooksPath to a generated dir under .git/.
  commit-msg <file>      Run the em-dash check on a commit message file.
  pretooluse             Read a claude-code PreToolUse JSON request from
                         stdin, write the response JSON to stdout.
  stop                   Read a claude-code Stop JSON request from stdin,
                         write the (currently no-op) response JSON.
  settings               Read a hook binding JSON from stdin, emit a
                         deterministic settings.json to stdout.
  watch-inbox <slug>          Stop-hook: block on inbox, exit 2 on message.
  notify-coordinator <slug>   Write a poke to the coordinator's project inbox.
`

const infectUsage = `spore infect - bootstrap a fresh server with NixOS via nixos-anywhere

Usage:
  spore infect <ip> --ssh-key <path> [--flake <path-or-attr>] [--hostname <name>] [--user <user>]

Flags:
  --ssh-key   Path to the private SSH key to install with (required).
              The .pub sibling is installed as the post-install root key.
  --flake     Path or flake-ref (with optional #attr) to use. Defaults
              to the bundled minimal flake at bootstrap/flake.
  --hostname  networking.hostName for the bundled flake. Default "nixos".
              Ignored when --flake is supplied.
  --user      SSH user nixos-anywhere connects as. Default "root".

WARNING: the target host is wiped during install. Only point this at a
freshly provisioned VM that has no data worth keeping.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "compose":
		if err := runCompose(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore compose:", err)
			os.Exit(1)
		}
	case "task":
		if err := runTask(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore task:", err)
			os.Exit(1)
		}
	case "fleet":
		if err := runFleet(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore fleet:", err)
			os.Exit(1)
		}
	case "infect":
		os.Exit(runInfect(args))
	case "lint":
		os.Exit(runLint(args))
	case "hooks":
		os.Exit(runHooks(args))
	case "align":
		if err := runAlign(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore align:", err)
			os.Exit(1)
		}
	case "bootstrap":
		if err := runBootstrap(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore bootstrap:", err)
			os.Exit(1)
		}
	case "install":
		os.Exit(runInstall(args))
	case "budget":
		os.Exit(runBudget(args))
	case "coordinator":
		os.Exit(runCoordinator(args))
	default:
		fmt.Fprintf(os.Stderr, "spore: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func runCompose(args []string) error {
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	consumer := fs.String("consumer", "", "consumer name (file under <rules>/consumers/<name>.txt)")
	rulesDir := fs.String("rules", "rules", "rule pool directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *consumer == "" {
		return fmt.Errorf("--consumer is required")
	}

	consumerPath := filepath.Join(*rulesDir, "consumers", *consumer+".txt")
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	alignActive, err := align.Active(root)
	if err != nil {
		return err
	}
	opts := composer.Options{Predicates: map[string]bool{"align": alignActive}}
	out, err := composer.Compose(*rulesDir, consumerPath, opts)
	if err != nil {
		return err
	}
	_, err = os.Stdout.WriteString(out)
	return err
}

// boolFlag mirrors the unexported interface stdlib `flag` uses to tell
// bool flags (which never consume a following arg) from value flags.
type boolFlag interface {
	flag.Value
	IsBoolFlag() bool
}

// reorderFlagsFirst moves `-` / `--` tokens to the head of args so
// stdlib flag (which stops at the first non-flag) accepts the
// title-first ordering documented in the task new / ls help. Bare
// `--` is honoured: tokens after it stay in place as positional.
//
// Non-bool flags written in split form (`--ssh-key path`) keep their
// value attached during the reorder so the value does not get
// reinterpreted as a positional. fs is consulted to distinguish bool
// from value flags; unknown names are also treated as value flags so
// flag.Parse reports the unknown name without swallowing a positional.
func reorderFlagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	passthrough := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if passthrough {
			positional = append(positional, a)
			continue
		}
		if a == "--" {
			passthrough = true
			positional = append(positional, a)
			continue
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if isBoolFlag(fs, name) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func isBoolFlag(fs *flag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	bf, ok := f.Value.(boolFlag)
	return ok && bf.IsBoolFlag()
}

// runInfect parses `spore infect` flags, runs the install, and
// returns the process exit code (mirroring nixos-anywhere's exit when
// the wrapped command failed).
func runInfect(args []string) int {
	fs := flag.NewFlagSet("infect", flag.ContinueOnError)
	sshKey := fs.String("ssh-key", "", "path to the private SSH key (required)")
	flake := fs.String("flake", "", "path or flake-ref (default: bundled minimal flake)")
	hostname := fs.String("hostname", infect.DefaultHostname, "networking.hostName for the bundled flake")
	user := fs.String("user", infect.DefaultUser, "SSH user nixos-anywhere connects as")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore infect:", err)
		fmt.Fprint(os.Stderr, infectUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(infectUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore infect: expected exactly one positional <ip>")
		fmt.Fprint(os.Stderr, infectUsage)
		return 2
	}
	if strings.TrimSpace(*sshKey) == "" {
		fmt.Fprintln(os.Stderr, "spore infect: --ssh-key is required")
		return 2
	}
	c := infect.Config{
		IP:       fs.Arg(0),
		SSHKey:   *sshKey,
		Flake:    *flake,
		Hostname: *hostname,
		User:     *user,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := infect.Run(ctx, c, spore.BundledFlake, os.Stdout, os.Stderr)
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		fmt.Fprintln(os.Stderr, "spore infect:", err)
		return exitErr.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "spore infect:", err)
	return 1
}
