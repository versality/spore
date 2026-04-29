# Bundled bootstrap flake

The smallest viable NixOS configuration that `spore infect` can install
on a fresh cloud VM. Used as the default flake when the operator does
not pass `--flake`.

## Shape

- `flake.nix` exposes `nixosConfigurations.spore-bootstrap` for
  `x86_64-linux`. nixpkgs tracks `nixos-unstable`, disko follows
  nixpkgs.
- `configuration.nix` enables `services.openssh` (key-only, no
  password, no root password login), wires GRUB for EFI, and
  imports the disko module plus a generated `local.nix` (see below).
- `disk-config.nix` declares a single-disk GPT layout: a 1M BIOS-boot
  partition, a 512M EFI System Partition (vfat at `/boot`), and the
  rest as ext4 at `/`. `device = "/dev/sda"` is the default; override
  with `disko.devices.disk.disk1.device` in `local.nix`.
- `local.nix` is generated at infect time by `spore infect` and holds
  the per-target hostname plus the post-install root authorized keys.
  `local.nix.example` shows the expected shape; the real file is
  written into a temp staging copy of this directory and never lands
  in the spore tree.

## Limits

- x86_64-linux only. aarch64 callers must pass their own `--flake`.
- Single disk only. Multi-disk, RAID, encryption, swap, and any
  non-ext4 root require a custom flake.
- No users beyond `root`. No services beyond openssh. Designed as the
  thinnest possible substrate that `spore` (or a downstream
  configuration push) can grow on top of.
- nixpkgs is unfrozen (no `flake.lock` shipped). Each install
  resolves the latest `nixos-unstable`. Override with `--flake` for
  pinned reproducibility.

## Override

Pass `--flake <path-or-attr>` to `spore infect` to use any other
flake. The flake must expose a `nixosConfigurations.<name>` matching
the attr after `#` (default attr when bundled is `spore-bootstrap`).
A custom flake handles its own hostname and authorized-keys wiring;
`spore infect` does not synthesize a `local.nix` for non-bundled
flakes.
