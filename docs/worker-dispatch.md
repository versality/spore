# worker dispatch: tmux only

**Status**: design constraint, lifted from a live infect target after
a worker-launch attempt via the Claude Code Agent tool returned blocked.

## TL;DR

Worker sessions must be spawned by `task.ensureSession` (the kernel
path: `tmux new-session ... <agent>` inside the worktree cwd), or by
an equivalent operator-side `tmux new-window` that exec's the same
agent with the brief baked in. The Claude Code Agent tool is **not**
a viable worker-dispatch primitive in this harness.

## Why

The Agent tool spawns subagents in a sandbox that cannot read or
write under `.worktrees/<slug>/`. A coordinator that tries to
delegate a worker via Agent receives a "blocked" response the moment
the subagent tries to `cd` into the worktree path or read files
under it. This is independent of the kernel: the sandbox sees
`.worktrees/` as outside its allowed root.

Tmux-spawned sessions, by contrast, run as ordinary processes under
the spore user's shell. They inherit the worktree cwd, full access
to the brief at `tasks/<slug>.md` (now also copied into the worktree
itself, see `internal/task/lifecycle.go`), and can write commits on
`wt/<slug>` without sandbox interference. The headless agent then
exits, the tmux window closes, and `spore-fleet-reconcile.timer`
respawns when the task is still active.

## Implications

- Anyone designing new worker-spawn paths in spore should assume the
  Agent tool is off-limits. Wrap the spawn in tmux.
- Cross-worker coordination has to flow through git (commits on
  `wt/<slug>`, observable from the coordinator's worktree), the
  `tasks/<slug>.md` brief, or files under
  `~/.local/state/spore/<project>/`. Direct in-process messaging
  between coordinator and workers is not on the table while the
  sandbox stands.
- The PreToolUse `block-bg-bash.pl` hook (in
  `bootstrap/handover/hooks/`) closes the obvious workaround of
  `Bash run_in_background:true`. Background bash silently buffers
  output where neither the operator nor the coordinator can see it.
  Tmux windows are the only sanctioned channel for long-running
  jobs.
