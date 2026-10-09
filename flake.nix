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

      # What `great-machine version` prints. A release is built with the
      # tag in GREAT_MACHINE_VERSION and --impure, without which Nix reads
      # no environment; every other build is named by its commit, with
      # -dirty when the tree had changes.
      version = let fromEnv = builtins.getEnv "GREAT_MACHINE_VERSION";
      in if fromEnv != "" then
        fromEnv
      else
        self.shortRev or self.dirtyShortRev or "dev";
    in {

      # Evaluating a gonixgo package runs `gonixgo resolve` through
      # builtins.exec, so every command that touches `packages` needs
      #   --option allow-unsafe-native-code-during-evaluation true
      packages = eachSystem (system: pkgs:
        let
          # This pkgs builds gonixgo's own tool and the Go program.
          #
          # The resolver runs on the machine that evaluates. That is this
          # system, unless a package of another system is asked for, as
          # the Linux image is on a Mac: then --impure tells which machine
          # evaluates, and its nixpkgs runs the resolver.
          goEnv = gonixgo.lib.mkGoEnv {
            inherit pkgs;
            evalPkgs = if builtins ? currentSystem
            && builtins.currentSystem != system then
              nixpkgs.legacyPackages.${builtins.currentSystem}
            else
              null;
          };

          # The one program: draal, zathras and the operator's commands.
          # Without cgo it is linked statically on Linux, and runs in an
          # image that has no libc and whose /nix/store is another one.
          great-machine = goEnv.buildGoApplication {
            pname = "great-machine";
            src = ./.;
            subPackages = [ "cmd/great-machine" ];
            CGO_ENABLED = 0;
            ldflags = [
              "-X github.com/fables-for-robots/great-machine/cli.Version=${version}"
            ];
          };

          busybox = pkgs.pkgsStatic.busybox;
        in {
          default = great-machine;
        } // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          # The image of the app container of a pod, and of draal:
          #   nix build .#image && docker load < result
          #
          # Nothing it needs lives in /nix/store, because a pod mounts the
          # sidecar's store there: the program, busybox and the
          # certificates are copied to where an image has them. That
          # works because all three are files that need no other.
          image = pkgs.dockerTools.buildLayeredImage {
            name = "ghcr.io/fables-for-robots/great-machine";
            tag = "nix";
            extraCommands = ''
              mkdir -p bin etc/ssl/certs root
              mkdir -m 1777 tmp

              cp ${great-machine}/bin/great-machine bin/great-machine

              # busybox is /bin/sh and the tools of a shell session.
              cp ${busybox}/bin/busybox bin/busybox
              for tool in $(bin/busybox --list); do
                [ -e "bin/$tool" ] || ln -s busybox "bin/$tool"
              done

              # iroh's relays are reached over HTTPS.
              cp ${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt etc/ssl/certs/ca-certificates.crt

              echo 'root:x:0:0:root:/root:/bin/sh' > etc/passwd
              echo 'root:x:0:' > etc/group
            '';
            config = {
              Entrypoint = [ "great-machine" ];
              Env = [
                "PATH=/bin"
                "HOME=/root"
                "SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt"
              ];
            };
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
