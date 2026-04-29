# spore infect

`spore infect` wraps
[nixos-anywhere](https://github.com/nix-community/nixos-anywhere) to
install NixOS over SSH onto a freshly provisioned, root-reachable VM.
spore does not reimplement nixos-anywhere; it stages a small flake,
shells out to `nix run github:nix-community/nixos-anywhere`, streams
the subprocess output, and runs a post-install ssh smoke check.

## Invocation

```
spore infect <ip> --ssh-key <path> [--flake <path-or-attr>]
                                     [--hostname <name>] [--user <user>]
```

### Minimal example

```
spore infect 203.0.113.7 --ssh-key ~/.ssh/id_ed25519
```

This stages the bundled flake at `bootstrap/flake/`, derives the
post-install root authorized key from `~/.ssh/id_ed25519.pub`, runs
nixos-anywhere as `root@203.0.113.7`, and finishes with `ssh
root@203.0.113.7 nixos-version`.

### Full example

```
spore infect 203.0.113.7 \
  --ssh-key ~/.ssh/id_ed25519 \
  --hostname web-1 \
  --user root \
  --flake ./my-config#hetzner
```

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `<ip>` | yes |  | Positional. IPv4 or IPv6 reachable from this host. |
| `--ssh-key` | yes |  | Path to a private SSH key. The `.pub` sibling must exist; spore reads it as the post-install root authorized key. nixos-anywhere also uses the private key (`-i`) to authenticate during install. |
| `--flake` | no | bundled | A flake path or full flake-ref. With `#attr` is taken verbatim; without `#attr` the bundled attr name `spore-bootstrap` is appended. When omitted the bundled flake at `bootstrap/flake/` is staged into a tempdir. |
| `--hostname` | no | `nixos` | Written into the staged `local.nix` as `networking.hostName`. Ignored when `--flake` is supplied (custom flakes own their own hostname). |
| `--user` | no | `root` | SSH user nixos-anywhere connects as. Non-root users must have password-less sudo on the target. |

## Prerequisites

- `nix` with flakes enabled on this machine (`nix run` must work).
- `ssh` and `ssh-keygen` on PATH.
- Target host: x86_64 Linux, root-reachable over SSH, kexec-capable,
  >= 1 GiB RAM. Hetzner / DigitalOcean / Vultr / equivalent default
  cloud Linux images all qualify.
- The `.pub` sibling of `--ssh-key`. Derive with
  `ssh-keygen -y -f <key> > <key>.pub` if missing.

## What the bundled flake provides

`bootstrap/flake/` is the smallest viable NixOS config: openssh
(key-only, no password, no root password login), GRUB EFI, and a
single-disk GPT layout (1M BIOS-boot, 512M ESP at `/boot`, ext4 at
`/`). nixpkgs tracks `nixos-unstable`, disko follows nixpkgs, no
`flake.lock` is shipped.

The hostname and authorized-keys list are written into a generated
`local.nix` that lives only inside the temp staging directory; the
spore tree itself never holds operator-specific state. See
`bootstrap/flake/README.md` for the full shape and the override
guidance.

## What spore infect does NOT do

- Provision the VM. Operator runs the cloud console / API.
- Re-infect an existing NixOS host. Use `nixos-rebuild switch
  --target-host` against your real flake instead.
- Wire secrets, agenix, or any project-specific module. The bundled
  flake stops at "ssh works, root can log in".
- Run the spore bootstrap stages on the freshly-installed server.
  That is a separate flow.

## Failure hints

- `Permission denied (publickey)` from nixos-anywhere: the install
  ssh key (`--ssh-key`) is not the one the cloud image accepts for
  root. Most cloud providers let you preselect the key when creating
  the VM. nixos-anywhere does not fall back to a password unless you
  also pass `SSHPASS` plus `--env-password` (spore does not surface
  these flags; pass a working key instead).
- `Host key verification failed` from the smoke check: an old entry
  for `<ip>` exists in `~/.ssh/known_hosts` from a previous install.
  Remove it with `ssh-keygen -R <ip>`. The smoke check uses
  `StrictHostKeyChecking=accept-new` so a first-time entry is fine.
- `disko: target disk is not /dev/sda`: the bundled flake assumes
  `/dev/sda`. Provide a `--flake` of your own or override
  `disko.devices.disk.disk1.device` in a wrapping module. Hetzner
  cloud, DigitalOcean droplets, and Vultr shared compute use
  `/dev/sda` by default; KVM nodes with virtio often expose
  `/dev/vda`.
- `kexec failed to load`: the target cannot kexec. Check
  [the upstream FAQ](https://github.com/nix-community/nixos-anywhere/blob/main/docs/howtos/INDEX.md);
  custom kexec images and limited-RAM walkthroughs live there.
- `public key "<key>.pub" not found`: spore needs the public sibling
  of `--ssh-key` to write into the bundled flake's authorized-keys
  list. Run `ssh-keygen -y -f <key> > <key>.pub`.

## Exit codes

- 0: install + smoke check both succeeded.
- 2: argument error (missing flag, bad positional count).
- otherwise: nixos-anywhere or the smoke-check ssh exited non-zero;
  spore mirrors that exit code so wrapping scripts can branch on it.
