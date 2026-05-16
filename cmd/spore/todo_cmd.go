package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/todo"
)

const todoUsage = `spore todo - maintenance on docs/todo/ specs

Usage:
  spore todo <subcommand> [flags]

Subcommands:
  archive-aged-maybes [--repo <path>] [--age-days N]
                              Move **Priority**: maybe specs older than
                              N days (default 30, env ARCHIVE_AGE_DAYS)
                              from docs/todo/ to docs/parked/. Idempotent.
`

func runTodo(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, todoUsage)
		return fmt.Errorf("subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(todoUsage)
		return nil
	case "archive-aged-maybes":
		return runTodoArchiveAgedMaybes(rest)
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", sub, todoUsage)
	}
}

func runTodoArchiveAgedMaybes(args []string) error {
	fs := flag.NewFlagSet("todo archive-aged-maybes", flag.ContinueOnError)
	repo := fs.String("repo", "", "repo root (default: git rev-parse --show-toplevel)")
	ageDays := fs.Int("age-days", 0, "archive threshold in days (default: $ARCHIVE_AGE_DAYS or 30)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("spore todo archive-aged-maybes: unexpected positional args: %v", fs.Args())
	}

	root := *repo
	if root == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("spore todo archive-aged-maybes: --repo not set and git rev-parse failed: %w", err)
		}
		root = strings.TrimSpace(string(out))
	}

	age := *ageDays
	if age == 0 {
		if v := os.Getenv("ARCHIVE_AGE_DAYS"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("spore todo archive-aged-maybes: ARCHIVE_AGE_DAYS=%q: want positive integer", v)
			}
			age = n
		}
	}

	res, err := todo.ArchiveAged(todo.ArchiveOptions{Repo: root, AgeDays: age})
	if err != nil {
		return err
	}
	count := len(res.Archived)
	if count > 0 {
		fmt.Printf("archived %d aged maybe(s):\n", count)
		for _, s := range res.Archived {
			fmt.Printf("  %s\n", s)
		}
	} else {
		fmt.Println("archived 0 aged maybes")
	}
	return nil
}
