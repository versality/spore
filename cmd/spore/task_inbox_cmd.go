package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
	inboxpkg "github.com/versality/spore/internal/task/inbox"
)

func runTaskTell(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: spore task tell <slug> <message>")
	}
	target, msg := args[0], args[1]
	if err := task.Tell(target, msg); err != nil {
		return err
	}
	if err := task.SelfBlockOnCoordinatorTell(resolveTasksDir(), target, msg, callerSlugFromEnv()); err != nil {
		return err
	}
	if hint := inboxHookHint(resolveTasksDir(), target); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}
	return nil
}

func inboxHookHint(tasksDir, slug string) string {
	raw, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		return ""
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil || !task.IsActive(m.Status) {
		return ""
	}
	root, err := resolveMainRoot()
	if err != nil {
		return ""
	}
	agent := m.Agent
	if agent == "" {
		agent = fleet.DefaultWorkerAgent
	}
	var runtime string
	switch agent {
	case "codex":
		runtime = filepath.Join(root, ".worktrees", slug, ".codex", "hooks.json")
	case "claude", "claude-code":
		runtime = filepath.Join(root, ".worktrees", slug, ".claude", "settings.local.json")
	default:
		return ""
	}
	if _, err := os.Stat(runtime); os.IsNotExist(err) {
		rel, _ := filepath.Rel(root, runtime)
		return "warning[inbox-hooks-missing]: wake is body-free; " + filepath.ToSlash(rel) + " is missing, so the worker may not drain inbox automatically"
	}
	return ""
}

// callerSlugFromEnv returns the slug of the worker session invoking
// the command, sourced from WT_TASK_SLUG (the wt-task bash launcher)
// or SPORE_TASK_SLUG (spore's Go launcher, lifecycle.go:550). Empty
// when neither is set: operator-interactive shells, coordinator
// sessions, ad-hoc scripts.
func callerSlugFromEnv() string {
	if s := os.Getenv("WT_TASK_SLUG"); s != "" {
		return s
	}
	if s := os.Getenv("SPORE_TASK_SLUG"); s != "" {
		return s
	}
	return ""
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
