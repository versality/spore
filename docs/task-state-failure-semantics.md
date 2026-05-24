# Task State Failure Semantics

Spore uses this contract:

- setup warns and does not block;
- planning warns and does not block;
- execution fails before lying about state.

`spore install`, bootstrap, and `spore task new` may print readiness
warnings. They still install files or create draft tasks.

`spore task start` runs execution preflight before writing
`status: active`. If the selected agent, `git`, or `tmux` is missing,
the task remains draft or blocked and no worker worktree is created.

`spore task ensure` is idempotent for a healthy existing session. When a
respawn is needed, it runs the same worker preflight first. Dead panes
and dead-on-spawn agents are not treated as healthy.

`spore fleet reconcile` separates outcomes:

- `spawned`: worker was created and the selected agent was persisted;
- `kept`: existing worker remained healthy;
- `skipped`: worker was not started because capacity was full;
- `failed`: worker could not start, with a per-task reason;
- `reaped`: stale session was removed.

Fleet agent assignment is persisted only after readiness and spawn can
proceed. A missing Codex binary must not write `agent: codex` to an
unstarted task.

Wake is body-free. `spore fleet wake` does not type inbox content into
tmux. A worker receives inbox messages through its runtime hooks on a
later turn. If runtime hooks are missing, `spore task tell` warns.

Recovery:

- run `spore doctor`;
- fix missing tools or hook source configs;
- run `spore task ensure <slug>` or `spore fleet reconcile`;
- use `spore fleet list-sessions` to inspect live tmux state.
