package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/budget"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"

	// Linear is the first matter adapter; the side-effect import
	// fires its init() so [matter.linear] in spore.toml (or
	// SPORE_MATTER_LINEAR__* env) wires through fleet.Reconcile's
	// matter prelude pass.
	_ "github.com/versality/spore/internal/matter/linear"
)

const fleetUsage = `spore fleet - reconcile worker tmux sessions against the task queue

Usage:
  spore fleet reconcile [--max-workers N]
  spore fleet replenish-hook
  spore fleet wake [<slug>]
  spore fleet reap [--force-published]
  spore fleet evict-idle [--idle-secs N] [--dry-run] [--all-projects]
  spore fleet auto-mint-cutover [flags]
  spore fleet enable
  spore fleet disable
  spore fleet status
  spore fleet list-sessions [--project NAME] [--kind worker|coordinator|any]

Subcommands:
  reconcile       Run a single reconcile pass: when [matter.linear] is
                  set in spore.toml, poll Linear for ready/done updates
                  first; then list status=active tasks; for each one
                  without a live tmux session, ensure the worktree and
                  spawn a session; for each managed tmux session
                  whose task is no longer active, kill it. Idempotent;
                  exits 0 when there is nothing to do. Short-circuits when
                  the kill-switch flag is missing.
  replenish-hook  Stop-hook variant of reconcile: reads context from env
                  ($SPORE_TASK_INBOX, $WT_PROJECT, $WT_FLEET_FLOOR), no-ops in
                  non-coordinator sessions, skips when the budget advice
                  is tighten, and never propagates errors.
  wake            Scan active workers across configured projects and
                  re-mint the tmux session of any idle worker with an
                  unread inbox. Body-free: never types the inbox event
                  into the tmux input. Deduped by a 5-min wake-pending
                  marker (override via WT_WORKER_WAKE_PENDING_TTL).
                  When <slug> is given, only that slug is considered.
  evict-idle      Flip status=active tasks to status=blocked /
                  blocker=auto:idle-no-progress when all three idle
                  signals hold for >threshold: tmux session inactive,
                  inbox drained, no recent commit on wt/<slug>. Uses
                  task.BlockAuto so reap semantics free the tmux
                  session. Disabled via SPORE_EVICTOR=0|false|off|no.
                  --all-projects sweeps every spore harness under
                  $HOME (used by the systemd-user timer).
  reap            Walk every worktree under .worktrees/ and tear down
                  done / orphan tasks: kill the tmux session, run
                  ` + "`" + `git worktree remove --force` + "`" + `, and delete the wt/<slug>
                  branch (refuses branches with unlanded commits unless
                  contained in origin/main). Blocked tasks have their
                  session killed but worktree preserved.
  auto-mint-cutover Mint consume-spore-lint-<x> drafts in a consumer
                  repo whenever a shipped spore lint still has its
                  bash predecessor named on harness/bash-migration-
                  allowlist.txt. Dedupe via scheduler-key
                  cutover-automint:<x>. No floor gate; bound by
                  --max-mints (default 3) per tick. See
                  ` + "`" + `spore fleet auto-mint-cutover --help` + "`" + ` for flags.
  enable          Create the kill-switch flag (the reconciler resumes
                  spawning on the next pass).
  disable         Remove the kill-switch flag (the reconciler stops
                  spawning; running sessions are left alone).
  status          Print the kill-switch state plus the list of slugs whose
                  session is currently alive.
  list-sessions   List tmux sessions belonging to a project, parsed
                  by the canonical matcher. TSV columns:
                  name<TAB>project<TAB>slug<TAB>kind<TAB>tag<TAB>shape
                  Default project = current cwd's project; default kind
                  = worker. Exits 0 with empty output when tmux is not
                  running or nothing matches.

Flags (reconcile):
  --max-workers N   Override concurrency cap. Beats spore.toml.

Flags (reap):
  --force-published Also reap active tasks whose wt/<slug> is contained
                    in origin/main and whose tasks/<slug>.md on
                    origin/main is done or superseded. Used after a
                    cross-fleet merge so the local copy catches up.
`

const fleetWakeUsage = `spore fleet wake - re-mint idle workers with unread inbox

Usage:
  spore fleet wake [<slug>]

Walks every status=active task on the local host (across projects
configured in $WT_CFG/projects), filters to ones whose tmux pane is
classified as idle and whose inbox has unread *.json events, and calls
the same ensure-session path used by ` + "`" + `spore task start` + "`" + ` to nudge the
agent back into a turn. Idempotent: a fresh wake-pending marker (5 min
default; WT_WORKER_WAKE_PENDING_TTL overrides) suppresses repeat wakes.

Body-free: the inbox event payload is never typed into the tmux input.
The worker drains its inbox via its own Stop-hook on the next agent turn.

Exit codes: 0 on success (including no-op), 2 when one or more wakes
failed.
`

const fleetReapUsage = `spore fleet reap - tear down done / orphan worktrees and sessions

Usage:
  spore fleet reap [--force-published]

Walks every git worktree rooted at <project>/.worktrees/ across the
configured projects (` + "$WT_CFG/projects" + `, falling back to cwd's main
repo when the file is missing). Per status:

  active            no-op (handled by spore fleet reconcile); see
                    --force-published for the published-merge cleanup.
  blocked           kill tmux session, keep worktree
  done | missing    kill session, ` + "`git worktree remove --force`" + `, and
                    ` + "`git branch -d`" + ` (or -D when contained in origin/main)

Sentinel: when tasks/<slug>.md is missing on main but wt/<slug> still
holds unlanded commits, the worktree/session/branch are preserved
(wt-merge-unblock window).

Flags:
  --force-published Also reap active tasks whose wt/<slug> is in
                    origin/main and whose origin/main task is
                    done/superseded.
`

func runFleet(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, fleetUsage)
		return fmt.Errorf("subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(fleetUsage)
		return nil
	case "reconcile":
		return runFleetReconcile(rest)
	case "replenish-hook":
		return runFleetReplenishHook(rest)
	case "wake":
		return runFleetWake(rest)
	case "reap":
		return runFleetReap(rest)
	case "evict-idle":
		return runFleetEvictIdle(rest)
	case "auto-mint-cutover":
		return runFleetAutoMintCutover(rest)
	case "enable":
		return runFleetEnable(rest)
	case "disable":
		return runFleetDisable(rest)
	case "status":
		return runFleetStatus(rest)
	case "list-sessions":
		return runFleetListSessions(rest)
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", sub, fleetUsage)
	}
}

func runFleetReconcile(args []string) error {
	fs := flag.NewFlagSet("fleet reconcile", flag.ContinueOnError)
	maxWorkers := fs.Int("max-workers", 0, "concurrency cap (0 = use spore.toml or default)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	resolved, err := resolveMaxWorkers(*maxWorkers, root, os.Stderr)
	if err != nil {
		return err
	}

	res, err := fleet.Reconcile(fleet.Config{
		TasksDir:    "tasks",
		ProjectRoot: root,
		MaxWorkers:  resolved,
	})
	if err != nil {
		return err
	}
	if res.Disabled {
		flagPath, _ := fleet.FlagPath()
		fmt.Printf("fleet: disabled (flag missing at %s)\n", flagPath)
		return nil
	}
	fmt.Printf("fleet: active=%d spawned=%d kept=%d reaped=%d skipped=%d\n",
		len(res.Active), len(res.Spawned), len(res.Kept), len(res.Reaped), len(res.Skipped))
	if len(res.Spawned) > 0 {
		fmt.Printf("  spawned: %s\n", strings.Join(res.Spawned, ", "))
	}
	if len(res.Reaped) > 0 {
		fmt.Printf("  reaped: %s\n", strings.Join(res.Reaped, ", "))
	}
	if len(res.Skipped) > 0 {
		fmt.Printf("  skipped: %s (max-workers=%d)\n", strings.Join(res.Skipped, ", "), resolved)
	}
	for _, m := range res.Matter {
		if m.Err != nil {
			fmt.Fprintf(os.Stderr, "  matter %s: %v\n", m.Name, m.Err)
			continue
		}
		fmt.Printf("  matter %s: created=%d updated=%d\n", m.Name, m.Created, m.Updated)
	}
	return nil
}

func runFleetEnable(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: spore fleet enable")
	}
	if err := fleet.Enable(); err != nil {
		return err
	}
	p, _ := fleet.FlagPath()
	fmt.Printf("fleet: enabled (%s)\n", p)
	return nil
}

func runFleetDisable(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: spore fleet disable")
	}
	if err := fleet.Disable(); err != nil {
		return err
	}
	p, _ := fleet.FlagPath()
	fmt.Printf("fleet: disabled (%s removed)\n", p)
	return nil
}

func runFleetWake(args []string) error {
	fs := flag.NewFlagSet("fleet wake", flag.ContinueOnError)
	slugFlag := fs.String("slug", "", "wake only this slug (also accepted as positional arg)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetWakeUsage)
		return nil
	}
	slug := *slugFlag
	switch fs.NArg() {
	case 0:
	case 1:
		if slug != "" && slug != fs.Arg(0) {
			return fmt.Errorf("fleet wake: positional slug %q conflicts with --slug %q", fs.Arg(0), slug)
		}
		slug = fs.Arg(0)
	default:
		return fmt.Errorf("fleet wake: unexpected positional args: %v", fs.Args()[1:])
	}
	rc, err := fleet.Wake(slug, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if rc != 0 {
		os.Exit(rc)
	}
	return nil
}

func runFleetReap(args []string) error {
	fs := flag.NewFlagSet("fleet reap", flag.ContinueOnError)
	forcePublished := fs.Bool("force-published", false, "also reap active tasks closed on origin/main")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetReapUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("fleet reap: unexpected positional args: %v", fs.Args())
	}
	rc, err := fleet.Reap(*forcePublished, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if rc != 0 {
		os.Exit(rc)
	}
	return nil
}

func runFleetStatus(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: spore fleet status")
	}
	on, err := fleet.Enabled()
	if err != nil {
		return err
	}
	if on {
		fmt.Println("fleet: enabled")
	} else {
		fmt.Println("fleet: disabled")
	}
	rc, err := fleet.RunStatus(os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if rc == 2 {
		os.Exit(2)
	}
	return nil
}

// runFleetListSessions prints parsed tmux session inventory for a
// project. Replaces the shell `tmux list-sessions | grep` patterns
// in nixosModules/spore-fleet.nix and is the sanctioned API for
// shell consumers that need worker-vs-coordinator awareness.
//
// Default project = task.ProjectName(cwd) with cwd's basename as
// fallback (mirrors LegacySessionName resolution). Default kind =
// worker.
//
// Exit codes:
//
//	0 always (no tmux server, no matching sessions, success).
//	non-zero only on flag parse or project-name errors.
func runFleetListSessions(args []string) error {
	fs := flag.NewFlagSet("fleet list-sessions", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project name (default: derived from cwd)")
	kindFlag := fs.String("kind", "worker", "filter by kind: worker, coordinator, any")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("fleet list-sessions: unexpected positional args: %v", fs.Args())
	}
	kind := *kindFlag
	switch kind {
	case "worker", "coordinator", "any":
	default:
		return fmt.Errorf("fleet list-sessions: --kind %q: want worker|coordinator|any", kind)
	}
	project := *projectFlag
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		name, err := task.ProjectName(cwd)
		if err != nil || name == "" {
			project = filepath.Base(cwd)
		} else {
			project = name
		}
	}

	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// No tmux server / no sessions: nothing to print, exit 0.
		return nil
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		p, ok := task.MatchProject(line, project)
		if !ok {
			continue
		}
		if kind != "any" && p.Kind != kind {
			continue
		}
		shape := "current"
		if p.Legacy {
			shape = "legacy"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.Project, p.Slug, p.Kind, p.Tag, shape)
	}
	return nil
}

func resolveMaxWorkers(flagVal int, projectRoot string, warn io.Writer) (int, error) {
	var envVal int
	if env := os.Getenv("SPORE_FLEET_MAX_WORKERS"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("SPORE_FLEET_MAX_WORKERS=%q: want positive integer", env)
		}
		envVal = n
	}
	if flagVal > 0 {
		if envVal > 0 && envVal != flagVal && warn != nil {
			fmt.Fprintf(warn, "spore fleet: --max-workers=%d wins over SPORE_FLEET_MAX_WORKERS=%d (env ignored)\n", flagVal, envVal)
		}
		return flagVal, nil
	}
	if envVal > 0 {
		return envVal, nil
	}
	if env := os.Getenv("WT_FLEET_FLOOR"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("WT_FLEET_FLOOR=%q: want positive integer", env)
		}
		return n, nil
	}
	return fleet.LoadMaxWorkers(projectRoot)
}

// runFleetReplenishHook is the Stop-hook entry point for `spore fleet
// replenish-hook`. It reads context entirely from the environment so a
// settings.json hook entry can be a static string (no slug
// interpolation). Behaviour mirrors the bash cmd_fleet_replenish_hook
// shim:
//
//   - swallow stdin (claude-code feeds the hook payload there)
//   - no-op when the firing session is not the coordinator (per
//     $SPORE_TASK_INBOX vs $SPORE_COORDINATOR_STATE_DIR)
//   - skip the spawn pass when budget advice is "tighten"
//   - never exit non-zero: a failing reconcile must not block the Stop
//     hook
//
// $WT_PROJECT and $WT_FLEET_FLOOR are read by the helpers above
// (resolveMaxWorkers honours $WT_FLEET_FLOOR; project root is the
// firing session's cwd, which is the per-project coordinator's tree).
func runFleetReplenishHook(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: spore fleet replenish-hook (no args; reads env)")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)

	if !isCoordinatorSession() {
		return nil
	}

	if a := budget.Advice(); a == "tighten" {
		fmt.Fprintf(os.Stderr, "replenish-hook: skipping reconcile (budget advice=%s)\n", a)
		return nil
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "replenish-hook:", err)
		return nil
	}
	resolved, err := resolveMaxWorkers(0, root, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replenish-hook:", err)
		return nil
	}

	res, err := fleet.Reconcile(fleet.Config{
		TasksDir:    "tasks",
		ProjectRoot: root,
		MaxWorkers:  resolved,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "replenish-hook:", err)
		return nil
	}
	if res.Disabled {
		return nil
	}
	fmt.Printf("replenish-hook: active=%d spawned=%d kept=%d reaped=%d skipped=%d\n",
		len(res.Active), len(res.Spawned), len(res.Kept), len(res.Reaped), len(res.Skipped))
	return nil
}

// isCoordinatorSession reports whether $SPORE_TASK_INBOX points under the
// coordinator state dir.
func isCoordinatorSession() bool {
	inbox := os.Getenv("SPORE_TASK_INBOX")
	if inbox == "" {
		return false
	}
	root := strings.TrimRight(os.Getenv("SPORE_COORDINATOR_STATE_DIR"), "/")
	if root == "" {
		return false
	}
	return inbox == root || strings.HasPrefix(inbox, root+"/")
}
