{
  description = "great-machine";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    systems.url = "github:nix-systems/default";

    gonixgo = {
      url = "github:draganm/gonixgo/v0.2.0";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.systems.follows = "systems";
    };

  };

  outputs = { self, nixpkgs, systems, gonixgo, ... }@inputs:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs (import systems)
        (system: f system nixpkgs.legacyPackages.${system});

      # What `great-machine version` prints: the commit the program was
      # built from, with -dirty when the tree had changes.
      version = self.shortRev or self.dirtyShortRev or "dev";
    in {

      # Evaluating a gonixgo package runs `gonixgo resolve` through
      # builtins.exec, so every command that touches `packages` needs
      #   --option allow-unsafe-native-code-during-evaluation true
      packages = eachSystem (system: pkgs:
        let
          # This pkgs builds gonixgo's own tool and the Go program.
          goEnv = gonixgo.lib.mkGoEnv { inherit pkgs; };
        in {
          # The one program: draal, zathras and the operator's commands.
          # Without cgo it is linked statically on Linux, and runs in an
          # image that has no libc.
          default = goEnv.buildGoApplication {
            pname = "great-machine";
            src = ./.;
            subPackages = [ "cmd/great-machine" ];
            CGO_ENABLED = 0;
            ldflags = [
              "-X github.com/fables-for-robots/great-machine/cli.Version=${version}"
            ];
          };
        });

      # The shell carries the built program, so entering it (or direnv
      # reloading it after a source change, see .envrc) rebuilds it. It
      # therefore needs the same evaluation option as `packages`.
      devShells = eachSystem (system: pkgs: {
        default = pkgs.mkShell {
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go sqlc self.packages.${system}.default ];
        };

        # The same tools without the built program. The default shell
        # cannot be entered while the module does not resolve, for example
        # after a `go get` that left go.sum behind; this one can, to run
        # `go mod tidy`. It needs no evaluation option.
        bare = pkgs.mkShell {
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go sqlc ];
        };
      });
    };
}
