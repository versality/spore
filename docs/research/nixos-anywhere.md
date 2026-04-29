# nixos-anywhere research notes

Source: https://github.com/nix-community/nixos-anywhere (quickstart +
howtos), reviewed 2026-04.

- Invocation: `nix run github:nix-community/nixos-anywhere -- --flake
  <path-or-url>#<attr> --target-host <user>@<ip>`. No install step.
- Required inputs: a flake exposing `nixosConfigurations.<attr>`, a
  disko config inside that configuration, and an SSH-reachable target
  (root, or a sudo user with NOPASSWD). `-i <key>` selects the
  install-time SSH key.
- Disko handles partition + format + mount via the bundled module
  (`disko.nixosModules.disko`). Single-disk GPT (BIOS-boot + EFI ESP +
  ext4 root on `/dev/sda`) covers Hetzner / DO / Vultr cloud VMs.
- Hardware config can be auto-generated with
  `--generate-hardware-config nixos-generate-config <path>` or
  `nixos-facter <path>`; the bundled minimal flake skips this and
  relies on the qemu-guest profile, sufficient for the cloud VM shape.
- Failure modes: target lacks kexec, target has < 1 GiB RAM, IPv6-only
  reachability without `--ssh-option`, password-only SSH (needs
  SSHPASS + `--env-password`), and stale `~/.ssh/known_hosts` entries
  after the install rotates the host key.
- Host key rotates on every install; downstream tooling should treat
  the first post-install ssh as a new host.
