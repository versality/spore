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

  # Operator-facing account. No shell prompt, no sudo, no wheel. SSH
  # in as spore -> the login shell (spore-attach) attaches you to a
  # tmux session and exits when you detach.
  #
  # Two landing modes, picked by the per-key authorized_keys command:
  # - bare key (no command=): primary-operator path. Attaches to the
  #   singleton coordinator session; falls back to a default pilot
  #   session if the coordinator is down (so SSH never bounces).
  # - command="/usr/local/bin/spore-attach pilot <name>": gives that
  #   key its own private session at spore/pilot/<name>. Use this for
  #   secondary pilots so they neither share a pane with the
  #   coordinator nor with each other.
  #
  # Authorized keys come from local.nix. Root SSH stays open for
  # emergency reconcile.
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
