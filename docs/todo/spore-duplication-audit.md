**Status**: spec
**Priority**: critical

# spore duplication and redundancy audit

## Executive summary

This audit found 12 consolidation candidates across fleet capacity, task lifecycle, state paths, session discovery, hook rendering, and config parsing.
The highest-risk issues are the soft fleet capacity model, lifecycle status drift, task root bypasses, and tmux session naming drift.
Most findings share the same root cause: defaults and selectors live near callers instead of in a small kernel package.
Recommended order: capacity enforcement, lifecycle FSM, task root resolution, session discovery, state path cleanup, then lower-risk parser and doc drift.

## Findings

Severity rubric: high=could crash fleet/cause data loss, medium=surprising behavior, low=annoyance.

### F-01: Fleet capacity has split floors, caps, and env names

**Class**: multi-defined-default | soft-limit-no-enforcement
**Severity**: high (if this fires, the fleet can over-promote or under-alert because reconcile and health checks use different capacity concepts)

**Locations**:
- `internal/fleet/fleet.go:30`
- `internal/fleet/fleet.go:90`
- `internal/fleet/fleet.go:192`
- `cmd/spore/fleet_cmd.go:330`
- `cmd/spore/fleet_cmd.go:337`
- `internal/coordinator/failuresummary/failuresummary.go:40`
- `internal/coordinator/failuresummary/failuresummary.go:92`
- `cmd/spore/coordinator_cmd.go:616`
- `nixosModules/spore-fleet.nix:218`

**Concept**: The worker fleet needs one capacity model with a desired floor, a hard cap, and clear override precedence.
**Drift**: Reconcile defaults `max_workers` to 3, the Nix module also defaults to 3, failure summary defaults its active-live floor to 6, and `resolveMaxWorkers` still accepts `WT_FLEET_FLOOR` as a max-workers override.
**Consequence**: A health check can demand 6 live workers while the reconciler deliberately caps at 3, and legacy `WT_FLEET_FLOOR` can change a cap at an unrelated entry point.
**Proposed consolidation**: Create one fleet capacity package that parses `[fleet]`, env, and flags into `desired_floor` and `max_workers`; enforce `max_workers` at every spawn path and make failure summary read the same resolved object.
**Suggested ticket title**: `consolidate-fleet-capacity-config` (effort: high).

### F-02: Task lifecycle statuses disagree across CLI, FSM, docs, and reconcile

**Class**: multi-defined-default | doc-code-drift | soft-limit-no-enforcement
**Severity**: high (if this fires, a parked worker can be reaped or hidden even though docs promise the worktree and session survive)

**Locations**:
- `cmd/spore/task_cmd.go:32`
- `cmd/spore/task_cmd.go:350`
- `internal/task/status.go:5`
- `internal/task/status.go:16`
- `internal/task/lifecycle.go:136`
- `internal/task/lifecycle.go:152`
- `internal/fleet/fleet.go:138`
- `docs/todo/worker-lifecycle-fsm.md:101`

**Concept**: Task status should be a single finite-state machine with one meaning for draft/backlog, paused, parked, blocked, active, and done.
**Drift**: CLI help says `pause` writes paused, the command actually writes parked, the status package aliases draft/paused/parked/blocked to backlog, lifecycle still has separate Pause and Park transitions, reconcile keeps active/paused/blocked sessions but reaps parked, and the FSM doc says parked worktrees are retained and scheduler-readable.
**Consequence**: Operators and coordinator logic cannot rely on the status word; a command that looks like a non-teardown pause can create a state that reconcile treats as stale.
**Proposed consolidation**: Move all allowed states, aliases, transitions, and session side effects into one `internal/task/fsm` package; make CLI help, task list filtering, reconcile, and docs consume that model.
**Suggested ticket title**: `centralize-task-lifecycle-fsm` (effort: high).

### F-03: `SPORE_TASKS_DIR` only affects some task entry points

**Class**: soft-limit-no-enforcement | parallel-impl
**Severity**: high (if this fires, systemd or hooks can inspect one task tree while operator commands mutate another)

**Locations**:
- `cmd/spore/task_cmd.go:270`
- `cmd/spore/task_cmd.go:279`
- `cmd/spore/task_cmd.go:283`
- `cmd/spore/task_cmd.go:350`
- `cmd/spore/task_cmd.go:488`
- `cmd/spore/task_cmd.go:571`
- `cmd/spore/task_cmd.go:591`
- `internal/coordinator/slascan/slascan.go:69`

**Concept**: Every task command should resolve the task root the same way.
**Drift**: `resolveTasksDir` supports `SPORE_TASKS_DIR`, git root, project list, and `tasks`, but core commands such as `new`, `ls`, `start`, `pause`, `done`, `merge`, `drift`, and `edit` hardcode `"tasks"` while `waybar`, `ship`, and coordinator SLA scan use the resolver or env.
**Consequence**: A nonstandard task directory is only partly honored; one entry point can report green while another writes to the checkout-local `tasks/`.
**Proposed consolidation**: Introduce a task command context with a resolved tasks dir and pass it through every task subcommand and coordinator scanner.
**Suggested ticket title**: `route-all-task-commands-through-task-root-resolver` (effort: medium).

### F-04: Tmux session naming and matching are implemented four ways

**Class**: parallel-impl | doc-code-drift | copy-paste
**Severity**: high (if this fires, deploy hooks or cleanup can miss live workers and leave stale sessions running)

**Locations**:
- `internal/task/session_name.go:73`
- `internal/task/session_name.go:81`
- `internal/task/session_name.go:142`
- `internal/task/lifecycle_session.go:31`
- `internal/fleet/livenessstatus.go:123`
- `internal/fleet/reap.go:250`
- `nixosModules/spore-fleet.nix:47`
- `README.md:81`

**Concept**: Spore should have one canonical worker session shape and one matcher for all legacy and current names.
**Drift**: New workers use `<emoji> <project>/<slug> [tag]`, legacy helpers use `spore/<project>/<slug>`, README still documents only legacy names, the Nix graceful-deploy script greps only `^spore/$project/`, lifecycle matching enforces a slug boundary, liveness matching only uses substring contains, and orphan reap parses names again by slash and space.
**Consequence**: A deployment can report "no active workers" while emoji-style workers are alive, and liveness/reap can disagree on duplicate or orphan detection.
**Proposed consolidation**: Export a session inventory API from `internal/task` that returns parsed project, slug, kind, and legacy/current match status; use it from Nix through a `spore fleet list-sessions` command instead of shell grep.
**Suggested ticket title**: `centralize-worker-session-discovery` (effort: high).

### F-05: Coordinator state and inbox paths have multiple roots

**Class**: multi-defined-default | doc-code-drift | copy-paste
**Severity**: high (if this fires, coordinator tools can read one state tree while role docs and inbox delivery point at another)

**Locations**:
- `internal/task/state.go:46`
- `internal/task/state.go:59`
- `internal/coordinator/statepath.go:8`
- `internal/coordinator/slascan/slascan.go:53`
- `internal/coordinator/statedebt/statedebt.go:65`
- `internal/coordinator/failuresummary/failuresummary.go:69`
- `internal/worker/tokenmonitor/tokenmonitor.go:69`
- `bootstrap/coordinator/role.md:15`
- `bootstrap/coordinator/role.md:32`

**Concept**: Coordinator state, inboxes, ledgers, and role docs should all name the same root layout.
**Drift**: Code defaults the singleton coordinator root to `$XDG_STATE_HOME/spore/coordinator/<project>/inbox`, several coordinator packages duplicate that fallback with slightly different XDG handling, while the role doc tells the coordinator its inbox and state live under `$XDG_STATE_HOME/spore/<project>/coordinator/`.
**Consequence**: A booted coordinator can create or read `state.md` from a path that no delivery or scanner writes, losing memory and missing operator messages.
**Proposed consolidation**: Make `internal/coordinator/statepath` the only resolver, expose helpers for project inbox, state.md, and ledgers, and render the coordinator role path prose from that contract.
**Suggested ticket title**: `unify-coordinator-state-layout` (effort: medium).

### F-06: `spore coordinator boot` still uses skyhelm defaults

**Class**: doc-code-drift | multi-defined-default
**Severity**: medium (if this fires, a spore coordinator boot reads and caps the wrong state file because the defaults are from a consumer harness)

**Locations**:
- `internal/coordinator/boot/boot.go:1`
- `internal/coordinator/boot/boot.go:25`
- `internal/coordinator/boot/boot.go:56`
- `cmd/spore/coordinator_cmd.go:688`
- `cmd/spore/coordinator_cmd.go:690`
- `cmd/spore/coordinator_cmd.go:703`
- `internal/coordinator/statepath.go:8`

**Concept**: Coordinator boot should use spore coordinator naming and state defaults unless a consumer explicitly overrides them.
**Drift**: The boot package describes itself as a skyhelm bash port, defaults state to `~/.local/state/skyhelm`, and reads `SKYHELM_*` env vars, while the rest of spore coordinator state defaults to `~/.local/state/spore/coordinator`.
**Consequence**: Running the spore command without consumer env can inspect `skyhelm/state.md` instead of the spore coordinator state used by other commands.
**Proposed consolidation**: Rename the env surface to `SPORE_COORDINATOR_*`, keep `SKYHELM_*` only as explicit compatibility aliases, and source state defaults from `internal/coordinator/statepath`.
**Suggested ticket title**: `remove-skyhelm-defaults-from-spore-coordinator-boot` (effort: medium).

### F-07: Hook settings render, inject, and lint each own the schema

**Class**: parallel-impl | copy-paste
**Severity**: medium (if this fires, installed hooks and per-session hooks can drift while lint only checks one rendering path)

**Locations**:
- `bootstrap/scripts/hooks-render.sh:21`
- `bootstrap/scripts/hooks-render.sh:33`
- `bootstrap/scripts/hooks-render.sh:42`
- `internal/hooks/inject/inject.go:23`
- `internal/hooks/inject/inject.go:76`
- `internal/hooks/inject/inject.go:154`
- `cmd/spore/hooks_cmd.go:221`
- `internal/lints/hooksdrift.go:35`

**Concept**: Hook config JSON should have one schema, one renderer, and one merge rule.
**Drift**: The shell renderer uses `SPORE_HOOKS_CLAUDE_DIR` and requires both JSON inputs, spawn-time inject uses `SPORE_HOOKS_CONFIG` and `SPORE_SETTINGS_EXTRAS` and treats a missing hooks config as no-op, while CLI settings, inject, and hooks-drift each define their own `events` schema struct.
**Consequence**: A field or missing-file policy can be added to one path and not the others; `spore lint` can pass while spawned sessions receive different settings.
**Proposed consolidation**: Move the JSON schema, kind filter, extras merge, and missing-file policy into `internal/hooks/settings`; have the shell call a Go command that uses the same package.
**Suggested ticket title**: `centralize-hooks-settings-rendering` (effort: medium).

### F-08: `spore.toml` is parsed by many tiny parsers

**Class**: parallel-impl | redundant-abstraction
**Severity**: medium (if this fires, a valid config idiom can work in one section and fail in another)

**Locations**:
- `internal/align/align.go:237`
- `internal/fleet/fleet.go:288`
- `internal/fleet/fleet.go:313`
- `internal/fleet/workers_config.go:55`
- `internal/fleet/coordinator_config.go:61`
- `internal/matter/config.go:72`
- `internal/lints/config.go:49`
- `internal/lints/config.go:302`

**Concept**: Project configuration should be parsed once with one comment, quoting, scalar, and list policy.
**Drift**: Align and fleet max-workers parse integer-only subsets and strip comments at the first `#`, workers/coordinator parse quoted strings with a separate comment stripper, matter has another scalar/bool parser, and lints has list parsing plus another quote-aware comment stripper.
**Consequence**: New config keys need parser work in several packages, and quoting a value with `#` or using a list can behave differently by section.
**Proposed consolidation**: Add one internal TOML reader or adopt a real TOML library behind an internal package; each feature should decode its own section from that parsed document.
**Suggested ticket title**: `replace-tiny-spore-toml-parsers` (effort: high).

### F-09: Opencode worker selectors disagree with fleet liveness

**Class**: parallel-impl | copy-paste
**Severity**: medium (if this fires, an opencode stop command can target a different worker set than the liveness report)

**Locations**:
- `internal/opencode/fleetstop/fleetstop.go:118`
- `internal/opencode/fleetstop/fleetstop.go:143`
- `internal/opencode/liveness/liveness.go:142`
- `internal/opencode/liveness/liveness.go:172`
- `internal/fleet/livenessstatus.go:249`
- `internal/fleet/livenessstatus.go:269`

**Concept**: "active opencode worker on this host" should be a shared task selector.
**Drift**: Fleet stop scans markdown directly and checks raw `status == "active"` and `agent == "opencode"`, opencode liveness scans markdown again but also requires a worktree and `.wt/agent`, and fleet liveness uses `task.List` plus `task.IsActive` and recorded sessions.
**Consequence**: Stop, liveness, and fleet status can show different counts, especially when legacy statuses, missing worktrees, or recorded sessions are present.
**Proposed consolidation**: Add a shared selector over `frontmatter.Meta` with options for agent, host, canonical status, worktree presence, and agent-file confirmation.
**Suggested ticket title**: `share-active-worker-selectors` (effort: medium).

### F-10: Cutover auto-mint hand-parses scheduler keys

**Class**: parallel-impl | copy-paste
**Severity**: medium (if this fires, an idempotency check can miss an existing task and mint a duplicate)

**Locations**:
- `internal/fleet/automintcutover.go:280`
- `internal/fleet/automintcutover.go:343`
- `internal/task/frontmatter/frontmatter.go:47`
- `internal/task/frontmatter/frontmatter.go:107`
- `internal/lints/taskschedulercontext.go:99`
- `internal/scout/mint.go:310`

**Concept**: Task frontmatter keys, including scheduler keys, should be read by the shared frontmatter parser.
**Drift**: Auto-mint cutover scans `tasks/*.md` and extracts only literal `scheduler_key:` lines with a local scanner, while the frontmatter parser already preserves unknown keys in `Meta.Extra`; other lints read `m.Extra["scheduler_key"]`, and scout writes scheduler keys through `Extra`.
**Consequence**: Quoting, malformed fences, or future key normalization can make dedupe disagree with the task parser and create duplicate consume-spore tasks.
**Proposed consolidation**: Parse task files with `frontmatter.Parse` and read `Meta.Extra["scheduler_key"]`, or promote scheduler key to a first-class `Meta` field.
**Suggested ticket title**: `use-frontmatter-parser-for-scheduler-keys` (effort: medium).

### F-11: Worker mix example drifts from the generated default

**Class**: doc-code-drift | multi-defined-default
**Severity**: low (if this fires, new projects copy a different worker ratio than `spore init` creates)

**Locations**:
- `internal/initconfig/initconfig.go:50`
- `internal/initconfig/initconfig.go:56`
- `internal/initconfig/initconfig.go:137`
- `README.md:198`
- `README.md:213`
- `README.md:222`

**Concept**: The documented worker mix example should match the scaffolded default, or clearly say it is only an example.
**Drift**: `spore init` scaffolds `claude = 67` and `codex = 33`, while README's worker mix block shows `claude = 70` and `codex = 30` after saying the command scaffolds a documented default `spore.toml`.
**Consequence**: A project owner following docs and a project owner running `spore init` get different fleet mix policy without noticing.
**Proposed consolidation**: Generate the README snippet from `internal/initconfig.DefaultTOML`, or make README link to the generated template instead of carrying its own numbers.
**Suggested ticket title**: `sync-worker-mix-docs-with-init-template` (effort: medium).

### F-12: `spore task new --draft` is a dead flag

**Class**: dead-code | doc-code-drift
**Severity**: low (if this fires, callers can pass a flag that appears meaningful but is ignored)

**Locations**:
- `cmd/spore/task_cmd.go:64`
- `cmd/spore/task_cmd.go:467`
- `cmd/spore/task_cmd.go:511`
- `internal/task/status.go:5`
- `internal/task/status.go:16`

**Concept**: Task creation should expose only real status choices.
**Drift**: CLI usage advertises `--draft`, the flag parser accepts it with default true but discards the value, task creation always writes `Status: "draft"`, and the status package now aliases draft to backlog.
**Consequence**: `--draft=false` is accepted and ignored, and the flag keeps the old draft/backlog split alive in the public surface.
**Proposed consolidation**: Remove the flag, or replace it with one explicit status option that validates against the central FSM.
**Suggested ticket title**: `remove-dead-task-new-draft-flag` (effort: medium).

## Cross-cutting recommendations

- Put every limit behind two things: one resolver that owns defaults and env precedence, and enforcement at every entry point that can create work.
- Treat task status, task root, coordinator state, and tmux session names as kernel contracts, not per-caller strings.
- Prefer shared selectors over direct markdown scans; callers should ask for "active workers matching X" rather than reread frontmatter by hand.
- Generate docs snippets for defaults from the same package that writes config templates, or add drift tests that compare docs to templates.
- Replace same-shaped JSON/TOML/frontmatter readers with small internal packages before adding more config surface.
- The expected consumer checkout `/home/sky/projects/nix-config` was not present in this worktree host, so consumer-side follow-ons should be minted as `consume-spore-<slug>` tickets after this spore doc lands.
