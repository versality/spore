# Stage: repo-mapped

The first gate. Spore inspects the project root for build-system
markers and, if instruction files are missing, drops starters that
point at `spore compose`.

## Detect

`internal/bootstrap/repo_mapped.go`. `flake.nix` is mandatory: Nix is
a hard requirement for spore, so a project root without one is
rejected before any other marker is considered. The remaining markers
only enrich the detected language label:

- `flake.nix` (nix, required)
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

1. `flake.nix` present at the project root.
2. `CLAUDE.md` and `AGENTS.md` exist. The detector writes starters
   when absent; the operator edits them during the rest of the
   bootstrap.

## Blocker

`no flake.nix at project root: Nix is a hard requirement for spore`.
The project has no flake. Add one (see the flake template under
`bootstrap/flake/`) before re-running the gate; spore does not
support a non-Nix project layout.

## Notes recorded

`detected: <comma-separated languages>; wrote starter CLAUDE.md / AGENTS.md`
(when applicable).
