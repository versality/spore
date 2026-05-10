package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/versality/spore/internal/lints"
)

// runLint runs the default lint set or a single named lint over the
// working tree and prints findings. Exits 0 when clean, 1 on any
// issue, 2 on usage error.
func runLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	root := fs.String("root", ".", "repo root to lint")
	list := fs.Bool("list", false, "list every named lint and exit")
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
	if *list {
		printLintList(os.Stdout)
		return 0
	}

	var toRun []lints.Lint
	switch fs.NArg() {
	case 0:
		toRun = lints.Default()
	case 1:
		name := fs.Arg(0)
		l, ok := lints.Named()[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "spore lint: unknown lint %q\n\n", name)
			printLintList(os.Stderr)
			return 2
		}
		toRun = []lints.Lint{l}
	default:
		fmt.Fprintln(os.Stderr, "spore lint: expected at most one positional <name>:", fs.Args())
		return 2
	}

	bad := false
	taskEvidenceWarnOnly := lints.EvidenceWarnOnly()
	var firstErr error
	for _, l := range toRun {
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

func printLintList(w *os.File) {
	defaults := map[string]bool{}
	for _, l := range lints.Default() {
		defaults[l.Name()] = true
	}
	names := make([]string, 0, len(lints.Named()))
	for n := range lints.Named() {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "available lints (D = in spore lint default set):")
	for _, n := range names {
		marker := " "
		if defaults[n] {
			marker = "D"
		}
		fmt.Fprintf(w, "  [%s] %s\n", marker, n)
	}
}

func prefix(name, msg string) string {
	return "[" + name + "] " + strings.TrimRight(msg, "\n")
}
