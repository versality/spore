# Session-kind scoping for claude-code hooks

claude-code reads one global `~/.claude/settings.json` per user, and
its hook bindings fire on every claude session the operator runs on
that host. A consumer that wires fleet-specific Stop hooks (coordinator
inbox watchers, worker-token monitors, ration reminders) into the
global file leaks those hooks into operator-interactive sessions, where
they are noise at best and corruption at worst.

Spore's spore-internal answer is a session-kind discriminator: spore's
tmux spawners tag each session with `WT_SESSION_KIND=coordinator` or
`WT_SESSION_KIND=worker`. Sessions spore did not spawn (operator-
interactive shells, ad-hoc debug claudes) leave the variable absent.

Three knobs work together:

1. The env. Set by `internal/fleet.EnsureCoordinator` and
   `internal/task.ensureSession`. Constants in
   `internal/task/sessionkind.go`.

2. The render-time filter. Hook bindings carry an optional
   `kinds: [...]` list; `spore hooks settings --kind <K>` (or
   `$SPORE_RENDER_KIND`) drops every binding whose `kinds` list lacks
   `K`. Empty `kinds` is unscoped and always passes. Empty kind on
   the renderer is a no-op (legacy bulk render).

3. The runtime gate. `spore hooks gate-kind <K...> -- <CMD> [ARGS...]`
   reads `WT_SESSION_KIND`, exits 0 silently on miss, otherwise
   runs `CMD ARGS` with stdin/stdout/stderr inherited.

## Wiring shape for a consumer

The consumer keeps one source-of-truth `hooks-config.json`:

```json
{
  "events": {
    "Stop": [
      {"command": "/usr/bin/coord-inbox-watch", "kinds": ["coordinator"]},
      {"command": "/usr/bin/worker-token-monitor", "kinds": ["worker"]},
      {"command": "/usr/bin/lint-noise"}
    ]
  }
}
```

The home-manager-rendered user-level `~/.claude/settings.json` calls
`spore hooks settings --kind operator < hooks-config.json` so it picks
up only the unscoped `lint-noise` binding. Coordinator and worker
sessions get their own per-project settings file written by spore at
spawn time: `internal/fleet.EnsureCoordinator` renders with
`--kind coordinator` into `<projectRoot>/.claude/settings.local.json`,
and `internal/task.ensureSession` does the same for each worker with
`--kind worker` into `<worktree>/.claude/settings.local.json`. Source
of truth is `<projectRoot>/configs/claude/hooks-config.json` (override
with `$SPORE_HOOKS_CONFIG`); when the source is absent the spawner
skips the write so unmanaged repos stay unmanaged. Set
`$SPORE_SKIP_SETTINGS_INJECT=1` on the spawn call to skip the write
entirely (the operator-hand-rolled `.claude/settings.local.json` is
left intact). claude-code merges per-project settings on top of the
user-level file, so an operator-interactive `claude` started outside
a spore-spawned session sees the user-level hooks unchanged.

Belt-and-suspenders for hooks that must stay wired user-globally:
wrap them with `gate-kind` in the source `hooks-config.json`:

```json
{"command": "spore hooks gate-kind coordinator -- /usr/bin/coord-inbox-watch"}
```

The gate exits 0 silently outside coordinator sessions, so the same
user-level settings.json stays attached to every claude session and
the binary only runs where it is wanted.

## Lint

`HooksDrift` carries a `Kind` field; set it to the rendered kind so
the lint compares per-kind files against per-kind renders.

## Back-compat

`hooks.Settings(events)` keeps the kind-blind behaviour for callers
that have not adopted `kinds`. `spore hooks settings` without
`--kind` matches. `WT_SESSION_KIND=""` (operator-interactive) misses
every non-empty `kinds` list, so a binding without a kind tag is the
only one that runs in every session.
