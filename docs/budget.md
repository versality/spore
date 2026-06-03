# spore budget

`spore budget` aggregates Anthropic spend across all claude-code
sessions for the current user on this host into rolling short (5h)
and long (7d) windows, then surfaces threshold-band advice
("ok"/"tighten") for stop-hook gating and one-line summaries.

## Subcommands

```
spore budget refresh       Recompute state.json from /usage + transcripts.
spore budget query         Print state.json with a fresh advice band.
spore budget summary       Human one-liner. Default when no subcommand is given.
spore budget stop-hook     Stop-hook helper: exit 2 with stderr reminder on a
                           fresh band crossing, exit 0 otherwise.
spore budget active-tier   Print the live credential's tier (max|pro|team|free).
spore budget debug-usage   Hit /usage once and print raw + parsed response
                           (bypasses the freshness throttle; diagnostic).
```

## Caps and bands

Two rolling windows, each with its own cap:

| Window | Default cap | Tighten at |
| ------ | ----------- | ---------- |
| short (5h) | $250 | 80% |
| long (7d)  | $2000 | 80% |

The advice band is the OR of the two windows: if either window is in
`tighten`, the advice is `tighten`. Otherwise `ok`. Per-window bands
are tracked separately for the stop-hook fresh-crossing detector.

Caps are configurable via `AGENT_BUDGET_SHORT_CAP` and
`AGENT_BUDGET_LONG_CAP` (USD floats). Caps only matter on the
transcript fallback: the `/usage` endpoint reports utilization percent
directly, so the cost/cap fields round-trip as zero in state.json when
the snapshot is the live signal.

## Pricing-table convention

The model price table lives in `internal/budget/pricing.toml`,
embedded into the binary at compile time. Per-million-token rates in
USD, sourced from LiteLLM's anthropic.* table (the same source ccusage
uses) so `spore budget summary` lines up with `ccusage` within
rounding.

Update the file in-tree to add a new model or revise rates; no Go
change is needed - the next build picks it up. Each section is one
model id, with four required keys:

```toml
fallback = "claude-sonnet-4-6"

[claude-opus-4-7]
input        = 5.00
output       = 25.00
cache_read   = 0.50
cache_create = 6.25
```

A top-level `fallback` key names the model whose rates apply when an
unknown model id appears in a transcript. The fallback is intentionally
biased high (currently the Sonnet rate) so a new model id overshoots
spend tracking rather than silently zero-costing.

The basecamp `agent-budget` binary inlines its pricing in code; spore
extracts it as part of this port so spore-side updates are decoupled
from a Go release.

## Collection

The primary signal is the OAuth /usage endpoint (account-wide, sees
every host's claude-code activity). It falls back to
`~/.claude/projects/*/*.jsonl` cost-weighted transcript aggregation
when /usage is unreachable.

`refresh` reads `~/.claude/.credentials.json`, hits
`https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer
<accessToken>` and `anthropic-beta: oauth-2025-04-20`, and stores the
returned utilization percent and `resets_at` per window. On a 401 with
a refresh token present, the binary refreshes once via
`https://platform.claude.com/v1/oauth/token` and retries; the rolled
credentials are written back via mktemp+rename. Other HTTP failures
keep the previous snapshot but flag it `Stale` so consumers can decide
whether to trust it.

A freshness gate prevents hammering `/usage`: successive `refresh`
calls within 60 seconds reuse the cached snapshot. Override with
`AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC` (0 disables the gate).

## Stop-hook contract

`spore budget stop-hook` is the entry point for a claude-code Stop
hook. It does not read stdin or write JSON; it only signals via exit
code and stderr.

| Exit | Meaning |
| ---- | ------- |
| 0    | No fresh band crossing, or band == "ok". Silent; nothing on stderr. |
| 2    | Fresh crossing into "tighten". Prints a one-line reminder on stderr. |

Spore's stop-hook does not gate on an orchestrator-identity env: the
consumer wires the hook into their settings.json only for the agents
they want monitored. Any orchestrator-shape gating (e.g. firing only
for coordinator turns, not worker turns) belongs in the consumer's
hook config, not in this binary.

### Marker semantics

`spore budget` keeps per-window-per-band markers under
`$AGENT_BUDGET_STATE_DIR/markers/`:

```
short-tighten  long-tighten
```

Invariants:

- band == ok:      marker absent.
- band == tighten: marker present.

A "fresh crossing" is defined as creating a marker that was absent on
entry. Drop a marker by hand to re-arm the reminder for a window.

### Reminder text

```
AGENT BUDGET (tighten): short=82% (resets in 1h12m), long=18%.
Defer non-urgent worker starts. Route lightweight turns through a cheaper
model. Reserve top-tier models for tool-use loops and code edits.
```

The "(resets in ...)" hint appears only on the binding window for the
band - the one whose frac actually crossed. The advice tail is band-
specific.

## State file

`$AGENT_BUDGET_STATE_DIR/state.json` (mode 0600). Default state dir is
`$XDG_STATE_HOME/agent-budget`, falling back to
`~/.local/state/agent-budget`.

The schema is byte-compatible with the basecamp `agent-budget` binary:
both can read and write the same file. This is the substrate of the
shadow-soak migration plan - run both binaries against the same
state.json for several days, compare `summary` outputs, then swap.

Schema (only fields a downstream tool should treat as stable; other
fields are internal):

```json
{
  "mode": "subscription",
  "updated_at": "2026-04-30T12:34:56Z",
  "short": {
    "duration_seconds": 18000,
    "cost_usd": 12.34,
    "cap_usd": 250.0,
    "frac": 0.0494,
    "oldest_event_at": "...",
    "reset_at": "...",
    "message_count": 7,
    "source": "transcript"
  },
  "long":  { ... same shape as short ... },
  "advice": "ok",
  "cache":  { "<path>": { "size": 0, "mtime_ns": 0, "messages": [...] } },
  "usage_snapshot": {
    "fetched_at": "...",
    "short": { "utilization": 5.0, "resets_at": "..." },
    "long":  { "utilization": 43.0, "resets_at": "..." },
    "stale": false
  }
}
```

`source` is one of `transcript`, `usage`, or `usage-stale`, depending
on which collection path filled the window.
