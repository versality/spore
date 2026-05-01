package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

const taskUsage = `spore task - manage tasks

Usage:
  spore task <subcommand> [flags]

Subcommands:
  new <title> [flags]          Create a tasks/<slug>.md.
  ls [--all] [--done]          List tasks (default hides done).
  edit <slug>                  Open task file in $EDITOR.
  pick                         Interactive rofi/fzf task picker.
  start <slug>                 Flip to active, spawn worktree + tmux session.
  pause <slug>                 Flip active task to paused (no teardown).
  block <slug>                 Flip active task to blocked (no teardown).
  done <slug>                  Flip to done, kill tmux + remove worktree.
  merge <slug>                 Fast-forward merge wt/<slug> into main.
  tell <slug> <message>        Append a message to the slug's inbox dir.
  verify <slug>                Print the evidence verdict for slug.
  waybar                       Print JSON chip for waybar custom module.
  drift                        Auto-commit task file changes.

Flags for 'new':
  --draft                      Set status=draft (default).
  --start                      Set status=active and launch agent after creation.
  --body <text>                Inline body text (skips editor).
  --body-stdin                 Read body from stdin (skips editor).
  --needs <slug>               Add a dependency (repeatable).
  --edit                       Force editor open.
  --no-edit                    Suppress editor.
`

func runTask(args []string) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, taskUsage)
		return fmt.Errorf("subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(taskUsage)
		return nil
	case "new":
		return runTaskNew(rest)
	case "ls":
		return runTaskLs(rest)
	case "edit":
		return runTaskEdit(rest)
	case "pick":
		return runTaskPick(rest)
	case "start":
		return runTaskStart(rest)
	case "pause":
		return runTaskPause(rest)
	case "block":
		return runTaskBlock(rest)
	case "done":
		return runTaskDone(rest)
	case "merge":
		return runTaskMerge(rest)
	case "tell":
		return runTaskTell(rest)
	case "verify":
		return runTaskVerify(rest)
	case "waybar":
		return runTaskWaybar(rest)
	case "drift":
		return runTaskDrift(rest)
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", sub, taskUsage)
	}
}

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

func runTaskMerge(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task merge <slug>")
	}
	return task.Merge("tasks", args[0])
}

func runTaskWaybar(_ []string) error {
	out, err := task.Waybar("tasks")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func runTaskDrift(_ []string) error {
	return task.AutoCommitDrift("tasks")
}

func runTaskStart(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task start <slug>")
	}
	session, err := task.Start("tasks", args[0])
	if err != nil {
		return err
	}
	fmt.Println(session)
	return nil
}

func runTaskPause(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task pause <slug>")
	}
	return task.Pause("tasks", args[0])
}

func runTaskBlock(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task block <slug>")
	}
	return task.Block("tasks", args[0])
}

func runTaskDone(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task done <slug>")
	}
	return task.Done("tasks", args[0])
}

func runTaskTell(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: spore task tell <slug> <message>")
	}
	return task.Tell(args[0], args[1])
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

// needsFlag is a repeatable string flag for --needs.
type needsFlag []string

func (n *needsFlag) String() string { return strings.Join(*n, ",") }
func (n *needsFlag) Set(v string) error {
	*n = append(*n, v)
	return nil
}

func runTaskNew(args []string) error {
	fs := flag.NewFlagSet("task new", flag.ContinueOnError)
	bodyStdin := fs.Bool("body-stdin", false, "read body from stdin")
	bodyText := fs.String("body", "", "inline body text")
	startFlag := fs.Bool("start", false, "set status=active and launch agent")
	_ = fs.Bool("draft", true, "set status=draft (default)")
	editFlag := fs.Bool("edit", false, "force editor open")
	noEdit := fs.Bool("no-edit", false, "suppress editor")
	var needs needsFlag
	fs.Var(&needs, "needs", "add dependency slug (repeatable)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("expected exactly one positional <title>, got %d", fs.NArg())
	}
	title := fs.Arg(0)
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title must not be empty")
	}

	tasksDir := "tasks"
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return err
	}
	base := task.Slugify(title)
	if base == "" {
		return fmt.Errorf("title %q yields empty slug", title)
	}
	slug, err := task.Allocate(tasksDir, base)
	if err != nil {
		return err
	}

	var body []byte
	if *bodyStdin {
		body, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	} else if *bodyText != "" {
		body = []byte("\n" + *bodyText + "\n")
	}

	project, _ := task.ProjectName("")
	m := frontmatter.Meta{
		Status:  "draft",
		Slug:    slug,
		Title:   title,
		Created: time.Now().UTC().Format(time.RFC3339),
		Project: project,
		Needs:   []string(needs),
	}
	out := frontmatter.Write(m, body)
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}

	wantEdit := *editFlag || (body == nil && !*noEdit && isTTY())
	if wantEdit {
		if editErr := task.Edit(tasksDir, slug); editErr != nil {
			return editErr
		}
	}

	fmt.Println(slug)

	if *startFlag {
		session, startErr := task.Start(tasksDir, slug)
		if startErr != nil {
			return startErr
		}
		fmt.Println(session)
	}
	return nil
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runTaskLs(args []string) error {
	fs := flag.NewFlagSet("task ls", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done tasks")
	doneOnly := fs.Bool("done", false, "show only done tasks")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional args: %v", fs.Args())
	}
	metas, err := task.List("tasks")
	if err != nil {
		return err
	}
	fmt.Println("SLUG\tSTATUS\tTITLE")
	for _, m := range metas {
		if *doneOnly && m.Status != "done" {
			continue
		}
		if !*all && !*doneOnly && m.Status == "done" {
			continue
		}
		fmt.Printf("%s\t%s\t%s\n", m.Slug, m.Status, m.Title)
	}
	return nil
}
