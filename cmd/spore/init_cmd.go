package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/initconfig"
)

const initUsage = `spore init - scaffold a default spore.toml in the project root

Usage:
  spore init [--root <path>] [--force]
  spore init [--root <path>] --section <name> [--section <name>...]

With no flags, writes a documented default spore.toml when one is
missing. Re-runs are no-ops on an existing file. Pass --force to
overwrite. Pass --section <name> (repeatable) to append a specific
block to a partial spore.toml without touching the rest.

Known sections:
  fleet.workers   default agent + ratio + (commented) complexity rules
  coordinator     coordinator session driver / model / brief
  align           pilot-agent alignment exit thresholds
  matter.linear   Linear adapter shape

Flags:
  --root      Project root (defaults to cwd).
  --force     Overwrite an existing spore.toml with the full template.
  --section   Section to add if missing. Repeatable. Mutually exclusive
              with --force; --force is ignored when --section is set.
`

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	root := fs.String("root", "", "project root (default: cwd)")
	force := fs.Bool("force", false, "overwrite an existing spore.toml")
	var sections stringList
	fs.Var(&sections, "section", "section to append if missing (repeatable)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore init:", err)
		fmt.Fprint(os.Stderr, initUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(initUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore init: unexpected positional args:", fs.Args())
		return 2
	}
	dest := *root
	if dest == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore init:", err)
			return 1
		}
		dest = cwd
	}
	res, err := initconfig.Run(dest, initconfig.Options{Force: *force, Sections: []string(sections)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore init:", err)
		return 1
	}
	rel := res.Path
	if r, err := filepath.Rel(dest, res.Path); err == nil {
		rel = r
	}
	switch {
	case res.Created && len(res.Added) > 0:
		fmt.Printf("created %s with sections: %v\n", rel, res.Added)
	case res.Created:
		fmt.Printf("created %s\n", rel)
	case res.Overwritten:
		fmt.Printf("overwrote %s with default template\n", rel)
	case len(res.Added) > 0:
		fmt.Printf("appended sections to %s: %v\n", rel, res.Added)
	case res.NoOp:
		fmt.Printf("%s already up to date\n", rel)
	}
	return 0
}
