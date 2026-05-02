package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/budget"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"
)

const fleetUsage = `spore fleet - reconcile worker tmux sessions against the task queue

Usage:
  spore fleet reconcile [--max-workers N]
  spore fleet replenish-hook
  spore fleet enable
  spore fleet disable
  spore fleet status

Subcommands:
  reconcile       Run a single reconcile pass: list status=active tasks; for
                  each one without a live tmux session, ensure the worktree
                  and spawn a session; for each spore-prefix tmux session
                  whose task is no longer active, kill it. Idempotent;
                  exits 0 when there is nothing to do. Short-circuits when
                  the kill-switch flag is missing.
  replenish-hook  Stop-hook variant of reconcile: reads context from env
                  ($SKYBOT_INBOX, $WT_PROJECT, $WT_FLEET_FLOOR), no-ops in
                  non-coordinator sessions, skips when the budget advice
                  is tighten/ration, and never propagates errors.
  enable          Create the kill-switch flag (the reconciler resumes
                  spawning on the next pass).
  disable         Remove the kill-switch flag (the reconciler stops
                  spawning; running sessions are left alone).
  status          Print the kill-switch state plus the list of slugs whose
                  session is currently alive.

Flags (reconcile):
  --max-workers N   Override concurrency cap. Beats spore.toml.
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
	case "enable":
		return runFleetEnable(rest)
	case "disable":
		return runFleetDisable(rest)
	case "status":
		return runFleetStatus(rest)
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
	resolved, err := resolveMaxWorkers(*maxWorkers, root)
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

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	slugs, err := task.SpawnedSlugs(root)
	if err != nil {
		return err
	}
	if len(slugs) == 0 {
		fmt.Println("sessions: none")
		return nil
	}
	fmt.Println("sessions:")
	for _, s := range slugs {
		fmt.Printf("  %s\n", s)
	}
	return nil
}

func resolveMaxWorkers(flagVal int, projectRoot string) (int, error) {
	if flagVal > 0 {
		return flagVal, nil
	}
	if env := os.Getenv("SPORE_FLEET_MAX_WORKERS"); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("SPORE_FLEET_MAX_WORKERS=%q: want positive integer", env)
		}
		return n, nil
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
//     $SKYBOT_INBOX vs $SKYHELM_STATE_DIR / $SPORE_COORDINATOR_STATE_DIR)
//   - skip the spawn pass when budget advice is "tighten" / "ration"
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

	if a := budget.Advice(); a == "tighten" || a == "ration" {
		fmt.Fprintf(os.Stderr, "replenish-hook: skipping reconcile (budget advice=%s)\n", a)
		return nil
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "replenish-hook:", err)
		return nil
	}
	resolved, err := resolveMaxWorkers(0, root)
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

// isCoordinatorSession reports whether $SKYBOT_INBOX points under the
// coordinator state dir (matching the bash self_id == "coordinator" test).
// Honours both SKYHELM_STATE_DIR (legacy) and SPORE_COORDINATOR_STATE_DIR
// (kernel-neutral) so the hook works during the rename transition.
func isCoordinatorSession() bool {
	inbox := os.Getenv("SKYBOT_INBOX")
	if inbox == "" {
		return false
	}
	for _, key := range []string{"SKYHELM_STATE_DIR", "SPORE_COORDINATOR_STATE_DIR"} {
		root := strings.TrimRight(os.Getenv(key), "/")
		if root == "" {
			continue
		}
		if inbox == root || strings.HasPrefix(inbox, root+"/") {
			return true
		}
	}
	return false
}
