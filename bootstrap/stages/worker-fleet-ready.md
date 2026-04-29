# Stage: worker-fleet-ready

The terminal stage. Spore can mint and operate tasks under the
project tree.

## Detect

`internal/bootstrap/worker_fleet.go`. In-process smoke of the task
data layer:

1. Allocate a slug with `task.Slugify` + `task.Allocate` under
   `<project>/tasks/`.
2. Write a `tasks/<slug>.md` with valid frontmatter.
3. Re-read via `task.List` and confirm the slug is present with
   status `draft`.
4. Delete the smoke file.

The hermetic round-trip is what gates this stage. The full
`new -> start -> done` lifecycle (which spawns tmux + a worktree)
is covered by `bootstrap/smoke.sh`, run as a separate end-to-end
check in CI.

## Exit criteria

Smoke round-trip succeeds and leaves no residue under `tasks/`.

## Blocker shapes

- `smoke task <slug> absent from task.List` - the data layer is
  broken; report and stop.
- Filesystem errors on the tempdir-scoped smoke file - typically a
  permissions issue under `<project>/tasks/`. Fix and re-run.

## Notes recorded

`task data layer round-tripped (<slug>)`. The slug is a one-shot
test slug under `spore-bootstrap-smoke*` and is deleted before the
detector returns.
