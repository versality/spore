package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/coordinator/loopguard"
	"github.com/versality/spore/internal/coordinator/statedebt"
	"github.com/versality/spore/internal/coordinator/verify"
)

const coordinatorUsage = `spore coordinator - coordinator support commands

Usage:
  spore coordinator <subcommand> [flags]

Subcommands:
  role-brief     Render the coordinator role brief to stdout.
  state-debt     Scan state.md for prose lessons that should be lifted.
  verify-done    Run the verify-done verdict for a slug.
  loop-guard     Check the respawn circuit breaker.
`

func runCoordinator(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, coordinatorUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(coordinatorUsage)
		return 0
	case "role-brief":
		return runCoordinatorRoleBrief(rest)
	case "state-debt":
		return runCoordinatorStateDebt(rest)
	case "verify-done":
		return runCoordinatorVerifyDone(rest)
	case "loop-guard":
		return runCoordinatorLoopGuard(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore coordinator: unknown subcommand %q\n\n%s", sub, coordinatorUsage)
		return 2
	}
}

func runCoordinatorRoleBrief(args []string) int {
	fs := flag.NewFlagSet("coordinator role-brief", flag.ContinueOnError)
	rolePath := fs.String("role", "", "path to role file (default: auto-detect)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator role-brief:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator role-brief - render the coordinator role brief")
		fmt.Println("  --role <path>   path to role file")
		return 0
	}

	path := *rolePath
	if path == "" {
		root, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore coordinator role-brief:", err)
			return 1
		}
		path = root + "/bootstrap/coordinator/role.md"
	}

	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator role-brief: %v\n", err)
		return 1
	}
	os.Stdout.Write(content)
	return 0
}

func runCoordinatorStateDebt(args []string) int {
	fs := flag.NewFlagSet("coordinator state-debt", flag.ContinueOnError)
	verbose := fs.Bool("verbose", false, "print full classification table")
	verboseShort := fs.Bool("v", false, "print full classification table")
	stateFile := fs.String("state-file", "", "path to state.md (default: auto-detect)")
	ageDays := fs.Int("age-days", statedebt.DefaultAgeDays, "threshold in days")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state-debt:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator state-debt - scan state.md for stale lessons")
		fmt.Println("  --verbose, -v     print full classification table")
		fmt.Println("  --state-file      path to state.md")
		fmt.Println("  --age-days N      threshold in days (default 14)")
		return 0
	}

	cfg := statedebt.Config{AgeDays: *ageDays}
	if *stateFile != "" {
		cfg.StateFile = *stateFile
	}

	result, err := statedebt.Scan(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator state-debt: %v\n", err)
		return 1
	}

	if *verbose || *verboseShort {
		fmt.Print(statedebt.FormatVerbose(result))
	}

	if result.StaleCount > 0 {
		fmt.Println(statedebt.FormatSummary(result))
		return 2
	}
	return 0
}

func runCoordinatorVerifyDone(args []string) int {
	fs := flag.NewFlagSet("coordinator verify-done", flag.ContinueOnError)
	root := fs.String("root", "", "project root (default: auto-detect)")
	events := fs.String("events", "", "path to events.jsonl")
	projects := fs.String("projects", "", "path to projects dir")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator verify-done:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator verify-done <slug> - run the verify-done verdict")
		fmt.Println("  --root       project root")
		fmt.Println("  --events     events.jsonl path")
		fmt.Println("  --projects   claude projects dir")
		return 0
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator verify-done <slug>")
		return 2
	}
	slug := fs.Arg(0)

	cfg := verify.Config{
		ProjectRoot: *root,
		EventsFile:  *events,
		ProjectsDir: *projects,
	}

	result := verify.Verify(slug, cfg)
	fmt.Print(result.Format())
	return 0
}

func runCoordinatorLoopGuard(args []string) int {
	fs := flag.NewFlagSet("coordinator loop-guard", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "", "state directory")
	maxRespawns := fs.Int("max-respawns", loopguard.DefaultMaxRespawns, "max respawns in window")
	reset := fs.Bool("reset", false, "reset the trip marker")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator loop-guard:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator loop-guard - check respawn circuit breaker")
		fmt.Println("  --state-dir       state directory")
		fmt.Println("  --max-respawns N  max respawns in window (default 5)")
		fmt.Println("  --reset           clear the trip marker")
		return 0
	}

	dir := *stateDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.local/state/coordinator"
	}

	if *reset {
		if err := loopguard.Reset(dir); err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "spore coordinator loop-guard: %v\n", err)
				return 1
			}
		}
		fmt.Println("loop-guard: reset")
		return 0
	}

	cfg := loopguard.Config{
		StateDir:    dir,
		MaxRespawns: *maxRespawns,
	}
	status, err := loopguard.Check(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator loop-guard: %v\n", err)
		return 1
	}

	if status.Tripped {
		fmt.Printf("loop-guard: TRIPPED (recent=%d, max=%d, cooldown=%s)\n",
			status.RecentCount, status.MaxRespawns, status.CooldownLeft)
		return 2
	}
	fmt.Printf("loop-guard: ok (recent=%d, max=%d)\n",
		status.RecentCount, status.MaxRespawns)
	return 0
}
