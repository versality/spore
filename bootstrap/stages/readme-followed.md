# Stage: readme-followed

A low-confidence stage. The agent walks the project README with the
operator and confirms that each "to use, do X" instruction is
satisfied. The Go detector validates the structure of the resulting
sentinel; the LLM is the actual checker.

## Detect

`internal/bootstrap/readme_followed.go`. Reads
`$XDG_STATE_HOME/spore/<project>/readme-followed.json`:

```json
{
  "readme_path": "README.md",
  "items": [
    {"step": "run `direnv allow`", "status": "ok"},
    {"step": "set NPM_TOKEN", "status": "skip", "comment": "owner-only"}
  ],
  "completed_at": "2026-04-29T10:00:00Z"
}
```

`status` is `ok` (step ran), `skip` (intentionally not attempted;
add a comment), or `fail` (broken; resolve before re-running).

## Exit criteria

1. A README exists at the project root (`README.md`, `README`,
   `Readme.md`, `README.rst`).
2. `readme-followed.json` exists with at least one item.
3. No item has `status: "fail"`.

## Runbook

Run the `spore-bootstrap` skill. The skill:

1. Reads the README.
2. Extracts each setup / usage instruction.
3. Attempts safe steps itself; marks results.
4. Writes `readme-followed.json`.

The agent picks the items, not the operator. The operator is
consulted only when a step is destructive or needs a key the agent
cannot reach.

## Blocker shapes

- `no README at project root` - add one (or add a setup section to
  an existing one).
- `no readme-followed.json under <state-dir>` - run the skill.
- `items[N].status=...; want one of ok/skip/fail` - the JSON has an
  unknown status; pick from the validated list.
- `items is empty` - the agent extracted zero items; either the
  README has no usage steps (write some) or the extractor missed
  them (re-run with the operator).
- `<N>/<M> items marked fail` - resolve the failing steps and
  re-run.
