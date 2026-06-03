<p align="center">
  <img src="docs/logo.png" alt="spore" width="850">
</p>

# spore

[![CI](https://github.com/versality/spore/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/versality/spore/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/versality/spore/branch/main/graph/badge.svg)](https://codecov.io/gh/versality/spore)

Spore is a small, local harness for LLM coding agents. It plants rules,
task files, hooks, validation gates, and tmux worker sessions into an
existing repo so agents can work on explicit tasks without turning the
project into a SaaS workflow.

**Status:** beta, dogfooded on live projects.

## How it works

- each task is a markdown file under `tasks/`;
- each active task gets its own git worktree and tmux session;
- a coordinator starts workers, watches progress, and kills stale
  sessions;
- hooks and lints run before a task can close;
- closing a task requires evidence: commits, changed files, tests, or a
  written reason none apply.

Everything runs in your repo through git and tmux. Attach to a session,
read the task file, inspect the branch, or kill the fleet.

## Install

Spore installs with Nix. On a flake-based system, add it as an input and
reference the package:

```nix
# flake.nix
inputs.spore.url = "github:versality/spore";

# NixOS
environment.systemPackages = [ inputs.spore.packages.${pkgs.system}.default ];

# home-manager
home.packages = [ inputs.spore.packages.${pkgs.system}.default ];
```

Or run it without installing:

```sh
nix run github:versality/spore -- <args>   # one-shot
nix shell github:versality/spore           # ephemeral shell on PATH
nix profile install github:versality/spore # imperative install
```

## Quick start

Adopt an existing project:

```sh
cd /path/to/project
spore bootstrap
```

Run `spore bootstrap` as many times as you like. Each run advances the
setup as far as it can, then stops and tells you the one thing it needs
from you next. The steps it walks through: scan the repo, find the test
command, set up credentials, check the README, run the tests, and turn
on the worker fleet.

When the project is worker-ready, create work and start the fleet:

```sh
spore task new "first task"
spore fleet enable
spore fleet reconcile
```

Each worker and the coordinator run in their own tmux session. Run
`spore fleet list-sessions` to see them all.

## Fresh server

`spore infect` installs NixOS over SSH and starts the coordinator on a
freshly provisioned, root-reachable VM:

```sh
spore infect 203.0.113.7 \
  --ssh-key ~/.ssh/id_ed25519 \
  --repo /path/to/project \
  --coordinator-agent claude \
  --coordinator-model sonnet

ssh -t spore@203.0.113.7
```

It is destructive: it wipes the target host. See
[docs/infect.md](docs/infect.md) for full flag behavior, the Codex
coordinator variant, custom flakes, and failure hints.

## Configuration

Run `spore init` to scaffold a documented `spore.toml`. The main
sections:

- [docs/worker-mix.md](docs/worker-mix.md) - pick worker agents (Claude,
  Codex, any binary) per task by ratio or complexity rule.
- [docs/matter.md](docs/matter.md) - pull work from external sources
  (Linear, GitHub Issues) and mirror task transitions back.
- [docs/fleet-module.md](docs/fleet-module.md) - autostart the fleet on
  NixOS hosts, horizontal scale, and graceful deployment drains.

## Architecture

Main extension points:

- [internal/bootstrap/](internal/bootstrap/) detects bootstrap stages
  and pairs with runbooks in [bootstrap/stages/](bootstrap/stages/).
- [internal/task/](internal/task/) owns task frontmatter, lifecycle,
  worktree creation, inbox handling, and merge close paths.
- [internal/fleet/](internal/fleet/) reconciles active tasks with tmux
  worker sessions and drives the matter sync prelude.
- [internal/matter/](internal/matter/) is the plugin layer for external
  work sources (e.g. [linear](internal/matter/linear/)).
- [internal/hooks/](internal/hooks/) emits and runs Claude Code and
  Codex hook bindings through `.claude/settings.json` and
  `.codex/hooks.json`.
- [internal/lints/](internal/lints/) holds portable repo lints.
- [internal/composer/](internal/composer/) renders instruction files
  from rule fragments in [rules/](rules/).
- [nixosModules/spore-fleet.nix](nixosModules/spore-fleet.nix)
  autostarts fleet reconciliation on NixOS hosts.

## Docs

- [docs/design.md](docs/design.md) - origin, design rationale, and
  unresolved tradeoffs.
- [docs/worker-dispatch.md](docs/worker-dispatch.md) - why workers are
  spawned through tmux and how the merge close path works.
- [docs/evidence.md](docs/evidence.md) - the evidence contract for task
  close gates.
- [docs/budget.md](docs/budget.md) - rolling Anthropic spend tracking
  and budget advice.
- [docs/development.md](docs/development.md) - dev shell, `just check`,
  and the release recipe.
- [bootstrap/README.md](bootstrap/README.md) - bootstrap layout, skills,
  stage runbooks, and smoke test.

## License

Spore is licensed under the Apache License, Version 2.0. See
[LICENSE](LICENSE) for the full text.
