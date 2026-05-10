package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	mergeaudit "github.com/versality/spore/internal/merge/audit"
	mergeunblock "github.com/versality/spore/internal/merge/unblock"
)

const mergeUsage = `spore merge - post-merge integrity helpers

Usage:
  spore merge <subcommand> [flags]

Subcommands:
  audit      Compare HEAD / index / working-tree blobs across a
             pathspec set and report any drift, with the commit that
             owns each blob. Exit 1 when drift is reported.
  unblock    Clear the tasks/<slug>.md drift that blocks wt merge:
             checkout (tracked) or rm (untracked) the working-tree
             copy when it matches HEAD or wt/<slug>; refuse with
             exit 2 when neither matches.
`

const mergeAuditUsage = `spore merge audit - post-merge integrity probe

Usage:
  spore merge audit [--root <path>] [--owner-scan-limit N] [pathspec...]

Flags:
  --root              Repo root (default: $PWD).
  --owner-scan-limit  Per-path log scan budget (default 200).

With no positional pathspecs, the conservative default set is used.
Exits non-zero when drift is reported.
`

const mergeUnblockUsage = `spore merge unblock - clear tasks/<slug>.md drift

Usage:
  spore merge unblock <slug> [--root <path>]

Flags:
  --root   Main repo root (default: parent of git common dir).

Exits 2 when the working tree matches neither HEAD nor wt/<slug>
(i.e. genuine local work, not drift).
`

func runMerge(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, mergeUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(mergeUsage)
		return 0
	case "audit":
		return runMergeAudit(rest)
	case "unblock":
		return runMergeUnblock(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore merge: unknown subcommand %q\n\n%s", sub, mergeUsage)
		return 2
	}
}

func runMergeAudit(args []string) int {
	fs := flag.NewFlagSet("merge audit", flag.ContinueOnError)
	root := fs.String("root", "", "repo root (default: $PWD)")
	scanLim := fs.Int("owner-scan-limit", mergeaudit.OwnerScanLimit, "per-path log scan budget")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore merge audit:", err)
		fmt.Fprint(os.Stderr, mergeAuditUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(mergeAuditUsage)
		return 0
	}
	if *root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore merge audit:", err)
			return 1
		}
		*root = cwd
	}
	pathspecs := fs.Args()
	cfg := mergeaudit.Config{Pathspecs: pathspecs, OwnerScanLim: *scanLim}
	drifts, err := mergeaudit.Run(mergeaudit.LocalGit{Root: *root}, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore merge audit:", err)
		return 1
	}
	if mergeaudit.FormatReport(os.Stdout, drifts) {
		return 1
	}
	return 0
}

func runMergeUnblock(args []string) int {
	fs := flag.NewFlagSet("merge unblock", flag.ContinueOnError)
	root := fs.String("root", "", "main repo root (default: git common dir parent)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore merge unblock:", err)
		fmt.Fprint(os.Stderr, mergeUnblockUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(mergeUnblockUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore merge unblock: expected exactly one positional <slug>")
		fmt.Fprint(os.Stderr, mergeUnblockUsage)
		return 2
	}
	slug := fs.Arg(0)
	if *root == "" {
		r, err := resolveMainRoot()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore merge unblock:", err)
			return 1
		}
		*root = r
	}
	repo := mergeunblock.LocalRepo{Root: *root}
	code, err := mergeunblock.Run(repo, slug, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore merge unblock:", err)
		return 1
	}
	return code
}
