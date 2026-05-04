# spore-fleet NixOS module

Once a project reaches `worker-fleet-ready`, a downstream NixOS host
can autostart the fleet reconciler by importing
`nixosModules.spore-fleet` from this flake.

The module declares a systemd-user oneshot driven by:

- a 60-second timer;
- path watches on the project's `tasks/` directory;
- a path watch on the kill-switch flag at
  `~/.local/state/spore/fleet-enabled`.

This keeps `spore fleet enable` and new active tasks responsive even
when the timer has not ticked yet. home-manager wiring for the target
user is assumed.

## Example

```nix
{
  inputs = {
    spore.url = "github:versality/spore";
    home-manager.url = "github:nix-community/home-manager";
  };

  outputs = { self, nixpkgs, spore, home-manager }: {
    nixosConfigurations.worker = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        spore.nixosModules.spore-fleet
        home-manager.nixosModules.home-manager
        ({ ... }: {
          users.users.spore = {
            isNormalUser = true;
            linger = true;
          };

          home-manager.users.spore.home.stateVersion = "25.11";

          services.spore-fleet = {
            enable = true;
            user = "spore";
            projectRoot = "/home/spore/project";
            maxWorkers = 6;
          };
        })
      ];
    };
  };
}
```

`package` and `claudeCodePackage` default to this flake's outputs.
Override either option to pin a specific build.

Runners spawn `claude` (claude-code), which manages its own credential
lifecycle inside the client. The module deliberately exposes no
Anthropic API key slot.

`credentialFiles` is for non-claude secrets the runners may need, such
as MCP server keys or git-push tokens. It wires them through systemd
`LoadCredential=` so values never enter Nix evaluation or the
`/nix/store`.

The full option reference lives in `nixosModules/spore-fleet.nix`.

## Horizontal Scale

Capacity scales additively by enabling the module on multiple hosts
that all see the same project tree, either through a shared filesystem
or through per-host checkouts of one branch.

Each reconciler picks up active tasks it notices first.
`services.spore-fleet.hostId` defaults to `networking.hostName` and
surfaces in `SPORE_HOST_ID` so logs and status displays can identify a
host.

The kill-switch flag is per-host and per-user. Pausing one machine
does not pause another.

There is no cross-host lock layer in v0. Races on `tasks/<slug>.md`
frontmatter are tolerated by Spore's file-based communication shape,
not arbitrated.
