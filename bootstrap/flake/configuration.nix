{ lib, modulesPath, pkgs, ... }:
let
  sporeAttach = pkgs.writeShellScriptBin "spore-attach" ''
    set -e

    if [ "''${1:-}" = "-c" ]; then
      set -- ''${2:-}
      shift
    fi

    mode="''${1:-coord}"

    attach_pilot() {
      name="''${1:-default}"
      exec ${pkgs.tmux}/bin/tmux new-session -A -s "spore/pilot/$name" ${pkgs.bashInteractive}/bin/bash -l
    }

    case "$mode" in
      coord)
        sessions="$(${pkgs.tmux}/bin/tmux ls -F '#{session_name}' 2>/dev/null | ${pkgs.gnugrep}/bin/grep '/coordinator$' || true)"
        n="$(printf '%s' "$sessions" | ${pkgs.gnugrep}/bin/grep -c . || true)"
        case "$n" in
          1)
            exec ${pkgs.tmux}/bin/tmux attach -t "$sessions"
            ;;
      0)
        printf '%s\n' \
          "spore-attach: no coordinator session running on this host." \
          "spore-attach: dropping into a default pilot session so you can recover." \
          "spore-attach: if Claude or Codex is not logged in yet, run its login command here first." \
          "spore-attach: then run: spore fleet enable && spore fleet reconcile" >&2
        attach_pilot default
        ;;
          *)
            echo "spore-attach: multiple coordinator sessions present:" >&2
            printf '  %s\n' $sessions >&2
            echo "spore-attach: ssh in as root to reconcile." >&2
            exit 1
            ;;
        esac
        ;;
      pilot)
        attach_pilot "''${2:-default}"
        ;;
      *)
        echo "spore-attach: unknown mode: $mode" >&2
        echo "usage: spore-attach [coord | pilot [<name>]]" >&2
        exit 2
        ;;
    esac
  '';
in
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
    shell = "${sporeAttach}/bin/spore-attach";
  };

  environment.systemPackages = with pkgs; [
    claude-code
    codex
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
      EnvironmentFile = "-/etc/spore/coordinator.env";
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
