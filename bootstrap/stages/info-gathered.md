# Stage: info-gathered

Sequenced right after `repo-mapped`. The bootstrap blocks here until
the agent and the operator have surveyed the project's existing
project-management and knowledge surfaces, recorded the access
shape, and persisted the result under
`$XDG_STATE_HOME/spore/<project>/info-gathered.json`.

## Why this stage exists

> "Job of the harness is to collect information as soon as possible
> before it starts building itself. The more data the better."
> -- operator amendment, 2026-04-29

Spore plus downstream runners should be able to read the operator's
existing tickets and wiki instead of asking them to re-state work
in file briefs. Doing the survey early (before `tests-pass`,
before any code-touching work) means the rest of the bootstrap can
already lean on the right ticket and doc surfaces.

## Exit criteria

1. `info-gathered.json` exists under the project state directory.
2. `tickets.tool` is one of `jira` / `linear` / `github-issues` /
   `none`. When non-`none`, `tickets.creds_ref` is set to the
   creds-broker reference key.
3. `knowledge.tool` is one of `notion` / `confluence` / `obsidian`
   / `google-docs` / `docs-tree` / `none`. Same creds_ref rule.

## Runbook

Run the `spore-bootstrap` skill from the agent. The skill uses
`AskUserQuestion` with a small enumerated choice for each tool
family (no free-form prompts) and writes the sentinel JSON.

```
{
  "tickets": {
    "tool": "linear",
    "creds_ref": "spore.creds.linear",
    "decision": "use existing"
  },
  "knowledge": {
    "tool": "docs-tree",
    "decision": "use existing docs/"
  },
  "completed_at": "2026-04-29T10:00:00Z"
}
```

`creds_ref` records only the broker reference key, never the
secret. If the operator picks `none`, record the substitute
decision (spore tasks for tickets, `docs/todo` + spore
`docs/list.md` for knowledge).

After writing the file:

```
spore bootstrap            # advances past info-gathered
```

## Blocker shapes

- `no info-gathered.json under <state-dir>` - run the skill, write
  the file.
- `tickets.tool=...; want one of ...` - the JSON has an unknown
  tool; pick from the validated list.
- `tickets.tool is set but tickets.creds_ref is empty` - the
  operator owes the creds-broker entry. Add it; record the key.
