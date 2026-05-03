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

          spore = pkgs.buildGoModule {
            pname = "spore";
            version = "0.0.1";
            src = ./.;
            subPackages = [ "cmd/spore" ];
            vendorHash = null;
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
                  package = pkgs.writeShellScriptBin "spore" ''
                    case "$1 $2" in
                      "fleet reconcile") echo "stub: reconcile" ; exit 0 ;;
                      *) echo "stub spore: unknown $*" >&2; exit 1 ;;
                    esac
                  '';
                  claudeCodePackage = pkgs.writeShellScriptBin "claude" "exit 0";
                };

                systemd.tmpfiles.rules = [
                  "d /home/spore-test/project 0750 spore-test users -"
                ];
              };
              testScript = ''
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
