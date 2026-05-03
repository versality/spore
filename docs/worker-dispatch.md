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

Worker branches are local implementation detail. The worker close
path must not push `wt/<slug>` or feature branches to GitHub; only
the landed `main` branch is published upstream.

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

## Agent Selection

`tasks/<slug>.md` frontmatter controls the worker process. With no
`agent:` field, Spore starts `claude-code`. `agent: claude` and
`agent: claude-code` use the same default. `agent: codex` starts:

```
codex --dangerously-bypass-approvals-and-sandbox --no-alt-screen --disable apps
```

Codex reasoning effort comes from `effort:` when present
(`low`, `medium`, `high`, `xhigh`, plus `very-high` / `very_high`
as aliases for `xhigh`). Without an explicit effort, `complexity:
light|medium` maps to `medium`, and `complexity: heavy` or an absent
complexity maps to `high`. `model:` pins the Codex model for that
task; otherwise `SPORE_CODEX_MODEL` may provide a process-wide
default, and an empty value lets Codex use its own default. The
legacy `SPORE_AGENT_BINARY` override still wins over frontmatter.

## Merge Close Path

`spore task merge <slug>` fast-forwards `wt/<slug>` into the main
checkout, runs the same inbox and evidence gates as a done flip, then
commits `tasks/<slug>: status -> done`. It then runs exactly
`git push origin main:main` and verifies that `origin`'s
`refs/heads/main` matches local `main` before removing the worktree,
branch, and tmux session. It does not push `wt/<slug>`, feature
branches, or any other local branch. If a close or push gate fails,
the branch and worktree stay in place so the worker can fix the
evidence, drain the inbox, or retry the upstream push.
