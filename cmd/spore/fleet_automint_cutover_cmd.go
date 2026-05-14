package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/fleet"
)

const fleetAutoMintCutoverUsage = `spore fleet auto-mint-cutover - mint consume-spore-lint-<x> drafts for shipped lints

Usage:
  spore fleet auto-mint-cutover [--repo PATH] [--allowlist PATH]
                                [--tasks-dir PATH] [--target-project NAME]
                                [--max-mints N] [--wt-bin PATH] [--dry-run]

Walks <repo>/harness/bash-migration-allowlist.txt (default
<cwd>/harness/bash-migration-allowlist.txt). For each row whose
basename matches lint-<x>.sh / check-<x>.sh / block-<x>.sh AND <x> is
a shipped spore lint AND no tasks/*.md already carries scheduler-key
cutover-automint:<x>, mints

  wt task new --project <target> --effort medium \
              --scheduler-key cutover-automint:<x> \
              "consume-spore-lint-<x>: drop bash <file>, wire spore lint <x>"

Up to --max-mints fresh mints per tick (default 3). Floor gate is
deliberately not enforced; the reconciler decides promotion.

Idempotency:
  - --scheduler-key dedupe at wt task new time (non-done only).
  - Pre-scan of tasks/*.md scheduler_key frontmatter (done included).

Flags:
  --repo PATH           Consumer repo root (default: cwd).
  --allowlist PATH      Allowlist file (default: <repo>/harness/bash-migration-allowlist.txt).
  --tasks-dir PATH      Tasks dir to scan for existing scheduler keys (default: <repo>/tasks).
  --target-project NAME wt project name for the mint (default: nix-config).
  --max-mints N         Cap on fresh mints per tick (default: 3).
  --wt-bin PATH         wt binary path (default: wt).
  --dry-run             Log intent without minting.
`

func runFleetAutoMintCutover(args []string) error {
	fs := flag.NewFlagSet("fleet auto-mint-cutover", flag.ContinueOnError)
	repo := fs.String("repo", "", "consumer repo root (default cwd)")
	allowlist := fs.String("allowlist", "", "allowlist path (default <repo>/harness/bash-migration-allowlist.txt)")
	tasksDir := fs.String("tasks-dir", "", "tasks dir (default <repo>/tasks)")
	target := fs.String("target-project", "nix-config", "wt project for the mint")
	maxMints := fs.Int("max-mints", 3, "max fresh mints per tick")
	wtBin := fs.String("wt-bin", "wt", "wt binary path")
	dryRun := fs.Bool("dry-run", false, "log intent without minting")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetAutoMintCutoverUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("fleet auto-mint-cutover: unexpected positional args: %v", fs.Args())
	}

	res, err := fleet.AutoMintCutover(fleet.AutoMintCutoverConfig{
		Repo:          *repo,
		AllowlistPath: *allowlist,
		TasksDir:      *tasksDir,
		TargetProject: *target,
		MaxMints:      *maxMints,
		WTBin:         *wtBin,
		DryRun:        *dryRun,
		Stdout:        os.Stdout,
		Stderr:        os.Stderr,
	})
	if err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		os.Exit(1)
	}
	return nil
}
