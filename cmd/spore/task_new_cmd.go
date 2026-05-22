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
	editFlag := fs.Bool("edit", false, "force editor open")
	noEdit := fs.Bool("no-edit", false, "suppress editor")
	priority := fs.String("priority", task.DefaultPriority, "critical|high|medium|low")
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
	if !task.IsValidPriority(*priority) {
		return fmt.Errorf("--priority %q: want one of critical|high|medium|low", *priority)
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
		Status:   "draft",
		Slug:     slug,
		Title:    title,
		Created:  time.Now().UTC().Format(time.RFC3339),
		Project:  project,
		Priority: *priority,
		Needs:    []string(needs),
	}
	out := frontmatter.Write(m, body)
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}

	tokens := len(out) / 4
	if tokens > 1500 {
		fmt.Printf("\x1b[33mbrief: ~%d tokens (over 1500, consider trimming)\x1b[0m\n", tokens)
	} else {
		fmt.Printf("brief: ~%d tokens\n", tokens)
	}

	wantEdit := *editFlag || (body == nil && !*noEdit && isTTY())
	if wantEdit {
		if editErr := task.Edit(tasksDir, slug); editErr != nil {
			return editErr
		}
	}

	fmt.Println(slug)

	if *startFlag {
		session, startErr := task.Start(tasksDir, slug, nil)
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
	if !*doneOnly && !*all {
		task.SortByPromoteOrder(metas)
	}
	fmt.Println("SLUG\tSTATUS\tPRIORITY\tTITLE")
	for _, m := range metas {
		if *doneOnly && !task.IsDone(m.Status) {
			continue
		}
		if !*all && !*doneOnly && task.IsDone(m.Status) {
			continue
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", m.Slug, m.Status, m.Priority, m.Title)
	}
	return nil
}
