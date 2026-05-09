package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/versality/spore/internal/event"
	"github.com/versality/spore/internal/shrink"
)

const shrinkUsage = `spore shrink - harness-thinness metrics

Usage:
  spore shrink probe [--repo-root PATH] [--wt-state-dir PATH] [--no-publish]

Subcommands:
  probe  Walk the repo, append one event to the bus, print JSON to stdout.

Flags (probe):
  --repo-root      Repo root to measure. Default: current working directory.
  --wt-state-dir   Wt task state dir. Default: $XDG_STATE_HOME/wt
                   (or ~/.local/state/wt).
  --no-publish     Skip writing to the event bus; print JSON only. Useful
                   for tests and ad-hoc snapshots.

The published event:
  source = repo-shrink-probe
  kind   = shrink-probe
  level  = info
  data   = {repo, bash_loc, bash_files, wtgo_loc, wt_state_files,
            hook_count, ts}
`

func runShrink(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, shrinkUsage)
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(shrinkUsage)
		return 0
	case "probe":
		return runShrinkProbe(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore shrink: unknown subcommand %q\n\n%s", sub, shrinkUsage)
		return 2
	}
}

func runShrinkProbe(args []string) int {
	fs := flag.NewFlagSet("shrink probe", flag.ContinueOnError)
	repoRoot := fs.String("repo-root", "", "")
	wtStateDir := fs.String("wt-state-dir", "", "")
	noPublish := fs.Bool("no-publish", false, "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore shrink probe:", err)
		fmt.Fprint(os.Stderr, shrinkUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(shrinkUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore shrink probe: unexpected positional args")
		return 2
	}

	root := strings.TrimSpace(*repoRoot)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore shrink probe:", err)
			return 1
		}
		root = cwd
	}

	snap, err := shrink.Probe(shrink.Options{
		RepoRoot:   root,
		WtStateDir: *wtStateDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore shrink probe:", err)
		return 1
	}

	body, err := json.Marshal(&snap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore shrink probe:", err)
		return 1
	}
	if _, err := os.Stdout.Write(append(body, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "spore shrink probe:", err)
		return 1
	}

	if *noPublish {
		return 0
	}

	msg := fmt.Sprintf(
		"shrink probe bash=%d/%d wtgo=%d wt-state=%d hooks=%d",
		snap.BashLoc, snap.BashFiles, snap.WtgoLoc, snap.WtStateFiles, snap.HookCount,
	)
	ev := &event.Event{
		Source:  "repo-shrink-probe",
		Kind:    "shrink-probe",
		Level:   event.LevelInfo,
		Message: msg,
		Data:    json.RawMessage(body),
		Ts:      snap.Ts,
	}
	if err := event.Append(ev); err != nil {
		fmt.Fprintln(os.Stderr, "spore shrink probe: publish:", err)
		return 1
	}
	return 0
}
