{ config, lib, pkgs, ... }:

let
  cfg = config.services.spore-fleet;
  stateRel = ".local/state/spore/fleet-enabled";
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
  };

  config = lib.mkIf cfg.enable {
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
