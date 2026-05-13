package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/coordinator/slascan"
)

func runCoordinatorSlaScan(args []string) int {
	fs := flag.NewFlagSet("coordinator sla-scan", flag.ContinueOnError)
	verbose := fs.Bool("verbose", false, "print classification trace for every scanned entry")
	verboseShort := fs.Bool("v", false, "print classification trace for every scanned entry")
	stateFile := fs.String("state-file", "", "path to state.md (default: $SPORE_COORDINATOR_STATE_DIR/state.md)")
	stateDir := fs.String("state-dir", "", "state directory (default: $SPORE_COORDINATOR_STATE_DIR or ~/.local/state/spore/coordinator)")
	tasksDir := fs.String("tasks-dir", "", "tasks directory for slug-status map (default: $SPORE_TASKS_DIR or empty)")
	ageSec := fs.Int64("age-seconds", 0, "stale threshold in seconds (default 7200)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator sla-scan:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator sla-scan - flag stale / done / orphan state.md entries")
		fmt.Println("  --verbose, -v          print classification trace for every entry")
		fmt.Println("  --state-file PATH      state.md path")
		fmt.Println("  --state-dir DIR        state dir (used when --state-file unset)")
		fmt.Println("  --tasks-dir DIR        tasks dir for slug-status map")
		fmt.Println("  --age-seconds N        stale threshold (default 7200)")
		fmt.Println("Env:")
		fmt.Println("  SPORE_COORDINATOR_STATE_DIR / SPORE_COORDINATOR_STATE_FILE")
		fmt.Println("  SPORE_TASKS_DIR / SPORE_SLA_AGE_SECONDS")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator sla-scan [--verbose] [--state-file PATH] [--tasks-dir DIR] [--age-seconds N]")
		return 2
	}

	cfg := slascan.Config{
		StateDir:   *stateDir,
		StateFile:  *stateFile,
		TasksDir:   *tasksDir,
		AgeSeconds: *ageSec,
		Verbose:    *verbose || *verboseShort,
	}
	res, err := slascan.Scan(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator sla-scan: %v\n", err)
		return 1
	}
	if cfg.Verbose {
		for _, line := range res.Trace {
			fmt.Println(line)
		}
	}
	if len(res.Findings) > 0 {
		fmt.Fprint(os.Stderr, slascan.FormatFindings(res.Findings))
		return 2
	}
	return 0
}
