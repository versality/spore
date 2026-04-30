package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/versality/spore/internal/lints"
)

// runLint runs every default lint over the working tree and prints
// findings. Exits 0 when clean, 1 on any issue, 2 on usage error.
func runLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	root := fs.String("root", ".", "repo root to lint")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore lint:", err)
		fmt.Fprint(os.Stderr, lintUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(lintUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore lint: unexpected positional args:", fs.Args())
		return 2
	}

	bad := false
	taskEvidenceWarnOnly := lints.EvidenceWarnOnly()
	var firstErr error
	for _, l := range lints.Default() {
		issues, err := l.Run(*root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "spore lint: %s: %v\n", l.Name(), err)
			if firstErr == nil {
				firstErr = err
			}
			bad = true
			continue
		}
		warnOnly := l.Name() == "task-evidence" && taskEvidenceWarnOnly
		for _, i := range issues {
			line := prefix(l.Name(), i.String())
			if warnOnly {
				fmt.Fprintln(os.Stderr, "warn: "+line)
				continue
			}
			fmt.Fprintln(os.Stdout, line)
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

func prefix(name, msg string) string {
	return "[" + name + "] " + strings.TrimRight(msg, "\n")
}
