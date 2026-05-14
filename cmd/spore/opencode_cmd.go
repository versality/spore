package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/opencode/fleetstop"
	"github.com/versality/spore/internal/opencode/liveness"
)

// resolveMainRoot returns the parent of `git rev-parse --git-common-dir`,
// which is the main repo root even when invoked from a linked worktree.
func resolveMainRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err != nil {
		return cwd, nil
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return cwd, nil
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	return filepath.Dir(common), nil
}

const opencodeUsage = `spore opencode - opencode-driver lifecycle helpers

Usage:
  spore opencode <subcommand> [flags]

Subcommands:
  fleet-stop   Pause every active opencode worker on this host, then
               sweep any orphan opencode process. Idempotent kill
               switch when ollama serializes opencode workers.
  liveness     Probe active opencode workers for stuckness. Exit 0
               when every worker made progress in the last 10 min;
               exit 2 when at least one is stuck.
`

const opencodeFleetStopUsage = `spore opencode fleet-stop - pause active opencode workers + reap orphans

Usage:
  spore opencode fleet-stop [--root <path>]

Flags:
  --root   Project main root (default: $PWD).
`

const opencodeLivenessUsage = `spore opencode liveness - probe opencode workers for stuckness

Usage:
  spore opencode liveness [--json] [--root <path>]
                          [--grace <seconds>] [--stuck <seconds>]

Flags:
  --json     Emit JSON instead of the human summary.
  --root     Project main root (default: git common dir parent).
  --grace    Mid-stream grace window in seconds (default 60).
  --stuck    Stuck threshold in seconds (default 600).
`

func runOpencode(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, opencodeUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(opencodeUsage)
		return 0
	case "fleet-stop":
		return runOpencodeFleetStop(rest)
	case "liveness":
		return runOpencodeLiveness(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore opencode: unknown subcommand %q\n\n%s", sub, opencodeUsage)
		return 2
	}
}

func runOpencodeFleetStop(args []string) int {
	fs := flag.NewFlagSet("opencode fleet-stop", flag.ContinueOnError)
	root := fs.String("root", "", "project main root (default: $PWD)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore opencode fleet-stop:", err)
		fmt.Fprint(os.Stderr, opencodeFleetStopUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(opencodeFleetStopUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, opencodeFleetStopUsage)
		return 2
	}
	if *root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore opencode fleet-stop:", err)
			return 1
		}
		*root = cwd
	}

	cfg := fleetstop.Config{MainRoot: *root}
	res, err := fleetstop.Run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore opencode fleet-stop:", err)
		return 1
	}
	fmt.Println(res.Summary())
	return 0
}

func runOpencodeLiveness(args []string) int {
	fs := flag.NewFlagSet("opencode liveness", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON output")
	root := fs.String("root", "", "project main root (default: git common dir parent)")
	grace := fs.Int("grace", 0, "mid-stream grace seconds")
	stuck := fs.Int("stuck", 0, "stuck threshold seconds")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore opencode liveness:", err)
		fmt.Fprint(os.Stderr, opencodeLivenessUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(opencodeLivenessUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, opencodeLivenessUsage)
		return 2
	}

	if *root == "" {
		r, err := resolveMainRoot()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore opencode liveness:", err)
			return 1
		}
		*root = r
	}

	cfg := liveness.Config{GraceSeconds: *grace, StuckSeconds: *stuck}
	db := liveness.NewOpencodeDB("")
	rep, err := liveness.Run(time.Now(), cfg, *root, db, liveness.LocalGit{MainRoot: *root})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore opencode liveness:", err)
		return 1
	}
	if *jsonOut {
		if err := liveness.FormatJSON(os.Stdout, rep); err != nil {
			fmt.Fprintln(os.Stderr, "spore opencode liveness:", err)
			return 1
		}
	} else {
		liveness.FormatText(os.Stdout, rep)
	}
	if len(rep.Stuck) > 0 {
		return 2
	}
	return 0
}
