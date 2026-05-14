**Status**: closed-wontfix 2026-05-14

# L1: pre-load Task* tools on claude worker spawn

## Goal

Skip the cold-boot ToolSearch round-trip a fresh worker pays before
its first `TaskCreate` / `TaskUpdate`. Same shape as the existing
`AskUserQuestion` round-trip documented in
`rules/core/ask-via-tool.md`.

## Finding: not reachable from our side

The canonical claude-code settings schema
(`https://json.schemastore.org/claude-code-settings.json`, the one
`internal/hooks/settings.go` emits against) has no slot for
preloading deferred tools. Top-level keys cover hooks, permissions,
MCP allow/deny lists, plugins, model selection, env, and statusline.
There is no `enabledTools`, `startupTools`, `initialTools`,
`preloadTools`, or equivalent.

The deferred-tool mechanism is harness-internal: the runtime decides
which tools are surfaced with full JSONSchema at session start and
which appear by name only, callable through `ToolSearch`. That
decision is not operator-configurable today.

Implication: the original acceptance criterion ("Task* available at
turn 1, zero boot-time ToolSearch") cannot be met without an upstream
claude-code feature.

## Fallback per brief

Treat the first ToolSearch round-trip for Task* (and AskUserQuestion,
on workers) as structural. `internal/worker/bootaudit` reports
`toolsearch-rounds` as a metric only; it does not enforce a budget,
so no threshold needs adjusting. The expected steady-state floor for
a worker that uses TaskCreate is `toolsearch-rounds >= 1`.

## What to revisit upstream

If claude-code grows a `preloadTools` / `enabledTools` slot,
re-open: emit it from `internal/hooks/settings.go` with
worker-vs-coordinator branching (workers get `AskUserQuestion` and
`Task*`; coordinator gets `Task*` only, per the alignment-mode rule
in `CLAUDE.md` plus the coordinator AskUserQuestion ban).

## Related

- `rules/core/ask-via-tool.md` - structural ToolSearch for
  `AskUserQuestion`.
- `internal/worker/bootaudit/bootaudit.go` - the metric.
- `tasks/spore-boot-audit-lift-l1-preload-task-tools.md` - the task
  brief that minted this investigation.
