{ config, lib, pkgs, ... }:

let
  cfg = config.services.spore-fleet;
  stateRel = ".local/state/spore/fleet-enabled";

  # Common preamble: re-exec as cfg.user with a clean systemd-user
  # environment when invoked as root (system.activationScripts and
  # colmena pre/postActivation both run as root). When already running
  # as cfg.user (manual smoke test, or a user-context call), the
  # re-exec is skipped and the body runs in place.
  asUserPreamble = ''
    set -eu
    user='${cfg.user}'
    uid="$(${pkgs.coreutils}/bin/id -u "$user")"
    if [ "$(${pkgs.coreutils}/bin/id -un)" != "$user" ]; then
      home="$(${pkgs.getent}/bin/getent passwd "$user" | ${pkgs.coreutils}/bin/cut -d: -f6)"
      if [ -z "$home" ]; then
        echo "spore-fleet-graceful: cannot resolve home for user '$user'" >&2
        exit 1
      fi
      exec ${pkgs.util-linux}/bin/runuser -u "$user" -- ${pkgs.coreutils}/bin/env -i \
        HOME="$home" \
        USER="$user" \
        LOGNAME="$user" \
        XDG_RUNTIME_DIR="/run/user/$uid" \
        PATH="/run/current-system/sw/bin:/run/wrappers/bin" \
        "$0" "$@"
    fi
  '';

  preScript = pkgs.writeShellScriptBin "spore-fleet-graceful-pre" ''
    ${asUserPreamble}

    project_root='${toString cfg.projectRoot}'
    project="$(${pkgs.coreutils}/bin/basename "$project_root")"
    timeout=${toString cfg.gracefulDeploy.timeout}
    message='${cfg.gracefulDeploy.message}'
    sporecli='${cfg.package}/bin/spore'
    tmuxcli='${pkgs.tmux}/bin/tmux'

    cd "$project_root"

    echo "spore-fleet-graceful: disabling kill-switch" >&2
    "$sporecli" fleet disable || true

    list_workers() {
      "$tmuxcli" list-sessions -F '#{session_name}' 2>/dev/null \
        | ${pkgs.gnugrep}/bin/grep -E "^spore/$project/" \
        | ${pkgs.gnugrep}/bin/grep -v "^spore/$project/coordinator$" || true
    }

    sessions="$(list_workers)"
    if [ -z "$sessions" ]; then
      echo "spore-fleet-graceful: no active workers" >&2
      exit 0
    fi

    while IFS= read -r s; do
      slug="''${s##spore/$project/}"
      echo "spore-fleet-graceful: signalling $slug" >&2
      "$sporecli" task tell "$slug" "$message" || true
    done <<< "$sessions"

    deadline=$(( $(${pkgs.coreutils}/bin/date +%s) + timeout ))
    while [ "$(${pkgs.coreutils}/bin/date +%s)" -lt "$deadline" ]; do
      remaining="$(list_workers | ${pkgs.gnugrep}/bin/grep -c '^' || true)"
      if [ "$remaining" = "0" ]; then
        echo "spore-fleet-graceful: workers drained" >&2
        exit 0
      fi
      ${pkgs.coreutils}/bin/sleep 2
    done

    echo "spore-fleet-graceful: timeout (''${timeout}s); killing remaining workers" >&2
    list_workers | while IFS= read -r s; do
      [ -z "$s" ] && continue
      "$tmuxcli" kill-session -t "$s" || true
    done
  '';

  postScript = pkgs.writeShellScriptBin "spore-fleet-graceful-post" ''
    ${asUserPreamble}

    sporecli='${cfg.package}/bin/spore'
    echo "spore-fleet-graceful: re-enabling kill-switch" >&2
    "$sporecli" fleet enable
  '';
in
{
  options.services.spore-fleet = {
    enable = lib.mkEnableOption "spore fleet reconciler (systemd-user)";

    package = lib.mkOption {
      type = lib.types.package;
      defaultText = lib.literalExpression "spore.packages.\${system}.spore";
      description = ''
        Spore CLI package. ExecStart runs `spore fleet reconcile`
        from this package on every timer tick.
      '';
    };

    claudeCodePackage = lib.mkOption {
      type = lib.types.package;
      defaultText = lib.literalExpression "claude-code.packages.\${system}.default";
      description = ''
        claude-code CLI placed on the unit's PATH so workers spawned
        by the reconciler can invoke `claude` directly.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      example = "spore";
      description = ''
        User account the reconciler runs under. Required: a user-
        service needs a real account to install under (the module
        does not declare the user; ensure it exists via
        `users.users.<name>` outside this module). home-manager
        wiring for this user is assumed.
      '';
    };

    projectRoot = lib.mkOption {
      type = lib.types.path;
      example = "/home/spore/project";
      description = ''
        Project tree containing tasks/. The reconciler scans
        `''${projectRoot}/tasks` and creates worker worktrees under
        `''${projectRoot}/.worktrees/<slug>`. Must be writable by
        `services.spore-fleet.user`.
      '';
    };

    maxWorkers = lib.mkOption {
      type = lib.types.ints.positive;
      default = 3;
      description = ''
        Concurrency cap. Wired through SPORE_FLEET_MAX_WORKERS so
        an explicit `[fleet] max_workers` in the project's
        spore.toml still wins (matching `spore fleet reconcile`
        precedence: --max-workers > env > spore.toml > built-in
        default).
      '';
    };

    interval = lib.mkOption {
      type = lib.types.str;
      default = "60s";
      description = ''
        Timer interval between reconcile passes. Combined with the
        Path watchers on tasks/ and the kill-switch flag, so
        flipping `spore fleet enable` or committing a new active
        task is responsive even on a slow timer.
      '';
    };

    hostId = lib.mkOption {
      type = lib.types.str;
      default = config.networking.hostName;
      defaultText = lib.literalExpression "config.networking.hostName";
      description = ''
        Free-form identifier surfaced as SPORE_HOST_ID for logs and
        operator-facing chips when more than one host runs a fleet
        against the same project tree. Disambiguation only; spore
        does not coordinate across hosts.
      '';
    };

    extraEnv = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      example = lib.literalExpression ''{ SPORE_LOG = "debug"; }'';
      description = ''
        Extra entries merged into the unit's Environment=. Values
        flow through Nix evaluation and the /nix/store; never put a
        secret here. Use `credentialFiles` for those.
      '';
    };

    credentialFiles = lib.mkOption {
      type = lib.types.attrsOf lib.types.path;
      default = { };
      example = lib.literalExpression ''
        {
          github-pat = config.age.secrets.spore-github-pat.path;
        }
      '';
      description = ''
        Per-credential files exposed to the unit via systemd
        LoadCredential=. The reconciler (and the workers it
        spawns under the same unit) read decrypted material from
        the directory pointed at by $CREDENTIALS_DIRECTORY. Values
        never appear in Nix evaluation or in /nix/store; the path
        is dereferenced by systemd at activation time, so an
        agenix-decrypted file at /run/agenix/<name> works as input.

        The reconciler does NOT take an Anthropic API key from
        here. Workers spawn `claude` (claude-code), which manages
        its own credential lifecycle inside the client; this slot
        is for non-claude secrets the workers happen to need (MCP
        server keys, git-push PATs, etc.).
      '';
    };

    gracefulDeploy = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Wire pre/post-activation hooks that drain active workers
          before a `nixos-rebuild switch` (or colmena deploy) and
          re-enable the reconciler after. Disable when the host is
          a one-off worker tier whose tasks should never see a
          wrap-up signal.
        '';
      };

      timeout = lib.mkOption {
        type = lib.types.ints.positive;
        default = 60;
        description = ''
          Seconds to wait for active workers to flush after the
          wrap-up signal before the pre-activation script kills
          remaining tmux sessions with SIGTERM.
        '';
      };

      message = lib.mkOption {
        type = lib.types.str;
        default = "wrap-up: deployment incoming";
        description = ''
          Body of the inbox message dropped into each active
          worker's inbox during the pre-activation drain. Workers
          should treat it as a request to flush in-progress notes
          to the task file before they get torn down.
        '';
      };

      preScript = lib.mkOption {
        type = lib.types.str;
        readOnly = true;
        description = ''
          Absolute path to the pre-activation script. Drop into
          colmena's `deployment.preActivation` to drive the same
          drain remotely. The script re-execs as
          `services.spore-fleet.user` via `runuser` and is safe to
          call from a root shell.
        '';
      };

      postScript = lib.mkOption {
        type = lib.types.str;
        readOnly = true;
        description = ''
          Absolute path to the post-activation script. Drop into
          colmena's `deployment.postActivation` to re-enable the
          fleet kill-switch after a successful deploy.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable {
    services.spore-fleet.gracefulDeploy = {
      preScript = "${preScript}/bin/spore-fleet-graceful-pre";
      postScript = "${postScript}/bin/spore-fleet-graceful-post";
    };

    # Wire the pre/post hooks into NixOS system activation so a
    # `nixos-rebuild switch` (and colmena, which lifts the same
    # activation flow on the remote) drains workers before the new
    # systemd-user units load and re-enables the kill-switch after.
    # The NIXOS_ACTION gate keeps boot-time activation a no-op: there
    # is nothing to drain on a fresh boot, and disabling the flag
    # there would leave the reconciler paused until the next deploy.
    system.activationScripts = lib.mkIf cfg.gracefulDeploy.enable {
      spore-fleet-pre.text = ''
        case "''${NIXOS_ACTION:-}" in
          switch|test) ${preScript}/bin/spore-fleet-graceful-pre || true ;;
        esac
      '';
      spore-fleet-post = {
        deps = [ "spore-fleet-pre" ];
        text = ''
          case "''${NIXOS_ACTION:-}" in
            switch|test) ${postScript}/bin/spore-fleet-graceful-post || true ;;
          esac
        '';
      };
    };

    home-manager.users.${cfg.user} = {
      systemd.user.services.spore-fleet-reconcile = {
        Unit = {
          Description = "spore fleet reconciler (host=${cfg.hostId})";
        };
        Service = {
          Type = "oneshot";
          WorkingDirectory = toString cfg.projectRoot;
          ExecStart = "${cfg.package}/bin/spore fleet reconcile";
          Environment = lib.mapAttrsToList (n: v: "${n}=${v}") (
            {
              SPORE_FLEET_MAX_WORKERS = toString cfg.maxWorkers;
              SPORE_HOST_ID = cfg.hostId;
              PATH = lib.makeBinPath [
                cfg.package
                cfg.claudeCodePackage
                pkgs.git
                pkgs.tmux
              ];
            } // cfg.extraEnv
          );
          NoNewPrivileges = true;
          LockPersonality = true;
          RestrictSUIDSGID = true;
          ReadWritePaths = [ (toString cfg.projectRoot) ];
          LoadCredential = lib.mapAttrsToList
            (name: path: "${name}:${toString path}")
            cfg.credentialFiles;
        };
      };

      systemd.user.timers.spore-fleet-reconcile = {
        Unit.Description = "Periodic spore fleet reconcile";
        Timer = {
          OnBootSec = "30s";
          OnUnitInactiveSec = cfg.interval;
          AccuracySec = "5s";
          Unit = "spore-fleet-reconcile.service";
        };
        Install.WantedBy = [ "timers.target" ];
      };

      systemd.user.paths = {
        spore-fleet-reconcile-flag = {
          Unit.Description = "Trigger spore-fleet-reconcile when the kill-switch flag changes";
          Path = {
            PathChanged = "%h/${stateRel}";
            Unit = "spore-fleet-reconcile.service";
          };
          Install.WantedBy = [ "default.target" ];
        };

        spore-fleet-reconcile-tasks = {
          Unit.Description = "Trigger spore-fleet-reconcile when tasks/ changes";
          Path = {
            PathChanged = "${toString cfg.projectRoot}/tasks";
            Unit = "spore-fleet-reconcile.service";
          };
          Install.WantedBy = [ "default.target" ];
        };
      };
    };
  };
}
