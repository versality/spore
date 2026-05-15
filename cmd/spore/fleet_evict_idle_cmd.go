package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/versality/spore/internal/evictor"
)

const fleetEvictIdleUsage = `spore fleet evict-idle - flip idle workers to status: blocked

Usage:
  spore fleet evict-idle [--idle-secs N] [--dry-run] [--all-projects]

Walks every status=active task in the current project (or each
project under $HOME when --all-projects is set) and evaluates the
three-input predicate: tmux session has not seen activity within the
soak window, the slug's inbox has no unread events, and the
wt/<slug> branch has not advanced within the soak window. When all
three hold, the worker is flipped via task.BlockAuto with blocker
` + "`" + evictor.BlockerKey + "`" + `. Tolerant: per-slug failures are recorded but
do not abort the sweep.

Flags:
  --idle-secs N    Soak window in seconds. Beats $SPORE_EVICTOR_IDLE_SECS;
                   default ` + "`" + `10m` + "`" + `.
  --dry-run        Report would-evict verdicts without flipping
                   frontmatter.
  --all-projects   Sweep every spore project under $HOME (one level
                   deep). Mirrors the bash spore-fleet-tick.sh walker
                   but stays in Go. Used by the systemd-user timer.

Environment:
  SPORE_EVICTOR=0|false|off|no  Disable the sweep (parity with
                                WT_TELL_AUTO_BLOCK). Anything else
                                (or unset) leaves it on.
  SPORE_EVICTOR_IDLE_SECS=N     Override the soak window. --idle-secs
                                wins when both are set.
`

func runFleetEvictIdle(args []string) error {
	fs := flag.NewFlagSet("fleet evict-idle", flag.ContinueOnError)
	idleSecs := fs.Int("idle-secs", 0, "soak window in seconds (0 = honour env / default)")
	dryRun := fs.Bool("dry-run", false, "report verdicts without flipping frontmatter")
	allProjects := fs.Bool("all-projects", false, "sweep every spore project under $HOME")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(fleetEvictIdleUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("fleet evict-idle: unexpected positional args: %v", fs.Args())
	}

	threshold := time.Duration(0)
	if *idleSecs > 0 {
		threshold = time.Duration(*idleSecs) * time.Second
	}

	if *allProjects {
		return sweepAllProjects(threshold, *dryRun)
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	return sweepOneProject(root, threshold, *dryRun, os.Stdout)
}

func sweepOneProject(root string, threshold time.Duration, dryRun bool, w io.Writer) error {
	cfg := evictor.Config{
		ProjectRoot: root,
		TasksDir:    filepath.Join(root, "tasks"),
		Threshold:   threshold,
		DryRun:      dryRun,
	}
	rep, err := evictor.Run(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "evictor: project=%s\n", filepath.Base(root))
	evictor.WriteReport(w, rep)
	return nil
}

// sweepAllProjects walks $HOME/*/ one level deep, mirroring the
// spore-fleet-tick.sh logic, and sweeps each directory that looks
// like a spore harness (has tasks/ AND .git). Per-project failures
// are logged and do not propagate: the systemd unit's
// SuccessExitStatus=0 contract holds.
func sweepAllProjects(threshold time.Duration, dryRun bool) error {
	home := os.Getenv("HOME")
	if home == "" {
		return fmt.Errorf("fleet evict-idle --all-projects: $HOME unset")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return fmt.Errorf("read $HOME: %w", err)
	}
	swept := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(home, e.Name())
		if !isSporeProject(dir) {
			continue
		}
		swept++
		if err := sweepOneProject(dir, threshold, dryRun, os.Stdout); err != nil {
			// Log and continue: best-effort sweep.
			fmt.Fprintf(os.Stderr, "evictor: %s: %v\n", e.Name(), err)
		}
	}
	if swept == 0 {
		fmt.Println("evictor: no spore projects under $HOME")
	}
	return nil
}

func isSporeProject(dir string) bool {
	if st, err := os.Stat(filepath.Join(dir, "tasks")); err != nil || !st.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	return true
}
