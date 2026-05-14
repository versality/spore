package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/versality/spore/internal/lints"
	"github.com/versality/spore/internal/scout"
)

// runScout dispatches the scout verb. Subcommands:
//   - (bare)         run lints and append findings to the ledger.
//   - mint-healers   read the ledger and mint healer tasks.
func runScout(args []string) int {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "mint-healers":
			return runScoutMint(args[1:])
		case "scan":
			args = args[1:]
		default:
			fmt.Fprintf(os.Stderr, "spore scout: unknown subcommand %q\n\n", args[0])
			fmt.Fprint(os.Stderr, scoutUsage)
			return 2
		}
	}
	return runScoutScan(args)
}

// runScoutScan walks the working tree with the default lint set and
// appends each finding as one JSONL row to the scout ledger. The
// ledger is the input to `spore scout mint-healers`.
func runScoutScan(args []string) int {
	fs := flag.NewFlagSet("scout", flag.ContinueOnError)
	root := fs.String("root", ".", "repo root to scan")
	stateFile := fs.String("state-file", "", "JSONL ledger path (default $XDG_STATE_HOME/spore/scout/scout-findings.jsonl)")
	stdoutAlso := fs.Bool("stdout", false, "also echo findings to stdout (off by default)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore scout:", err)
		fmt.Fprint(os.Stderr, scoutUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(scoutUsage)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "spore scout: unexpected positional args: %v\n\n", fs.Args())
		fmt.Fprint(os.Stderr, scoutUsage)
		return 2
	}
	if *stateFile == "" {
		*stateFile = DefaultScoutStateFile()
	}
	if err := os.MkdirAll(filepath.Dir(*stateFile), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "spore scout:", err)
		return 2
	}
	f, err := os.OpenFile(*stateFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore scout:", err)
		return 2
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	var stdoutEnc *json.Encoder
	if *stdoutAlso {
		stdoutEnc = json.NewEncoder(os.Stdout)
	}

	bad := false
	totals := map[string]int{}
	taskEvidenceWarnOnly := lints.EvidenceWarnOnly()
	taskPriorityWarnOnly := lints.PriorityWarnOnly()
	for _, l := range lints.Default() {
		issues, runErr := l.Run(*root)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "spore scout: %s: %v\n", l.Name(), runErr)
			bad = true
			continue
		}
		warnOnly := (l.Name() == "task-evidence" && taskEvidenceWarnOnly) ||
			(l.Name() == "task-priority" && taskPriorityWarnOnly)
		for _, i := range issues {
			row := buildFinding(l.Name(), i, warnOnly)
			if err := enc.Encode(row); err != nil {
				fmt.Fprintln(os.Stderr, "spore scout:", err)
				return 2
			}
			if stdoutEnc != nil {
				_ = stdoutEnc.Encode(row)
			}
			totals[l.Name()]++
		}
	}
	if bad {
		return 1
	}
	if len(totals) == 0 {
		fmt.Fprintf(os.Stderr, "spore scout: clean (ledger %s)\n", *stateFile)
		return 0
	}
	fmt.Fprintf(os.Stderr, "spore scout: appended findings to %s\n", *stateFile)
	names := make([]string, 0, len(totals))
	for n := range totals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %s: %d\n", n, totals[n])
	}
	return 0
}

// DefaultScoutStateFile returns the standard ledger location.
// Override at the CLI with --state-file or by exporting XDG_STATE_HOME.
func DefaultScoutStateFile() string {
	return filepath.Join(scoutStateDir(), "scout-findings.jsonl")
}

func defaultScoutMintedFile() string {
	return filepath.Join(scoutStateDir(), "scout-minted.tsv")
}

func defaultScoutFalseposFile() string {
	return filepath.Join(scoutStateDir(), "scout-falsepos.tsv")
}

func scoutStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".local", "state")
		}
	}
	return filepath.Join(base, "spore", "scout")
}

// runScoutMint reads the scout ledger and mints one healer task per
// new cluster of findings, up to --max. Writes draft tasks/<slug>.md
// under --tasks-dir and records minted scheduler keys in
// scout-minted.tsv.
func runScoutMint(args []string) int {
	fs := flag.NewFlagSet("scout mint-healers", flag.ContinueOnError)
	ledger := fs.String("ledger", "", "JSONL ledger path (default DefaultScoutStateFile)")
	tasksDir := fs.String("tasks-dir", "tasks", "directory to write tasks/<slug>.md into")
	project := fs.String("project", "spore", "project name on minted briefs")
	minted := fs.String("minted", "", "path to scout-minted.tsv (default state-dir/scout-minted.tsv)")
	falsepos := fs.String("falsepos", "", "path to scout-falsepos.tsv (default state-dir/scout-falsepos.tsv)")
	max := fs.Int("max", 10, "hard cap on healers minted per run")
	dryRun := fs.Bool("dry-run", false, "compute clusters and slugs but do not write tasks")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore scout mint-healers:", err)
		fmt.Fprint(os.Stderr, scoutMintUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(scoutMintUsage)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "spore scout mint-healers: unexpected positional args: %v\n\n", fs.Args())
		fmt.Fprint(os.Stderr, scoutMintUsage)
		return 2
	}
	if *ledger == "" {
		*ledger = DefaultScoutStateFile()
	}
	if *minted == "" {
		*minted = defaultScoutMintedFile()
	}
	if *falsepos == "" {
		*falsepos = defaultScoutFalseposFile()
	}

	res, err := scout.Mint(scout.Options{
		LedgerPath:   *ledger,
		TasksDir:     *tasksDir,
		Project:      *project,
		MintedPath:   *minted,
		FalseposPath: *falsepos,
		Max:          *max,
		DryRun:       *dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore scout mint-healers:", err)
		return 1
	}
	for _, slug := range res.Minted {
		fmt.Println(slug)
	}
	verb := "minted"
	if *dryRun {
		verb = "would-mint"
	}
	fmt.Fprintf(os.Stderr, "spore scout mint-healers: %s %d (considered=%d, falsepos=%d, skipped=%d, capped=%d)\n",
		verb, len(res.Minted), res.Considered, res.FalsePos, res.Skipped, res.Capped)
	return 0
}
