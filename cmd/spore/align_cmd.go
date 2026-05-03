package main

import (
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/align"
)

const alignUsage = `spore align - track the pilot-agent alignment period

Usage:
  spore align <subcommand> [args]

Subcommands:
  status              Print N/M criteria met plus a checklist.
  flip                Mark alignment mode off (operator confirmation).
                      The next compose drops the alignment-mode rule.
  note "<line>"       Append one observation to alignment.md. Prefix
                      the body with "[promoted]" when the operator
                      promotes the preference to a rule-pool entry.

State lives under "$XDG_STATE_HOME/spore/<project>/" (default
"$HOME/.local/state/spore/<project>/"). Defaults can be overridden
in a top-level "spore.toml" with an [align] section
(required_notes, required_promoted).
`

func runAlign(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, alignUsage)
		return fmt.Errorf("subcommand required")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	paths, err := align.Resolve(root)
	if err != nil {
		return err
	}
	criteria, err := align.LoadCriteria(root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(alignUsage)
		return nil
	case "status":
		s, err := align.Read(paths, criteria)
		if err != nil {
			return err
		}
		printAlignStatus(os.Stdout, paths, s)
		return nil
	case "flip":
		if err := align.Flip(paths); err != nil {
			return err
		}
		fmt.Printf("alignment flipped off for %s\n", paths.Project)
		fmt.Println("rerun `spore compose` to drop the alignment-mode rule from the instruction files.")
		return nil
	case "note":
		if len(args) != 2 {
			return fmt.Errorf("usage: spore align note \"<line>\"")
		}
		if err := align.Note(paths, args[1]); err != nil {
			return err
		}
		s, err := align.Read(paths, criteria)
		if err != nil {
			return err
		}
		fmt.Printf("noted. %d/%d notes, %d/%d promoted.\n",
			s.Notes, criteria.RequiredNotes, s.Promoted, criteria.RequiredPromoted)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", args[0], alignUsage)
	}
}

func printAlignStatus(w io.Writer, paths align.Paths, s align.Status) {
	checks := []struct {
		ok    bool
		label string
	}{
		{s.Notes >= s.Criteria.RequiredNotes,
			fmt.Sprintf("notes      %d/%d", s.Notes, s.Criteria.RequiredNotes)},
		{s.Promoted >= s.Criteria.RequiredPromoted,
			fmt.Sprintf("promoted   %d/%d", s.Promoted, s.Criteria.RequiredPromoted)},
		{s.Flipped, "flipped    operator confirmation"},
	}
	met := 0
	for _, c := range checks {
		if c.ok {
			met++
		}
	}
	fmt.Fprintf(w, "alignment %d/%d criteria met for %s:\n", met, len(checks), paths.Project)
	for _, c := range checks {
		mark := "[ ]"
		if c.ok {
			mark = "[x]"
		}
		fmt.Fprintf(w, "  %s %s\n", mark, c.label)
	}
	fmt.Fprintf(w, "log: %s\n", paths.AlignmentFile)
}
