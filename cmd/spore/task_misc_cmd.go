package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/versality/spore/internal/task"
)

func runTaskEdit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task edit <slug>")
	}
	return task.Edit("tasks", args[0])
}

func runTaskPick(_ []string) error {
	slug, err := task.Pick("tasks")
	if err != nil {
		return err
	}
	fmt.Println(slug)
	return nil
}

func runTaskWaybar(_ []string) error {
	out, err := task.Waybar(resolveTasksDir())
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func runTaskDrift(_ []string) error {
	return task.AutoCommitDrift("tasks")
}

func runTaskAutoCommit(args []string) error {
	fs := flag.NewFlagSet("task auto-commit", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo root (required)")
	lock := fs.String("lock", "", "flock path (default: $WT_STATE/merge-<basename>.lock)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("spore task auto-commit: unexpected positional args: %v", fs.Args())
	}
	if *repo == "" {
		return fmt.Errorf("spore task auto-commit: --repo is required")
	}
	err := task.AutoCommit(task.AutoCommitOptions{Repo: *repo, Lock: *lock})
	if err == nil {
		return nil
	}
	if errors.Is(err, task.ErrAutoCommitLocked) {
		return nil
	}
	var staged *task.AutoCommitStagedNonTasksError
	if errors.As(err, &staged) {
		fmt.Fprintln(os.Stderr, staged.Error())
		os.Exit(2)
	}
	return err
}

func runTaskVerify(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task verify <slug>")
	}
	verdict, diags, err := task.Verify("tasks", args[0])
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", args[0], verdict)
	for _, d := range diags {
		fmt.Printf("  %s\n", d)
	}
	return nil
}
