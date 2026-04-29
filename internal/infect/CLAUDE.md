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

## defaults the agent applies without asking

When the operator hands you `<ip>` and (optionally) a target repo:

- SSH user: `root`. SSH key: `~/.ssh/id_ed25519`.
- Hostname: `nixos` (the bundled flake default; survives
  reinstall).
- Disk: `/dev/sda` (`bootstrap/flake/disk-config.nix`). The infect
  command exists to wipe this. Do not ask.
- Repo destination on box: `/root/<basename of source>`.
- Stages to `--skip` on `spore bootstrap`: `tests-pass`,
  `creds-wired`, `readme-followed`, `validation-green`,
  `pilot-aligned`. Each fails on consumer-side state the agent
  cannot or should not edit. Skipping is the prescribed escape
  hatch.
- `info-gathered.json`: write
  `{"tickets":{"tool":"none"},"knowledge":{"tool":"none"}}` unless
  the operator named a real ticketing or wiki tool.
- Greet wrappers: install `bootstrap/handover/greet-*.sh` to
  `/usr/local/bin/spore-greet-{coordinator,worker}` on the box.
  Persist `SPORE_COORDINATOR_AGENT` and `SPORE_AGENT_BINARY` in
  `/root/.bashrc`. NixOS has no `/etc/profile.d`.

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
8. `scp bootstrap/handover/greet-*.sh root@<ip>:/usr/local/bin/`
   then on the box: `chmod +x /usr/local/bin/spore-greet-*`,
   `mv /usr/local/bin/greet-coordinator.sh
   /usr/local/bin/spore-greet-coordinator` (and worker).
   Append to `/root/.bashrc`:
   `export SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-greet-coordinator`
   `export SPORE_AGENT_BINARY=/usr/local/bin/spore-greet-worker`
9. `bash -lc "spore fleet enable && spore fleet reconcile"`.
   Verify `tmux ls` on box shows
   `spore/<project>/coordinator`.
10. Locally, open the handover window:
    `tmux new-window -d -n coord "ssh -t -o
    ServerAliveInterval=30 root@<ip> 'tmux attach -t
    spore/<project>/coordinator'"`. Tell the operator the
    window is named `coord`.

## known gaps

- The bundled flake does not include `claude-code`. Real Claude
  agents in coordinator + worker panes require a separate install
  pass with auth. Until then the greet wrappers above act as
  stand-ins.
- `creds-wired`, `readme-followed`, `validation-green` skip with
  warnings. Consumer projects that want clean stages must
  document the secret surface in their CLAUDE.md, ship a README
  with run / test instructions, and resolve any
  comment-noise / em-dash / file-size lint hits.
