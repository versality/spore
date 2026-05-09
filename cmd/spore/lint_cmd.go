package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/versality/spore/internal/lints"
	"github.com/versality/spore/internal/lints/bashnetpositive"
)

// runLint runs every default lint over the working tree and prints
// findings. Exits 0 when clean, 1 on any issue, 2 on usage error.
//
// `spore lint bash-net-positive` is a separate path: it takes a base
// ref and inspects commit messages, so it doesn't fit the standard
// Lint contract over the working tree.
func runLint(args []string) int {
	if len(args) > 0 && args[0] == "bash-net-positive" {
		return runLintBashNetPositive(args[1:])
	}

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

// runLintBashNetPositive runs the harness bash hard gate. Returns 0
// on pass / override-applied, 1 on refuse, 2 on usage error.
func runLintBashNetPositive(args []string) int {
	fs := flag.NewFlagSet("lint bash-net-positive", flag.ContinueOnError)
	repoRoot := fs.String("repo-root", ".", "repo root to inspect")
	baseRef := fs.String("base-ref", "main", "base ref to diff HEAD against")
	format := fs.String("format", "text", "output format: text or json")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore lint bash-net-positive:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(lintUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore lint bash-net-positive: unexpected positional args:", fs.Args())
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(os.Stderr, "spore lint bash-net-positive: --format must be text or json")
		return 2
	}

	res, err := bashnetpositive.Run(*repoRoot, *baseRef)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore lint bash-net-positive:", err)
		return 2
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, "spore lint bash-net-positive:", err)
			return 2
		}
	} else {
		printBashNetPositiveText(os.Stdout, os.Stderr, res)
	}

	if res.Verdict == bashnetpositive.Refuse {
		return 1
	}
	return 0
}

func printBashNetPositiveText(stdout, stderr io.Writer, r bashnetpositive.Result) {
	for _, w := range r.Warnings {
		fmt.Fprintln(stderr, "warn:", w)
	}
	fmt.Fprintf(stdout, "verdict: %s\n", r.Verdict)
	fmt.Fprintf(stdout, "net bash LOC (harness/): %+d\n", r.NetBashLoc)
	if len(r.AllowedNewFiles) > 0 {
		fmt.Fprintf(stdout, "allowed new bash files: %s\n", strings.Join(r.AllowedNewFiles, ", "))
	}
	if len(r.NewBashFiles) > 0 {
		fmt.Fprintf(stdout, "new bash files: %s\n", strings.Join(r.NewBashFiles, ", "))
	}
	if r.Override != "" {
		fmt.Fprintf(stdout, "override: %s\n", r.Override)
	}
	for _, reason := range r.Reasons {
		fmt.Fprintf(stdout, "reason: %s\n", reason)
	}
}
