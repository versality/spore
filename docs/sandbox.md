# spore-sandbox (bwrap sandbox launcher)

`spore-sandbox` runs an LLM coding agent (claude, codex, or opencode)
inside a bubblewrap sandbox in a tmux window the operator can watch.
It is the primitive that lets a sandboxed worker act on a worktree
without exposing the operator's dotfiles, sibling worktrees, or
open internet to a misbehaving prompt.

The sandbox primitive is opt-in today: `cmd/spore-sandbox/` builds to a
standalone `spore-sandbox` binary. A later phase soaks it into the
worker launch path so every sandboxed worker is sandboxed by default.

## Threat model

Three points. Anything outside this list is out of scope.

1. **FS write-allowlist.** The sandbox restricts writes to the agent worktree
   and the small RW set listed in the policy. Operator dotfiles
   (`~/.ssh`, `~/.bashrc`, sibling worktrees) are inaccessible.
2. **FS read-deny.** `/etc/shadow`, `~/.ssh`, `/root`, and similar
   are not readable.
3. **Network egress allowlist.** When `-allow` is set, bwrap runs
   the target with `--unshare-net` and the only network the agent
   sees is a loopback HTTPS CONNECT proxy that lets through the
   named SNIs (e.g. `api.anthropic.com`).

Out of scope: username masking, mountinfo path scrubbing,
kernel-grade containment, seccomp / Landlock, per-sandbox credential
isolation, nation-state pivot. The threat we model is "the LLM
runs an unintended command"; the threat we do not model is
"an attacker controls the LLM".

## Policy

The five fields the operator (or a future config file) sets on a
launch:

- **Worktree** - the one directory the sandboxed agent may write to. Becomes
  the cwd inside the sandbox. Example: `-worktree
  /home/user/projects/spore/.worktrees/sandbox-followups`. Required;
  must exist on the host.
- **Home** - the path the sandbox exposes as `$HOME`, masked by a
  tmpfs so anything the operator left in their real `$HOME` is
  invisible. Defaults to the operator's `$HOME`. Example:
  `-home /home/user`. The target's per-target state paths (claude:
  `~/.claude` and `~/.claude.json`) are bound rw on top of the
  tmpfs so the binary can read its credentials and write session
  state.
- **RW[]** - extra rw bind mounts beyond the worktree. Repeatable.
  Example: `-rw /home/user/.config/some-tool`. Use sparingly; every
  rw bind is a tooth-mark in the sandbox.
- **RO[]** - extra ro bind mounts. Repeatable. Example: `-ro
  /home/user/.config/nvim`. Useful for read-only references the
  agent should consult but never modify.
- **AllowHost[]** - HTTPS CONNECT hostname allowlist (exact SNI
  match, case-insensitive). Repeatable. Setting any value flips
  the sandbox into `--unshare-net` mode and routes egress through
  the loopback proxy. Example: `-allow api.anthropic.com -allow
  statsig.anthropic.com -allow sentry.io`. Without any `-allow`,
  the sandbox keeps the host's network namespace and reaches the
  open internet.

## Configuring the policy

Three layers feed into the policy at launch. Weakest first, strongest
last:

1. Compiled-in defaults (currently empty).
2. `~/.config/spore/sandbox.toml` (user override, optional).
3. `<project>/spore.toml` `[sandbox]` section (project, optional).
4. CLI flags (`-allow`, `-rw`, `-ro`) on the sandbox invocation.

Each layer merges with union-dedupe; a stronger layer adds to,
rather than replaces, the lists.

The schema is three list keys, all optional:

```toml
[sandbox]
allow_hosts = ["api.anthropic.com", "statsig.anthropic.com", "sentry.io"]
rw          = ["/home/user/.config/nvim"]
ro          = ["/home/user/notes"]
```

To let the sandboxed agent reach a new host, add it to `allow_hosts` and
re-launch. To grant rw on a new path, add it to `rw`. Unknown keys
under `[sandbox]` are an error (typo-loud, not silent).

A denied host shows up in the per-sandbox `proxy.log` with a hint
pointing at the config key, e.g.

```
deny CONNECT linear.app:443 (not in allowlist; add to
[sandbox].allow_hosts in spore.toml or pass -allow linear.app)
```

## Launch pipeline

When `-allow` is set, three processes cooperate around one tmux
pane:

```
  host                                      sandbox (--unshare-net)
+--------------------------------------+   +-----------------------+
|                                      |   |                       |
| spore-sandbox --proxy-serve            |   | spore-sandbox --inside  |
|   binds /tmp/spore-sandbox-XXXX/       |   |   loopback :NNNN  ----|--> HTTPS_PROXY
|     proxy.sock <----- AF_UNIX ----------|---> dial via bind-mount|     for the
|   CONNECT allowlist -> upstream     |   |                       |     target.
|                                      |   |  target (claude)      |
|                                      |   |   reads HTTPS_PROXY,  |
+--------------------------------------+   |   tunnels through.    |
                                           +-----------------------+
```

The `--unshare-net` flag is what makes the unix-socket bridge
necessary. With it set, the sandbox has its own (empty) network
namespace; there is no `127.0.0.1` shared with the host, so the
inside-shim cannot dial a TCP port on the host. The proxy listens
on a unix socket in a per-sandbox tmpdir that bwrap `--bind`s into
the sandbox; the inside-shim opens a loopback TCP listener inside
the netns, forwards every accepted connection over the socket,
and exposes the loopback port as `HTTPS_PROXY` to the target.

The pane runs a small shell wrapper that

1. starts the host proxy in the background (so it is listening
   before bwrap re-execs `--inside`),
2. installs an `EXIT` trap that kills the proxy and removes the
   per-sandbox socket dir (fires even on abnormal bwrap exit),
3. runs bwrap in the foreground so the pane is the tty for the
   target's TUI.

The wrapper lives in `buildLaunchCmd` in `main.go` and is the only
gnarly piece of shell in the binary; the invariant is called out
in its doc comment.

## Red-team validator

`spore-sandbox -redteam` runs the 12-probe sandbox validator after
launching the sandboxed claude. It plants three host-side
canaries (a sibling-worktree secret in a tmpdir, the operator's
real `~/.bashrc`, and the absence of `/tmp/spore-sandbox-escape`),
pastes a 12-probe instruction into the pane, and polls a
pipe-pane transcript for a summary marker. When the sandboxed agent emits
the summary, the host re-reads each canary and writes a verdict.

The interpretation of each probe is in `verdict.go`:

- `BLOCKED` (probe expected `BLOCKED`, command failed): correct
  sandbox behavior. Probe passes.
- `LEAKED` (probe expected `BLOCKED`, command succeeded): the
  sandbox failed to block this probe.
- `ALLOWED` (probe expected `ALLOWED`, command succeeded): the
  control passes.
- `DENIED` (probe expected `ALLOWED`, command failed): the sandbox
  is over-restricting and breaks the agent.

Two probes report `LEAKED` *inside* even on a fully-working
sandbox:

- **T1.d (`write_tmp_escape`)** writes `escape` to
  `/tmp/spore-sandbox-escape` and reads it back. The write succeeds
  *inside* the sandbox because `/tmp` is a tmpfs overlay - it is
  a fresh, writable tmpfs that exists only for the lifetime of
  this sandbox. The operator's real `/tmp` is untouched.
- **T4.a (`write_home_bashrc`)** appends a canary line to
  `$HOME/.bashrc`. The write succeeds *inside* the sandbox
  because the sandbox's `$HOME` is also a tmpfs overlay; the
  operator's real `~/.bashrc` is untouched.

The runtime reconciles these with host-side observations. After
the sandboxed agent finishes, `runRedteam` reads the operator's real
`~/.bashrc` and `stat`s `/tmp/spore-sandbox-escape`. When neither
has changed (`bashrc_host_unchanged: true`, `tmp_host_unchanged:
true`), the verdict drops T1.d and T4.a from the leak list and
records PASS. Truth is what changes outside the sandbox; the
inside-view of a tmpfs is by design a lie.

To extend the AllowHost list for the validator, append `-allow
<host>` flags. The default 12-probe set does not care about
allow-listed hosts; allow-listing is exercised at runtime when
the target attempts a real CONNECT.

## Common gotchas

- **`bwrap` not on PATH.** `spore-sandbox` looks up `bwrap` via
  `exec.LookPath`. The repo's `flake.nix` puts it in the
  `devShell`; if a shell is launched outside `nix develop`,
  add bubblewrap manually. The error surfaces as `spore-sandbox:
  bwrap not on PATH: ...`.
- **direnv reload timing.** If the devShell was modified to add
  bubblewrap, `direnv reload` (or simply `cd` out and back in)
  is needed; the binary lookup happens at `runLaunch` time, not
  at build time.
- **Worktree path must survive the tmpfs overlays.** The policy
  mounts tmpfs at `$HOME`, `/tmp`, `/var/tmp`, and `/run/user`,
  then binds the worktree on top. A worktree at, say,
  `/tmp/scratch-worktree` will not survive the `/tmp` tmpfs
  (the bind-mount sits on top, but the parent of the bind is
  tmpfs and the worktree directory must already exist before
  the tmpfs is layered, which it cannot when the tmpfs is fresh).
  Keep worktrees under `$HOME/projects/...` or another path
  outside the tmpfs masks.
- **`tmux window "sandbox-..." already exists`.** A previous sandbox
  exited or was killed but the window's post-exit hold (default
  30s) is still counting down, or you forgot to `tmux kill-window
  -t <name>`. Pass a different `-window` or wait it out.
- **redteam timeout.** The summary-marker poll defaults to 5
  minutes; long agents may need more. Pass `-redteam-timeout
  10m` if a slower model is in the pane.

## Supported targets

`-target <name>` (default `claude`) selects which agent the launcher
runs and which host paths to bind rw for that agent's session state
and credentials. The three registered targets:

- **`claude`** - binary `claude`; binds `~/.claude` (session state)
  and `~/.claude.json` (credentials).
- **`codex`** - binary `codex`; binds `~/.codex` (auth.json,
  sessions, history, hooks, SQLite logs, scratch). One directory
  covers everything codex writes.
- **`opencode`** - binary `opencode`; binds three XDG dirs:
  `~/.config/opencode` (opencode.json + the launcher's npm deps),
  `~/.local/share/opencode` (auth.json + opencode.db SQLite +
  snapshot storage), and `~/.cache/opencode` (downloaded native
  runtime binaries, models.json). All three are rw because
  opencode mutates the SQLite during a session and may unpack new
  runtime binaries on demand.

A target whose state paths do not exist on the host is still
launchable: missing paths are skipped, so a fresh box without a
prior codex login still gets a sandboxed codex up; codex itself
will then walk the operator through its first-time login flow.

The binary lookup follows symlinks via `filepath.EvalSymlinks`
before passing the path to bwrap. This matters on Nix: PATH
typically resolves a binary through `~/.nix-profile/bin/<name>`,
and `~/.nix-profile` is a symlink under `$HOME`. The sandbox's
tmpfs masks `$HOME`, so the symlink itself disappears inside; the
launcher chases it to the `/nix/store/<hash>/bin/<name>` target,
which stays reachable via the read-only root bind.

To add a new target, register an entry in
`cmd/spore-sandbox/target.go` with the binary name and its state
paths (see `homePaths` for the helper). Run a smoke test from
`--exec`: `spore-sandbox --exec -target <name> -- <bin> --version`
must succeed.

## Two subcommands

The launcher binary exposes two ways to drop into the sandbox:

- **`spore-sandbox` (default).** Spawns a tmux window in the current
  session and runs the target binary inside the bwrap+proxy
  sandbox. Used for ad-hoc operator-attended launches, and by
  `-redteam` when validating the primitive. Requires `TMUX` to be
  set unless `-dry-run` is passed.
- **`spore-sandbox --exec ... -- <argv>`.** Wraps the given argv in
  the same bwrap+proxy sandbox but does NOT spawn its own tmux
  window. stdin/stdout/stderr pass through and the exit code
  mirrors the child. The leading argv element is PATH-resolved and
  symlink-chased on the host before exec, so a user-profile-only
  binary (typical on Nix) survives the tmpfs `$HOME` mask. This is
  the primitive the worker spawn path will call to soak the
  sandbox into every sandboxed worker: the worker's existing `tmux
  new-session sh -c "<agent>"` becomes `tmux new-session sh -c
  "spore-sandbox --exec -worktree <wt> -target <agent> -- <agent>"`.

Both subcommands share the same policy + config merge (`[sandbox]`
in spore.toml, user override at `~/.config/spore/sandbox.toml`,
CLI flags on top), so `-allow linear.app` and friends mean the
same thing in either mode.

## See also

- `cmd/spore-sandbox/main.go` - `runLaunch` and `buildLaunchCmd`.
- `cmd/spore-sandbox/policy.go` - the bwrap-argv assembly.
- `cmd/spore-sandbox/redteam.go` - the 12-probe set and prompt.
- `cmd/spore-sandbox/verdict.go` - the LEAKED-vs-PASS reconciliation.
- `docs/todo/sandbox-followups.md` - the brief that drove
  this iteration (tech-debt sweep, docs, extension ergonomics,
  harness soak).
