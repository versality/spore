{ lib, modulesPath, pkgs, ... }:
{
  imports = [
    (modulesPath + "/installer/scan/not-detected.nix")
    (modulesPath + "/profiles/qemu-guest.nix")
    ./disk-config.nix
  ] ++ lib.optional (builtins.pathExists ./local.nix) ./local.nix;

  nixpkgs.config.allowUnfreePredicate =
    pkg: builtins.elem (lib.getName pkg) [ "claude-code" ];

  boot.loader.grub = {
    efiSupport = true;
    efiInstallAsRemovable = true;
  };

  services.openssh = {
    enable = true;
    settings.PasswordAuthentication = false;
    settings.KbdInteractiveAuthentication = false;
    settings.PermitRootLogin = "prohibit-password";
  };

  # Operator-facing account. Lives only to attach the operator to the
  # coordinator tmux session; no shell prompt, no sudo, no wheel. SSH
  # in as spore -> the login shell (spore-attach) wires you straight
  # into the coordinator pane and exits when you detach. Authorized
  # keys come from local.nix. Root SSH stays open for emergency.
  users.users.spore = {
    isNormalUser = true;
    home = "/home/spore";
    shell = "/usr/local/bin/spore-attach";
  };

  environment.systemPackages = with pkgs; [
    claude-code
    git
    rsync
    curl
    gnumake
    htop
    tmux
    vim
  ];

  system.stateVersion = "24.05";
}
