# spore event

`spore event` is the canonical fleet event bus. One jsonl, one schema,
three commands. Every consumer (skyhelm, wt-task, repo probes,
nix-config jsonl writers, marketer-com pipelines, wingbot, future
agents) publishes through the same binary so tools can tail, filter,
and react across the fleet without per-source schemas.

## Schema

One JSON object per line. Required fields:

| field   | type                | notes                                       |
| ------- | ------------------- | ------------------------------------------- |
| ts      | RFC3339 nanos (UTC) | stamped by `Append` if zero on the way in   |
| source  | string              | free-form, e.g. `skyhelm`, `wt-task`        |
| level   | enum                | `info` \| `warn` \| `error`                 |
| kind    | string              | convention: `<source>:<event-name>`         |
| message | string              | human-readable one-liner                    |

Optional:

| field | type     | notes                                                    |
| ----- | -------- | -------------------------------------------------------- |
| slug  | string   | task slug, project name, anything stable for filtering   |
| data  | raw JSON | arbitrary structured payload (any valid JSON value)      |

`level` is a closed enum. Anything outside `info|warn|error` is
rejected at publish time. The other strings are free-form by design:
the bus does not own consumer taxonomies.

## Storage

```
$XDG_STATE_HOME/spore/events.jsonl       # default ~/.local/state/spore/events.jsonl
$XDG_STATE_HOME/spore/events-<ts>.jsonl  # rotated files
```

Append-only. Publishers open with `O_APPEND` and write a single line
per event; the kernel guarantees atomic writes for line-sized buffers
on local filesystems, so no flock is needed.

When the current file would exceed `$SPORE_EVENT_MAX_BYTES` (default
64 MiB) on the next write, it's renamed to `events-<rfc3339>.jsonl`
and a fresh `events.jsonl` starts. Readers (`tail`, `watch`) merge
rotated files in name-sorted order before the current one, so
rotation is invisible to consumers.

Override the directory via `$SPORE_EVENT_DIR` (test-only escape
hatch; production should rely on `$XDG_STATE_HOME`).

## Commands

### publish

```
spore event publish --source S --kind K --level L [--slug X] [--data JSON] -- "<message>"
spore event publish --source S --kind K --level L --message-stdin
```

Appends one event. Exits non-zero on schema violation: missing
required field, invalid level, malformed `--data`. Use
`--message-stdin` to pipe multi-line bodies.

### tail

```
spore event tail [--since DUR] [--level L] [--source S] [--kind K] [--slug X] [-n N] [--follow]
```

Reads events from the bus. Default: most recent 50 events. Filters
compose AND. `--since 1h` is "events at or after now-1h". `-n 0` lifts
the cap. `--follow` continues tailing the current file after the
historical read.

### watch

```
spore event watch --filter '<expr>' --exec '<cmd>'
```

Long-running. Runs `<cmd>` once per matching event. `<expr>` is a
case-insensitive `key=value AND key=value` expression over `level`,
`source`, `kind`, `slug` (an empty filter matches everything).

Each match spawns one shell process and waits for it to exit before
reading the next event, so handlers serialize. The handler receives:

- the event JSON object on stdin (one line, no trailing newline
  beyond a single `\n`);
- env vars `SPORE_EVENT_TS`, `SPORE_EVENT_SOURCE`, `SPORE_EVENT_LEVEL`,
  `SPORE_EVENT_KIND`, `SPORE_EVENT_SLUG`, `SPORE_EVENT_MESSAGE`.

A non-zero handler exit logs to stderr but does not stop the watcher.

## Integration patterns

- **Bash consumers** (existing nix-config writers like token-monitor,
  proactive-loop, rower-watch, voluntary-events, harness-notify
  migrate to this):

  ```sh
  spore event publish \
    --source token-monitor --kind token-monitor:cap \
    --level warn --slug "$WT_SLUG" \
    --data "$(jq -nc --arg pct "$PCT" '{pct: ($pct|tonumber)}')" \
    -- "short window crossed ration"
  ```

- **Go consumers**: import `github.com/versality/spore/internal/event`
  and call `event.Append(&event.Event{...})`. The package handles
  validation, rotation, and atomic append.

- **Reactive handlers**: instead of polling, run

  ```sh
  spore event watch --filter 'level=error' \
    --exec 'notify-send "fleet error: $SPORE_EVENT_KIND"'
  ```

  under a systemd user unit.

## Why not stdout / journald / sqlite

- **stdout**: not durable across shells, not filterable post-hoc.
- **journald**: schema-free strings only, hard to query by
  source/kind/slug, host-bound.
- **sqlite**: needs a writer lock and a migration story; jsonl gives
  us cheap atomic appends and lets `jq` / `tail -f` / `awk` work as
  fallback debug tools.

The bus is intentionally minimal. If a consumer needs more
structure, encode it in `data`.
