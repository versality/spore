# Stage: repo-mapped

The first gate. Spore inspects the project root for build-system
markers and, if instruction files are missing, drops starters that
point at `spore compose`.

## Detect

`internal/bootstrap/repo_mapped.go`. Looks for any of:

- `flake.nix` (nix)
- `Cargo.toml` (rust)
- `go.mod` (go)
- `package.json` (node)
- `pyproject.toml` / `setup.py` (python)
- `Gemfile` (ruby)
- `deps.edn` / `project.clj` (clojure)
- `pom.xml` (java)
- `build.gradle` (gradle)
- `Makefile` (make)
- `justfile` (just)

## Exit criteria

1. At least one marker present at the project root.
2. `CLAUDE.md` and `AGENTS.md` exist. The detector writes starters
   when absent; the operator edits them during the rest of the
   bootstrap.

## Blocker

`no recognised project marker (...)`. The project has no language
or build system spore can hook. Either it is empty (run the
project's own scaffolding first) or it uses a marker spore does not
yet recognise (extend `repoMarkers` in `repo_mapped.go`).

## Notes recorded

`detected: <comma-separated languages>; wrote starter CLAUDE.md / AGENTS.md`
(when applicable).
