# spore: design notes

**Status**: kernel + bootstrap stages shipped. This doc is preserved
as origin and rationale; current behaviour is documented in the
top-level README plus the per-stage runbooks under `bootstrap/stages/`.

## Origin

spore was extracted from a personal NixOS + home-manager flake that
grew an LLM-agent harness incrementally. The upstream harness landed
in one commit on 2026-04-14 ("Add harness") and accreted heavily
through 2026-04-21: an agent-instruction rule pool plus composer,
several drift / file-size / comment-noise / em-dash lints, an
`install-hooks` recipe wiring `core.hooksPath` for each worktree.
Each lint exists because the agent made a specific mistake first;
the rule was the lesson learned.

That organic-growth pattern is what spore needs to reproduce. The
kernel is the substrate (rule pool, composer, hook plumbing, lint
framework, worktree-task driver). The rules themselves are emergent
per project.

## Two slices

Naming kept generic on purpose. Concrete tickets to be drafted
later, after the kernel directory layout settles.

### Slice 1: kernel

Drop-in substrate, generic enough to plant in any project repo.

- Worktree-task driver. Port the upstream's existing `wt-go` (Go,
  stdlib-only where possible) renamed and stripped of Nix-specific
  paths.
- Instruction tier system + composer. Pool of rule fragments composed
  into per-project `CLAUDE.md` / `AGENTS.md` files plus per-tool
  mirrors. A drift gate
  prevents the rendered file from diverging from the pool silently.
- Hook patterns. `Stop` (auto-commit on green), `PreToolUse`
  (forbidden-command guard), with a pluggable shape so each project
  adds its own.
- Minimal lint set. drift, file-size, comment-noise, em-dash. These
  are general; they apply to any source tree.
- Distribution. Vendored binaries via a single install script for
  non-Nix consumers; flake input or submodule for Nix consumers.

### Slice 2: progressive adoption

A `/harness-bootstrap` skill (or slash command) that walks the
operator from an unprepared repo to a worker-ready one.

- Map the project. Detect language, test runner, CI shape, secrets
  layer (or its absence).
- Generate the initial instruction files. Skeleton plus per-language
  fragments pulled from the rule pool. Operator edits before commit.
- Wire the validation gate. `make check`, `npm test`, `cargo check`,
  whichever shape the project actually uses. The gate must be green
  before the bootstrap continues.
- Stage gates, in order:
    1. `repo-mapped`: language, runner, CI, secrets posture known.
    2. `tests-pass`: existing tests run green from the agent shell.
    3. `creds-wired`: whatever secrets / auth the project needs is
       reachable from the agent's environment.
    4. `readme-followed`: the project's own README setup steps run
       end to end. The operator dogfoods their docs through the
       agent.
    5. `validation-green`: the wired gate is committed and green.
    6. `worker-fleet-ready`: kernel installed, gate green, rules
       seeded; the project can host autonomous worker agents.
- State file analogous to the upstream's coordinator state file.
  Resumable across sessions; not a database.

## Hard parts (named, not solved)

### Packaging: Nix-first

v1 ships as a Nix flake: a derivation that builds the spore binary,
a dev shell carrying the full agent tool set (Go toolchain, lints,
formatter, search helpers, tmux, jq, ripgrep, and friends), and the
install path (`nix profile install github:.../spore` or as a flake
input in the consumer's own flake). Nix is chosen deliberately, not
as a last-resort packaging step.

The upstream harness leans hard on Nix and the payoff transfers: the
agent can `nix shell -p <pkg>` to acquire any tool transiently
without polluting the host; the dev shell is reproducible across
hosts; install / uninstall is atomic; binary cache hits make every
agent session fast. Spore inherits all of that, intentionally.

Consumers without Nix can vendor the binary out and replace the
packaging layer with whatever fits (Homebrew bottle, plain make
install, container image). Spore does not commit to supporting that
path as a first-class story; the surface is left replaceable.

Secrets and activation stay project-local (operator-bound). The
kernel does not prescribe them. The bootstrap flow records what the
project uses; it does not impose.

### Two-harness sync

After extraction, every harness improvement splits attention: does
the fix land in the upstream or in spore first? The mitigation we have
in mind, but are not committing to yet, is to make the upstream harness
consume spore as a vendor (git submodule, flake input, or vendored
copy with a drift gate). Then the template evolves and the upstream
pulls. Open question: do we instead keep them divergent on purpose,
with spore deliberately leaner, and only port specific patterns when
they prove generic across more than one project?

### Specificity tax

Most rules in the upstream's root instruction files exist because the agent
made specific mistakes there. A generic template ships infrastructure
(rule pool, lints framework, drift gate) but not the rules
themselves. Rules grow organically per project, the same way they
grew in the upstream. The bootstrap flow seeds a small starter set;
the project accretes the rest as the agent fails.

This is a feature, not a bug, but it has to be communicated clearly
to anyone adopting spore: "the kernel is small on purpose; your
rules will grow over time, driven by the mistakes your agent makes
in your tree".

## Naming: coordinator and worker

Spore's internal defaults are `dispatcher` (coordinator) and `runner`
(worker), used in spore's own repo, docs, and tests.

When spore initiates in a downstream project, the bootstrap flow
**prompts the operator of that project** for the two role names
during the `repo-mapped` stage, before any kernel files are written.
Defaults are `dispatcher` / `runner`; the operator can accept the
defaults or pick anything (`foreman` / `crew`, `pilot` / `cell`,
`captain` / `hand`, or anything fitting the project's voice).

The picked names get baked into the project's instruction files, the task
driver's CLI, and the rendered hook plumbing. Renaming after the
fact is not free but is a recipe, not a fork.

## Sync model with the upstream harness

Open. Two main shapes on the table:

1. spore as upstream. The originating harness consumes spore as a
   vendor, pulls updates, contributes generic improvements back.
   Specific rules stay upstream. Asymmetric: spore is intentionally
   leaner.
2. Independent forks with a periodic diff sweep. No vendor link;
   improvements are ported manually when they prove generic across
   projects. Slower, more flexible, more drift risk.

Decision deferred.
