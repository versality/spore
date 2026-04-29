package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/versality/spore/internal/align"
	"github.com/versality/spore/internal/bootstrap"
)

const bootstrapUsage = `spore bootstrap - walk a fresh project through the stage gates

Usage:
  spore bootstrap [--root <path>] [--skip <stage>]...
  spore bootstrap status [--root <path>]
  spore bootstrap reset [--root <path>] [--yes]

Re-entrant. Each call advances the project as far as the gates allow,
then prints the current stage and any blocker. Once a stage is
completed or skipped it is not re-run; use ` + "`reset`" + ` to wipe state.

Stage sequence (default):
  repo-mapped -> info-gathered -> tests-pass -> creds-wired ->
  readme-followed -> validation-green -> pilot-aligned -> worker-fleet-ready

Flags:
  --root        Project root (defaults to cwd).
  --skip NAME   Mark NAME skipped without running its detector. Repeat
                for multiple stages. Logs a warning. For testing only;
                do not skip in production bootstraps.
  --yes         Confirm destructive subcommand (reset).
`

// stringList is a flag.Value that accumulates --skip occurrences.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runBootstrap(args []string) error {
	if len(args) >= 1 {
		switch args[0] {
		case "status":
			return runBootstrapStatus(args[1:])
		case "reset":
			return runBootstrapReset(args[1:])
		case "-h", "--help", "help":
			fmt.Print(bootstrapUsage)
			return nil
		}
	}

	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "project root (defaults to cwd)")
	var skips stringList
	fs.Var(&skips, "skip", "stage to mark skipped (repeatable)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if *help || *helpLong {
		fmt.Print(bootstrapUsage)
		return nil
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	paths, err := align.Resolve(root)
	if err != nil {
		return err
	}
	for _, s := range skips {
		fmt.Fprintf(os.Stderr, "warning: skipping stage %q (testing only; resets on `spore bootstrap reset`)\n", s)
	}
	res, err := bootstrap.Run(root, paths.StateDir, bootstrap.DefaultStages(), bootstrap.Options{Skip: skips})
	if err != nil {
		return err
	}
	if len(res.Advanced) > 0 {
		fmt.Println("advanced through:")
		for _, name := range res.Advanced {
			fmt.Printf("  - %s\n", name)
		}
	}
	if len(res.Skipped) > 0 {
		fmt.Println("skipped:")
		for _, name := range res.Skipped {
			fmt.Printf("  - %s\n", name)
		}
	}
	fmt.Printf("current stage: %s\n", res.Current)
	if res.Done {
		fmt.Println("bootstrap complete.")
		return nil
	}
	if res.Blocker != "" {
		fmt.Printf("blocked: %s\n", res.Blocker)
	}
	if res.Current == "pilot-aligned" {
		fmt.Println("see `spore align status` for the alignment checklist.")
	}
	return nil
}

func runBootstrapStatus(args []string) error {
	fs := flag.NewFlagSet("bootstrap status", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "project root (defaults to cwd)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	paths, err := align.Resolve(root)
	if err != nil {
		return err
	}
	rows, err := bootstrap.Status(paths.StateDir, bootstrap.DefaultStages())
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STAGE\tSTATUS\tCOMPLETED\tNOTES")
	for _, r := range rows {
		notes := r.Record.Notes
		if len(notes) > 80 {
			notes = notes[:77] + "..."
		}
		notes = strings.ReplaceAll(notes, "\n", " | ")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Record.Status, r.Record.CompletedAt, notes)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nstate: %s/bootstrap.json\n", paths.StateDir)
	return nil
}

func runBootstrapReset(args []string) error {
	fs := flag.NewFlagSet("bootstrap reset", flag.ContinueOnError)
	rootFlag := fs.String("root", "", "project root (defaults to cwd)")
	yes := fs.Bool("yes", false, "skip the interactive confirmation prompt")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	root, err := resolveRoot(*rootFlag)
	if err != nil {
		return err
	}
	paths, err := align.Resolve(root)
	if err != nil {
		return err
	}
	if !*yes {
		fmt.Printf("about to wipe %s/bootstrap.json. proceed? [y/N] ", paths.StateDir)
		reader := bufio.NewReader(os.Stdin)
		ans, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("aborted.")
			return nil
		}
	}
	if err := bootstrap.Reset(paths.StateDir); err != nil {
		return err
	}
	fmt.Printf("reset bootstrap state for %s\n", paths.Project)
	return nil
}

func resolveRoot(rootFlag string) (string, error) {
	if rootFlag != "" {
		return rootFlag, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd, nil
}
