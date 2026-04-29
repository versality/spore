package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/install"
)

const installUsage = `spore install - drop spore skills into a target project

Usage:
  spore install [--root <path>]

Copies the bundled skill bodies (spore-bootstrap, diagram) into
<root>/.claude/skills/ so claude-code in that project can discover and
run them. Idempotent: re-runs only rewrite files whose contents drifted
from the embedded copy.

Flags:
  --root   Project root to install into. Defaults to the current
           directory.
`

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	root := fs.String("root", "", "project root (default: cwd)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		fmt.Fprint(os.Stderr, installUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(installUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore install: unexpected positional args:", fs.Args())
		return 2
	}

	dest := *root
	if dest == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore install:", err)
			return 1
		}
		dest = cwd
	}

	res, err := install.Install(dest, spore.BundledSkills, "bootstrap/skills")
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		return 1
	}
	for _, p := range res.Written {
		rel, _ := filepath.Rel(dest, p)
		fmt.Printf("wrote %s\n", rel)
	}
	if len(res.Written) == 0 {
		fmt.Println("install: already up to date")
	} else {
		fmt.Printf("installed %d file(s) under %s/.claude/skills/\n", len(res.Written), dest)
	}
	return 0
}
