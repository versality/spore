---
name: spore-bootstrap
description: Walk an adopted project through spore's bootstrap stage gates. Invoke when the operator runs `/spore-bootstrap`, drops into a fresh tree, or sees `spore bootstrap` block on a stage that needs the agent (info-gathered, readme-followed). The skill introspects the on-disk stage state, runs the stage's runbook, and writes the sentinel files the Go detectors check.
---

# spore-bootstrap

Spore drives an adopted project through ordered stage gates:

```
repo-mapped -> info-gathered -> tests-pass -> creds-wired ->
readme-followed -> validation-green -> pilot-aligned ->
worker-fleet-ready
```

Each stage has a Go detector under `internal/bootstrap/`. Some
detectors (info-gathered, readme-followed) need the agent to walk
the operator through a prompt, then write a sentinel JSON file the
detector reads. This skill is what does that walk.

## When to use

- The operator runs `/spore-bootstrap`.
- `spore bootstrap` blocks at `info-gathered` or `readme-followed`.
- The operator opens a tree spore has not yet bootstrapped.

## Orienting

Read the current state first; do not re-prompt for stages already
completed.

```
spore bootstrap status
```

The table shows per-stage status (pending / completed / skipped /
failed) plus the recorded notes. The state file lives at
`$XDG_STATE_HOME/spore/<project>/bootstrap.json` (default
`~/.local/state/spore/<project>/`).

## Per-stage agent action

### `repo-mapped`

The Go detector handles this on its own: it inspects the project
root for known markers (flake.nix, Cargo.toml, go.mod, package.json,
pyproject.toml, Gemfile, deps.edn, pom.xml, Makefile, justfile) and
drops starter instruction files if absent. Do not pre-empt it.

### `info-gathered`

This stage needs you. The brief: surface the project's existing
project-management and knowledge surfaces so spore plus downstream
runners can read the operator's existing tickets and wiki instead of
asking them to re-state work in file briefs.

Use `AskUserQuestion` with a small enumerated choice for each tool
family. Do not ask free-form. Examples:

- Tickets: `Jira` / `Linear` / `GitHub Issues` / `none (use spore tasks)`.
- Knowledge: `Notion` / `Confluence` / `Obsidian` / `Google Docs` /
  `docs/ tree in this repo` / `none (use docs/todo + spore docs/list.md)`.

For each tool the operator picks (other than `none`):

1. Ask the operator to add the access creds via the creds-broker.
   The skill must record the broker reference key, never the secret
   itself.
2. If the operator picks none, record the substitute decision
   (spore tasks / spore docs/todo).

Write the result to
`$XDG_STATE_HOME/spore/<project>/info-gathered.json` with this
shape (the Go detector validates the schema):

```json
{
  "tickets": {
    "tool": "linear",
    "creds_ref": "spore.creds.linear",
    "decision": "use existing"
  },
  "knowledge": {
    "tool": "none",
    "decision": "use docs/todo + spore docs/list.md"
  },
  "completed_at": "2026-04-29T10:00:00Z"
}
```

`tool` must be one of:
- tickets: `jira` / `linear` / `github-issues` / `none`.
- knowledge: `notion` / `confluence` / `obsidian` / `google-docs` /
  `docs-tree` / `none`.

`creds_ref` is required when `tool != "none"`. Re-run
`spore bootstrap` after writing the file to advance.

### `tests-pass`

The Go detector sniffs for the project's test command (`just check`,
`just test`, `go test ./...`, `cargo test --no-run`, `pytest`, or
`npm test`) and runs it. If it fails, the blocker shows the tail of
the output. Fix the underlying issue; do not skip unless the suite
is genuinely unreachable in this environment.

### `creds-wired`

The detector checks for any obvious secret surface (`.env`,
`.envrc`, `secrets/`, `.env.example`, `*.age`) and that agent
instructions mention how the agent obtains values. If the blocker
fires, edit the instruction files to document the secret surface
(creds-broker reference,
`.envrc` shape, agenix path, etc) before re-running. Never paste
the value itself.

### `readme-followed`

This stage needs you. Walk the project README with the operator and
record one ReadmeFollowItem per "to use, do X" instruction.

1. Read the README at the project root.
2. Extract each setup / install / usage instruction. Three to ten
   items is typical.
3. For each item, attempt the step yourself if it is safe and
   non-destructive (read-only commands, env inspections). Mark
   the item:
   - `ok` if the step worked.
   - `skip` if you cannot run it from this environment (e.g. needs
     a key the operator has, sudo not available). Add a comment.
   - `fail` if the step is broken; flag it and stop.
4. Write
   `$XDG_STATE_HOME/spore/<project>/readme-followed.json`:

```json
{
  "readme_path": "README.md",
  "items": [
    {"step": "run `direnv allow`", "status": "ok"},
    {"step": "set NPM_TOKEN", "status": "skip", "comment": "owner-only"},
    {"step": "run `make build`", "status": "ok"}
  ],
  "completed_at": "2026-04-29T10:00:00Z"
}
```

The detector blocks if any item is `fail`. Resolve the failing step
(open a follow-up task or fix the README) before re-running.

### `validation-green`

The detector runs the spore lint set (emdash, filesize,
comment-noise, claude-drift). On failure, the blocker shows the
first issue. Fix the source; do not silence the lint.

### `pilot-aligned`

Gated on `spore align`. The blocker references the alignment
checklist; surface it to the operator and follow `rules/core/alignment-mode.md`.
Do not write the alignment sentinel manually; let `spore align flip`
do it once the criteria are met.

### `worker-fleet-ready`

The detector smoke-tests the task data layer (allocate / write /
re-read / delete) in `<project>/tasks/`. If it fails, the task
package is broken; report and stop.

## End of bootstrap

When `spore bootstrap` reports `bootstrap complete.` the project is
ready for routine work. Subsequent `spore bootstrap` calls are
no-ops; `spore bootstrap reset` (with `--yes`) wipes state if the
operator wants to re-run.
