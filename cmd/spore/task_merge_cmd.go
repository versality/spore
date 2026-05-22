package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/cutover"
	"github.com/versality/spore/internal/task/ship"
)

func runTaskMerge(args []string) error {
	slug := ""
	force := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--force-merge-red":
			if i+1 >= len(args) || args[i+1] == "" {
				return fmt.Errorf("--force-merge-red requires a <reason> argument")
			}
			force = args[i+1]
			i++
		case strings.HasPrefix(a, "--force-merge-red="):
			force = strings.TrimPrefix(a, "--force-merge-red=")
			if force == "" {
				return fmt.Errorf("--force-merge-red requires a <reason> argument")
			}
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("spore task merge: unknown flag: %s", a)
		default:
			if slug != "" {
				return fmt.Errorf("usage: spore task merge <slug> [--force-merge-red <reason>]")
			}
			slug = a
		}
	}
	if slug == "" {
		return fmt.Errorf("usage: spore task merge <slug> [--force-merge-red <reason>]")
	}
	err := task.MergeWithOptions("tasks", slug, task.MergeOptions{ForceMergeRed: force})
	if err != nil {
		var gateErr *task.MergeGateError
		if errors.As(err, &gateErr) {
			fmt.Fprintln(os.Stderr, "spore task merge:", err)
			os.Exit(gateErr.ExitCode())
		}
		return err
	}
	return nil
}

func runTaskShip(args []string) error {
	slug := ""
	strategy := ""
	base := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--strategy":
			if i+1 >= len(args) {
				return fmt.Errorf("--strategy requires an argument")
			}
			strategy = args[i+1]
			i++
		case strings.HasPrefix(a, "--strategy="):
			strategy = strings.TrimPrefix(a, "--strategy=")
		case a == "--base":
			if i+1 >= len(args) {
				return fmt.Errorf("--base requires an argument")
			}
			base = args[i+1]
			i++
		case strings.HasPrefix(a, "--base="):
			base = strings.TrimPrefix(a, "--base=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("spore task ship: unknown flag: %s", a)
		default:
			if slug != "" {
				return fmt.Errorf("usage: spore task ship <slug> [--strategy <s>] [--base <b>]")
			}
			slug = a
		}
	}
	if slug == "" {
		return fmt.Errorf("usage: spore task ship <slug> [--strategy <s>] [--base <b>]")
	}
	return ship.Run(ship.Options{
		TasksDir: resolveTasksDir(),
		Slug:     slug,
		Strategy: strategy,
		Base:     base,
	}, ship.Deps{})
}

func runTaskCutover(args []string) error {
	fs := flag.NewFlagSet("task cutover", flag.ContinueOnError)
	consumer := fs.String("consumer", "", "consumer repo name (required)")
	feature := fs.String("feature", "", "feature name (required)")
	sourceRepo := fs.String("source-repo", "", "origin repo name")
	sourceSlug := fs.String("source-slug", "", "origin task slug")
	sourcePR := fs.Int("source-pr", 0, "origin PR number")
	claim := fs.String("claim", "", "raw claim expression")
	reason := fs.String("reason", "", "one-line justification")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("spore task cutover: unexpected positional args: %v", fs.Args())
	}
	if *consumer == "" || *feature == "" {
		return fmt.Errorf("spore task cutover: --consumer and --feature are required")
	}
	r, err := cutover.Mint(cutover.Options{
		Consumer:   *consumer,
		Feature:    *feature,
		SourceRepo: *sourceRepo,
		SourceSlug: *sourceSlug,
		SourcePR:   *sourcePR,
		Claim:      *claim,
		Reason:     *reason,
	}, cutover.Deps{})
	if err != nil {
		return err
	}
	if r.Skipped {
		fmt.Fprintf(os.Stderr, "cutover: %s already exists at %s\n", r.Slug, r.Path)
	} else {
		fmt.Fprintf(os.Stderr, "cutover: minted %s at %s\n", r.Slug, r.Path)
	}
	fmt.Println(r.Slug)
	return nil
}
