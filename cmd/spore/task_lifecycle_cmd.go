package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/versality/spore/internal/task"
)

func runTaskStart(args []string) error {
	slug, env, err := parseSlugAndEnv("start", args)
	if err != nil {
		return err
	}
	session, err := task.Start("tasks", slug, env)
	if err != nil {
		return err
	}
	fmt.Println(session)
	return nil
}

func runTaskEnsure(args []string) error {
	slug, env, err := parseSlugAndEnv("ensure", args)
	if err != nil {
		return err
	}
	session, err := task.Ensure("tasks", slug, env)
	if err != nil {
		return err
	}
	fmt.Println(session)
	return nil
}

// parseSlugAndEnv pulls a single positional <slug> and a repeatable
// --env KEY=VAL flag out of args. Used by `spore task start` and
// `spore task ensure`. Rejects --env without `=` so a typo doesn't
// silently land an env-less worker.
func parseSlugAndEnv(sub string, args []string) (string, []string, error) {
	slug := ""
	var env []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--env":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--env requires a KEY=VAL argument")
			}
			kv := args[i+1]
			i++
			if !strings.Contains(kv, "=") {
				return "", nil, fmt.Errorf("--env %q: expected KEY=VAL", kv)
			}
			env = append(env, kv)
		case strings.HasPrefix(a, "--env="):
			kv := strings.TrimPrefix(a, "--env=")
			if !strings.Contains(kv, "=") {
				return "", nil, fmt.Errorf("--env %q: expected KEY=VAL", kv)
			}
			env = append(env, kv)
		case strings.HasPrefix(a, "-"):
			return "", nil, fmt.Errorf("spore task %s: unknown flag: %s", sub, a)
		default:
			if slug != "" {
				return "", nil, fmt.Errorf("usage: spore task %s <slug> [--env KEY=VAL ...]", sub)
			}
			slug = a
		}
	}
	if slug == "" {
		return "", nil, fmt.Errorf("usage: spore task %s <slug> [--env KEY=VAL ...]", sub)
	}
	return slug, env, nil
}

func runTaskBlock(args []string) error {
	fs := flag.NewFlagSet("task block", flag.ContinueOnError)
	blocker := fs.String("blocker", "", "named blocker reason (e.g. scheduler:<key>, or operator note)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: spore task block <slug> [--blocker \"<reason>\"]")
	}
	return task.Block("tasks", fs.Arg(0), *blocker)
}

func runTaskUnblock(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task unblock <slug>")
	}
	return task.Unblock("tasks", args[0])
}

func runTaskDone(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: spore task done <slug> [--force]")
	}
	slug := args[0]
	force := false
	for _, a := range args[1:] {
		if a == "--force" {
			force = true
		} else {
			return fmt.Errorf("spore task done: unknown flag: %s", a)
		}
	}
	return task.Done("tasks", slug, force)
}
