# Stage: tests-pass

The project's existing test suite must run green from the agent
shell.

## Detect

`internal/bootstrap/tests_pass.go`. The detector picks the first
recipe whose marker is present and (when relevant) whose preflight
check passes:

| Marker | Command |
| --- | --- |
| `justfile` with `check:` recipe | `just check` |
| `justfile` with `test:` recipe | `just test` |
| `go.mod` | `go test ./...` |
| `Cargo.toml` | `cargo test --no-run` |
| `pyproject.toml` / `setup.py` | `pytest -q` |
| `package.json` | `npm test --silent` |

## Exit criteria

The picked command exits zero.

## Blocker shapes

- `no recognised test recipe (...)` - the project has no marker
  spore knows. Add one (or extend `recipesFor` in
  `tests_pass.go`) and re-run.
- `test recipe ... needs <bin> on PATH` - install the toolchain
  first. Skipping is testing-only and weakens the gate.
- `<command> failed: ...` - the suite is red. Fix the underlying
  failure; the blocker shows the tail of the output for triage.

## Notes recorded

`ran <command>`. Keeps the audit trail of which recipe spore
chose; useful when re-bootstrapping a project whose stack changed.
