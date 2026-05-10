package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/coordinator/idlewatchdog"
)

func runCoordinatorIdleWatchdog(args []string) int {
	fs := flag.NewFlagSet("coordinator idle-watchdog", flag.ContinueOnError)
	hook := fs.Bool("hook", false, "run as Stop hook (self-gate by SKYBOT_INBOX, dedupe via fingerprint)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator idle-watchdog:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator idle-watchdog [--hook] - quiet-idle gate")
		fmt.Println("  --hook  run as Stop hook (self-gate, fingerprint dedup)")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "spore coordinator idle-watchdog: unexpected arg: %s\n", fs.Arg(0))
		return 1
	}

	res := idlewatchdog.Check(idlewatchdog.Config{HookMode: *hook})
	if res.Stdout != "" {
		os.Stdout.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		os.Stderr.WriteString(res.Stderr)
	}
	return res.ExitCode
}
