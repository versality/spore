# Worker mix

The fleet reconciler picks a worker agent (Claude, Codex, or any binary
on PATH) for each task it spawns. Default behaviour without `spore.toml`
is "all Claude". Run `spore init` to scaffold a documented default
`spore.toml` (worker ratio plus commented stubs for `[coordinator]`,
`[align]`, and `[matter.linear]`). The command is a no-op on an existing
file; pass `--force` to overwrite or `--section <name>` to append a
single missing block.

To run a mix, declare a default, optionally a ratio, and optionally
rules keyed on a task's `complexity:` extra:

```toml
# spore.toml
[fleet.workers]
default = "claude"

[fleet.workers.ratio]
claude = 70
codex = 30

[fleet.workers.rules]
mechanical = "codex"
deep = "claude"
```

Selection precedence:

1. An explicit `agent:` already in the task frontmatter wins.
2. A rule whose key matches the task's `complexity:` value wins next.
3. Otherwise the ratio balancer picks the agent whose currently spawned
   share is furthest below its target share, so the active fleet
   converges on the configured split.
4. Otherwise `default` (or `claude` when omitted) is used.

The picked agent is written back into the task's frontmatter `agent:`
when reconcile spawns the session, so downstream tools see the same
answer the launcher used. Coordinator agent selection lives under the
separate `[coordinator]` section and is never subject to the worker
ratio.
