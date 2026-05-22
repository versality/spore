**Status**: planned
**Priority**: high
**Branch**: TBD (work on main or a feature branch)
**Predecessor**: commits de00256..a0174de (the sandbox primitive ships,
this brief hardens, soaks, and opens it up for extension)

# Sandbox rover follow-ups

The four commits above land a working bwrap+tmux sandbox with the
HTTPS CONNECT proxy and a 12-probe red-team validator. The primitive
is correct under the agreed threat model (LLM-running-a-bad-command,
not nation-state pivot). This brief is the second pass: pay off the
debt before it ossifies, document what we built, soak the sandbox
into the existing rover-launch path so it stops being an opt-in side
binary, and design the extension surface so a downstream user adding
linear.app or a new rw mount does the obvious thing.

The threat model itself is settled. Do not re-litigate it.

## What ships

### 1. Tech-debt sweep (cmd/spore-rover/)

Files: main.go (210 LOC), policy.go, proxy.go, inside.go,
proxy_cmd.go, redteam.go, verdict.go, redteam_driver.go, tmux.go.

Concretes to fix, not exhaustive:

- main.go's subcommand dispatch is an inline string switch. Split
  into a small dispatch table or one function per subcommand so
  flag parsing for each lives next to the implementation.
- claudeStatePaths is hardcoded for claude. Generalize so codex
  and opencode rovers slot in: a `target` abstraction that names
  the binary plus its required rw paths.
- The launchCmd shell-script generation in main.go (background
  proxy + trap + bwrap) is the gnarliest code in the binary.
  Either extract it behind a clearly named helper with the
  pane-lifecycle invariant called out, or replace it with a
  small Go orchestrator that double-forks the proxy itself.
- The redteam summary-detection uses a >=2 occurrence heuristic
  to avoid the prompt's own example marker triggering completion.
  Replace with an offset capture: snapshot `len(transcript)`
  right after pasteIntoPane returns, only scan past that offset.
- The ANSI strip is a basic CSI regex. Probably fine; revisit if
  any escape style slips through.
- inside.go's signal-forwarding goroutine is boilerplate; consider
  a small helper in tmux.go or signal.go.
- Hardcoded 5-minute redteam timeout. Make it a flag.

Acceptance: `go test ./cmd/spore-rover/` green, `spore lint` green,
end-to-end `-redteam` still PASS with 12/12 probes.

### 2. Documentation

Author `docs/sandbox-rover.md` with:

- Threat model (three points, what is out of scope).
- Policy fields explained, one paragraph each: Worktree, Home,
  RW[], RO[], AllowHost[]. Each with one concrete example.
- The launch pipeline (host proxy + bwrap netns + inside shim +
  target binary), with a small diagram. The diagram should make
  it obvious why `--unshare-net` forces the unix-socket bridge.
- The red-team validator's interpretation rules: why T1.d and
  T4.a can report LEAKED inside while the verdict still passes
  (host-side hostChecks reconcile the tmpfs-overlay write).
- "Common gotchas" section: bwrap missing from PATH, direnv
  reload timing, the operator's worktree must be on a path that
  survives the tmpfs overlays.

Update `CLAUDE.md`'s source map to list `cmd/spore-rover/`.
Update `README.md` if the sandbox is a user-facing feature (and
it should be, eventually).

Acceptance: a reader who has never seen the sandbox can extend
the AllowHost list, run the red-team validator, and explain why
T4.a's LEAKED-inside still PASSes - without asking the operator
a single question.

### 3. Harness soak

Today `spore-rover` is a standalone binary. The actual rover
launch path is in `internal/task/` (worker mint, worktree, tmux
session start) and `internal/fleet/` (coordinator+workers
consuming the task queue). Find the one spot where the worker's
agent process is currently spawned and route it through the
sandbox launcher.

Investigation tasks (do these before writing code):

- `grep -rn 'claude\|codex\|opencode' internal/task/ internal/fleet/ internal/worker/`
  to find where the agent binary is currently exec'd.
- Read `internal/worker/` end to end. Look for where the tmux
  window is named and the command is set.
- Decide: invoke spore-rover as a binary, or import the package
  and call its launcher directly. The binary route is easier to
  ship and easier to disable per-rover; the import route avoids
  one fork. Prefer the binary unless the per-fork latency or the
  argv ergonomics force the import.

Acceptance: `spore task new <slug>` mints a worker whose claude
runs sandboxed by default. The operator can opt out per task via
frontmatter (`sandbox: false`) or a global flag. Existing tests
still pass.

### 4. Extension ergonomics

The model the operator wants is: "I just installed spore, my
rover needs to hit linear.app and read ~/.config/nvim - what do
I edit?" The answer must be one file, one obvious key, one
restart of the rover.

Shape (decide on details during implementation):

- A `[sandbox]` section in spore.toml (or a dedicated
  `sandbox.toml`) with `allow_hosts = [...]`, `rw = [...]`,
  `ro = [...]`. Merge order: project config beats user config
  beats hardcoded defaults; CLI flags beat everything.
- Per-rover override via task frontmatter:
  `sandbox: { allow_hosts: [linear.app] }`. Merge with project
  config.
- The CLI flags (-allow, -rw, -ro) stay as they are - they are
  the escape hatch for ad-hoc / debugging use.
- Document the precedence in docs/sandbox-rover.md and link to
  it from the validation error message that fires when a host
  is denied. (Make the proxy log line for a denied host include
  "add to [sandbox].allow_hosts to permit" or similar.)

Constraint: a coding agent reading the brief at top of a new
session should be able to extend the allowlist without grep'ing
the codebase. The config key name and the file location must be
discoverable from `spore --help` or `docs/sandbox-rover.md`.

Acceptance: adding `allow_hosts = ["linear.app"]` to spore.toml
and restarting the rover lets it reach linear.app, and a denied
host's proxy.log line tells the operator where to add it.

## Order

1. Tech-debt sweep (item 1). Smallest, lowest risk, unblocks
   everything downstream.
2. Documentation (item 2). Cheap once the code is settled, and
   item 4 needs the docs page to point at.
3. Extension ergonomics (item 4) before harness soak (item 3),
   because the soak needs to know the config shape.
4. Harness soak (item 3). Largest blast radius; do it last with
   the threat model and config shape locked.

## Out of scope

- Re-litigating the threat model. Username masking, mountinfo
  path scrubbing, kernel-grade containment, seccomp, Landlock,
  per-rover credential isolation - none of these are this brief.
- Replacing bwrap with another primitive.
- Rewriting the red-team probe set.
