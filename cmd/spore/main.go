// Command spore is the kernel CLI: compose agent instructions from a
// rule pool, drive the bootstrap stage gates, manage tasks, run lints
// and hooks, and wrap nixos-anywhere for fresh-server installs.
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
  version    Print the spore version.
  compose    Render agent instructions from a consumer's rule list.
  task       Manage tasks (new, ls, start, pause, park, block, done, tell).
  todo       Maintenance on docs/todo/ specs (archive-aged-maybes).
  fleet      Run the worker fleet against the task queue (up/down/status).
  align      Track and exit the pilot-agent alignment period.
  bootstrap  Walk a fresh project through the stage gates.
  init       Write a default spore.toml when one is missing.
  install    Drop the spore skills into a project's .claude/skills/.
  infect     Bootstrap a fresh server with NixOS via nixos-anywhere.
  lint       Run portable lints over the working tree.
  scout      Append lint findings to a JSONL ledger for the healer pipeline.
  hooks      Install or run claude-code / git hooks.
  budget     Track rolling 5h + 7d Anthropic spend; gate Stop on cap crossings.
  coordinator  Coordinator session lifecycle (start/stop/restart/status) plus support hooks.
  worker     Worker support hooks (token-monitor).
  opencode   Opencode-driver lifecycle helpers (fleet-stop, liveness).
  merge      Post-merge integrity helpers (audit, unblock).
  search     Lookup helpers (nix packages / options against search.nixos.org).
  secret     Manage age secrets (add via tmux popup; audit registration / consumers).
  signal     Record warning / error signals from a wrapped command.
  wt-check   Run the project's lint+test gate (nix develop -c just check).
`

const lintUsage = `spore lint - run portable lints over the working tree

Usage:
  spore lint [--root <path>]
  spore lint <name> [--root <path>]
  spore lint --list

Flags:
  --root   Repo root to lint. Defaults to the current directory.
  --list   Print every named lint with a marker for default-set membership.
  --json   Emit findings as JSONL on stdout (one object per line:
           ts, lint, severity, path, line, message, fingerprint).
           Consumed by 'spore scout mint-healers'.
  --allowlist, --consumers-dir, --rules-dir, --render-cmd, --consumers-cmd,
           --limit, --ext, --skip-path, --root-line-limit, --root-char-limit,
           --subdir-line-limit

Per-lint config can also live in spore.toml as [lint.<name>].

claude-drift adapters:
  Default (file-based):  one consumer per <consumers_dir>/<name>.txt with
                         "# target: <repo-relative-path>" directives.
                         render_cmd swaps the built-in composer; reads
                         SPORE_LINT_ROOT, SPORE_LINT_CONSUMER,
                         SPORE_LINT_CONSUMER_FILE, SPORE_LINT_RULES_DIR.
  Composer-driven:       consumers_cmd shells out once; stdout must be
                         JSON of the form
                           [{"name": "<id>",
                             "target_path": "<repo-relative>",
                             "rendered_text": "<expected content>"}, ...]
                         Spore reads target_path off disk and diffs
                         against rendered_text. When set, consumers_dir,
                         rules_dir, and render_cmd are ignored.

With no positional, runs the default set. With a name, runs that single
named lint (default-set or not). Exits non-zero when any lint reports
an issue.
`

const scoutUsage = `spore scout - append lint findings to a JSONL ledger

Usage:
  spore scout [--root <path>] [--state-file <path>] [--stdout]

Flags:
  --root         Repo root to scan. Defaults to the current directory.
  --state-file   JSONL ledger to append findings to. Defaults to
                 $XDG_STATE_HOME/spore/scout/scout-findings.jsonl
                 (with $HOME/.local/state fallback).
  --stdout       Also echo each finding to stdout. Off by default;
                 scout is silent unless a lint errors.

scout is the upstream half of the self-heal pipeline: it runs the
default lint set and records every finding as one JSONL row
{ts, lint, severity, path, line, message, fingerprint} appended to
the ledger. 'spore scout mint-healers' consumes the ledger to mint
healer tasks.

Re-running scout appends; the mint step de-dups by fingerprint so a
fresh ledger row for the same finding does not mint a duplicate task.
Exit codes: 0 clean (or appended cleanly), 1 a lint errored, 2 usage.

Subcommands:
  scan          (default) walk the tree and append to the ledger.
  mint-healers  read the ledger and mint healer tasks for new clusters.
                See 'spore scout mint-healers --help'.
`

const scoutMintUsage = `spore scout mint-healers - mint healer tasks from the scout ledger

Usage:
  spore scout mint-healers [--ledger <path>] [--tasks-dir <path>]
                           [--project <name>] [--max <int>]
                           [--minted <path>] [--falsepos <path>]
                           [--dry-run]

Flags:
  --ledger     JSONL findings ledger. Defaults to
               $XDG_STATE_HOME/spore/scout/scout-findings.jsonl.
  --tasks-dir  Directory to write tasks/<slug>.md into. Default 'tasks'.
  --project    Project name written into each brief. Default 'spore'.
  --max        Hard cap on healers minted per run. Default 10. Oldest
               cluster first; the rest carry to the next run.
  --minted     Append-only ledger of scheduler keys already minted.
               Default state-dir/scout-minted.tsv.
  --falsepos   List of fingerprints to skip. Healers append a finding
               here when they decide it's not a real issue. Default
               state-dir/scout-falsepos.tsv.
  --dry-run    Compute clusters and would-be slugs without writing.

Clustering: findings group by (lint, dirname(path)). One healer per
cluster, scheduler-keyed by the cluster's content hash. Re-running
without changing findings is a no-op.

Stdout: one minted slug per line. Stderr: a one-line summary.
`

const hooksUsage = `spore hooks - install or run kernel hooks

Usage:
  spore hooks <subcommand> [args]

Subcommands:
  install                Wire core.hooksPath to a generated dir under .git/.
  commit-msg <file>      Run the em-dash check on a commit message file.
  pre-commit             Run staged Go gofmt checks.
  pretooluse             Read a claude-code PreToolUse JSON request from
                         stdin, write the response JSON to stdout.
  stop                   Read a claude-code Stop JSON request from stdin,
                         write the (currently no-op) response JSON.
  wtmerge-mechanical     Stop-hook (M1): exit 2 with a wt-merge nudge
                         when a worker idles on wt/<slug> with shipped-
                         but-unmerged commits and a clean tree.
  push-pending           Stop-hook (M-finish-B / I8): exit 2 with a
                         push nudge when local main is ahead of
                         origin/main and the worker is idle.
  pr-finish              Stop-hook (M-finish-C / I9 + I10): exit 2
                         with a merge / rebase / CI-fix prompt based
                         on the open PR for wt/<slug>.
  settings [--kind K]    Read a hook binding JSON from stdin, emit a
                         deterministic settings.json to stdout. --kind
                         (or $SPORE_RENDER_KIND) drops bindings whose
                         "kinds" list omits K; empty omits no bindings.
  render [--kind K] [--hooks-config P] [--extras P] [--out P] [--claude-dir D]
                         End-to-end: load hooks-config.json, render for
                         --kind, overlay settings-extras.json, write to
                         --out (stdout if omitted). --claude-dir defaults
                         the three paths to <dir>/{hooks-config,
                         settings-extras,settings}.json. Missing extras
                         is treated as no overlay.
  gate-kind K... -- CMD ARGS
                         Read $WT_SESSION_KIND. If unset or not in K...,
                         exit 0 silently. Otherwise exec CMD ARGS. Use
                         as a wrapper around session-scoped hooks.
  watch-inbox [slug]          Stop-hook: block on inbox, exit 2 on message.
                              With no slug, reads $SPORE_TASK_INBOX.
  stop-watchdog               Stop-hook: spawn a detached tick child and
                              exit 0. Wire as the first Stop hook so the
                              child gets a head start on the chain.
  stop-watchdog-tick          Detached child of stop-watchdog. Sleeps
                              $SPORE_STOP_WATCHDOG_SECONDS (default 5),
                              classifies the agent's tmux pane, and on a
                              still-running pane appends a slow-stop-chain
                              row to worker-stop-errors.jsonl plus a force-
                              release tell into $SPORE_TASK_INBOX. Intended
                              for harness wiring only; do not invoke
                              directly.
  notify-coordinator [project]
                              Write a poke to the coordinator's project inbox.
                              With no project, reads $WT_PROJECT.
  plan-ready-mechanical       Stop-hook: if tasks/<slug>.md has a ## Plan
                              section, task status is active, and the
                              coordinator inbox has no "plan ready: <slug>"
                              envelope yet, emit one. Reads $SPORE_TASK_SLUG,
                              $WT_PROJECT, $SPORE_COORDINATOR_STATE_DIR;
                              always exits 0 (no-op outside worker context).
  worker-continue              Stop-hook: refuse to idle when tasks/<slug>.md
                              is status=active and no blocker is recorded
                              (no unread inbox, no plan ack pending, no
                              recent token-wrap). Exit 2 with a one-line
                              reminder. Honours the fleet kill-switch
                              (silent during operator pause windows).
                              Per-slug ledger at $XDG_STATE_HOME/spore/
                              worker-continue/<slug>.json suppresses
                              re-firing until the worker makes progress
                              (frontmatter mtime/size or HEAD changes).
  worker-stop-force-closing   Stop-hook: refuse to end a turn without a
                              closing move. Exit 2 unless any of these
                              fired since the previous Stop: status flip
                              away from active, HEAD advanced on wt/<slug>,
                              or an Edit/Write/MultiEdit tool_use in the
                              claude-code transcript. First turn is a no-op
                              (state seeds on the first invocation).
                              Reads $SPORE_TASK_SLUG plus the request's
                              transcript_path. State at $XDG_STATE_HOME/
                              spore/worker-stop-force-closing/<slug>.json.
  codex <event>          Codex hook adapters. Events: session-start.
  context-tee            Stop-hook: write a per-session token JSON
                         snapshot for status displays. Reads
                         $SPORE_TASK_INBOX (and $SPORE_COORDINATOR_STATE_DIR
                         for the role decision); writes
                         <state>/token.json (coordinator) or
                         <SPORE_WORKER_TOKEN_DIR>/<slug>.json (worker).
                         Always exits 0.
`

const infectUsage = `spore infect - bootstrap a fresh server with NixOS via nixos-anywhere

Usage:
  spore infect <ip> --ssh-key <path> [--repo <local-path>] [--flake <path-or-attr>] [--hostname <name>] [--user <user>]
                                      [--coordinator-agent claude|codex] [--coordinator-model <model>]
                                      [--coordinator-effort <effort>]

Flags:
  --ssh-key   Path to the private SSH key to install with (required).
              The .pub sibling is installed as the post-install root and spore key.
  --repo      Local repo checkout to copy to /home/spore/<basename> after install.
              The copy includes .git and excludes .env* secrets and build artifacts.
  --flake     Path or flake-ref (with optional #attr) to use. Defaults
              to the bundled minimal flake at bootstrap/flake.
  --hostname  networking.hostName for the bundled flake. Default "nixos".
              Ignored when --flake is supplied.
  --user      SSH user nixos-anywhere connects as. Default "root".
  --coordinator-agent
              Agent provider for the first coordinator. Default "claude".
  --coordinator-model
              Model passed to the coordinator agent. Empty uses that CLI's default.
  --coordinator-effort
              Codex reasoning effort for the coordinator. Default "high".

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
	case "-v", "--version", "version":
		fmt.Println(spore.BuildVersion())
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
	case "todo":
		if err := runTodo(args); err != nil {
			fmt.Fprintln(os.Stderr, "spore todo:", err)
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
	case "scout":
		os.Exit(runScout(args))
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
	case "init":
		os.Exit(runInit(args))
	case "install":
		os.Exit(runInstall(args))
	case "budget":
		os.Exit(runBudget(args))
	case "coordinator":
		os.Exit(runCoordinator(args))
	case "worker":
		os.Exit(runWorker(args))
	case "opencode":
		os.Exit(runOpencode(args))
	case "merge":
		os.Exit(runMerge(args))
	case "search":
		os.Exit(runSearch(args))
	case "secret":
		os.Exit(runSecret(args))
	case "signal":
		os.Exit(runSignal(args))
	case "wt-check":
		os.Exit(runWtCheck(args))
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
	repo := fs.String("repo", "", "local repo checkout to copy after install")
	flake := fs.String("flake", "", "path or flake-ref (default: bundled minimal flake)")
	hostname := fs.String("hostname", infect.DefaultHostname, "networking.hostName for the bundled flake")
	user := fs.String("user", infect.DefaultUser, "SSH user nixos-anywhere connects as")
	coordinatorAgent := fs.String("coordinator-agent", infect.DefaultCoordinatorAgent, "coordinator agent provider: claude or codex")
	coordinatorModel := fs.String("coordinator-model", "", "model passed to the coordinator agent")
	coordinatorEffort := fs.String("coordinator-effort", infect.DefaultCoordinatorEffort, "codex reasoning effort for the coordinator")
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
		IP:                fs.Arg(0),
		SSHKey:            *sshKey,
		Repo:              *repo,
		Flake:             *flake,
		Hostname:          *hostname,
		User:              *user,
		CoordinatorAgent:  *coordinatorAgent,
		CoordinatorModel:  *coordinatorModel,
		CoordinatorEffort: *coordinatorEffort,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := infect.Run(ctx, c, spore.BundledFlake, spore.BundledHandover, os.Stdout, os.Stderr)
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
