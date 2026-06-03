# Development

Use the flake dev shell for the toolchain used by CI:

```sh
nix develop
just check
```

`just check` runs formatting checks, Go vet, golangci-lint, Spore's own
lint suite, Go tests, govulncheck, `nix flake check`, and the Go + flake
build. It is a strict superset of what CI verifies, so a green
`just check` locally means a green CI Check step. `just build` is still
available standalone for a quick binary rebuild without the lint suite.
`just coverage` writes local coverage reports under `coverage/` and is
required in CI before the advisory Codecov upload step runs.

Without entering the shell:

```sh
nix develop --command just check
```

The CLI is plain Go. Start at [../cmd/spore/main.go](../cmd/spore/main.go)
for command routing.

## Releases

`just release X.Y.Z` is the only supported way to cut a release. The
recipe runs `just check`, bumps `VERSION`, commits, tags `vX.Y.Z`,
pushes both to origin, and creates a GitHub release with auto-generated
notes in one shot. Tag and GitHub release move together: never tag
without a release, never release without a tag. The recipe refuses to
run off `main`, on a dirty tree, on a duplicate tag, or on a red
`just check`.
