package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/versality/spore/internal/budget"
	"github.com/versality/spore/internal/coordinator/spawn"
)

func runCoordinatorSpawn(args []string) int {
	fs := flag.NewFlagSet("coordinator spawn", flag.ContinueOnError)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator spawn:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator spawn - bring up coordinator tmux session, block until it dies")
		fmt.Println("  Reads SKYHELM_BRIEF, SKYHELM_DRIVER[_FILE], SKYHELM_STATE_DIR,")
		fmt.Println("  SKYHELM_CODEX_EFFORT, SKYHELM_CLAUDE_EFFORT, SKYHELM_MODEL,")
		fmt.Println("  WT_AGENT_MODEL, WT_CFG. Installs a session-closed[99] hook and")
		fmt.Println("  blocks on tmux wait-for. SIGTERM kills the session and exits 0.")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator spawn")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := spawn.Config{TierLookup: budget.LookupActiveTier}
	if err := spawn.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "[skyhelm-spawn]", err)
		return 1
	}
	return 0
}
