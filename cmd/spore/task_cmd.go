package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/cutover"
	"github.com/versality/spore/internal/task/frontmatter"
	inboxpkg "github.com/versality/spore/internal/task/inbox"
	"github.com/versality/spore/internal/task/ship"
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
  park <slug>                  Flip active task to parked (no teardown).
  block <slug>                 Flip active task to blocked (no teardown).
  done <slug> [--force]         Flip to done, kill tmux + remove worktree.
  merge <slug> [--force-merge-red <reason>]
                               Merge wt/<slug> into main; push origin main:main only.
                               Refuses on red 'just check' (exit 2);
                               --force-merge-red bypasses with a logged reason.
  ship <slug> [--strategy <s>] [--base <b>]
                               Streamlined PR-flow ship: just check, push branch,
                               gh pr create, wait for checks, gh pr merge (squash
                               by default), ff local main, task.Done. One verb;
                               idempotent per-step.
  cutover --consumer <repo> --feature <name> [...]
                               Mint a draft task brief in a consumer repo asking
                               it to catch up to a spore lift. Flags:
                               --source-repo, --source-slug, --source-pr,
                               --claim, --reason. Idempotent on the derived slug.
  tell <slug> <message>        Append a message to the slug's inbox dir.
  inbox-dispatch --token <regex> --handler <bin> [--inbox <dir>]
                               Drain inbox envelopes whose body matches <regex>,
                               exec <bin> with the envelope path as $1, and move
                               handled envelopes to inbox/read/ on rc=0. Defaults
                               to the coordinator inbox for the current project
                               (override with --inbox or $SPORE_TASK_INBOX).
  verify <slug>                Print the evidence verdict for slug.
  waybar                       Print JSON chip for waybar custom module.
  drift                        Auto-commit task file changes.
  migrate-priority [--dry-run] Backfill 'priority:' on tasks missing it.
  migrate-status <from> <to>   Rewrite every tasks/*.md status==<from> to <to>.

Flags for 'new':
  --draft                      Set status=draft (default).
  --start                      Set status=active and launch agent after creation.
  --body <text>                Inline body text (skips editor).
  --body-stdin                 Read body from stdin (skips editor).
  --needs <slug>               Add a dependency (repeatable).
  --priority <v>               critical|high|medium|low (default: medium).
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
	case "ensure":
		return runTaskEnsure(rest)
	case "start":
		return runTaskStart(rest)
	case "pause":
		return runTaskPause(rest)
	case "park":
		return runTaskPark(rest)
	case "block":
		return runTaskBlock(rest)
	case "done":
		return runTaskDone(rest)
	case "merge":
		return runTaskMerge(rest)
	case "ship":
		return runTaskShip(rest)
	case "cutover":
		return runTaskCutover(rest)
	case "tell":
		return runTaskTell(rest)
	case "inbox-dispatch":
		return runTaskInboxDispatch(rest)
	case "verify":
		return runTaskVerify(rest)
	case "waybar":
		return runTaskWaybar(rest)
	case "drift":
		return runTaskDrift(rest)
	case "migrate-priority":
		return runTaskMigratePriority(rest)
	case "migrate-status":
		return runTaskMigrateStatus(rest)
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

func runTaskPause(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task pause <slug>")
	}
	fmt.Fprintln(os.Stderr, "status: pause is deprecated, use park or block (flipping to parked)")
	return task.Park("tasks", args[0])
}

func runTaskPark(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task park <slug>")
	}
	return task.Park("tasks", args[0])
}

func runTaskBlock(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: spore task block <slug>")
	}
	return task.Block("tasks", args[0])
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

func runTaskInboxDispatch(args []string) error {
	fs := flag.NewFlagSet("task inbox-dispatch", flag.ContinueOnError)
	tokenRe := fs.String("token", "", "regex matched against envelope body (required)")
	handler := fs.String("handler", "", "executable invoked per matched envelope (required)")
	inboxDir := fs.String("inbox", "", "inbox dir (default: $SPORE_TASK_INBOX or coordinator inbox)")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("spore task inbox-dispatch: unexpected positional args: %v", fs.Args())
	}
	if *tokenRe == "" || *handler == "" {
		return fmt.Errorf("spore task inbox-dispatch: --token and --handler are required")
	}
	re, err := regexp.Compile(*tokenRe)
	if err != nil {
		return fmt.Errorf("spore task inbox-dispatch: --token %q: %w", *tokenRe, err)
	}
	dir := *inboxDir
	if dir == "" {
		dir = os.Getenv("SPORE_TASK_INBOX")
	}
	if dir == "" {
		dir, err = task.CoordinatorInboxDirForProject("")
		if err != nil {
			return err
		}
	}
	res, err := inboxpkg.Dispatch(inboxpkg.DispatchOptions{
		Dir:     dir,
		Token:   re,
		Handler: *handler,
		Log:     os.Stderr,
	})
	if err != nil {
		return err
	}
	if res.Handled > 0 || res.Failed > 0 {
		fmt.Fprintf(os.Stderr, "inbox-dispatch: scanned=%d matched=%d handled=%d failed=%d\n",
			res.Scanned, res.Matched, res.Handled, res.Failed)
	}
	return nil
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

// resolveTasksDir returns an absolute tasks/ path. Priority:
//  1. SPORE_TASKS_DIR env var (explicit override)
//  2. git root from cwd (works when called from project root)
//  3. first entry in ~/.config/wt/projects (fallback for waybar/systemd callers)
//  4. relative "tasks" (last resort)
func resolveTasksDir() string {
	if v := os.Getenv("SPORE_TASKS_DIR"); v != "" {
		return v
	}
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if i := strings.Index(root, "/.worktrees/"); i >= 0 {
			root = root[:i]
		}
		return filepath.Join(root, "tasks")
	}
	if home, err := os.UserHomeDir(); err == nil {
		data, err := os.ReadFile(filepath.Join(home, ".config", "wt", "projects"))
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				dir := filepath.Join(line, "tasks")
				if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
					return dir
				}
			}
		}
	}
	return "tasks"
}
