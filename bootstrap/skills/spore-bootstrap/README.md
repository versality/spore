# spore-bootstrap skill

Agent-facing instructions for walking an adopted project through
spore's bootstrap stage gates.

## Files

- `SKILL.md` - Claude skill definition. Drop this where the agent
  runtime expects skills.

## Install

```
mkdir -p ~/.claude/skills/spore-bootstrap
cp SKILL.md ~/.claude/skills/spore-bootstrap/SKILL.md
```

For other agent runtimes, place `SKILL.md` wherever that runtime
discovers skills (`.cursor/`, `.opencode/`,
`~/.config/<tool>/skills/`, etc).

## What the skill does

Reads `spore bootstrap status` to learn the cursor, runs the
matching stage's runbook, writes the sentinel JSON the Go detector
validates (info-gathered.json or readme-followed.json), then
re-runs `spore bootstrap` to advance.

The Go detectors live under `internal/bootstrap/`; per-stage
runbooks live under `bootstrap/stages/`. Treat the skill as the
agent-side companion to the runbooks.
