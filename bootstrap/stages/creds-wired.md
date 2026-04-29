# Stage: creds-wired

Any required env / secret stubs are documented in `CLAUDE.md`. No
values are stored in the repo; only references.

## Detect

`internal/bootstrap/creds_wired.go`. Looks for any of `.env`,
`.envrc`, `secrets/`, `credentials`, `.env.example`,
`.env.template`, or `secrets/*.age` at the project root. When at
least one is present, `CLAUDE.md` must mention at least one
credentials keyword (`creds-broker`, `credential`, `secret`,
`agenix`, `.env`, `vault`, `envrc`, `environment variable`).

When no secret surface is present at all, the stage passes with
note `no secret surface detected; nothing to document`.

## Exit criteria

Either the project carries no secret surface, or the secret surface
is documented in `CLAUDE.md`.

## Blocker shapes

- `found <surface> but CLAUDE.md is absent` - run repo-mapped first
  (the starter CLAUDE.md unblocks this).
- `found ... but CLAUDE.md mentions none of ...` - edit `CLAUDE.md`
  to describe how the agent obtains values: which broker reference,
  which `.envrc` shape, which agenix path. Never paste the value
  itself.

## Why a documentation gate

Spore deliberately does not store secrets and does not auto-detect
how a project sources them. The gate keeps the agent honest: if the
operator wants the agent to use a credential, the operator has to
write down where it lives. The next session reads the same
`CLAUDE.md` and follows the same path.
