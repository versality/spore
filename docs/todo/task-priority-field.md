**Status**: spec
**Priority**: critical

# task-priority-field

Add a `priority:` frontmatter scalar to `tasks/<slug>.md` so promote-order
is data-driven rather than operator-memory.

## Value space

Four-tier ladder: `critical | high | medium | low`. Highest first.

Rationale:

- `critical|queued|maybe` (the docs/todo precedent) mixes priority with
  backlog-state. Every non-done task is "queued" once we ship the field;
  reusing the value forces the lint to forbid `queued` on `active`, which
  is awkward.
- `p0..p3` is ops jargon. The alignment-mode rule asks for plain words.
- 4 tiers (not 3) because the operator wants headroom: `high` covers
  "next-up" without burning `critical`, and `low` covers "do eventually,
  do not promote over anything".
- `critical` matches docs/todo's top tier, so the two systems still align
  at the high end where it matters most.

## Default

`medium` when `--priority` is omitted on `spore task new`. The CLI flag
is `--priority critical|high|medium|low`; unknown values are rejected at
the boundary.

## Required vs optional

Reconciled with the canonical state machine (`internal/task/status.go`
collapses draft/paused/parked/blocked into `backlog`):

- Required on `active` and `backlog` (lint warns when missing or
  invalid).
- Optional on `done` (priority is a promote-order signal; done tasks do
  not promote).

The legacy-status nuance ("low for parked, medium for paused/draft") is
preserved only in the migration script's backfill heuristic, not in the
lint, because the canonical statuses do not distinguish them.

## Promote-order

`critical > high > medium > low`. Tiebreak by `created` ascending, then
`slug` ascending. Exposed as `task.PriorityRank(string) int` and
`task.SortByPromoteOrder([]frontmatter.Meta)`.

Unknown or empty priorities sort *after* `low` so a missing field never
jumps the queue.

The downstream coordinator reads frontmatter directly and picks the
highest-priority backlog task when promoting. Auto-replenish stays off
(floor=0); priority guides the human, not the harness.

## Display

- `spore task ls` adds a `PRIORITY` column between `STATUS` and `TITLE`
  and switches its default sort to promote-order. `--all` and `--done`
  paths keep slug-sort for stability.
- `spore fleet status` per-slug priority line is deferred (scope cut).
- Tmux session tier-tag is untouched.

## Lint

`task-priority` (registered in `Named()`; not in `Default()` yet).

- Flags active/backlog tasks missing `priority:`.
- Flags any task with an invalid priority value.
- Skips done tasks.

Warn-only by default during the soak window:
`SPORE_PRIORITY_WARN_ONLY=0` flips to fail-on-finding once a repo is
fully migrated.

## Migration

`spore task migrate-priority [--dir tasks] [--dry-run]` walks
`tasks/*.md`:

- already-valid priority: skip.
- already-present but invalid priority: error, operator fixes by hand.
- missing on `active|paused|draft|backlog`: assign `medium`.
- missing on `parked`: assign `low`.
- missing on `blocked`: skipped and surfaced at the end of the run
  ("blocked tasks need operator priority"); the lint will keep flagging
  until the operator edits.
- missing on `done`: skip.

Routes through `frontmatter.Parse` / `frontmatter.Write` so other
fields round-trip.
