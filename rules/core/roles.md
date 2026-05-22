## Roles

spore's kernel uses two role names:

- `coordinator` - the long-lived agent that pilots a project, owns the
  task queue, and routes worker output back to the operator.
- `worker` - a short-lived agent spawned against a single task slug in
  a `.worktrees/<slug>` checkout. Workers run autonomously between
  handovers and report through the task file, commits, and the inbox.

The bubblewrap sandbox launcher (`cmd/spore-sandbox/`) is a primitive
that wraps a worker's agent command; it is not a separate role.

Downstream projects can rename `coordinator` and `worker` at bootstrap
time. When working inside this repo, always use the kernel names.
