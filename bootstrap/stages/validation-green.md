# Stage: validation-green

Spore's portable lint set runs over the project tree without
issues.

## Detect

`internal/bootstrap/validation_green.go`. Runs the set returned by
`internal/lints.Default()`:

- `emdash` - flags em-dashes / en-dashes in any tracked source.
- `filesize` - source files under 500 lines.
- `comment-noise` - section labels, restated bindings, undated
  TODOs, change-history comments.
- `claude-drift` - rendered `CLAUDE.md` consistent with composer
  output, when `rules/consumers/` is set up.

## Exit criteria

Every lint reports zero issues.

## Blocker shapes

- `lints reported issues: <name>: <count> issue(s); first: <path>:<line>: ...`
  Fix the issue; do not silence the lint. The detector tail
  references the first issue from each lint that fired.
- `<name> errored: ...` - the lint itself crashed. Open a follow-up
  task; do not skip.

## Notes recorded

`emdash: 0, filesize: 0, comment-noise: 0, claude-drift: 0` (or
whatever counts the lints reported). Useful in the audit trail
when comparing across bootstraps.
