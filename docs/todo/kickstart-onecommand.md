**Status**: done

# kickstart: collapse the operator path to one command

## Goal

The README's "Getting started" describes the operator path:

1. `nix run <spore-checkout> -- infect <ip> --ssh-key <key>
   --repo <local-path> --coordinator-agent claude|codex
   --coordinator-model <model>` (local)
2. `ssh -t spore@<ip>` (remote attach surface)
3. If needed, log the selected agent in interactively, then run
   `spore fleet reconcile` from the recovery pane.

The one-command path now owns the install, repo copy, spore CLI copy,
handover scripts, initial coordinator provider/model env, and fleet
reconcile timer.

## Progress

2026-05-04: Verified the one-command handoff against
`188.245.113.128` with:

```
nix run . -- infect 188.245.113.128 --ssh-key ~/.ssh/id_ed25519 --repo /home/sky/projects/spore --coordinator-agent codex --coordinator-model gpt-5.5 --coordinator-effort high
```

Post-infect checks showed `spore@188.245.113.128` attaching to
`spore/spore/coordinator`, the coordinator pane waiting at Codex's
interactive login chooser, `/home/spore/spore/.git` present, and
`/etc/spore/coordinator.env` recording `codex`, `gpt-5.5`, and `high`.

## Why `--repo` takes a local path, not a URL

Most operator-facing repos are private. A URL form forces a
credential question on the box: forward an SSH agent, mint a PAT,
install a deploy key, etc. Each option leaves an artifact (or a
window) we do not want to manage in v0.

A local path sidesteps it. The operator already has an
authenticated checkout on their machine; spore rsyncs the working
tree (with `.git/`) to the box. No credential crosses, no token
lives on disk, public and private repos work the same way.

A future iteration can add `--repo <url>` for the "I do not have a
local clone" case, with `--ssh-agent` forwarding as the default
auth path. Out of scope for v0.

## Current flow

`spore infect --repo` now performs the handoff directly:

- installs NixOS with the bundled flake
- authorizes the install key for both `root` and `spore`
- copies the running spore binary to `/usr/local/bin/spore`
- rsyncs the local checkout, including `.git/`, to
  `/home/spore/<basename>`
- excludes `.env*` secrets while preserving `.env.example`
- creates an empty `tasks/` directory on the target when the checkout
  does not already contain one, so first reconcile can spawn the
  coordinator even for a kernel checkout
- installs `bootstrap/handover/` scripts, hooks, settings, and user
  units from embedded CLI assets
- writes `/etc/spore/coordinator.env` with the selected provider,
  model, and effort
- enables the fleet flag, runs the first reconcile, and restarts
  `spore-coordinator.service`

## Landed Scope

### 1. `spore infect --repo <local-path>`

After the kexec / nixos-anywhere install completes, before exit,
infect does:

- rsync the working tree at `<local-path>` to `/root/project` on
  the box, including `.git/` so `repo-mapped` and the lints see
  history
- apply the default exclude set: `.env*` (except `.env.example`),
  `node_modules/`, `vendor/bundle/`, `tmp/`, `log/`, `storage/`,
  `public/assets/`, `public/packs/`, `.bundle/`, `coverage/`,
  and build artifacts
- move the copied repo to `/home/spore/<basename>` and chown it to
  `spore:users`
- ensure the spore CLI is on `PATH` at `/usr/local/bin/spore`

Acceptance: `spore infect <ip> --ssh-key <key> --repo <local-path>`
lands the box in a state where `ssh -t spore@<ip>` reaches the
coordinator attach surface or a clear first-login recovery pane.

### 2. Bundled flake bakes spore + runtime deps

`bootstrap/flake/configuration.nix` adds to
`environment.systemPackages`:

- `git` (required by `repo-mapped` and every git-based lint)
- `claude-code` so
  `/spore-bootstrap` can run on the box without an extra install
- `codex` so `--coordinator-agent codex` has a binary on the bundled
  target
- `tmux`, `rsync`, and the runtime tools the handoff scripts call

The running local spore binary is copied to `/usr/local/bin/spore`
after install, so the target uses the exact checkout the operator
ran.

## Deferred Follow-Up

### `info-gathered` and `readme-followed` driven without an agent

Today the spore-bootstrap skill uses `AskUserQuestion`, which only
works when an agent (claude-code) is on the box driving the repo.
Two improvements:

- Document the on-box claude flow clearly (one paragraph in
  `bootstrap/stages/info-gathered.md`).
- Bigger: have the spore CLI itself prompt for the `info-gathered`
  answers (the gate is enumerated choices: `jira` / `linear` /
  `github-issues` / `none`, etc.). Reserve the skill for free-form
  gates (`readme-followed`).

Acceptance: a typical bootstrap walk requires no JSON hand-writing
and does not require claude-code merely to answer a four-option
multiple-choice prompt.

## Cross-references

- Alignment notes 1, 3, 4: kickstart UX, bundled flake gaps, skill
  cross-machine flow.
- Stages affected: `repo-mapped` (depends on git), `info-gathered`,
  `readme-followed`.
- Related code: `cmd/spore/main.go` (`runInfect`),
  `internal/infect/infect.go`, `bootstrap/flake/configuration.nix`,
  `bootstrap/skills/spore-bootstrap/SKILL.md`.
