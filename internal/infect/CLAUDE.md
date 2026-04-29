# infect

`spore infect <ip>` wipes a fresh server and reinstalls NixOS via
`nixos-anywhere`, using the bundled flake at `bootstrap/flake/`.
This file is the operations contract for the agent driving an
end-to-end infect: do not re-derive it, do not ask the operator for
it, do not pre-confirm steps the contract already authorises.

## handover contract

End state: a local tmux window named `coord` attached over SSH to
the live `spore/<project>/coordinator` session on the infected box.
The coordinator greets the operator on attach with project name,
host, fleet status, and active-task count, then drops to a shell.

The agent produces this end state. The operator confirms only the
IP. They do not need to confirm wipe-and-reinstall; that is the
whole job.

## two users, by design

- `root`: only used during infect (nixos-anywhere SSHes here, the
  agent rsyncs and runs `spore bootstrap` here) and for emergency
  admin. Operator-facing tooling never logs in as root.
- `spore`: declared by the bundled flake. Login shell is
  `/usr/local/bin/spore-attach` (in `bootstrap/handover/`); it
  attaches to the project's coordinator tmux session and exits
  when the operator detaches. No sudo, no wheel, no shell prompt.
  This is what the local `coord` window connects to:
  `ssh -t spore@<ip>` is enough; the forced login shell does the
  attach. Tmux sessions live in spore's tmux server, not root's.

## defaults the agent applies without asking

When the operator hands you `<ip>` and (optionally) a target repo:

- SSH user during infect: `root`. SSH key: `~/.ssh/id_ed25519`.
  Post-infect operator SSH: `spore` (forced into coord pane).
- Hostname: `nixos` (the bundled flake default; survives
  reinstall).
- Disk: `/dev/sda` (`bootstrap/flake/disk-config.nix`). The infect
  command exists to wipe this. Do not ask.
- Repo destination on box: `/home/spore/<basename of source>`.
  Owned by `spore:users`. Rsync from local goes to root, then
  the agent moves and chowns; spore has no sshd write access
  beyond what spore-attach allows.
- Stages to `--skip` on `spore bootstrap`: `tests-pass`,
  `creds-wired`, `readme-followed`, `validation-green`,
  `pilot-aligned`. Each fails on consumer-side state the agent
  cannot or should not edit. Skipping is the prescribed escape
  hatch.
- `info-gathered.json`: write
  `{"tickets":{"tool":"none"},"knowledge":{"tool":"none"}}` unless
  the operator named a real ticketing or wiki tool.
- Handover artifacts: install `bootstrap/handover/*.sh` to
  `/usr/local/bin/`, `bootstrap/handover/hooks/*.pl` to
  `/home/spore/.claude/hooks/`, and `bootstrap/handover/settings.json`
  to `/home/spore/.claude/settings.json`. Persist
  `SPORE_COORDINATOR_AGENT` and `SPORE_AGENT_BINARY` in
  `/home/spore/.bashrc` (NixOS sshd does not source `/root/.bashrc`
  and has no `/etc/profile.d`). Default
  `SPORE_AGENT_BINARY=/usr/local/bin/spore-worker-brief` and
  `SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch`:
  the wrappers exec the bundled `claude` with
  `--dangerously-skip-permissions` and pipe `tasks/<slug>.md` /
  `bootstrap/coordinator/role.md` in as headless prompt / interactive
  seed respectively. The greet wrappers stay installed as a manual
  fallback when claude is not yet authed.

## when to ask

Only when an action is operator-bound or genuinely ambiguous:

- The host already runs an unrelated workload (mounts / hostnames
  do not match a fresh provider image). Confirm before wiping.
- The operator has not named a target repo and the box is meant
  to host one.
- An interactive auth dance (e.g. installing claude-code on the
  box with an OAuth flow).

## the script (idempotent)

1. `ssh-keygen -R <ip>` to clear stale host keys.
2. `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o
   /tmp/spore-linux-amd64 ./cmd/spore`.
3. Launch infect in a tmux window so the operator can watch:
   `tmux new-window -d -n infect "go run ./cmd/spore infect <ip>
   --ssh-key ~/.ssh/id_ed25519 | tee /tmp/spore-infect.log"`.
   Wait via `Monitor` until `=== EXIT ===` lands.
4. `scp /tmp/spore-linux-amd64 root@<ip>:/usr/local/bin/spore`,
   `chmod +x` it.
5. `rsync -az --exclude='log/*.log' --exclude='tmp/*'
   --exclude='storage/*' --exclude='node_modules/' <src>/
   root@<ip>:/root/<basename>/`. The dubious-ownership fix
   landed in `internal/task` so no chown is needed.
6. SSH in, `mkdir -p /root/.local/state/spore/<project>`, write
   `info-gathered.json` with the defaults above.
7. Run `spore bootstrap` with the five `--skip` flags listed
   above. It walks the rest and lands at `worker-fleet-ready`.
8. Install the handover artifacts (NixOS does not put
   `/usr/local/bin` on the default PATH; see step 10):
   - `scp bootstrap/handover/spore-attach.sh root@<ip>:/usr/local/bin/spore-attach`
   - `scp bootstrap/handover/greet-coordinator.sh root@<ip>:/usr/local/bin/spore-greet-coordinator`
   - `scp bootstrap/handover/greet-worker.sh root@<ip>:/usr/local/bin/spore-greet-worker`
   - `scp bootstrap/handover/spore-coordinator-launch.sh root@<ip>:/usr/local/bin/spore-coordinator-launch`
   - `scp bootstrap/handover/spore-worker-brief.sh root@<ip>:/usr/local/bin/spore-worker-brief`
   `chmod +x` all five.
   Then `mkdir -p /home/spore/.claude/hooks` and
   `scp bootstrap/handover/hooks/{block-bg-bash,load-state-md}.pl
   root@<ip>:/home/spore/.claude/hooks/`, `chmod +x` both. Drop the
   user-scope settings template:
   `scp bootstrap/handover/settings.json root@<ip>:/home/spore/.claude/settings.json`.
9. Move the repo to spore's home and chown:
   `mv /root/<basename> /home/spore/<basename>`,
   `mv /root/.local/state/spore /home/spore/.local/state/spore`,
   `chown -R spore:users /home/spore`.
10. Write `/home/spore/.bashrc`. Point both agent vars at the
    handover wrappers (which exec `claude` with the right flags and
    pipe brief / role files in):
    `export PATH=/usr/local/bin:$PATH`
    `export SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch`
    `export SPORE_AGENT_BINARY=/usr/local/bin/spore-worker-brief`
    `chown spore:users` it. To swap in the greet stand-ins (when
    claude is not yet authed), the operator overrides those two vars
    in their own shell before `spore fleet reconcile`.
11. As spore (`sudo -u spore bash -lc '...'` from root or
    `runuser -l spore -c '...'`):
    `cd ~/<basename> && spore fleet enable && spore fleet reconcile`.
    The tmux server now belongs to spore; root cannot see it via
    `tmux ls` and that is intentional.
12. Locally, open the handover window:
    `tmux new-window -d -n coord "ssh -t -o
    ServerAliveInterval=30 spore@<ip>"`. The forced login shell
    (`spore-attach`) does the tmux attach itself; do not pass an
    explicit attach command.

## known gaps

- The bundled flake includes `claude-code` but not its OAuth
  credential. First operator login on the box still needs an
  interactive `claude /login` once; the greet wrappers stand in
  until that lands.
- `creds-wired`, `readme-followed`, `validation-green` skip with
  warnings. Consumer projects that want clean stages must
  document the secret surface in their CLAUDE.md, ship a README
  with run / test instructions, and resolve any
  comment-noise / em-dash / file-size lint hits.
