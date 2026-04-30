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

  # Coordinator watchdog. /usr/local/bin/spore-fleet-tick is the
  # idempotent reconciler the infect handover drops on the box: it
  # walks /home/spore/* and runs `spore fleet reconcile` in any
  # project that looks like a spore harness. `reconcile` spawns the
  # coordinator tmux session when missing and is a no-op when alive.
  # Pair a oneshot with a minute timer; if the session dies (operator
  # kill, crash) it comes back within 60s, and after a reboot it is
  # up 30s after multi-user.target.
  #
  # Run as user spore, so the tmux server lives in spore's UID
  # namespace (/tmp/tmux-<uid>/default) and is visible from later
  # interactive ssh-ins. Environment is set explicitly because system
  # services do not source /home/spore/.bashrc.
  systemd.services.spore-coordinator = {
    description = "spore coordinator tmux watchdog";
    after = [ "network.target" ];
    serviceConfig = {
      Type = "oneshot";
      User = "spore";
      Group = "users";
      KillMode = "process";
      Environment = [
        "HOME=/home/spore"
        "PATH=/run/current-system/sw/bin:/run/wrappers/bin:/usr/local/bin"
        "SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch"
        "SPORE_AGENT_BINARY=/usr/local/bin/spore-worker-brief"
      ];
      ExecStart = "/usr/local/bin/spore-fleet-tick";
    };
  };

  systemd.timers.spore-coordinator = {
    description = "spore coordinator watchdog (1 min)";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "30s";
      OnUnitInactiveSec = "1min";
      AccuracySec = "5s";
      Unit = "spore-coordinator.service";
    };
  };

  system.stateVersion = "24.05";
}
