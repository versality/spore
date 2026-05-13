**Status**: spec
**Priority**: critical
**Owner**: TBD (spec only; implementation split into M1-M3 follow-ups)
**Source ticket**: tasks/rower-lifecycle-fsm-design.md

# Rower lifecycle FSM

One spore rower (one task, one tmux session, one wt/<slug> branch)
moves through a small set of well-defined states from draft to done.
Today the state lives implicitly across `tasks/<slug>.md`
frontmatter, the tmux session list, the worktree, the wt branch, and
the inbox - and the transitions between them are policed by prose
rules. This spec collapses the implicit FSM into one explicit shape
and names the enforcement (lint, hook, runtime) for each invariant.

The spec does not implement anything. It defines the shape and lists
the follow-up rowers (M1-M3) that will land enforcement piece by
piece without a big-bang flip.

## 1. Scope

In scope:

- The lifecycle of one rower from `draft` through `done`, including
  paused / parked / blocked detours.
- Observable substates of `active` that drive the operator and the
  fleet (idle, blocked-on-permission, idle-with-unmerged-commits,
  merging, ...).
- Invariants the harness must hold across (frontmatter, tmux, git
  branch, worktree, inbox).
- The first concrete enforcement (Stop hook) and the next two
  follow-ups.

Out of scope:

- Queue scheduling and dispatcher selection (which task runs next,
  how many workers).
- Matter sync (issue tracker -> task file).
- Fleet kill-switch (`fleet enabled`) toggling.
- Coordinator-singleton lifecycle. This spec is per-rower only.

## 2. Vocabulary: macro state vs micro substate

The single load-bearing design call: split rower state into two
layers and route every concern to exactly one of them.

**Macro state**

- Lives in `tasks/<slug>.md` frontmatter (`status:` field).
- Persistent. Survives reboots, fleet disables, tmux restarts.
- Six values, total. The alphabet is closed; new values require
  a spec amendment.
- Mutated only through typed entry points in `internal/task`
  (`Start`, `Pause`, `Park`, `Block`, `Done`, `Merge`).

**Micro substate**

- Computed at runtime from `(tmux pane signal, branch state, inbox
  state, wake-pending marker)`.
- Never written to frontmatter. There is no migration story for a
  micro substate label, no rule that drifts when the classifier
  improves, no second source of truth.
- Lifted into UI by the fleet status reporter and into hook
  decisions by the Stop hook.
- The classifier is the single function that answers "what micro
  substate is this rower in right now?" - today that is
  `internal/fleet/liveness.classifyAgentPane` (extended).

This split mirrors the one already done in `internal/task/status.go`
(canonical macro alphabet) and `internal/fleet/liveness.go` (runtime
classifier producing live/idle/dead/zombie/unknown). The spec just
makes the split explicit and re-uses both.

## 3. Prior art

**Temporal workflow lifecycle** (`Running`, `ContinuedAsNew`,
`Completed`, `Failed`, `Canceled`, `Terminated`, `TimedOut`).

- Adopt: the closed-alphabet macro state stored as the source of
  truth, with separate observable conditions on top. Adopt
  `Canceled` vs `Failed` separation - the operator-pause vs
  blocked-by-environment distinction maps cleanly.
- Reject: history-replay, deterministic-replay engine. Overkill;
  spore rowers are not durable workflows.

**Kubernetes Pod phase + container conditions** (`Pending`,
`Running`, `Succeeded`, `Failed`, `Unknown` + a separate
`conditions` vector like `Ready`, `ContainersReady`,
`PodScheduled`).

- Adopt: macro phase is a small set; conditions vector is open and
  observable. Spore's micro substate maps directly to conditions.
- Reject: the controller-loop reconciler shape. Spore's `fleet
  reconcile` is one-shot by design and that is the right call here.

Both references confirm the macro/micro split. Neither is imported
wholesale.

## 4. Macro states

Six values. Names match `internal/task/status.go` constants exactly.

| State     | Meaning                                                   | Flip in (from)                                  | Flip out (to)                                   | File / process side effects                                                                       | Scheduler / replenish                              |
| --------- | --------------------------------------------------------- | ----------------------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| `draft`   | Brief written, not yet picked. No worktree, no branch.    | (none; created here)                            | `active`                                        | None.                                                                                              | Eligible for pickup.                                |
| `active`  | Rower owns the brief. Worktree + branch + tmux exist.     | `draft`, `paused`, `parked`, `blocked`          | `paused`, `parked`, `blocked`, `done`           | Worktree at `.worktrees/<slug>`, branch `wt/<slug>`, tmux session per `tmuxSessionName`.          | Counted against MaxWorkers.                         |
| `paused`  | Operator-suspended in-flight. Worktree kept; intent: "I will come back to this".   | `active` (operator command)                     | `active` (operator resume)                      | Worktree retained. Tmux reaped only if idle past `IdleReapThreshold` (`reapIdleSlugSessions`).    | Skipped. Not counted against MaxWorkers.            |
| `parked`  | Waits-for-named-trigger or external dependency. Worktree kept; intent: "wake when X happens". | `active` (rower or operator declares trigger)   | `active` (named trigger fires)                  | Worktree retained. Tmux reaped on idle, same policy as `paused`.                                  | Read by scheduler (trigger watcher); replenish skips. |
| `blocked` | Rower hit an environment / dependency wall. Worktree kept; intent: "I cannot proceed". | `active` (rower self-flip on permanent block)   | `active` (operator unblock), `done` (giving up) | Worktree retained. Tmux reaped on idle, same policy as `paused`.                                  | Skipped; surfaced in fleet status as a red row.     |
| `done`    | Unit shipped (merged) or explicitly killed. Terminal.     | `active` via `Merge`; `active` via `Done` (force) | (terminal)                                      | Worktree removed, branch deleted, tmux killed (`killAllSlugSessions`).                            | Ignored.                                           |

Notes:

- `paused` vs `parked` is operator-distinct and intentional. `paused`
  is "human took the wheel"; `parked` is "machine waits on a named
  signal". Keep both. The scheduler only reads `parked`.
- `blocked` is distinct from `parked`: `blocked` means the rower has
  no path forward without operator help; `parked` means the rower
  knows what it is waiting for and can resume autonomously.
- `done` is terminal. There is no `archived`. Archival is a
  filesystem concern (move out of `tasks/`), not a state.

The `draft` value here corresponds to the brief state pre-`Start`.
Today `internal/task/status.go` aliases `draft` to `backlog`; this
spec retains `draft` as the user-facing macro name and treats
`backlog` as the legacy alias. No code change is required for the
spec to land; M2's invariant check uses the canonical form.

## 5. Micro substates (computed)

Micro substates apply only when the macro state is `active`. They
are computed on demand and never persisted. The classifier function
is `internal/fleet/liveness.classifyAgentPane` (today returning
`running` / `idle` / `dead` / `zombie` / `unknown`), extended to
distinguish a small set of operator-relevant cases.

The signal sources are fixed:

- **T**: tmux pane (`capture-pane -p`, the existing classifier).
- **G**: git state on `wt/<slug>` (`rev-list main..wt/<slug>`,
  `status --porcelain`, `rebase-merge`/`MERGE_HEAD` markers).
- **I**: inbox (unread `*.json` count via `CountUnreadInbox`).
- **W**: wake-pending marker (`internal/fleet/wakepending.go`).

| Substate                    | Signal                                                         | Operator meaning                                  | Default policy                                                  |
| --------------------------- | -------------------------------------------------------------- | ------------------------------------------------- | --------------------------------------------------------------- |
| `active.spawning`           | T: pane just created, no mode line yet.                        | Tmux is up; the agent is starting.                | Wait. No alert.                                                  |
| `active.working`            | T: classifier returns `running` (busy markers present).        | Doing the work.                                   | Wait. No alert.                                                  |
| `active.idle`               | T: classifier returns `idle`. G: clean tree, no unmerged.      | At an empty prompt; nothing to do.                | Send next-step poke or move to `parked`.                         |
| `active.idle-unmerged`      | T: idle. G: clean tree, `rev-list main..wt/<slug>` > 0.        | Work shipped to a commit, not merged.             | **Stop hook fires (M1)**: tells the rower to run `wt merge`.     |
| `active.idle-unread`        | T: idle. I: unread > 0. W: no fresh marker.                    | A poke arrived; the rower has not woken to read.  | Fleet status surfaces; auto-wake (existing wakepending path).    |
| `active.merging`            | G: `MERGE_HEAD` or `rebase-merge` present.                     | Merge in flight; do not interrupt.                | Wait. No Stop-hook firing here.                                  |
| `active.blocked-permission` | T: pane shows a permission-prompt dialog (driver-specific regex). | Rower cannot proceed; harness-level wedge.        | Surface in fleet status; operator action required.              |
| `active.blocked-operator`   | T: idle. G: any. The brief has a question for the operator (heuristic: rower posted `wt ask`). | Rower is awaiting an operator answer.             | Surface in fleet status; do not auto-poke.                      |
| `active.dead`               | T: pane dead or zombie shell.                                  | Agent process gone.                                | Reconciler respawns; or operator marks `blocked`.               |

Substates are **observed**, not declared. There is no rower call
"please mark me idle-unmerged"; the classifier reads the world.

## 6. Transitions

One row per legal edge. Triggers, preconditions, side effects, and
the function that owns the edge.

| #   | From         | To         | Trigger                                                  | Precondition                                                  | Side effect                                                                          | Owner                                              |
| --- | ------------ | ---------- | -------------------------------------------------------- | ------------------------------------------------------------- | ------------------------------------------------------------------------------------ | -------------------------------------------------- |
| E1  | (none)       | `draft`    | Brief authored                                           | `tasks/<slug>.md` valid frontmatter                           | File on disk                                                                          | Author / matter sync                                |
| E2  | `draft`      | `active`   | `spore task start <slug>` or scheduler pickup            | None                                                          | Create branch + worktree + tmux session (`task.Start`)                               | `task.Start`                                        |
| E3  | `active`     | `paused`   | Operator command `spore task pause <slug>`               | Inbox empty (`inboxGate`)                                     | `flipStatus` to paused; `reapIdleSlugSessions`                                       | `task.Pause`                                        |
| E4  | `paused`     | `active`   | Operator command `spore task resume <slug>`              | None                                                          | Ensure session (`task.Ensure`); fresh agent if reaped                                | `task.Ensure` via resume command                    |
| E5  | `active`     | `parked`   | Rower or operator declares a named trigger               | Inbox empty                                                   | `flipStatus` to parked; record trigger in frontmatter (`trigger:` field, see sec 11) | `task.Park` (extend to take trigger name)           |
| E6  | `parked`     | `active`   | Named trigger fires (scheduler or operator)              | Trigger condition met                                         | Ensure session                                                                       | Scheduler / `task.Ensure`                           |
| E7  | `active`     | `blocked`  | Rower self-flip or operator command                      | Inbox empty                                                   | `flipStatus` to blocked; `reapIdleSlugSessions`; surface in fleet status              | `task.Block`                                        |
| E8  | `blocked`    | `active`   | Operator unblock                                         | None                                                          | Ensure session                                                                       | `task.Ensure` via resume command                    |
| E9  | `active`     | `done`     | `spore task merge <slug>`                                | On main, ff-only possible, `just check` green or forced       | Fast-forward main, push, close task, kill tmux, drop worktree+branch                 | `task.Merge`                                        |
| E10 | `active`     | `done`     | `spore task done <slug> --force`                         | Bypasses inbox + unmerged gates (logs override)               | Status flip + cleanup (`task.Done`)                                                  | `task.Done` (force)                                 |
| E11 | `blocked`    | `done`     | Operator gives up: `spore task done <slug> --force`      | Same as E10                                                   | Same as E10                                                                          | `task.Done` (force)                                 |

Illegal direct edges (must transit through `active`):

- `paused -/-> parked`, `parked -/-> paused`. The intent change
  must be explicit.
- `paused -/-> blocked`, `parked -/-> blocked`. Same reason.
- `paused -/-> done`, `parked -/-> done`, `blocked -/-> done`
  except via E10 / E11 (force, with cleanup).
- `done -/-> *`. Terminal.

Substate transitions are NOT in this table. Substates are computed,
not triggered.

## 7. Invariants

Numbered, falsifiable, each with a check site. The follow-up rowers
land the missing checks.

- **I1.** No two live tmux sessions per slug.
  - Check site: `internal/task/lifecycle.matchingSlugSessions`
    (already enumerates dups). Promote to a fleet-status warning
    row in M2.
- **I2.** No `done` flip with unmerged commits on `wt/<slug>`,
  except via `--force` (which logs an override).
  - Check site: `task.Done` (`UnmergedCommits` gate, already
    enforced).
- **I3.** No `active` task without a live tmux session, OR a
  reconciler-acknowledged wake-pending marker within TTL.
  - Check site: fleet reconciler + `wakepending`. Surface
    violations in `fleet status` (M2).
- **I4.** Macro state ascends only along edges in section 6. The
  alias table in `status.go` is the single source.
  - Check site: every `flipStatus` call (already centralized in
    `lifecycle.go`); add a debug-build assertion in M2 that
    refuses unknown `to` values.
- **I5.** Micro substate is read-only. No code path writes a
  substate string anywhere on disk.
  - Check site: lint that greps for hard-coded substate names
    being assigned into frontmatter; M2 adds it.
- **I6.** Unmerged commits on `wt/<slug>` + clean tree + observed
  idle => Stop hook fires (M1). The rower receives a
  deterministic prompt to merge or to declare next step.
  - Check site: `claude-wt-merge-mechanical` Stop hook (M1).
- **I7.** A `parked` task records the trigger that will move it
  back to `active` (frontmatter `trigger:` field). No `parked`
  without a trigger.
  - Check site: `task.Park` (extend to require trigger arg); M3
    or earlier as the scheduler grows.

Invariants I1, I3 are the "no orphan tmux" pair. I2, I6 are the
"no unmerged work lost" pair. I4, I5 are the "macro/micro split
holds" pair. I7 is the "parked is real, not a parking lot" guard.

## 8. Mapping to observed failure modes

Each row of the motivation gets a today-vs-FSM column and the
follow-up rower that closes it.

| Failure                          | Today: macro state  | Today: micro signal                              | FSM macro     | FSM micro                  | New enforcement                                | Closed by |
| -------------------------------- | ------------------- | ------------------------------------------------ | ------------- | -------------------------- | ---------------------------------------------- | --------- |
| Commit-vs-merge gap              | `active`            | invisible (idle pane after a commit)             | `active`      | `active.idle-unmerged`     | Stop hook (I6)                                  | M1        |
| Perm-mode regression             | `active`            | invisible until classifier extension              | `active`      | `active.blocked-permission`| Classifier extension; fleet-status row (I3)     | M2        |
| Auto-flip duplicate session      | `active`            | two sessions, one slug                            | `active`      | n/a                         | Fleet-status invariant check (I1)              | M2        |
| Session-naming dup               | `active`            | orphan named sessions after pause/resume          | `active`      | n/a                         | Fleet-status invariant check (I1)              | M2        |
| Prompt-blocking despite --danger | `active`            | dialog text in pane; rower wedged                 | `active`      | `active.blocked-permission`| Same as perm-mode regression                    | M2        |

The first two columns describe today; the next two describe what
the FSM names. The "new enforcement" column is the check site that
catches the failure once the FSM is in. Without the FSM, each
failure has been patched with a one-off (host config, prose rule,
ad-hoc fleet logic). With the FSM, each failure has a named
substate or invariant that catches it the same way.

## 9. First hook: claude-wt-merge-mechanical

Closes I6 / the commit-vs-merge gap. Single Go binary, single Stop
hook, no per-driver branching in the decision logic.

### Location

- Binary: `internal/hooks/wtmergemechanical/wtmergemechanical.go`
  (new package; mirrors `internal/hooks/notifycoordinator.go` and
  `internal/hooks/watchinbox.go` shape).
- Wired as a Stop hook into the rower's claude settings via
  `bootstrap/skills/spore-rower-perm-default` (or its successor).
  Codex / opencode parity is M3 (sec 11).

### Inputs

Read from the hook envelope and the environment:

- `Request.CWD` (the worktree where the rower runs).
- `SPORE_TASK_SLUG` (env, set on every spawn by `ensureSession`).
- `SPORE_PROJECT_ROOT` (env, set on every spawn).
- `Request.SessionID` (for logs).
- Tmux session name from `tmuxSessionName` for capture-pane.

Read from the world:

- `git -C <projectRoot> rev-list main..wt/<slug>` for unmerged count.
- `git -C <worktree> status --porcelain` for clean-tree check.
- `git -C <worktree> rev-parse --git-dir` to see if `MERGE_HEAD`
  or `rebase-merge` is present (mid-merge / mid-rebase guard).
- `internal/fleet/liveness.classifyAgentPane` (or its lifted-out
  helper, see M1) to read the rower's own pane and decide if it
  is observed-idle.

### Decision boundary

Fires only when ALL of:

1. `unmerged := UnmergedCommits(projectRoot, "wt/<slug>") > 0`.
2. Working tree clean (`git status --porcelain` empty).
3. Not mid-merge / mid-rebase (`MERGE_HEAD` absent,
   `rebase-merge` absent).
4. Pane substate is `idle` per `classifyAgentPane`. Mid-unit
   work between commits classifies as `running` and the hook
   exits 0; this is the load-bearing distinction that prevents
   the hook from blocking real progress.

If any of (1)-(4) is false: exit 0 (allow Stop, no message).

If all true: exit 2 with this message on stderr (deterministic,
no template variables beyond N and slug):

```
Branch wt/<slug> has N unmerged commit(s), a clean tree, and you
have stopped. Run `wt merge` to ship the unit; or, if the unit is
not done, write the next step and continue.
```

Exit 2 is the claude Stop-hook protocol for "block stop and feed
the message back to the agent as a prompt".

### Why a Go program

- `no-new-bash` lint refuses new shell scripts in the hook surface.
- The hook ships in the spore CLI binary; no extra install step,
  no PATH dependency.
- It can call directly into `internal/task.UnmergedCommits` and
  `internal/fleet/liveness.classifyAgentPane`, so the decision
  boundary stays in one place.
- Easy to unit-test against a fake repo + a fake `tmuxRunner`
  (the seam is already in `liveness.go`).

### Out of scope for the hook

- Does NOT actually run `wt merge`. The rower runs it, or refuses
  and writes the next step.
- Does NOT distinguish "unit-end commit" from "intermediate
  commit on a multi-step unit". That distinction is captured by
  the rower's behavior: if the rower is still working, the
  classifier sees `running` and the hook does not fire. If the
  rower stops mid-unit on purpose (e.g. brief is open-ended), the
  rower writes the next step and continues; the hook fires once
  more on the next stop, which is the desired pressure.
- Does NOT touch frontmatter. Macro state stays where it is
  (`active`), and the substate is computed.

## 10. Migration path

Land enforcement piece by piece. No big-bang flip. Each step has
its own slug for a follow-up rower.

### M1: claude-wt-merge-mechanical (cheapest, lands first)

- Slug: `spore-claude-wt-merge-mechanical`.
- Build the Stop hook binary per sec 9.
- If `internal/fleet/liveness.classifyAgentPane` is not callable
  from `internal/hooks` (cyclic import or unexported helpers),
  M1 also lifts the classifier into a small leaf package both can
  import. No behavior change; pure refactor.
- Wire into claude rower settings via the existing perm-default
  skill or a sibling skill.
- Test: unit tests against a fake repo + fake tmux capture; end-
  to-end test on this very project (rower commits, stops, hook
  fires, rower runs `wt merge`, hook does not fire again).
- No frontmatter changes, no rule shuffling.

### M2: fleet status invariant check

- Slug: `spore-fleet-status-invariants`.
- Extend `internal/fleet/livenessstatus.go` to emit warning rows
  for I1 (dup tmux per slug) and I3 (active task without live
  session and no fresh wake-pending). Reuses
  `matchingSlugSessions` and the wake-pending TTL machinery
  already present.
- Add a debug-build assertion in `flipStatus` that the `to` value
  is one of the six canonical macro states (I4).
- Add a lint that refuses substate strings being written into
  frontmatter (I5).
- Add the classifier extension for `active.blocked-permission`
  (driver-specific regex; for claude, the perm-prompt dialog).

### M3: codex / opencode parity for the Stop hook

- Slug: `spore-fsm-driver-parity`.
- Wire the same hook binary into codex (`~/.codex/hooks.json` and
  the project equivalent) and opencode. Decision logic is
  unchanged; only the install layer branches.
- Extend the classifier with codex-specific and opencode-specific
  perm-prompt regex if needed (driver-specific only at the
  classifier).

### Order

M1 first because it closes the biggest visible failure (commit-
vs-merge gap) with the smallest blast radius (one Go binary, one
hook wiring). M2 next because it makes the rest of the failure
modes visible in `fleet status` without changing rower behavior.
M3 last because it requires per-driver install testing and the
payoff per driver is incremental, not cliff-edge.

There is **no** alias-collapse step for `parked -> paused`. The
two macro states stay distinct per sec 4.

## 11. Driver universality

Every transition in sec 6 works for claude, codex, and opencode
without per-driver branching. The macro state machine, the
invariants, and the migration path are driver-agnostic.

Branching is unavoidable in exactly two places, and both are
isolated:

1. **Hook install path**. Claude reads
   `.claude/settings.json`; codex reads `~/.codex/hooks.json` (and
   a project equivalent); opencode reads its own hook surface
   (TBD; flagged for M3 research). The hook binary is the same;
   only the install file differs. Justified: each driver owns its
   own settings format, and there is no upstream proposal to
   unify. The bootstrap composer already emits per-driver settings
   files, so this branching lives in the composer layer.
2. **Classifier regex / busy markers**. The TUI of each driver
   shows different "I am thinking" indicators:
   - claude: `esc to interrupt`, `Cogitating… (Ns ...)`,
     `-- INSERT --` mode line.
   - codex: `• Working (`, `• Waiting for background terminal`,
     `› ` / `> ` prompt.
   - opencode: `esc to interrupt`, `ctrl+c to interrupt`,
     `Thinking`, `>` / `│ >` prompt.

   The classifier already branches on driver name in
   `classifyAgentPane`. Justified: there is no protocol contract;
   each TUI's pane output is what it is. The branching is a
   single switch statement and stays inside `liveness.go`.

The decision boundary of the Stop hook (sec 9) does NOT branch on
driver. It calls `classifyAgentPane` with the agent name and
treats the returned substate uniformly. A new driver requires only
a new case in the classifier and a new entry in the install layer;
the FSM, invariants, and hook decision logic do not change.

## 12. What this spec deliberately does NOT do

- Does not introduce a new persistent substate alphabet. Substates
  are computed.
- Does not collapse `parked` and `paused`.
- Does not mandate a daemon or a controller loop. The reconciler
  stays one-shot.
- Does not replace any existing `task.*` entry point. Every
  transition in sec 6 names an existing function (or a small,
  named extension).
- Does not specify the `trigger:` frontmatter field's full schema
  (sec 7 I7). That is left to the scheduler-growth ticket and is
  out of scope here.

## 13. Done = follow-up rowers can start

This spec is done when:

- M1 (`spore-claude-wt-merge-mechanical`) can be cut as a brief
  with no further design questions: sections 5, 9, and 11 are
  enough to implement against.
- M2 (`spore-fleet-status-invariants`) can be cut: sections 5, 7,
  and 8 enumerate the invariants and the substates the status
  rows surface.
- M3 (`spore-fsm-driver-parity`) can be cut: section 11 names the
  install paths and the classifier extension points.

No source edits land with this spec. Implementation is the
follow-ups.
