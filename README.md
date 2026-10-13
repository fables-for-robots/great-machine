# great-machine

great-machine rolls a program that was built with Nix on a laptop out to a
group of pods, and shows what those pods are doing.

A pod holds no image of the program. Its one container runs **zathras**,
an init that keeps a Nix store of its own in `/nix/store`: it fetches
into it what it is told to run, as plain files, and runs it. Every
zathras reports to **draal**, the controller, which also keeps the store
of the fleet: every store path that was rolled out, as pack files in an
S3 bucket. The operator talks to draal from the command line.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌──────────────────────────────┐
 │ great-machine rollout│── iroh ───────▶ │ draal: the controller,       │
 │   nix-store -qR      │                 │        and the store         │
 │ status, logs, shell  │                 └──────────────▲───────────────┘
 └──────────┬───────────┘                                │ iroh
            │ packs up                                   │
            ▼                                            │
      ┌───────────┐                       ┌──────────────┴───────────────┐
      │ S3 bucket │ ◀──── packs down ──── │ pod: zathras, /nix/store     │
      └───────────┘                       │       └─ the service         │
                                          └──────────────────────────────┘
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
reach it, for the store as for everything else: no address, no port, no
Service.

The store is [jaccard-store](https://github.com/amber-store/jaccard-store),
inside the program: there is no server of it to run. A pod has no
sidecar and mounts nothing: zathras pulls the store paths it is to run
and writes them out as files.

draal also serves [a web page](#the-web-page) that shows the fleet, the
figures and the log of every zathras, and gives a shell in its container.
[`great-machine tui`](#the-terminal-ui) shows the same in a terminal.

The design is in
[`docs/superpowers/specs/2026-10-09-great-machine-design.md`](docs/superpowers/specs/2026-10-09-great-machine-design.md).

## Getting started

What has to be there: an S3 bucket and credentials for it, a cluster,
and Nix on the laptop. A pod asks nothing of its node or of its
namespace that a container does not have anyway (see [Pod](#pod)).

**The network.** draal, every pod and the laptop dial out: to iroh's
public relays, which carry a connection until a direct path is found
(and for good where none is), and to pkarr and DNS, through which an
endpoint is announced and looked up by its ID. A cluster whose egress is
restricted has to allow that. When it does not, draal warns at its start
that it has no relay connection, and a zathras never appears in
`great-machine status`. The laptop and every pod also reach the bucket:
pack files go straight between them and it, by URLs that draal signs.

**0. The program.** On the laptop, build it with Nix from the flake.
The build runs a program while Nix evaluates, which Nix allows only when
asked (see [Development](#development)):

```sh
nix build github:fables-for-robots/great-machine/v0.3.0 \
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
cluster with a volume for its data. Put in the operator's endpoint ID and
the bucket, make the Secret with the bucket's credentials that the file
names, then:

```sh
kubectl apply -f deploy/draal.yaml
kubectl logs deployment/draal | grep -i endpoint
```

draal logs its endpoint ID when it starts. By hand it is:

```sh
export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...
great-machine draal --data /var/lib/draal --operator OPERATOR_ID \
  --s3-bucket my-packs --s3-region eu-central-1
```

Its [web page](#the-web-page) is at `http://127.0.0.1:8080/` on the
machine draal runs on. In the cluster that is the pod, and a forward
brings it to the laptop:

```sh
kubectl port-forward deployment/draal 8080:8080    # then http://localhost:8080/
```

**3. A pod.** [`deploy/pod.yaml`](deploy/pod.yaml) is a whole pod of the
service `web`: one container, which runs `great-machine zathras`. Put in
the endpoint ID of draal, then:

```sh
kubectl apply -f deploy/pod.yaml
```

The pod runs and is not ready: its zathras has opened its store, which
is empty, has registered with draal, and has nothing to run yet.

**4. A rollout.** On the laptop, build the program for the platform of the
pods and roll it out:

```sh
export GREAT_MACHINE_DRAAL=DRAAL_ID
nix build .#packages.x86_64-linux.web        # in the program's own flake
great-machine rollout web ./result/bin/web -- --listen :8080
```

The pods run Linux, so the program has to be a Linux one: on a Mac that
takes a Linux builder for Nix. What follows `--` are the arguments of the
program. The command pushes the closure to draal's store, has every
zathras of `web` fetch it, moves them to it one at a time, and ends when
all of them run it. The pod is ready now.

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
| `great-machine tui` | the fleet in the terminal: services, zathrases, figures, logs, and a shell |
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
great-machine draal --data DIR --s3-bucket NAME [--s3-region R] [--operator ID]... [--web-addr ADDR]
```

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run), `draal.sqlite` and the database of the store, `store.sqlite` |
| `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
| `--s3-bucket NAME` | `GREAT_MACHINE_S3_BUCKET` | required | the bucket of [the store](#the-store) |
| `--s3-prefix P` | `GREAT_MACHINE_S3_PREFIX` | none | put before the name of every object in the bucket |
| `--s3-endpoint URL` | `GREAT_MACHINE_S3_ENDPOINT` | AWS | an S3 service other than AWS, as `http://HOST[:PORT]` or `https://HOST[:PORT]`, with the path the service is under if it has one |
| `--s3-region R` | `GREAT_MACHINE_S3_REGION` | the AWS SDK's, as `AWS_REGION` | the region of the bucket, which the URLs of the store are signed for; one of the two has to say it |
| `--s3-path-style` | `GREAT_MACHINE_S3_PATH_STYLE` | off | the bucket in the path of a URL, not in its host name |
| `--store-scratch DIR` | `GREAT_MACHINE_STORE_SCRATCH` | `--data` | where packs are verified: the store makes a directory of its own there, `great-machine-scratch`, and empties that one at every start |
| `--store-verify-jobs N` | `GREAT_MACHINE_STORE_VERIFY_JOBS` | `2` | packs verified at once |
| `--store-max-pack-size SIZE` | `GREAT_MACHINE_STORE_MAX_PACK_SIZE` | `16GiB` | the largest pack, uncompressed: bytes, or a number with `KiB`, `MiB` or `GiB` |
| `--store-no-verify` | `GREAT_MACHINE_STORE_NO_VERIFY` | off | take a pack without reading its data |
| `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where [the web page](#the-web-page) listens, as `HOST:PORT`; empty for no web page |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |

At start draal logs its endpoint ID, the bucket, and the URL of its web
page. Without `--operator` it warns that nobody can act as an operator. An
address for the web page that is taken ends the command with 1. The
credentials of the bucket come from the AWS SDK's default chain:
`AWS_ACCESS_KEY_ID` and the rest, a profile, or a role. **draal asks the
bucket nothing when it starts**: a bucket that is not there, or
credentials that are wrong, show in the first push, and not before. A
draal without a region for its bucket does end at its start, and says
so.

draal keeps the services, their generations and their rollouts in
`draal.sqlite`. Which zathrases are connected, and what they report, it
keeps in memory. There is one draal for a fleet. **While it is down or
cannot be reached, every service goes on running**: a zathras keeps what
it runs and dials again, and a rollout that was under way is carried on
with when draal is back. After it starts, draal waits 45 seconds before it
decides anything, so that the zathrases are back first.

#### The store

draal is the store of the fleet: a
[jaccard-store](https://github.com/amber-store/jaccard-store) server on
draal's own endpoint, under draal's endpoint ID. A rollout pushes to it,
and the pods fetch from it.

- **Every service has references of its own.** A store path that was
  pushed for the service `web` is the reference
  `/services/web/nix/store/NAME`, and the `/nix/store` of a pod is made
  of its service's alone. A store path that two services run is pushed
  for each; the second time nothing is uploaded, as the store has its
  pack already.
- **Packs do not pass through draal.** They go between the laptop, the
  pods and the bucket by URLs that draal signs, each valid for an hour.
  So the laptop and the pods have to reach the bucket, and under the
  name draal has for it: the `--s3-endpoint` that draal is given is the
  one in the URLs.
- **The bucket's service must honor conditional writes**
  (`If-None-Match: *` on PUT, as AWS S3 does): upload URLs are signed
  with that condition, so that a pack the store has verified cannot be
  replaced.
- **A pack is verified before a reference points at it**: draal downloads
  it, unpacks it into its scratch directory and checks every object
  against its key. That takes room for `--store-max-pack-size` times
  `--store-verify-jobs`, 32 GiB with the defaults, which is why
  `deploy/draal.yaml` gives the scratch space a volume of its own. The
  scratch directory is `great-machine-scratch` inside `--store-scratch`,
  made by the store and emptied whenever draal starts; nothing else in
  `--store-scratch` is touched, so it may be any directory, the data
  directory among them. A
  store path whose pack is larger than `--store-max-pack-size` cannot be
  pushed. `--store-no-verify` leaves the verification out: a pack that is
  damaged is then found by the pod that fetches it, and not by the
  rollout that pushed it.
- **`store.sqlite` is the only record** of the references and of which
  pack leans on which. The bucket alone does not describe the store: back
  the data directory up.
- **Nothing is removed from the store.** A store path that no generation
  needs any more stays in the bucket.
- **Who may do what.** Operators push. Anyone who knows the endpoint ID
  of draal can fetch: see [Security](#security).
- What the store holds, and what sharing and compression save, is on
  draal's [web page](#the-web-page) under `/store/`.

### zathras

```sh
great-machine zathras --draal ID --service NAME
```

| flag | environment | default | meaning |
| --- | --- | --- | --- |
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
| `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | where the pack cache is kept |
| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | fetch nothing: `/nix/store` is whatever the machine has |
| `--user UID[:GID]` | `GREAT_MACHINE_USER` | those of zathras | the user and the group the service and the shells run as |
| `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
| `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |
| `--health-addr ADDR` | `GREAT_MACHINE_HEALTH_ADDR` | `:9890` | where the health check listens; empty for none |

zathras is the first process of the app container. It runs the service
with its own environment and working directory, and:

- **It keeps the store of its service in `/nix/store`**, as plain
  files, and mounts nothing. Every store path of a rollout is fetched
  before the rollout touches the service: pulled from draal's store into
  the pack cache, `packs/` under `--store-cache`, written out, and moved
  to its name when all of it is on disk. A store path under its name
  in `/nix/store` is whole, and one that is there is not fetched again.
  While it is written it is beside its name, as
  `/nix/store/.great-machine-partial-` and a number; what a zathras was
  writing when it ended is removed when it next starts.
- **What is written out is what a Nix store holds**: directories and
  the files that can be executed with mode `0555`, every other file with
  `0444`, symbolic links as they are, all of it owned by zathras. A
  store path that is one file gets an execute bit. A store path that
  holds a device, a FIFO or a socket is refused.
- **The pack cache makes the next fetch small.** It keeps every object
  that was pulled, and a fetch downloads what it lacks. A store path
  that a rollout changed leans on the pack of the one before: a pod that
  has the one before downloads one pack for it, and a pod that is new
  two at the most. A pod's disk holds every store path twice, as packs
  and as files, and nothing is removed from either.
- **A fetch that fails is tried again** for 2 minutes. One of a store
  path that the store does not have for the service fails at once, and
  says so.
- **`/nix/store` and the cache are zathras's alone to write.** It makes
  them so when it starts, whatever a volume came as, and ends at once
  when it cannot open them. A `/nix/store` that holds something is used
  as it is: zathras adds to it, and hides nothing.
- **The service and the shells are started with what the container
  has**, which is no capability beyond a container's own ([Pod](#pod)).
  With `--user` they run as that user and group, with no capability at
  all in the container, and cannot write the store; their standard
  output and standard error, and the terminal of a shell, are theirs, so
  a program that opens `/dev/stdout` anew can.
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
  run until draal has sent it the generation of its service. What it had
  fetched is still in `/nix/store`, when that is a volume of the pod;
  where it is not, it is written out again from the pack cache.
- `--no-store` is for a machine that has a `/nix/store` of its own,
  which zathras is not to write to: nothing is fetched, and a rollout
  runs what is there.

### Operator commands

`id` takes `--key` alone. `rollout`, `rollback`, `status`, `logs`, `shell`
and `tui` take both of these:

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
| `--jobs N` | `GREAT_MACHINE_JOBS` | `20` | the number of store paths pushed at a time, 32 at the most: the store answers 64 requests of a connection at a time |
| `--temp-dir DIR` | `GREAT_MACHINE_TEMP_DIR` | the system's temporary directory | where the temporary stores of a push are made |

Every store path that is being pushed is in `--temp-dir` once more,
uncompressed, with its pack beside it: up to `--jobs` of them at a time. A
laptop that is short of room there gives a smaller `--jobs`.

`logs` takes `-f` (`GREAT_MACHINE_FOLLOW`): go on with every new line.

`shell` is a shell in the container of the zathras: it sees the store,
the files and the processes the service sees, as the user the service
runs as. On a terminal, `Ctrl-]`
leaves the session. With a standard input that is no terminal the input
is passed on, and the exit status of the shell is that of the command:

```sh
echo uptime | great-machine shell web
```

`tui` takes no argument and no flag of its own: see
[The terminal UI](#the-terminal-ui).

## The web page

draal serves a web page, on `http://127.0.0.1:8080/` unless `--web-addr`
says otherwise:

| view | address | shows |
| --- | --- | --- |
| services | `/` | every service: its stable generation, its zathrases by phase, the last rollout and how far it is |
| a service | `/s/SERVICE` | its zathrases (name, phase, generation, time running, restarts, CPU and memory), the rollout under way with every zathras' part in it, and the last 20 rollouts |
| a zathras | `/s/SERVICE/z/ID` | its state in full; CPU, memory, disk and network over the last 15 minutes; the last 400 lines of its log, followed as they come; a shell in its container |
| the store | `/store/` | what [the store](#the-store) holds: its references and packs, and what sharing and compression save. It is jaccard-store's own page, and changes nothing |

The page is what `status`, `logs` and `shell` are on the command line.
**It can roll nothing out and nothing back**: a rollout needs the Nix
store of the laptop, and the page has no authentication.

It draws from one connection to draal, says so when that connection is
lost, and connects again. It asks nothing of any other host: its scripts,
styles and fonts are in the program.

**How it is reached.** On the machine draal runs on, by its URL, which
draal logs at its start. In the cluster the page listens on the loopback
address of draal's pod, where no other pod and no Service reaches it, and
a forward brings it to the laptop:

```sh
kubectl port-forward deployment/draal 8080:8080
```

then `http://localhost:8080/`. Whoever may forward a port to the pod
(`pods/portforward` in the cluster's RBAC) reaches the page so, and with
it a shell in every container: in a cluster that permission is the page's
only authentication.

**The page has no authentication, and whoever can reach its port has a
shell in every container.** It answers under any host name: `localhost`,
an IP address, or whatever name a tunnel, a forward or a proxy gives it.

- **It listens on loopback by default**, `127.0.0.1:8080`, where only the
  machine itself, or the pod, reaches it. An address that is not
  loopback, such as `:8080`, is allowed, and draal warns about it at its
  start. Do not put a Service or an Ingress in front of the port.
- **The origin.** A WebSocket, which is how the page gets the fleet, the
  logs and the shell, is refused with `403` when the page that opens it
  was not loaded from the host it is opened on. That keeps another web
  site that is open in the same browser from opening one directly.
- **A limit that is known and was accepted.** The rule of the origin
  does not hold against a web site that points a name of its own at
  draal's address (DNS rebinding): its page is then of the same origin as
  draal's, and gets a socket as draal's own page does. So the port should
  be reachable only by people and machines that are trusted, and the
  browser that is used for the page is best not used for browsing
  elsewhere while the port is reachable from it.

**Behind a proxy.** A proxy in front of draal has to authenticate whoever
it lets through: it is then all the authentication there is. The name
under which the proxy is reached may be any name, but the proxy has to
pass on the `Host` header the browser sent, unchanged. One that rewrites
it (nginx does by default; there it takes `proxy_set_header Host
$http_host;`) loads the page and gets `403` for every WebSocket, so the
page then says that its connection is lost. A WebSocket through nginx
also needs the `Upgrade` and `Connection` headers passed on
(`proxy_http_version 1.1; proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";`), without which draal answers
`400`. A proxy that does not authenticate puts a shell in every
container on the network it listens on.

`--web-addr ""` (or an empty `GREAT_MACHINE_WEB_ADDR`) turns the page off:
nothing listens.

A program that was built with `go build` alone has no page in it, since
the page is built by Node (see [Development](#development)). Its draal
warns about that at its start, and answers `501` with a line that says
how the page is built. The program that Nix builds, and the image, have
it.

## The terminal UI

```sh
great-machine tui --draal ID
```

shows in a terminal what the web page shows, and follows it. It talks to
draal as an operator, as `status`, `logs` and `shell` do: it takes
`--draal` and `--key`, draal lets it in by its allowlist, and it needs
neither the web page nor a forwarded port.

| view | shows |
| --- | --- |
| services | every service: its stable generation, its zathrases by phase, its last rollout and how far it is |
| a service | its zathrases as a table (name, phase and since when, generation and whether it is the stable one, restarts, last exit, CPU and memory), the rollout under way with where every zathras stands in it, and its last rollouts |
| a zathras | its state in full; CPU, memory, disk and network over the last 15 minutes as sparklines, with the value now; the last 400 lines of its log, followed as they come |

One view at a time fills the terminal, under a line that says where one
is, the beginning of draal's endpoint ID and the state of the connection,
and above a line of the keys that work there.

| key | does |
| --- | --- |
| `↑` `↓`, `k` `j` | move in a list; scroll a log |
| `Enter`, `→`, `l` | open what is chosen |
| `Esc`, `←`, `h`, `Backspace` | back |
| `PgUp` `PgDn` | a page up, a page down |
| `g` `G` | the start, the end; in a log `G` also follows it again |
| `s` | on a zathras: open a shell |
| `?` | the keys, and what the signs mean |
| `q`, `Ctrl-C` | quit |

**It can roll nothing out and nothing back.** `rollout` and `rollback`
are commands of their own.

**The shell.** `s` on the view of a zathras that is connected opens a
shell in its container, the one `great-machine shell` opens. First draal
is asked for it, and a line says so; the terminal UI keeps the terminal
meanwhile, `Esc` gives the shell up, and nothing else that is typed then
is acted on or passed on to the shell. When draal has opened the shell
the terminal UI steps aside for it: the shell is on the terminal's own
screen, under a line that says whose shell it is, and what it wrote is
still there, in the terminal's scrollback, when the terminal UI has
ended. Everything that is typed goes to the shell, `Ctrl-C` and `q` too,
until the shell ends (`exit`) or `Ctrl-]` is typed. `Ctrl-]` hangs the
shell up, as closing its terminal would: what runs in it ends with it.
Back in the terminal UI, a line says how the shell ended: its exit
status, that it was left, or why it failed; a shell that draal refuses
is said the same way, without the terminal UI leaving its screen. While
a shell is open the terminal UI draws nothing; it shows the fleet as it
is then when it is back. What is typed in the shell never reaches the
terminal UI, and what is typed before `s` never reaches the shell. Within
a few milliseconds of the terminal changing hands, in either direction,
a key may be dropped; and a key typed in the instant before the shell
takes the terminal may be the shell's or, rarely, be acted on by the
terminal UI when the shell has ended.

**When the connection to draal is lost** (draal restarted, the network
gone), the first line says so and for how long, what was shown stays and
is marked as what draal sent last, and draal is dialed again: after one
second, then after pauses that double up to 30 seconds. Any key asks at
once. When draal answers, the view that was open is alive again. A draal
that takes the connection and does not answer within a minute is given
up and dialed again, like a dial that failed; until then a key changes
nothing, since there is no pause to end. What draal refuses at the very
start, an operator that is not on its allowlist, ends the command with
that refusal and the status 1.

**The terminal.** The terminal UI needs a terminal as its standard input
and its standard output, and ends with the status 2 and one line
otherwise: `status` and `logs` print the same without one. It draws on
the terminal's alternate screen and leaves the terminal as it found it,
however it ends. It wants 80 columns and 24 rows, uses more, draws down
to 50 by 12, and says so in one line below that. The keyboard alone
drives it: it does not ask for the mouse, so the terminal's own selection
works. Its colours are the terminal's own sixteen, so it is light or dark
as the terminal is, and it has no theme. With `NO_COLOR` set it draws
without colour; every state is also said by a sign and a word. It needs
UTF-8 and a font with the box-drawing block elements (`▁▂▃▄▅▆▇█`) for
the sparklines. Nothing that draal or a zathras sent, a name, a reason or
a line of a log, reaches the terminal as it came, but through a shell:
control characters and escape sequences are taken out, and a log line
keeps its colours by being read and drawn again.

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
3. **The push.** The command asks draal for the store of the service,
   closes that connection, and pushes to draal's endpoint ID: every store
   path of the closure that the store does not have yet for the service
   is pushed, `--jobs` at a time, as the reference
   `/services/SERVICE/nix/store/NAME`. A store path that another service
   has pushed is read again on the laptop and not uploaded again. If a
   path fails, nothing is rolled out.
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

- **Preparing.** Every zathras fetches the closure into its store.
  Nothing is restarted before all of them have it, so a path that cannot
  be fetched halts the rollout while the service still runs everywhere.
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

[`deploy/pod.yaml`](deploy/pod.yaml) is a whole pod. It has one container:

```yaml
containers:
  - name: app
    image: ghcr.io/fables-for-robots/great-machine:v0.3.0
    command: ["great-machine", "zathras"]
    securityContext:
      allowPrivilegeEscalation: false
      seccompProfile: { type: RuntimeDefault }
    env:
      - { name: GREAT_MACHINE_DRAAL, value: <endpoint id of draal> }
      - { name: GREAT_MACHINE_SERVICE, value: web }
    volumeMounts:
      - { name: store, mountPath: /nix/store }
      - { name: cache, mountPath: /var/cache/great-machine }
    readinessProbe:
      httpGet: { path: /healthz, port: 9890 }
volumes:
  - { name: store, emptyDir: {} }
  - { name: cache, emptyDir: {} }
terminationGracePeriodSeconds: 150   # longer than --stop-timeout
```

- **The container is given nothing beyond what a container has.** No
  capability is added, and the AppArmor profile is the runtime's:
  zathras mounts nothing. The runtime's seccomp profile is written out
  because a pod that says nothing has none on most clusters. A namespace
  that enforces the Pod Security Standards admits such a pod at the
  level `baseline`.
- **`allowPrivilegeEscalation: false` sets `no_new_privs`** for every
  process of the container, zathras among them: nothing in it gains a
  privilege by executing a program, by a setuid program or otherwise.
- **A service that is root is root in the container of zathras.** It
  can signal zathras, and write what root may: `/nix/store` and the pack
  cache among it. What it writes there, it and the generations after it
  run from, in that pod and no other. With `GREAT_MACHINE_USER` it
  cannot: both directories are zathras's alone to write, whatever the
  volumes came as.
- **A container that was given more is warned about.** When the service
  is root and the container has a capability that a container does not
  get by default, zathras says so at its start, and names them and
  `--user`. Nothing takes such a capability from the service.
- **The store and the cache** are volumes of the pod, so that a
  container that was restarted finds its store. Between them they hold
  every store path the pod has fetched twice, as files and as packs, and
  they grow until the pod is gone. Without the volume on `/nix/store`
  the store is the container's own, and a container that was restarted
  writes every store path out again from the cache: it asks draal for
  each, and downloads nothing.
- **The environment of the service is that of the container.** What the
  program needs of settings and secrets is put on the app container.
- **What the container is given of memory and CPU** is for the service
  and for the store together: fetching a store path and writing it out
  count with the service, and so do they in the figures of the zathras.
- **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
  arm64, holds the program, a static busybox for `/bin/sh` and the tools
  of a shell session, CA certificates, a `/tmp`, and an empty
  `/nix/store` for the volume of the store to go over. Nothing in it
  needs a `/nix/store` of its own. It is the image of draal too. Name it
  by a release tag, as `v0.3.0`, which exists once it has been tagged,
  and keep draal and every zathras on the same tag: a draal and a
  zathras of different versions may not speak the same protocol.

## Security

- **Operators are an allowlist.** draal does what an operator command
  asks only for the endpoint IDs it was given with `--operator`. The
  operator's key file is the credential: whoever has it can roll out, and
  has a shell in every container.
- **The web page has no authentication.** Whoever can reach its port can
  read every log and has a shell in every container, though no rollout.
  It listens on loopback unless told otherwise, answers under any host
  name, and is reached through `kubectl port-forward`: see
  [The web page](#the-web-page). The port must not be exposed by a
  Service, a proxy in front of it has to authenticate, and it should be
  reachable only by people and machines that are trusted.
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
- **The store takes a push from operators alone**, and so a listing and
  a removal. **Fetching is open**, as registration is: whoever knows the
  endpoint ID of draal can fetch any store path of any service by its
  name. The prefixes keep the stores of the services apart, and not their
  contents secret. Put no secret into a store path, which a Nix store is
  no place for anyway.
- **The app container is given no capability** beyond a container's
  own, and has `no_new_privs`: zathras mounts nothing (see [Pod](#pod)).
  The service and the shells are started with what the container has. As
  root they can do what root in an ordinary container can, in their own
  container: signal zathras, and write `/nix/store` and the pack cache,
  which they and the generations after them run from. `--user` makes
  them another user, which can write neither.
- **Nothing in the pod takes requests to fetch.** zathras dials draal
  for what it fetches, and listens for nothing but its health check.
- A shell runs as the user of the service, with everything the service
  can see.

## What to know

- The pods run Linux. `rollout` refuses a binary for macOS, and does not
  check the architecture.
- The first rollout of a service makes the service. Nothing removes one.
- A zathras that is gone is forgotten; its log goes with its container.
- The figures of a zathras (CPU, memory, disk, network) are read from
  cgroup v2. On a node without it there are none. They are those of the
  container: the service, zathras and its store, and the shells.
- A service runs from files on the pod's own disk: a rollout fetches
  every path of its closure before it starts anything. Nothing is
  fetched by being named, so a program that names a store path outside
  its closure finds no such file.
- While draal cannot be reached, a pod runs what it has fetched and can
  fetch nothing new.

## Upgrading from v0.2.0

Up to v0.2.0 zathras mounted its store, for which its container had
`CAP_SYS_ADMIN` and no AppArmor profile. From v0.3.0 on it writes the
store paths out as files, and its container is given nothing. draal, its
store and the protocol are as they were, so:

1. **No service is rolled out again.** A pod of v0.3.0 is sent the
   stable generation of its service, as any new pod is, and fetches it
   from the store that v0.2.0 pushed to.
2. **The image and the `securityContext` of a pod change together**, to
   what [`deploy/pod.yaml`](deploy/pod.yaml) has: no `capabilities`, no
   `appArmorProfile`, `allowPrivilegeEscalation: false`, and a volume on
   `/nix/store`. The image of v0.3.0 with the `securityContext` of
   v0.2.0 runs, but nothing takes `CAP_SYS_ADMIN` from the service any
   more: a service that is root has it, and zathras warns about that at
   its start. The image of v0.2.0 with the `securityContext` of v0.3.0
   cannot mount, and ends at its start.
3. **draal and the pods need not change in one step.** They end on one
   tag, as with any release.
4. **A namespace that was put at the Pod Security level `privileged`**
   for these pods needs no more than `baseline` for them now.

A cache that a pod of v0.2.0 kept is not used and not removed: the pack
cache is a directory of its own in it. In a Deployment a pod of v0.3.0
is a new pod, with new volumes.

## Upgrading from v0.1.0

Up to v0.1.0 the store was a jaccard-store server of its own, and every
pod had a sidecar. From v0.2.0 on draal is the store and a pod is one
container, so:

1. **draal gets a bucket**, with `--s3-bucket`, a region and
   credentials. `--jaccard-server` and `--jaccard-prefix` are gone: a
   draal that is still given one of them ends and names it (`flag
   provided but not defined`), and the variables `JACCARD_SERVER` and
   `JACCARD_PREFIX` are read no more, so a draal that has those alone
   ends for want of `--s3-bucket`. Its data directory stays: the
   services, their generations and their rollouts are kept.
2. **Every pod becomes one container**, as in
   [`deploy/pod.yaml`](deploy/pod.yaml): no sidecar, and the
   `securityContext` and the volumes of that file. draal, the pods and
   the operator's own program change together, as with any release: a
   `great-machine rollout` of v0.1.0 is refused by a draal of v0.2.0 or
   later, which wants to be told the service when it is asked for the
   store.
3. **Every service is rolled out once more**, with the binary it runs if
   nothing else is to change. draal's store begins empty: what the old
   server holds is not taken over, and the stable generation of a
   service names store paths that the new store does not have. Until the
   service is rolled out again, a pod that starts fetches nothing, says
   which store path the store lacks, and stays not ready.

The old jaccard-store server and its bucket are not touched by any of
this, and can go when every service has been rolled out again.

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

In the dev shell are `go` (1.27), `sqlc`, `node` with `npm`, and
`great-machine` as built from the tree, with the web page in it. With
direnv, `.envrc` passes the option and reloads the shell, rebuilding the
program, when the sources of the program or of the page change. Nix sees
only the files git tracks, so `git add` new ones.

```sh
go test ./...
sqlc generate    # after changing the queries or the migrations in db/
```

The default shell cannot be entered while the Go module does not resolve,
because building the program is part of it: after a `go get` that left
`go.sum` behind, for example. `nix develop .#bare` has `go`, `sqlc` and
`node` without the program and needs no option; run `go mod tidy` there.

The tests need no cluster and no network beyond the loopback interface.
Those in `e2e` run draal, with its store over an S3 in memory, and
several zathrases in one process, with real child processes for the
services, and the terminal UI against them in a pseudo-terminal.
`scripts/test-pid1.sh` runs zathras as the first process of a container,
and takes Docker.

The store of a zathras is built and tested on every system, without
root, against a store in the test's process. What needs Linux and root
is `--user`, a service that is started as another user: the tests of
that are skipped anywhere else. `scripts/test-linux.sh` runs the tests
as root in a container that has what a pod's container has, which is no
capability beyond a container's own:

```sh
scripts/test-linux.sh                           # every package
scripts/test-linux.sh -run User -v ./launcher/  # the arguments are go test's
```

### The page

The sources of the web page are in `ui/`: SolidJS and TypeScript, built
by Vite. `ui/` has a `go.mod` of its own, which holds no Go and keeps the
Go tools out of the directory. The package `web` serves the page from
`web/dist`, which it embeds, and has the HTTP API the page talks to.

```sh
cd ui
npm ci           # the dependencies, as package-lock.json names them
npm run check    # the type check
npm test         # the unit tests, in Node
npm run build    # into ../web/dist; `go build` after it has the page
```

`web/dist` is not committed, but for a placeholder that keeps the
directory there. **The Go module builds and its tests pass without the
build**; a program built so answers `501` in place of the page. Nix needs
no `npm` by hand: `nix build` builds the page in a derivation of its own
(`.#ui`) and the program from the tree with that build at `web/dist`.
Evaluating the flake therefore builds the page, the first time and after
every change in `ui/`.

To work on the page, run the API over a made-up fleet in one terminal and
Vite in another:

```sh
go run ./web/demo           # the real API on 127.0.0.1:8080, no draal and no cluster
cd ui && npm run dev        # the page on http://localhost:5173/, reloaded as it is edited
```

**The shell of the demo is a real one**: a shell on the machine that runs
the demo, as the user who runs it. The demo has no authentication and
answers under any host name, like draal's page, so its `--addr` should
stay on loopback, which is its default.

Vite passes `/api`, WebSockets included, on to `127.0.0.1:8080`, which
can be a real draal as well.

The terminal UI has the same made-up fleet behind it with

```sh
go run ./tui/demo           # the terminal UI, no draal and no cluster; --empty for a fleet without a service
```

**Its shell is a real one too**: `s` on a zathras opens a shell on the
machine that runs the demo, as the user who runs it.

After a change of the dependencies (`npm install …`, which rewrites
`ui/package-lock.json`), the flake has to be told their new hash:

```sh
nix run nixpkgs#prefetch-npm-deps ui/package-lock.json    # prints npmDepsHash for flake.nix
```

With a stale hash `nix build .#ui` fails and names the right one in its
`got:` line. The shells and the derivation have the same Node, 24.

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
says), the type check and the build of the page and the tests of what
serves it once more with it, zathras as the first process of a container,
the tests that need root (`scripts/test-linux.sh`), the Nix build and
the image. `scripts/test-page.sh` asks the program that
Nix built, and the draal of the image, for the page.

A tag `v*` is a release: `.github/workflows/release.yml` builds the image
with Nix on a runner of each platform, pushes it as `:<tag>-amd64` and
`:<tag>-arm64`, joins the two as `:<tag>` and `:latest`, and creates a
GitHub release with notes generated from what was merged since the last
one.

```sh
git tag v0.3.0 && git push origin v0.3.0
```

## License

Licensed under the GNU Lesser General Public License, version 3 only
(`LGPL-3.0-only`). See [`LICENSE`](LICENSE) for the LGPL terms and
[`COPYING`](COPYING) for the GPL terms incorporated by the LGPL.
