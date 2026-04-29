{
  description = "spore bootstrap: minimal NixOS for a fresh cloud VM, installed via nixos-anywhere.";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.disko.url = "github:nix-community/disko";
  inputs.disko.inputs.nixpkgs.follows = "nixpkgs";

  outputs =
    { nixpkgs, disko, ... }:
    {
      nixosConfigurations.spore-bootstrap = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [
          disko.nixosModules.disko
          ./configuration.nix
        ];
      };
    };
}
