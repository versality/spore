**Status**: open

# kickstart: collapse the operator path to one command

## Goal

The README's "Getting started" describes the intended operator path:

1. `nix profile install github:versality/spore` (local)
2. `spore infect <ip> --ssh-key <key> --repo <local-path>` (local)
3. `spore bootstrap` (remote, re-entrant; agent prompts the operator
   at gate-bound stages)
4. `spore task new` + `spore fleet enable` (remote)

In v0 the agent legwork inside steps 2 and 3 leaks back to the
operator. This doc tracks the gaps and the manual workaround so the
public README does not need to enumerate the friction.

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

## Current workaround (v0)

Until the gaps below close, the operator runs these by hand after
`spore infect` returns:

```
# (local) cross-build and copy the spore binary
GOOS=linux GOARCH=amd64 go build -o /tmp/spore-linux ./cmd/spore
ssh root@<ip> 'mkdir -p /root/bin'
scp /tmp/spore-linux root@<ip>:/root/bin/spore

# (remote) install git
ssh root@<ip> 'nix profile install nixpkgs#git \
  --extra-experimental-features "nix-command flakes"'

# (local) rsync the local checkout, with sane excludes
rsync -az --info=stats1 \
  --exclude='.env' --exclude='.env.test' --exclude='.env.development' \
  --exclude='.env.local' --exclude='node_modules/' \
  --exclude='vendor/bundle/' --exclude='tmp/' --exclude='log/' \
  --exclude='storage/' --exclude='public/assets/' --exclude='public/packs/' \
  --exclude='.bundle/' --exclude='coverage/' \
  ~/projects/myrepo/ root@<ip>:/root/project/

# (remote) install claude-code so /spore-bootstrap can run on the box
ssh root@<ip> 'nix profile install github:versality/spore#claude-code \
  --extra-experimental-features "nix-command flakes"'

# (remote) walk gates, drive skill-bound stages
ssh -t root@<ip> 'cd /root/project && claude'
# inside claude: /spore-bootstrap
```

If the agent flow is unavailable, the operator can write the
sentinel JSON sentinel files directly per the runbook in
`bootstrap/stages/<stage>.md`.

## Gaps to close

### 1. `spore infect --repo <local-path>`

After the kexec / nixos-anywhere install completes, before exit,
infect should:

- rsync the working tree at `<local-path>` to `/root/project` on
  the box, including `.git/` so `repo-mapped` and the lints see
  history
- apply the default exclude set: `.env*` (except `.env.example`),
  `node_modules/`, `vendor/bundle/`, `tmp/`, `log/`, `storage/`,
  `public/assets/`, `public/packs/`, `.bundle/`, `coverage/`,
  build artifacts. A `--exclude` flag can extend the set.
- chown `/root/project` to root after the transfer so git stops
  complaining about dubious ownership (or rely on the lint-side
  `safe.directory` patch already shipped)
- ensure the spore CLI is on `PATH` at a stable location

Acceptance: `spore infect <ip> --ssh-key <key> --repo <local-path>`
lands the box in a state where `cd /root/project && spore bootstrap`
works without further setup.

### 2. Bundled flake bakes spore + runtime deps

`bootstrap/flake/configuration.nix` adds to
`environment.systemPackages`:

- `spore` (this flake's `packages.<system>.default`)
- `git` (currently absent; required by `repo-mapped` and every
  git-based lint)
- `claude-code` (this flake's `packages.<system>.claude-code`) so
  `/spore-bootstrap` can run on the box without an extra install

Acceptance: a freshly-infected box has `spore`, `git`, and `claude`
on `PATH` out of the box. No `nix profile install` step required.

### 3. `info-gathered` and `readme-followed` driven without an agent

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
