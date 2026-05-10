# bootstrap/scripts

Generic-core harness shell scripts that ship from spore. `spore install`
drops these into `<root>/harness/` next to the consumer's per-project
shell glue.

Lifted from nix-config in `lift-hybrid-harness-scripts` (see
docs/todo/ for the parallel `consume-lifted-hybrid-harness` cutover).
Consumer-specific paths are parameterized by env vars or positional
args; defaults match the original nix-config layout where the script
is unambiguous on its own (`auto-commit-tasks.sh <repo-root>`,
`report-main-worktree-dirty.sh [root]`).

## Inventory

- `auto-commit-tasks.sh` - stage + commit drift in `tasks/*.md` via
  `spore task drift`. Idempotent. Standalone-runnable for testing.
- `hooks-render.sh` - settings.json composer wrapper. Calls
  `spore hooks settings` and merges with a per-consumer extras file.
  Reads the claude config dir from `$SPORE_HOOKS_CLAUDE_DIR`
  (default `<repo>/configs/claude`).
- `quiet-run.sh` - `<label> <cmd...>` wrapper. With `QUIET_SUCCESS=1`
  captures output and only prints on non-zero exit. When the label
  starts with `build`, takes a per-user flock at
  `$XDG_RUNTIME_DIR/spore-build.lock` so concurrent rower fleets do
  not fan out into N parallel builds.
- `report-main-worktree-dirty.sh [root]` - one-shot diagnostic that
  prints a short status block when the main checkout has local
  changes. Used by `wt merge` post-merge probes.

Scripts are dropped 0755 (their shebangs trip the executable mode in
`internal/install`). Re-running `spore install` is idempotent: only
drifted files are rewritten.
