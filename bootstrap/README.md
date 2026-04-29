Skills, stage runbooks, and the bundled NixOS-anywhere flake that
spore drops into adopting projects.

## Layout

- `skills/` - agent-side skills (`diagram`, `spore-bootstrap`).
  Drop the `SKILL.md` of each into the runtime's skill directory.
- `stages/` - one runbook per bootstrap stage. The Go detectors
  under `internal/bootstrap/` reference these for the operator-
  facing exit criteria + blocker shapes.
- `mcp/` - per-project MCP server templates.
- `flake/` - minimal NixOS flake used by `spore infect`.
- `smoke.sh` - end-to-end test that walks `spore bootstrap` from a
  fresh git repo to `worker-fleet-ready`. Run inside `nix develop`.

## Stage sequence

```
repo-mapped -> info-gathered -> tests-pass -> creds-wired ->
readme-followed -> validation-green -> pilot-aligned ->
worker-fleet-ready
```

Each stage has a Go detector under `internal/bootstrap/<stage>.go`
and a runbook under `stages/<stage>.md`. The `spore-bootstrap`
skill is the agent-side companion that walks the operator through
the stages that need the agent (`info-gathered`,
`readme-followed`).
