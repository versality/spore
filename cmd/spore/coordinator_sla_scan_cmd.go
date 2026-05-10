package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/coordinator/slascanner"
)

func runCoordinatorSLAScan(args []string) int {
	fs := flag.NewFlagSet("coordinator sla-scan", flag.ContinueOnError)
	verbose := fs.Bool("verbose", false, "print full classification table")
	verboseShort := fs.Bool("v", false, "print full classification table")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator sla-scan:", err)
		return 1
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator sla-scan - flag stale / done / orphan state.md entries")
		fmt.Println("  --verbose, -v  print classification trace for every scanned entry")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator sla-scan [--verbose]")
		return 1
	}

	cfg := slascanner.Config{Verbose: *verbose || *verboseShort}
	res, err := slascanner.Scan(cfg)
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
		fmt.Fprint(os.Stderr, slascanner.FormatFindings(res.Findings))
		return 2
	}
	return 0
}
