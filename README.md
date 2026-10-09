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

**The network.** draal, every pod and the laptop dial out: to iroh's
public relays, which carry a connection until a direct path is found
(and for good where none is), and to pkarr and DNS, through which an
endpoint is announced and looked up by its ID. A cluster whose egress is
restricted has to allow that. When it does not, draal warns at its start
that it has no relay connection, and a zathras never appears in
`great-machine status`.

**0. The program.** On the laptop, build it with Nix from the flake.
The build runs a program while Nix evaluates, which Nix allows only when
asked (see [Development](#development)):

```sh
nix build github:fables-for-robots/great-machine/v0.1.0 \
  --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
```

`nix profile install` with the same option puts it on the `PATH`. A
release also publishes the image `ghcr.io/fables-for-robots/great-machine`
under its tag, which holds the program, for Linux on amd64 and arm64:
the cluster runs draal and every zathras from it.

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
with when draal is back. After it starts, draal waits 45 seconds before it
decides anything, so that the zathrases are back first.

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

`id` takes `--key` alone. `rollout`, `rollback`, `status`, `logs` and
`shell` take both of these:

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
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

1. **The binary.** A `PATH` inside `/nix/store` is taken as written, but
   it is refused when, through its symbolic links, it ends outside the
   store (a link to another store path is fine). Any other path is
   resolved through its symbolic links and has to end inside `/nix/store`,
   so `./result/bin/web` works. A binary for macOS is refused.
2. **The closure** is what `nix-store --query --requisites` prints for the
   store path the binary is in.
3. **The push.** The command asks draal for the store, closes that
   connection, and pushes: every store path of the closure that the
   jaccard-store server does not have yet is pushed to it, `--jobs` at a
   time, as the reference `prefix + name`. If a path fails, nothing is
   rolled out.
4. **The rollout.** The command dials draal again to send the rollout, as
   the operator's key must not have two endpoints at once. If that second
   dial fails, the closure was pushed and nothing was rolled out, and
   running the command again is cheap. draal records a new generation and
   a rollout, and the command follows the rollout until it has completed
   or halted. An interrupt, and `--detach`, end the watching and not the
   rollout.

The command ends with 0 when the rollout completed, and with 1 when it
halted or was superseded, having printed the reason.

A rollout has two phases, and a state:

| state | meaning |
| --- | --- |
| `preparing` | zathrases are fetching the closure; nothing was restarted |
| `activating` | zathrases are being moved to the new generation, `--parallel` at a time |
| `completed` | every connected zathras runs the generation, and none that left during the rollout is still waited for; it is the stable one now |
| `halted` | it failed, and the reason is recorded |
| `superseded` | another rollout of the service was begun before this one ended |

- **Preparing.** Every zathras has its sidecar fetch the closure. Nothing
  is restarted before all of them have it, so a path that cannot be
  fetched halts the rollout while the service still runs everywhere.
- **Activating.** A zathras stops its service and starts the new binary.
  It counts as done when the new binary has run for 30 seconds, and the
  next one follows. A zathras that runs nothing yet is not waited for.
- **A zathras that left during a rollout without being done holds the
  completion of the rollout for 2 minutes.** A fleet that loses draal for
  a moment would otherwise complete the rollout by its absence, and be
  restarted all at once on its return. If the zathras is back in time it
  takes its place in the rollout again; if not, the rollout completes over
  those who are there. The limit: after an absence of more than 2 minutes,
  or after a restart of draal from which zathrases stay away longer than
  45 seconds, those who return are moved to the stable generation at once.
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
    image: ghcr.io/fables-for-robots/great-machine:v0.1.0
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
  image of draal too. Name it by a release tag, as `v0.1.0`, the first
  release, which exists once it has been tagged, and keep draal and every
  zathras on the same tag: a draal and a zathras of different versions may
  not speak the same protocol.

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
- **What draal takes from a registrant is bounded.** A registration is
  refused unless the name is 1 to 253 bytes of letters, digits, `.`, `_`
  and `-` and the version at most 64 bytes of printable ASCII. What a
  zathras reports is cleaned of control characters and cut before draal
  keeps or shows it (the binary at 4 KiB, the arguments at 128 of 1 KiB,
  the reason of a failure at 4 KiB), a report that is not of the protocol
  ends the connection, and draal keeps at most 900 samples of a zathras,
  twice what 15 minutes bring. What draal does not bound is how many register:
  a peer can register any number of zathrases, every one is sent to
  every watcher, and a fleet that comes to more than a message holds
  (4 MiB) ends `status` and the following of a rollout, which draal
  logs.
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

## License

Licensed under the GNU Lesser General Public License, version 3 only
(`LGPL-3.0-only`). See [`LICENSE`](LICENSE) for the LGPL terms and
[`COPYING`](COPYING) for the GPL terms incorporated by the LGPL.
