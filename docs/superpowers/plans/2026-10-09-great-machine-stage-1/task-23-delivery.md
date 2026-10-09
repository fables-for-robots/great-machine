### Task 23: Delivery

The image, the files of a deployment, the release, and the README. These
are files and no code with tests, so the "test" of a step is a validation:
an evaluation, a linter, a build.

**Files:**
- Modify: `flake.nix` (the version from the environment, the resolver for a package of another system, `packages.<linux>.image`)
- Modify: `.github/workflows/test.yml` (a step `image` at the end)
- Create: `.github/workflows/release.yml`
- Create: `deploy/pod.yaml`
- Create: `deploy/draal.yaml`
- Create: `README.md`

**Interfaces:**
- Consumes:
  - `cli.Version`, set by the linker with
    `-X github.com/fables-for-robots/great-machine/cli.Version=<version>`;
    `great-machine version` prints it and a line feed.
  - The commands `draal`, `zathras`, `id`, `rollout`, `rollback`, `status`,
    `logs`, `shell` and `version`, with the flags and variables of section 3
    of the spec, without `--web-addr`.
  - The health check of zathras: `GET /healthz` on port 9890.
  - `.github/workflows/test.yml` of Task 1: one job, `test`, whose `run`
    steps run in the flake's `bare` shell and whose last step is
    `nix build`.
- Produces: nothing a later task relies on.
  - `packages.x86_64-linux.image` and `packages.aarch64-linux.image`: the
    image as a `docker load` archive, named
    `ghcr.io/fables-for-robots/great-machine:nix`.
  - `GREAT_MACHINE_VERSION` in the environment of a `nix build --impure`
    is what `great-machine version` prints.

- [ ] **Step 1: The flake**

Three things change in `flake.nix`: `version` is read from the
environment first; `mkGoEnv` is given `evalPkgs`, so that a package of
another system can be evaluated; and the Linux systems get `image`. The
whole file, as it is after this step (if an earlier task changed the file
in another place, keep that change):

```nix
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
```

Why the image is made this way, for whoever changes it: `dockerTools`
puts what an image contains in `/nix/store` and links to it from `/bin`.
In a pod the sidecar's store is mounted over `/nix/store`, and those links
would point at nothing. So the three files are copied, and all three
stand alone: the program is linked statically, busybox is the static one,
and the certificates are one file.

- [ ] **Step 2: Evaluate the flake, and build what this machine can**

Run:

```bash
git add flake.nix
nix eval --impure --option allow-unsafe-native-code-during-evaluation true .#packages.x86_64-linux.image.drvPath
nix eval --impure --option allow-unsafe-native-code-during-evaluation true .#packages.aarch64-linux.image.drvPath
nix eval --impure --option allow-unsafe-native-code-during-evaluation true .#packages.aarch64-darwin --apply builtins.attrNames
GREAT_MACHINE_VERSION=v0.0.0-test nix build --impure --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
rm result
```

Expected: two different paths that end in `-great-machine.tar.gz.drv`;
`[ "default" ]`, as a Mac has no image; and `v0.0.0-test`.

On a Mac the two `nix eval` of a Linux package need `--impure`. Without
it Nix tries to build gonixgo's resolver for Linux and fails with
`a 'x86_64-linux' with features {} is required to build …`.

- [ ] **Step 3: Build the image and run it**

On Linux:

```bash
GREAT_MACHINE_VERSION=v0.0.0-test nix build .#image --impure \
  --option allow-unsafe-native-code-during-evaluation true
docker load < result
rm result
```

On a Mac with Docker, the same build in a container of the Mac's own
architecture:

```bash
out=$(mktemp -d)
docker run --rm -v "$PWD":/src:ro -v "$out":/out nixos/nix:2.26.1 sh -c '
  set -e
  cp -r /src /work && cd /work && rm -f result
  git config --global --add safe.directory /work
  GREAT_MACHINE_VERSION=v0.0.0-test nix --extra-experimental-features "nix-command flakes" \
    build .#image --impure --option allow-unsafe-native-code-during-evaluation true
  cp -L result /out/image.tar.gz'
docker load < "$out/image.tar.gz"
rm -r "$out"
```

Either way, then:

```bash
IMAGE=ghcr.io/fables-for-robots/great-machine:nix
docker run --rm --tmpfs /nix/store "$IMAGE" version
docker run --rm --tmpfs /nix/store --entrypoint sh "$IMAGE" -c \
  'ls -ld /bin/sh /tmp; ls /nix/store | wc -l; whoami; wc -c < /etc/ssl/certs/ca-certificates.crt'
docker image rm "$IMAGE"
```

Expected: `Loaded image: ghcr.io/fables-for-robots/great-machine:nix`, then

```
v0.0.0-test
lrwxrwxrwx    1 root     root             7 Jan  1  1980 /bin/sh -> busybox
drwxrwxrwt    2 root     root          4096 Jan  1  1980 /tmp
0
root
472033
```

(the size of the certificates is that of the nixpkgs of `flake.lock`, some
hundred thousand bytes). `--tmpfs /nix/store` puts an empty directory
where a pod has the sidecar's store: the image has to run without its own.
The last command removes the image again. Where there is neither Linux nor
Docker, this step is left to CI, which Step 6 gives it to.

- [ ] **Step 4: Commit**

```bash
git add flake.nix
git commit -m "Flake: the image, and the version of a release"
```

- [ ] **Step 5: The deployment files**

`deploy/pod.yaml`:

```yaml
# A pod of the service `web`: its app container runs zathras, which runs
# whatever draal rolls out, out of a Nix store the pod does not hold. The
# sidecar fetches every store path from a jaccard-store server.
#
# What to change: the server's endpoint ID and the prefix, which are those
# draal was given; draal's endpoint ID; and the name of the service. In a
# Deployment or a StatefulSet the `spec` below is the template's, and
# every pod is a zathras under its own name.
apiVersion: v1
kind: Pod
metadata:
  name: web-example
spec:
  # zathras gives the service 2 minutes to end after SIGTERM
  # (--stop-timeout) before it kills it. The kubelet has to wait longer
  # than that, or it kills zathras first.
  terminationGracePeriodSeconds: 150
  initContainers:
    # An init container that restarts is a sidecar: it is started before
    # the app containers and stopped after them.
    - name: nix-store
      image: ghcr.io/amber-store/jaccard-nfs-nix-store:latest
      restartPolicy: Always
      securityContext:
        # It mounts, and a mount that is to reach the other containers
        # (Bidirectional) is allowed to privileged containers alone.
        privileged: true
      env:
        - name: JACCARD_SERVER
          value: "0000000000000000000000000000000000000000000000000000000000000000"
        # Put before a name as it is. `great-machine rollout` pushes a
        # store path under the prefix draal has, so the two are the same.
        - name: JACCARD_PREFIX
          value: /great-machine/nix/store/
        - name: JACCARD_NFS_CACHE
          value: /cache
        - name: JACCARD_NFS_MOUNT
          value: /export
      volumeMounts:
        - name: store
          mountPath: /export
          mountPropagation: Bidirectional
        - name: cache
          mountPath: /cache
      # The app container starts when the store is mounted, not before.
      # The directory is a mount point from the start, the volume's, so
      # what is looked for is the NFS mount on it.
      startupProbe:
        exec:
          command: ["grep", "-q", " /export nfs4 ", "/proc/mounts"]
        periodSeconds: 1
        failureThreshold: 60
  containers:
    - name: app
      # The image has zathras, a shell and its tools, and nothing of the
      # service: that comes from the store.
      image: ghcr.io/fables-for-robots/great-machine:latest
      command: ["great-machine", "zathras"]
      env:
        - name: GREAT_MACHINE_DRAAL
          value: "1111111111111111111111111111111111111111111111111111111111111111"
        - name: GREAT_MACHINE_SERVICE
          value: web
        # The name the operator sees is the host name, which is the pod's
        # name. zathras reaches the sidecar's preload endpoint at
        # 127.0.0.1:9889, which the containers of a pod share.
      volumeMounts:
        - name: store
          mountPath: /nix/store
          # It receives the sidecar's mount and can make none of its own.
          mountPropagation: HostToContainer
      # Ready while the service runs, and at no other time: not before
      # the first rollout, not while the service waits to be started
      # again. Not a liveness probe: that would have the kubelet restart
      # the container of a service that stays down, and with the
      # container go the backoff of zathras, its log and its identity.
      readinessProbe:
        httpGet:
          path: /healthz
          port: 9890
        periodSeconds: 2
        failureThreshold: 1
  volumes:
    # What the sidecar mounts its server on, and the app sees as the store.
    - name: store
      emptyDir: {}
    # What was fetched. It lives as long as the pod, across restarts of
    # the sidecar.
    - name: cache
      emptyDir: {}
```

`deploy/draal.yaml`:

```yaml
# draal, the controller: one of it, with a volume for its key and its
# database.
#
# What to change: the endpoint IDs of the operators (`great-machine id`
# prints one), the jaccard-store server's endpoint ID and the prefix,
# which the sidecars of the pods are given too.
#
# The endpoint ID of draal is what the pods and the operators need. draal
# logs it when it starts:
#
#   kubectl logs deployment/draal | grep -i 'endpoint'
#
# It is that of the key in the volume, and stays as long as the volume.
#
# draal listens on no TCP port, and needs no Service: zathrases and
# operators reach it over iroh by its endpoint ID.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: draal-data
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      # A key and a database of rollouts: megabytes.
      storage: 1Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: draal
spec:
  # More than one draal for a fleet is not supported.
  replicas: 1
  # The old draal is gone before the new one starts: two would open the
  # same database, and evict each other at the relay, having one key.
  # While there is none, every service goes on running.
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: draal
  template:
    metadata:
      labels:
        app: draal
    spec:
      containers:
        - name: draal
          image: ghcr.io/fables-for-robots/great-machine:latest
          # The image's entrypoint is great-machine.
          args: ["draal"]
          env:
            # Holds draal.key, created on first run, and draal.sqlite.
            - name: GREAT_MACHINE_DATA
              value: /data
            # Who may roll out, read logs and open shells: endpoint IDs
            # with commas between them. Without any, nobody can.
            - name: GREAT_MACHINE_OPERATORS
              value: "2222222222222222222222222222222222222222222222222222222222222222"
            # Where `great-machine rollout` pushes closures to.
            - name: JACCARD_SERVER
              value: "0000000000000000000000000000000000000000000000000000000000000000"
            # Put before the name of a store path as it is, to make its
            # reference. The same value the sidecars have.
            - name: JACCARD_PREFIX
              value: /great-machine/nix/store/
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: draal-data
```

The comment at the top of `deploy/draal.yaml` says how the endpoint ID of
draal is found in its log. Hold it to the program: run draal for ten
seconds, built into a temporary directory that is removed again, with an
endpoint ID made for the occasion in the server's place.

```bash
dir=$(mktemp -d)
go build -o "$dir/great-machine" ./cmd/great-machine
id=$("$dir/great-machine" id --key "$dir/operator.key")
timeout 10 "$dir/great-machine" draal --data "$dir/data" \
  --jaccard-server "$id" --jaccard-prefix /great-machine/nix/store/ 2>&1 | grep -i 'endpoint'
rm -r "$dir"
```

Expected: a line of the log with the endpoint ID of draal in it, 64
hexadecimal characters, and perhaps the warning that there is no operator.
If draal logs its endpoint ID in a line that has no `endpoint` in it, the
`grep` prints nothing: then run it without the `grep`, and write a word of
that line into the `grep` of the comment in `deploy/draal.yaml` and of the
README (Step 8) instead.

Validate the two files against the schemas of Kubernetes:

```bash
nix run nixpkgs#kubeconform -- -strict -summary deploy/
```

Expected: `Summary: 3 resources found in 2 files - Valid: 3, Invalid: 0, Errors: 0, Skipped: 0`

- [ ] **Step 6: The workflows**

At the end of `.github/workflows/test.yml`, after the step `nix build`,
add this step, indented as the steps before it:

```yaml
      # The image as a release builds it, and what a pod asks of it: that
      # it runs with another store mounted over /nix/store.
      - name: image
        run: |
          nix build .#image --option allow-unsafe-native-code-during-evaluation true
          docker load < result
          docker run --rm --tmpfs /nix/store ghcr.io/fables-for-robots/great-machine:nix version
          docker run --rm --tmpfs /nix/store --entrypoint sh ghcr.io/fables-for-robots/great-machine:nix \
            -c 'test -d /tmp && test -s /etc/ssl/certs/ca-certificates.crt'
```

`.github/workflows/release.yml`:

```yaml
name: Release

# A tag v* is a release: the image is built by Nix on a runner of each
# platform and pushed to ghcr.io under the tag and the platform, the two
# are joined under the tag and as latest, and a GitHub release is created
# for the tag with notes generated from what was merged since the last one.
on:
  push:
    tags: ["v*"]

permissions:
  contents: write
  packages: write

env:
  IMAGE: ghcr.io/fables-for-robots/great-machine

jobs:
  build:
    strategy:
      matrix:
        include:
          - arch: amd64
            runner: ubuntu-24.04
          - arch: arm64
            runner: ubuntu-24.04-arm
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v4
      - uses: cachix/install-nix-action@v31
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      # The flake takes the version from the environment, which Nix reads
      # with --impure alone. gonixgo runs a program while Nix evaluates,
      # which Nix allows only when asked.
      - name: Build the image
        run: |
          nix build .#image --impure \
            --option allow-unsafe-native-code-during-evaluation true
        env:
          GREAT_MACHINE_VERSION: ${{ github.ref_name }}
      # Nix names the image $IMAGE:nix. It has to say the tag it was
      # built as before it is pushed.
      - name: Push the image
        run: |
          docker load < result
          test "$(docker run --rm "$IMAGE:nix" version)" = "$TAG"
          docker tag "$IMAGE:nix" "$IMAGE:$TAG-$ARCH"
          docker push "$IMAGE:$TAG-$ARCH"
        env:
          TAG: ${{ github.ref_name }}
          ARCH: ${{ matrix.arch }}
  manifest:
    runs-on: ubuntu-24.04
    needs: build
    steps:
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Create the multi-arch manifest
        run: |
          docker buildx imagetools create \
            -t "$IMAGE:$TAG" -t "$IMAGE:latest" \
            "$IMAGE:$TAG-amd64" "$IMAGE:$TAG-arm64"
          docker buildx imagetools inspect "$IMAGE:$TAG"
        env:
          TAG: ${{ github.ref_name }}
  release:
    runs-on: ubuntu-24.04
    needs: manifest
    steps:
      - uses: actions/checkout@v4
      - uses: softprops/action-gh-release@v2
        with:
          generate_release_notes: true
```

Run:

```bash
nix run nixpkgs#actionlint -- .github/workflows/test.yml .github/workflows/release.yml
```

Expected: no output, and an exit status of 0.

- [ ] **Step 7: Commit**

```bash
git add deploy .github
git commit -m "Deploy: a pod, draal, and the release of the image"
```

- [ ] **Step 8: The README**

`README.md`:

````markdown
# great-machine

great-machine rolls a program that was built with Nix on a laptop out to a
group of pods, and shows what those pods are doing.

A pod holds no image of the program. It mounts a Nix store that the
[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store)
sidecar serves out of a [jaccard-store](https://github.com/amber-store/jaccard-store)
server, and its app container runs **zathras**, an init that starts the
program it is told to run. Every zathras reports to **draal**, the
controller. The operator talks to draal from the command line.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌───────────────────────────────────┐
 │ great-machine rollout│── push ──────▶  │ jaccard-store server and bucket   │
 │   nix-store -qR      │                 └──────────────▲────────────────────┘
 │                      │                                │ fetch
 │ status, logs, shell  │── iroh ──▶ draal ◀── iroh ── zathras ─ sidecar (/nix/store)
 └──────────────────────┘                                │
                                                         └─ the service
```

- A **service** is a named group of zathrases that are meant to run the
  same thing. Its name is a DNS label. One draal controls any number of
  services.
- A **generation** is what a service runs, numbered from 1 within the
  service: the path of a binary under `/nix/store`, its arguments, and the
  closure of the store path the binary is in.
- A **rollout** moves a service to a generation. The **stable generation**
  is that of the last rollout that completed.

Everything travels over [iroh](https://github.com/tmc/go-iroh). draal, an
operator and every zathras are known by their **endpoint ID**, the public
half of a key, and the endpoint ID of draal is all that the others need to
reach it: no address, no port, no Service.

A web page and a terminal UI that show the same are planned, and are not
there yet.

The design is in
[`docs/superpowers/specs/2026-10-09-great-machine-design.md`](docs/superpowers/specs/2026-10-09-great-machine-design.md).

## Getting started

What has to be there: a jaccard-store server and its endpoint ID, a
cluster whose nodes can run the
[sidecar](https://github.com/amber-store/jaccard-nfs-nix-store#pod), and
Nix on the laptop.

**1. The operator's key.** On the laptop:

```sh
great-machine id
```

prints the endpoint ID of the operator's key, and creates the key the
first time.

**2. draal.** [`deploy/draal.yaml`](deploy/draal.yaml) runs it in the
cluster with a volume for its data. Put in the operator's endpoint ID, the
jaccard-store server's, and a prefix, then:

```sh
kubectl apply -f deploy/draal.yaml
kubectl logs deployment/draal | grep -i endpoint
```

draal logs its endpoint ID when it starts. By hand it is:

```sh
great-machine draal --data /var/lib/draal --operator OPERATOR_ID \
  --jaccard-server SERVER_ID --jaccard-prefix /great-machine/nix/store/
```

**3. A pod.** [`deploy/pod.yaml`](deploy/pod.yaml) is a whole pod of the
service `web`: the sidecar, and an app container that runs
`great-machine zathras`. Put in the jaccard-store server's endpoint ID and
the same prefix, and the endpoint ID of draal, then:

```sh
kubectl apply -f deploy/pod.yaml
```

The pod runs and is not ready: its zathras has registered with draal and
has nothing to run yet.

**4. A rollout.** On the laptop, build the program for the platform of the
pods and roll it out:

```sh
export GREAT_MACHINE_DRAAL=DRAAL_ID
nix build .#packages.x86_64-linux.web        # in the program's own flake
great-machine rollout web ./result/bin/web -- --listen :8080
```

The pods run Linux, so the program has to be a Linux one: on a Mac that
takes a Linux builder for Nix. What follows `--` are the arguments of the
program. The command pushes the closure, has every zathras of `web` fetch
it, moves them to it one at a time, and ends when all of them run it. The
pod is ready now.

```sh
great-machine status web
great-machine logs -f web
great-machine shell web
```

## Commands

| command | what it does |
| --- | --- |
| `great-machine draal` | the controller |
| `great-machine zathras` | the init of an app container |
| `great-machine id` | prints the endpoint ID of the operator's key, creating the key if it is missing |
| `great-machine rollout [--parallel N] [--detach] SERVICE PATH [-- ARG...]` | pushes the closure and rolls it out |
| `great-machine rollback [--parallel N] [--detach] SERVICE` | rolls the stable generation out again |
| `great-machine status [SERVICE]` | lists services, or the zathrases of one |
| `great-machine logs [-f] SERVICE [ZATHRAS]` | prints the last 400 lines, and follows with `-f` |
| `great-machine shell SERVICE [ZATHRAS]` | a shell in the container of a zathras |
| `great-machine version` | prints the version of the program |

Every flag has an environment variable, and a flag wins over its variable.
Flags come before the arguments. A command ends with 0, with 1 when it
failed, and with 2 when its command line was not valid.

`ZATHRAS` is a name or the beginning of one, or the beginning of an
endpoint ID. It may be left out when the service has one zathras. A name
that matches none, or more than one, ends the command with the zathrases
there are.

### draal

```sh
great-machine draal --data DIR --jaccard-server ID --jaccard-prefix P [--operator ID]...
```

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
| `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |

At start draal logs its endpoint ID. Without `--operator` it warns that
nobody can act as an operator.

draal keeps the services, their generations and their rollouts in
`draal.sqlite`. Which zathrases are connected, and what they report, it
keeps in memory. There is one draal for a fleet. **While it is down or
cannot be reached, every service goes on running**: a zathras keeps what
it runs and dials again, and a rollout that was under way is carried on
with when draal is back.

### zathras

```sh
great-machine zathras --draal ID --service NAME
```

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
| `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
| `--preload-url URL` | `GREAT_MACHINE_PRELOAD_URL` | `http://127.0.0.1:9889/v1/preload` | the preload endpoint of the sidecar |
| `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
| `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |
| `--health-addr ADDR` | `GREAT_MACHINE_HEALTH_ADDR` | `:9890` | where the health check listens; empty for none |

zathras is the first process of the app container. It runs the service
with its own environment and working directory, and:

- **A service that ends is started again**, whatever its exit status: at
  once if it ran for 30 seconds or longer, and otherwise after a delay of
  1 second, then twice as long each time, up to 2 minutes.
- **On SIGTERM** it sends the service SIGTERM, and SIGKILL after
  `--stop-timeout`, and exits with 0. The pod's
  `terminationGracePeriodSeconds` has to be longer than `--stop-timeout`.
- It waits for every child of the container, so that none stays a zombie.
- Standard output and standard error of the service are those of the
  container, so `kubectl logs` shows what it always shows. The last 400
  lines are also kept for `great-machine logs`, across restarts of the
  service.
- **A zathras makes a new key every time it starts.** A container that
  was restarted is a new zathras under the old name: it has nothing to
  run until draal has sent it the generation of its service.

### Operator commands

`id`, `rollout`, `rollback`, `status`, `logs` and `shell` take:

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required, but for `id` | the endpoint ID of draal |
| `--key FILE` | `GREAT_MACHINE_KEY` | `great-machine/operator.key` in the user's configuration directory | the operator's key, created on first use |

`rollout` and `rollback` take:

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--parallel N` | `GREAT_MACHINE_PARALLEL` | `1` | how many zathrases are restarted at a time; `0` is all of them at once |
| `--detach` | `GREAT_MACHINE_DETACH` | off | start the rollout and do not wait for it |

`rollout` also takes:

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--jobs N` | `GREAT_MACHINE_JOBS` | `5` | the number of store paths pushed at a time |
| `--temp-dir DIR` | `GREAT_MACHINE_TEMP_DIR` | the system's temporary directory | where the temporary stores of a push are made |

`logs` takes `-f` (`GREAT_MACHINE_FOLLOW`): go on with every new line.

`shell` is a shell in the container of the zathras: it sees the store,
the files and the processes the service sees. On a terminal, `Ctrl-]`
leaves the session. With a standard input that is no terminal the input
is passed on, and the exit status of the shell is that of the command:

```sh
echo uptime | great-machine shell web
```

## Rollout

```sh
great-machine rollout [--parallel N] [--detach] SERVICE PATH [-- ARG...]
```

1. **The binary.** A `PATH` inside `/nix/store` is taken as written. Any
   other path is resolved through its symbolic links and has to end
   inside `/nix/store`, so `./result/bin/web` works. A binary for macOS is
   refused.
2. **The closure** is what `nix-store --query --requisites` prints for the
   store path the binary is in.
3. **The push.** Every store path of the closure that the jaccard-store
   server does not have yet is pushed to it, `--jobs` at a time, as the
   reference `prefix + name`. If a path fails, nothing is rolled out.
4. **The rollout.** draal records a new generation and a rollout, and the
   command follows the rollout until it has completed or halted. An
   interrupt, and `--detach`, end the watching and not the rollout.

The command ends with 0 when the rollout completed, and with 1 when it
halted or was superseded, having printed the reason.

A rollout has two phases, and a state:

| state | meaning |
| --- | --- |
| `preparing` | zathrases are fetching the closure; nothing was restarted |
| `activating` | zathrases are being moved to the new generation, `--parallel` at a time |
| `completed` | every connected zathras runs the generation, which is the stable one now |
| `halted` | it failed, and the reason is recorded |
| `superseded` | another rollout of the service was begun before this one ended |

- **Preparing.** Every zathras has its sidecar fetch the closure. Nothing
  is restarted before all of them have it, so a path that cannot be
  fetched halts the rollout while the service still runs everywhere.
- **Activating.** A zathras stops its service and starts the new binary.
  It counts as done when the new binary has run for 30 seconds, and the
  next one follows. A zathras that runs nothing yet is not waited for.
- **A rollout always makes a new generation**, even of the same binary:
  rolling out what runs already is how a service is restarted.
- **A new pod**, and one that missed a rollout, is sent the stable
  generation.

### Halted

A rollout halts when a zathras could not fetch the closure, when it
refused the binary, or when the new binary ended within 30 seconds of its
start. The reason names the zathras and says how its service ended.

A halted rollout is not undone. **The service is frozen as it is**: the
zathrases that were moved run the new generation, the others the old one,
and the one that failed goes on starting the new binary with backoff. Only
a zathras that runs nothing is still sent the stable generation. It stays
so until the operator rolls out again, or back:

```sh
great-machine rollback web
```

rolls the stable generation out again, as a rollout of its own. It is
refused when the service has no stable generation, and when the last
rollout completed, as there is nothing to go back from.

## Health check

zathras answers `GET /healthz` on port 9890 (`--health-addr`), on every
address, as the kubelet asks at the address of the pod:

| the service | answer |
| --- | --- |
| runs | `200`, with `running` as its body |
| was never started, is being stopped, or waits to be started again | `503`, with `idle`, `stopping` or `backoff` as its body |

- It passes as soon as the process was started, and says nothing about
  what the service does. A service that has a check of its own is probed
  at its own port instead.
- Whether draal can be reached changes nothing.
- **It is a readiness probe.** As a liveness probe it would have the
  kubelet restart the container of a service that stays down, and with
  the container go the backoff of zathras, its last lines and its
  identity.

```yaml
readinessProbe:
  httpGet: { path: /healthz, port: 9890 }
```

## Pod

[`deploy/pod.yaml`](deploy/pod.yaml) is the sidecar as
[jaccard-nfs-nix-store documents it](https://github.com/amber-store/jaccard-nfs-nix-store#pod)
and an app container:

```yaml
containers:
  - name: app
    image: ghcr.io/fables-for-robots/great-machine:latest
    command: ["great-machine", "zathras"]
    env:
      - { name: GREAT_MACHINE_DRAAL, value: <endpoint id of draal> }
      - { name: GREAT_MACHINE_SERVICE, value: web }
    volumeMounts:
      - { name: store, mountPath: /nix/store, mountPropagation: HostToContainer }
    readinessProbe:
      httpGet: { path: /healthz, port: 9890 }
terminationGracePeriodSeconds: 150   # longer than --stop-timeout
```

- **The prefix of the sidecar is that of draal.** A rollout pushes a store
  path as `prefix + name`, and the sidecar looks it up as that.
- **The environment of the service is that of the container.** What the
  program needs of settings and secrets is put on the app container.
- **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
  arm64, holds the program, a static busybox for `/bin/sh` and the tools
  of a shell session, CA certificates and a `/tmp`. Nothing in it needs
  its own `/nix/store`, which the sidecar's is mounted over. It is the
  image of draal too.

## Security

- **Operators are an allowlist.** draal does what an operator command
  asks only for the endpoint IDs it was given with `--operator`. The
  operator's key file is the credential: whoever has it can roll out, and
  has a shell in every container.
- **Registration is open.** Any peer that knows the endpoint ID of draal
  can register as a zathras of any service. It is then listed with the
  others, is sent the path and the arguments of what the service runs,
  and can halt a rollout by failing it. Treat the endpoint ID of draal as
  something the pods know and the world does not, and put no secret in
  the arguments of a service.
- **The jaccard-store server** takes a push from anyone who can reach it,
  and the sidecar's preload endpoint has no authentication, which is why
  it listens on the loopback address of the pod.
- A shell runs as the user of the app container, with everything the
  service can see.

## What to know

- The pods run Linux. `rollout` refuses a binary for macOS, and does not
  check the architecture.
- The first rollout of a service makes the service. Nothing removes one.
- A zathras that is gone is forgotten; its log goes with its container.
- The figures of a zathras (CPU, memory, disk, network) are read from
  cgroup v2. On a node without it there are none.

## Development

The Nix build is [gonixgo](https://github.com/draganm/gonixgo)'s: one
derivation per Go package, and no Nix file to update when `go.mod` changes.
gonixgo runs a program while Nix evaluates, which Nix allows only when
asked, so every command that touches the flake's packages takes an option.
The dev shell carries the built program and needs it too:

```sh
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version

nix develop --option allow-unsafe-native-code-during-evaluation true
```

In the dev shell are `go`, `sqlc`, and `great-machine` as built from the
tree. With direnv, `.envrc` passes the option and reloads the shell,
rebuilding the program, when the sources change. Nix sees only the files
git tracks, so `git add` new ones.

```sh
go test ./...
sqlc generate    # after changing the queries or the migrations in db/
```

The default shell cannot be entered while the Go module does not resolve,
because building the program is part of it: after a `go get` that left
`go.sum` behind, for example. `nix develop .#bare` has `go` and `sqlc`
without the program and needs no option; run `go mod tidy` there.

The tests need no cluster and no network beyond the loopback interface.
Those in `e2e` run draal and several zathrases in one process, with real
child processes for the services. `scripts/test-pid1.sh` runs zathras as
the first process of a container, and takes Docker.

### The image

```sh
nix build .#image --option allow-unsafe-native-code-during-evaluation true
docker load < result
docker run --rm ghcr.io/fables-for-robots/great-machine:nix version
```

`image` is a package of the Linux systems, built on Linux. On a Mac it
can be evaluated, with `--impure`, which tells the flake what machine it
is evaluated on (`nix eval --impure … .#packages.aarch64-linux.image.drvPath`),
and built where Nix has a Linux builder.

`great-machine version` prints the commit the program was built from. A
release is built with the tag in `GREAT_MACHINE_VERSION` and `--impure`.

### Continuous integration and releases

`.github/workflows/test.yml` runs on every push and pull request: `gofmt`,
`go vet`, the tests, `sqlc diff` (the generated queries are what the SQL
says), zathras as the first process of a container, the Nix build and the
image.

A tag `v*` is a release: `.github/workflows/release.yml` builds the image
with Nix on a runner of each platform, pushes it as `:<tag>-amd64` and
`:<tag>-arm64`, joins the two as `:<tag>` and `:latest`, and creates a
GitHub release with notes generated from what was merged since the last
one.

```sh
git tag v0.1.0 && git push origin v0.1.0
```
````

- [ ] **Step 9: Hold the README to the program**

The tables of the README are copied from the spec. The program is what
counts. Run:

```bash
for c in draal zathras id rollout rollback status logs shell; do
  go run ./cmd/great-machine "$c" --help
done
```

Expected: the help of every command, with its flags and, in brackets, the
variable of each. Compare with the README and correct the README where
they differ: a flag or a variable that is named otherwise (the variables
of `--detach` and `-f` are not in the spec, so look at those two first), a
default, a flag the README has and the program has not. `--web-addr` and
`tui` are of a later stage and are in neither.

Then look at the names the README takes from other tasks, and correct
them the same way:

```bash
ls scripts/test-pid1.sh sqlc.yaml
grep -n 'name:' .github/workflows/test.yml
```

Expected: both files are there (`sqlc generate` in the README is run where
`sqlc.yaml` is; if that is `db/`, write `(cd db && sqlc generate)`), and
the steps of `test.yml` are those the README's section on continuous
integration lists.

- [ ] **Step 10: Run everything once more**

```bash
gofmt -l .
go vet ./...
go test ./...
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
rm result
```

Expected: `gofmt` and `go vet` print nothing, every package is `ok` or has
no test files, and the program prints the short commit with `-dirty`
(the README is not committed yet). `rm result` leaves nothing that was
built in the repository.

- [ ] **Step 11: Commit**

```bash
git add README.md
git commit -m "README: what great-machine is and how it is run"
```
