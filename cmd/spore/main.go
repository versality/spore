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
	"time"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/align"
	"github.com/versality/spore/internal/composer"
	"github.com/versality/spore/internal/infect"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
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

const taskUsage = `spore task - manage tasks

Usage:
  spore task <subcommand> [flags]

Subcommands:
  new <title> [--body-stdin]   Create a tasks/<slug>.md with status=draft.
  ls [--all]                   List tasks (default hides done).
  start <slug>                 Flip to active, spawn worktree + tmux session.
  pause <slug>                 Flip active task to paused (no teardown).
  block <slug>                 Flip active task to blocked (no teardown).
  done <slug>                  Flip to done, kill tmux + remove worktree.
  tell <slug> <message>        Append a message to the slug's inbox dir.
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

func runTask(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, taskUsage)
		return fmt.Errorf("subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(taskUsage)
		return nil
	case "new":
		return runTaskNew(rest)
	case "ls":
		return runTaskLs(rest)
	case "start":
		return runTaskStart(rest)
	case "pause":
		return runTaskPause(rest)
	case "block":
		return runTaskBlock(rest)
	case "done":
		return runTaskDone(rest)
	case "tell":
		return runTaskTell(rest)
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", sub, taskUsage)
	}
}

func runTaskStart(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task start <slug>")
	}
	session, err := task.Start("tasks", args[0])
	if err != nil {
		return err
	}
	fmt.Println(session)
	return nil
}

func runTaskPause(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task pause <slug>")
	}
	return task.Pause("tasks", args[0])
}

func runTaskBlock(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task block <slug>")
	}
	return task.Block("tasks", args[0])
}

func runTaskDone(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task done <slug>")
	}
	return task.Done("tasks", args[0])
}

func runTaskTell(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: spore task tell <slug> <message>")
	}
	return task.Tell(args[0], args[1])
}

func runTaskNew(args []string) error {
	fs := flag.NewFlagSet("task new", flag.ContinueOnError)
	bodyStdin := fs.Bool("body-stdin", false, "read body from stdin")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("expected exactly one positional <title>, got %d", fs.NArg())
	}
	title := fs.Arg(0)
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title must not be empty")
	}

	tasksDir := "tasks"
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return err
	}
	base := task.Slugify(title)
	if base == "" {
		return fmt.Errorf("title %q yields empty slug", title)
	}
	slug, err := task.Allocate(tasksDir, base)
	if err != nil {
		return err
	}

	var body []byte
	if *bodyStdin {
		body, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	}

	project, _ := task.ProjectName("")
	m := frontmatter.Meta{
		Status:  "draft",
		Slug:    slug,
		Title:   title,
		Created: time.Now().UTC().Format(time.RFC3339),
		Project: project,
	}
	out := frontmatter.Write(m, body)
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Println(slug)
	return nil
}

func runTaskLs(args []string) error {
	fs := flag.NewFlagSet("task ls", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done tasks")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	metas, err := task.List("tasks")
	if err != nil {
		return err
	}
	fmt.Println("SLUG\tSTATUS\tTITLE")
	for _, m := range metas {
		if !*all && m.Status == "done" {
			continue
		}
		fmt.Printf("%s\t%s\t%s\n", m.Slug, m.Status, m.Title)
	}
	return nil
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

