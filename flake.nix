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

      # The Node that builds the page, in the derivation and in the
      # shells: the same one, so that `npm ci` in a shell and the
      # derivation read package-lock.json alike.
      nodejs = pkgs: pkgs.nodejs_24;

      # The page: ui/ built by Vite. Its output is what web/dist holds
      # after `npm run build`: index.html, assets/ and the placeholder.
      #
      # npmDepsHash is the hash of the dependencies package-lock.json
      # names. After any change of ui/package-lock.json:
      #   nix run nixpkgs#prefetch-npm-deps ui/package-lock.json
      # prints the new one. With a stale hash the build fails and names
      # the right one in its "got:" line.
      ui = pkgs:
        pkgs.buildNpmPackage {
          pname = "great-machine-ui";
          version = "0";
          src = ./ui;
          nodejs = nodejs pkgs;
          npmDepsHash = "sha256-iF9vMOHv9JOptMwCe4LclyvzZbTkk8c1x3Sz2dl0ctI=";
          # vite.config.ts writes the build here instead of ../web/dist,
          # which is outside what a derivation may write.
          env.GREAT_MACHINE_UI_DIST = "dist";
          installPhase = ''
            runHook preInstall
            cp -r dist $out
            runHook postInstall
          '';
        };
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
          evalPkgs = if builtins ? currentSystem && builtins.currentSystem
          != system then
            nixpkgs.legacyPackages.${builtins.currentSystem}
          else
            null;
          # Buildbarn's packages, of which the mount of a zathras is
          # made, need Go 1.27, which is not yet the Go of this nixpkgs.
          # The Go that resolves the packages while Nix evaluates has to
          # be the same one.
          goEnv = gonixgo.lib.mkGoEnv {
            inherit pkgs evalPkgs;
            go = pkgs.buildPackages.go_1_27;
            evalGo = if evalPkgs != null then evalPkgs.go_1_27 else null;
          };

          # The source gonixgo builds: the repository with the build of
          # the page at web/dist, where the package web embeds it.
          #
          # gonixgo asks `go list` while Nix evaluates which files a
          # package embeds, so this source has to exist then: Nix builds
          # it, and the page before it, during evaluation, on the machine
          # that evaluates. That is why both come from that machine's
          # nixpkgs. The files of the page are the same wherever they are
          # built, and gonixgo copies the files of each package out of
          # this source, so the derivations of the program do not depend
          # on what machine evaluated them.
          src = let
            p = if evalPkgs != null then evalPkgs else pkgs;
            page = ui p;
          in p.runCommand "great-machine-src" { } ''
            cp -r ${./.} $out
            chmod -R u+w $out
            cp -r ${page}/. $out/web/dist/
          '';

          # The one program: draal, zathras and the operator's commands.
          # Without cgo it is linked statically on Linux, and runs in an
          # image that has no libc and whose /nix/store is the one zathras
          # mounts.
          great-machine = goEnv.buildGoApplication {
            pname = "great-machine";
            inherit src;
            subPackages = [ "cmd/great-machine" ];
            CGO_ENABLED = 0;
            ldflags = [
              "-X github.com/fables-for-robots/great-machine/cli.Version=${version}"
            ];
            meta.license = pkgs.lib.licenses.lgpl3Only;
          };

          busybox = pkgs.pkgsStatic.busybox;
        in {
          default = great-machine;
          ui = ui pkgs;
        } // nixpkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
          # The image of the app container of a pod, and of draal:
          #   nix build .#image && docker load < result
          #
          # Nothing it needs lives in /nix/store, because zathras mounts
          # the store of its service there: the program, busybox and the
          # certificates are copied to where an image has them. That
          # works because all three are files that need no other.
          #
          # And nothing at all is in its /nix/store, which zathras
          # refuses to mount over otherwise. Left to itself Nix puts
          # there what the copies name: busybox names itself, and the
          # Go of nixpkgs writes into every program where it would look
          # for the time zones, the MIME types and the names of
          # protocols and ports when the machine has none (tzdata,
          # mailcap, iana-etc). Neither program needs what it names, so
          # the image is made without store paths.
          image = pkgs.dockerTools.buildLayeredImage {
            name = "ghcr.io/fables-for-robots/great-machine";
            tag = "nix";
            includeStorePaths = false;
            extraCommands = ''
              mkdir -p bin etc/ssl/certs root
              mkdir -m 1777 tmp

              # What zathras mounts the store on, and where it keeps
              # what it fetched unless a volume is put there.
              mkdir -p nix/store var/cache/great-machine

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

          packages = with pkgs; [
            go_1_27
            sqlc
            (nodejs pkgs)
            self.packages.${system}.default
          ];
        };

        # The same tools without the built program and the built page. The default shell
        # cannot be entered while the module does not resolve, for example
        # after a `go get` that left go.sum behind; this one can, to run
        # `go mod tidy`. It needs no evaluation option.
        bare = pkgs.mkShell {
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go_1_27 sqlc (nodejs pkgs) ];
        };
      });
    };
}
