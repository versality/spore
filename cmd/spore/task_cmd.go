package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
  pause <slug>                 Retired; errors with redirect to block.
  park <slug>                  Retired; errors with redirect to block.
  block <slug> [--blocker R]   Flip active task to blocked. Refuses
                               when called from a coordinator session
                               (the coordinator surfaces attention via
                               notification, never by parking work).
  unblock <slug>               Flip blocked task back to active and
                               clear the blocker reason. No
                               coordinator gate.
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
  auto-commit --repo <path> [--lock <path>]
                               Safety-wrapped drift commit for systemd
                               path units. Holds an flock, refuses when
                               non-tasks/ paths are staged, then runs drift.
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
	case "block":
		return runTaskBlock(rest)
	case "unblock":
		return runTaskUnblock(rest)
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
	case "auto-commit":
		return runTaskAutoCommit(rest)
	case "migrate-priority":
		return runTaskMigratePriority(rest)
	case "migrate-status":
		return runTaskMigrateStatus(rest)
	default:
		return fmt.Errorf("unknown subcommand %q\n\n%s", sub, taskUsage)
	}
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
	if root, err := resolveMainRoot(); err == nil {
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
