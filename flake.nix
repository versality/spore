{
  description = "spore - drop-in harness template for LLM-coding agents";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    claude-code = {
      url = "github:sadjow/claude-code-nix";
    };
  };

  outputs =
    { self, nixpkgs, flake-utils, home-manager, claude-code }:
    let
      perSystem = flake-utils.lib.eachDefaultSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          version = pkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);
          commit =
            if self ? rev then self.rev
            else if self ? dirtyRev then self.dirtyRev
            else "unknown";

          spore = pkgs.buildGoModule {
            pname = "spore";
            inherit version;
            src = ./.;
            subPackages = [ "cmd/spore" ];
            vendorHash = null;
            ldflags = [ "-X=github.com/versality/spore.buildCommit=${commit}" ];
            meta = {
              description = "Drop-in harness template for LLM-coding agents.";
              homepage = "https://github.com/versality/spore";
              license = pkgs.lib.licenses.asl20;
              mainProgram = "spore";
              platforms = pkgs.lib.platforms.unix;
            };
          };
        in
        {
          packages = {
            inherit spore;
            default = spore;
          };

          apps = {
            spore = {
              type = "app";
              program = "${spore}/bin/spore";
              meta = {
                description = "spore kernel CLI";
                mainProgram = "spore";
              };
            };
            default = {
              type = "app";
              program = "${spore}/bin/spore";
              meta = {
                description = "spore kernel CLI";
                mainProgram = "spore";
              };
            };
            claude-code = {
              type = "app";
              program = "${claude-code.packages.${system}.default}/bin/claude";
              meta = {
                description = "claude-code CLI tracked from sadjow/claude-code-nix";
                mainProgram = "claude";
              };
            };
          };

          devShells.default = pkgs.mkShell {
            packages = (with pkgs; [
              go
              golangci-lint
              govulncheck
              gopls
              just
              jq
              nixpkgs-fmt
              tmux
              fzf
              ripgrep
            ]) ++ [
              claude-code.packages.${system}.default
            ];
          };

          checks = {
            go-fmt = pkgs.runCommand "spore-go-fmt"
              {
                nativeBuildInputs = [ pkgs.go ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              unformatted="$(find . -name '*.go' | sort | xargs gofmt -l)"
              if [ -n "$unformatted" ]; then
                echo "gofmt needed:"
                echo "$unformatted"
                exit 1
              fi
              touch $out
            '';
            go-vet = pkgs.runCommand "spore-go-vet"
              {
                nativeBuildInputs = [ pkgs.go ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go vet ./...
              touch $out
            '';
            golangci-lint = pkgs.runCommand "spore-golangci-lint"
              {
                nativeBuildInputs = [ pkgs.go pkgs.golangci-lint ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              golangci-lint run ./...
              touch $out
            '';
            go-test = pkgs.runCommand "spore-go-test"
              {
                nativeBuildInputs = [ pkgs.go pkgs.git pkgs.just ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go test ./...
              touch $out
            '';
            spore-lint = pkgs.runCommand "spore-lint"
              {
                nativeBuildInputs = [ pkgs.go pkgs.git ];
              } ''
              cp -r ${./.}/. ./src
              cd src
              chmod -R u+w .
              git init -q
              git -c user.email=t@t -c user.name=t add -A
              git -c user.email=t@t -c user.name=t commit -q -m seed
              export HOME=$TMPDIR
              export GOCACHE=$TMPDIR/gocache
              export GOMODCACHE=$TMPDIR/gomod
              export CGO_ENABLED=0
              go run ./cmd/spore lint
              touch $out
            '';
          } // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
            nixosModules-spore-fleet = pkgs.testers.runNixOSTest {
              name = "spore-fleet-module";
              nodes.machine = { config, lib, pkgs, ... }: {
                imports = [
                  self.nixosModules.spore-fleet
                  home-manager.nixosModules.home-manager
                ];

                users.users.spore-test = {
                  isNormalUser = true;
                  home = "/home/spore-test";
                  createHome = true;
                  linger = true;
                };

                home-manager.useGlobalPkgs = true;
                home-manager.useUserPackages = true;
                home-manager.users.spore-test.home.stateVersion = config.system.stateVersion;

                services.spore-fleet = {
                  enable = true;
                  user = "spore-test";
                  projectRoot = "/home/spore-test/project";
                  # Stub spore CLI: log every invocation to a per-user
                  # trace file the test inspects after the activation
                  # scripts run, then handle the subset of subcommands
                  # the graceful-deploy hooks invoke.
                  package = pkgs.writeShellScriptBin "spore" ''
                    trace="''${HOME:-/tmp}/spore-stub.log"
                    echo "$*" >> "$trace"
                    case "$1 $2" in
                      "fleet reconcile") echo "stub: reconcile" ; exit 0 ;;
                      "fleet enable") : > "''${HOME}/.local/state/spore/fleet-enabled" ; exit 0 ;;
                      "fleet disable") rm -f "''${HOME}/.local/state/spore/fleet-enabled" ; exit 0 ;;
                      "task tell") shift 2; echo "stub: tell $*" ; exit 0 ;;
                      *) echo "stub spore: unknown $*" >&2; exit 1 ;;
                    esac
                  '';
                  claudeCodePackage = pkgs.writeShellScriptBin "claude" "exit 0";
                  gracefulDeploy.timeout = 5;
                };

                systemd.tmpfiles.rules = [
                  "d /home/spore-test/project 0750 spore-test users -"
                  "d /home/spore-test/.local 0755 spore-test users -"
                  "d /home/spore-test/.local/state 0755 spore-test users -"
                  "d /home/spore-test/.local/state/spore 0755 spore-test users -"
                ];
              };
              testScript = { nodes, ... }:
                let
                  preScript = nodes.machine.services.spore-fleet.gracefulDeploy.preScript;
                  postScript = nodes.machine.services.spore-fleet.gracefulDeploy.postScript;
                in
                ''
                  machine.wait_for_unit("multi-user.target")
                  machine.wait_for_unit("default.target", user="spore-test")
                  # Talk to the lingered user instance via systemctl
                  # --machine so the call uses the right XDG_RUNTIME_DIR
                  # without an interactive login.
                  def usercmd(cmd):
                      return f"systemctl --machine=spore-test@.host --user {cmd}"
                  # Oneshot; trigger it and assert exit 0 plus that the
                  # timer + path watchers are active.
                  machine.succeed(usercmd("start spore-fleet-reconcile.service"))
                  machine.succeed(usercmd("is-active spore-fleet-reconcile.timer"))
                  machine.succeed(usercmd("is-active spore-fleet-reconcile-flag.path"))
                  machine.succeed(usercmd("is-active spore-fleet-reconcile-tasks.path"))

                  # Graceful-deploy: pre-script disables the kill-switch
                  # and tells every active worker to wrap up; post-script
                  # re-enables it. With no live tmux sessions the pre-
                  # script exits after the disable + "no active workers"
                  # branch and never blocks on the timeout.
                  machine.succeed("touch /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("chown spore-test:users /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("${preScript}")
                  machine.fail("test -e /home/spore-test/.local/state/spore/fleet-enabled")
                  machine.succeed("${postScript}")
                  machine.succeed("test -e /home/spore-test/.local/state/spore/fleet-enabled")

                  # The pre-script must have invoked `spore fleet disable`
                  # via the stub; the trace confirms re-exec as the user
                  # and cwd handling worked.
                  machine.succeed("grep -q 'fleet disable' /home/spore-test/spore-stub.log")
                  machine.succeed("grep -q 'fleet enable' /home/spore-test/spore-stub.log")
                '';
            };
          };

          formatter = pkgs.nixpkgs-fmt;
        });
    in
    perSystem // {
      nixosModules.spore-fleet = { pkgs, lib, ... }: {
        imports = [ ./nixosModules/spore-fleet.nix ];
        services.spore-fleet.package =
          lib.mkDefault self.packages.${pkgs.stdenv.hostPlatform.system}.spore;
        services.spore-fleet.claudeCodePackage =
          lib.mkDefault claude-code.packages.${pkgs.stdenv.hostPlatform.system}.default;
      };
      nixosModules.default = self.nixosModules.spore-fleet;
    };
}
