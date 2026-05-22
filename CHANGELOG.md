# Changelog

## 0.9.0 - 2026-05-22

### Highlights

`spore-rover` lands: a bubblewrap-based sandbox launcher that wraps an LLM coding agent (claude, codex, or opencode) so a misbehaving prompt cannot reach the operator's dotfiles, sibling worktrees, or the open internet.

The primitive itself shipped during the previous week; v0.9.0 hardens the debt, documents the surface, opens it up for extension, and adds first-class targets for codex and opencode alongside claude.

### Threat model

What the sandbox defends against:

- **Filesystem write-allowlist.** The rover writes only inside its worktree and the small RW set listed in the policy. `~/.ssh`, `~/.bashrc`, sibling worktrees, system dotfiles - all inaccessible.
- **Filesystem read-deny.** `/etc/shadow`, `~/.ssh`, `/root`, and similar are not readable.
- **Network egress allowlist.** With any `-allow` flag set, bwrap runs the target under `--unshare-net` and the only network the agent sees is a loopback HTTPS CONNECT proxy that lets through the named SNIs.

Out of scope on purpose: username masking, kernel-grade containment, seccomp/Landlock, per-rover credential isolation, nation-state pivot. The threat modelled is "the LLM runs an unintended command", not "an attacker controls the LLM".

### Supported targets

`-target <name>` selects the agent. Three are registered:

| Target | Binary | RW state paths |
|---|---|---|
| `claude` (default) | `claude` | `~/.claude`, `~/.claude.json` |
| `codex` | `codex` | `~/.codex` |
| `opencode` | `opencode` | `~/.config/opencode`, `~/.local/share/opencode`, `~/.cache/opencode` |

Adding a new agent is a registry entry in `cmd/spore-rover/target.go`. Binary lookup chases `~/.nix-profile/bin/<name>` symlinks to their `/nix/store` target, so user-profile-only binaries survive the tmpfs `$HOME` mask.

### Two subcommands

- **`spore-rover`** (default) spawns a tmux window in the current session and runs the target inside the sandbox. Operator-attended path; also what `-redteam` uses to validate the primitive.
- **`spore-rover --exec ... -- <argv>`** wraps an arbitrary command in the same bwrap+proxy sandbox without spawning a tmux window. stdin/stdout/stderr pass through; exit code mirrors the child. This is the primitive the worker spawn path will wrap to soak the sandbox into every minted rover.

### Durable per-project policy

A new `[sandbox]` section in `spore.toml` (with an optional user override at `~/.config/spore/sandbox.toml`):

```toml
[sandbox]
allow_hosts = ["api.anthropic.com", "statsig.anthropic.com", "sentry.io"]
rw          = ["/home/sky/.config/nvim"]
ro          = ["/home/sky/notes"]
```

Precedence, weakest to strongest:

1. Compiled-in defaults (empty)
2. `~/.config/spore/sandbox.toml`
3. `<project>/spore.toml` `[sandbox]`
4. CLI flags

Each layer unions onto the previous with dedupe. Unknown keys are a hard error so typos surface loudly.

### Self-service discovery on denied egress

The proxy log line for a denied host now points at the config key the operator needs to extend:

```
deny CONNECT linear.app:443 (not in allowlist; add to
[sandbox].allow_hosts in spore.toml or pass -allow linear.app)
```

### 12-probe red-team validator

`spore-rover -redteam` plants three host-side canaries (sibling-worktree secret, the operator's real `~/.bashrc`, a `/tmp` escape file), pastes the 12 probes into the sandboxed claude pane, polls for the summary marker, and writes a verdict.

The verdict reconciles inside-view `LEAKED` reports on tmpfs-masked paths (T1.d writes to `/tmp`, T4.a writes to `~/.bashrc`) against host-side observation. The inside-view of a tmpfs is by design a lie; truth is what changes outside the sandbox.

`-redteam-timeout` (default `5m`) caps the wait.

### Documentation

`docs/sandbox-rover.md` is the operator-facing surface: threat model, policy fields with worked examples, configuration precedence, the host-proxy + unix-socket + `--unshare-net` + inside-shim launch pipeline (with an ASCII diagram of why the unix-socket bridge is necessary), the redteam interpretation rules including the T1.d/T4.a LEAKED-but-PASS reconciliation, and a "common gotchas" section.

The kernel source map (`rules/core/source-map.md`, rendered into `CLAUDE.md` and `AGENTS.md`) now lists `cmd/spore-rover/`.

### In progress

The worker spawn path in `internal/task/lifecycle.go` is not yet wrapping its agent command through `spore-rover --exec`. The remaining bucket-4 work (tracked in `docs/todo/sandbox-rover-followups.md`): add a default rw bind for the main repo's `.git/worktrees/<slug>/` so `git commit` from inside the sandbox works, plus the `sandbox: false` per-task opt-out via frontmatter.

## Unreleased

- New `spore search nix {packages|options} QUERY` subcommand. Queries
  the same Elasticsearch backend behind https://search.nixos.org so
  agents get fast, scriptable nixpkgs / NixOS-option lookup without
  evaluating the registry. Default output is TSV (attr/version/description
  for packages, name/type/description for options); `--json` swaps in a
  structured array. Flags: `-c CHANNEL` (default `unstable`), `-n SIZE`
  (default 10). Env overrides: `NIXOS_SEARCH_VERSION`,
  `NIXOS_SEARCH_USER`, `NIXOS_SEARCH_PASS`. Lifted from
  `nix-config:harness/search-nix.sh`; nix-config keeps a thin shim so
  the script keeps working.

- `spore install` now also drops the bundled harness shell scripts
  (`auto-commit-tasks.sh`, `hooks-render.sh`, `quiet-run.sh`,
  `report-main-worktree-dirty.sh`) into `<root>/harness/`. These
  generic-core wrappers were lifted out of nix-config so consumers
  pick them up via the spore binary instead of vendoring per-project.
  `internal/install.Install` gained a `destSubpath` parameter so the
  same walker handles both skill and script asset trees. `quiet-run`'s
  build-lock file/env renamed from `nix-config-build.lock` /
  `NIX_CONFIG_BUILD_LOCK_HELD` to `spore-build.lock` /
  `SPORE_BUILD_LOCK_HELD`; `hooks-render` reads the claude config dir
  from `$SPORE_HOOKS_CLAUDE_DIR` (default `<repo>/configs/claude`).
  Coordinator/operator binaries, the four scripts flagged for Go
  ports (`capture-command-signal`, `opencode-fleet-stop`,
  `opencode-worker-liveness`, `merge-integrity-audit`), and the
  consumer-coupled bundle (`sweep-tech-debt`, `archive-aged-maybes`,
  `self-care`, `notify-attention`, `block-sources-inference`,
  `look-desk`, `look-web.mjs`, `plan-first-enforcement`,
  `no-ask-via-tool`, `wt-merge-unblock`) are out of scope for this
  cut and tracked as separate lift drafts.

- Renamed the per-task inbox env var to `SPORE_TASK_INBOX` to drop a
  foreign prefix that had leaked in from a consumer harness. Spore
  sets and reads the new name only; consumers exporting the previous
  name for their own hooks must rename on their side. No
  backward-compat alias.

## 0.4.2 - 2026-05-06

- Fixed `spore coordinator start` lying about success when the agent
  binary failed to exec. `driverToBinary("claude")` returned the
  package name `claude-code` instead of the actual binary name
  `claude`, so the inner shell command died on `exec` and tmux tore
  the session down before any `has-session` check ever ran. Two
  changes: (1) "claude" now maps to `claude` (the kernel default
  fallback also moves to `claude`); (2) `EnsureCoordinator` now
  waits a short settle window after `tmux new-session -d` and
  re-checks the session, returning a real error if the spawn died.
- Synced `VERSION` with the git tag scheme (was stuck at `0.0.3`
  while tags advanced to `v0.4.x`). New `just release X.Y.Z` recipe
  validates clean+green, bumps `VERSION`, commits, and tags so the
  two stay in step from now on.
- Refactored `embed_test.go` to read `VERSION` at test time instead
  of hardcoding the expected string, so a release no longer requires
  a paired test edit.

## 0.0.3 - 2026-05-05

Spore 0.0.3 lands the universal coordinator entry point. "How do I
start the coordinator?" now has the same answer for every consumer:
`spore coordinator start`.

- Added `spore coordinator start [--wait] [--poll-sec N]`,
  `stop`, `restart`, and `status` over the existing fleet
  coordinator helpers. `start` is idempotent; `--wait` blocks
  until the session exits.
- Added the `[coordinator]` section in `spore.toml` with `driver`,
  `model`, and `brief` keys. Env vars still win; driver "claude"
  maps to `claude-code`, "codex" to `codex`, and any other value
  passes through (so a project can point at a launcher script).
- Injected `SPORE_COORDINATOR_PROVIDER` and
  `SPORE_COORDINATOR_MODEL` into the session env when configured,
  matching the bundled `spore-coordinator-launch` dispatch shape.
- `status` prints up/down, the configured driver/model/brief, and
  exits 3 when the coordinator session is down (0 when up).

## 0.0.2 - 2026-05-04

Spore 0.0.2 is a focused release: it can now run Codex-backed workers
as a first-class task option while keeping the
existing Claude Code path intact. Feedback and rough edges are welcome.

- Added Codex worker support through `agent: codex`, with task-level
  model and reasoning-effort selection.
- Kept mixed Claude Code and Codex workflows on the same task
  frontmatter, tmux worker sessions, inbox/tell protocol, and merge
  close path.
- Bumped the package and CLI version to `0.0.2` and added
  `spore version`.
- Smoothed the README opening flow by removing the "Choose a Path"
  table.
