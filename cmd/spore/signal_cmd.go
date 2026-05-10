package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	sig "github.com/versality/spore/internal/signal"
)

const signalUsage = `spore signal - record warning / error signals from a wrapped command

Usage:
  spore signal capture [flags] [--] CMD...
  spore signal scan    [flags] FILE

Flags (both subcommands):
  --dry-run            Print the [signal:hash] line, do not touch state
  --state-dir DIR      State dir (default: $XDG_STATE_HOME/spore-command-signals)
  --tool NAME          Tool name (default: basename of CMD or FILE)
  --owner SLUG         Owner slug (default: $WT_TASK_SLUG, then wt/<slug> branch, then "unowned")
  --ticket-after N     Repeat threshold for ticket-candidate (default: $SPORE_SIGNAL_TICKET_AFTER or 3)

capture only:
  --preserve-streams   Buffer stdout/stderr separately and replay after exit
                       (matches the Stop-hook contract)

scan only:
  --exit-code N        Treat the file as the captured output of a command
                       that exited with N (synthesizes an exit=N signal)

Auto-mint:
  When SPORE_SIGNAL_AUTO_MINT=1 (and not --dry-run), every ticket-candidate
  that has not been minted before runs:
    wt task new --draft "<title>" --body-stdin --no-edit
`

func runSignal(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, signalUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(signalUsage)
		return 0
	case "capture":
		return runSignalCapture(args[1:])
	case "scan":
		return runSignalScan(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "spore signal: unknown subcommand %q\n\n%s", args[0], signalUsage)
		return 2
	}
}

type signalFlags struct {
	dryRun      bool
	stateDir    string
	tool        string
	owner       string
	ticketAfter string

	// capture-only
	preserveStreams bool

	// scan-only
	exitCode string
}

func (f *signalFlags) bind(fs *flag.FlagSet, includeCapture, includeScan bool) {
	fs.BoolVar(&f.dryRun, "dry-run", false, "do not touch state")
	fs.StringVar(&f.stateDir, "state-dir", "", "state dir")
	fs.StringVar(&f.tool, "tool", "", "tool name")
	fs.StringVar(&f.owner, "owner", "", "owner slug")
	fs.StringVar(&f.ticketAfter, "ticket-after", "", "repeat threshold")
	if includeCapture {
		fs.BoolVar(&f.preserveStreams, "preserve-streams", false, "buffer streams")
	}
	if includeScan {
		fs.StringVar(&f.exitCode, "exit-code", "", "synthetic exit code")
	}
}

func (f signalFlags) toConfig(toolDefault string) (sig.Config, error) {
	cfg := sig.Config{
		StateDir: f.stateDir,
		Tool:     f.tool,
		Owner:    f.owner,
		DryRun:   f.dryRun,
		AutoMint: os.Getenv(sig.EnvAutoMint) == "1",
		Mint:     sig.MintViaWT,
	}
	if cfg.Tool == "" {
		cfg.Tool = toolDefault
	}
	if cfg.Owner == "" {
		cfg.Owner = defaultOwner()
	}
	if cfg.StateDir == "" {
		cfg.StateDir = sig.DefaultStateDir()
	}
	ta := f.ticketAfter
	if ta == "" {
		ta = os.Getenv(sig.EnvTicketAfter)
	}
	if ta == "" {
		cfg.TicketAfter = sig.DefaultTicketAfter
	} else {
		n, err := strconv.Atoi(ta)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("--ticket-after must be a positive integer (got %q)", ta)
		}
		cfg.TicketAfter = n
	}
	return cfg, nil
}

func defaultOwner() string {
	if s := os.Getenv("WT_TASK_SLUG"); s != "" {
		return s
	}
	if branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		b := strings.TrimSpace(string(branch))
		if strings.HasPrefix(b, "wt/") {
			return strings.TrimPrefix(b, "wt/")
		}
	}
	return "unowned"
}

func runSignalCapture(args []string) int {
	fs := flag.NewFlagSet("signal capture", flag.ContinueOnError)
	var f signalFlags
	f.bind(fs, true, false)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore signal capture:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(signalUsage)
		return 0
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "spore signal capture: CMD is required")
		fmt.Fprint(os.Stderr, signalUsage)
		return 2
	}
	toolDefault := filepath.Base(cmdArgs[0])
	cfg, err := f.toConfig(toolDefault)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore signal capture:", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	output, exitCode, err := sig.Capture(ctx, cmdArgs, f.preserveStreams, cfg.Tool, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore signal capture:", err)
		return 1
	}
	cmdDisplay := sig.ShellQuote(cmdArgs)
	if _, err := sig.Process(cfg, output, cmdDisplay, exitCode); err != nil {
		fmt.Fprintln(os.Stderr, "spore signal capture:", err)
		return 1
	}
	return exitCode
}

func runSignalScan(args []string) int {
	fs := flag.NewFlagSet("signal scan", flag.ContinueOnError)
	var f signalFlags
	f.bind(fs, false, true)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore signal scan:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(signalUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore signal scan: expected exactly one FILE")
		fmt.Fprint(os.Stderr, signalUsage)
		return 2
	}
	scanFile := fs.Arg(0)
	exitCode := 0
	if f.exitCode != "" {
		n, err := strconv.Atoi(f.exitCode)
		if err != nil || n < 0 {
			fmt.Fprintln(os.Stderr, "spore signal scan: --exit-code must be a non-negative integer")
			return 2
		}
		exitCode = n
	}
	output, err := os.ReadFile(scanFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore signal scan:", err)
		return 1
	}
	toolDefault := filepath.Base(scanFile)
	cfg, err := f.toConfig(toolDefault)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore signal scan:", err)
		return 2
	}
	if _, err := sig.Process(cfg, output, "", exitCode); err != nil {
		fmt.Fprintln(os.Stderr, "spore signal scan:", err)
		return 1
	}
	return exitCode
}
