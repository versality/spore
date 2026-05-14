package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/wtcheck"
)

const wtCheckUsage = `spore wt-check - run the project's lint+test gate

Usage:
  spore wt-check [--root <path>]
  spore wt-check --probe

Flags:
  --root    Repo root the check runs in. Defaults to
            ` + "`git rev-parse --show-toplevel`" + ` from $PWD.
  --probe   Print one line and exit 0 without running the check.
            wt-go's merge step uses this to detect that ` + "`spore wt-check`" + `
            is available before falling back to .wt/check.sh.

Behavior:
  Executes ` + "`nix develop -c just check`" + ` from the repo root and
  propagates the exit code. Wrapper-level errors (nix missing on PATH,
  empty root) exit 2 with a diagnostic on stderr.

The --probe contract is the stable interface wt-go's merge step
calls. It must stay cheap (no subprocess) and side-effect-free.
`

func runWtCheck(args []string) int {
	fs := flag.NewFlagSet("wt-check", flag.ContinueOnError)
	root := fs.String("root", "", "repo root (default: git rev-parse --show-toplevel)")
	probe := fs.Bool("probe", false, "print availability and exit 0 without running")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore wt-check:", err)
		fmt.Fprint(os.Stderr, wtCheckUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(wtCheckUsage)
		return 0
	}
	if *probe {
		fmt.Println("spore wt-check available")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "spore wt-check: unexpected positional args: %v\n\n%s", fs.Args(), wtCheckUsage)
		return 2
	}
	if *root == "" {
		r, err := wtcheck.GitTopLevel(".")
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore wt-check: git rev-parse --show-toplevel:", err)
			return 2
		}
		*root = r
	}
	return wtcheck.Run(wtcheck.Config{Root: *root}, wtcheck.LocalRunner, os.Stdout, os.Stderr)
}
