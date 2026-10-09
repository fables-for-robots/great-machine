# great-machine: design

2026-10-09

## 1. Purpose

great-machine rolls a program that was built with Nix on a laptop out to a
group of pods, and shows what those pods are doing.

A pod holds no image of the program. It mounts a Nix store that the
[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store)
sidecar serves out of a [jaccard-store](https://github.com/amber-store/jaccard-store)
server, and its app container runs **zathras**, an init that starts the
program it is told to run. Every zathras reports to **draal**, the
controller. The operator talks to draal: from the command line to roll out,
and through a web page or a terminal UI to look.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌───────────────────────────────────┐
 │ great-machine rollout│── push ──────▶  │ jaccard-store server and bucket   │
 │   nix-store -qR      │                 └──────────────▲────────────────────┘
 │                      │                                │ fetch
 │ great-machine tui    │── iroh ──▶ draal ◀── iroh ── zathras ─ sidecar (/nix/store)
 └──────────────────────┘             │                  │
        browser ── HTTP, loopback ────┘                  └─ the service
```

It is done when:

- `great-machine rollout web /nix/store/<hash>-web/bin/web` on the laptop
  ends with every zathras of the service `web` running that binary, or with
  the rollout halted and a reason that names the zathras it failed on;
- a service that dies within 30 seconds of its start is restarted with
  exponential backoff, and a draal that is down or unreachable stops no
  service;
- the web page and the TUI both show every connected zathras with its
  status and its CPU, memory, disk and network figures, the last 400 lines
  its service wrote, and a shell in its container.

## 2. Terms

- **service**: a named group of zathrases that are meant to run the same
  thing. A name is a DNS label: `[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?`.
  One draal controls any number of services.
- **generation**: what a service runs, numbered from 1 within the service
  and never changed: the absolute path of a binary under `/nix/store`, its
  arguments, and the closure of the store path the binary is in.
- **rollout**: the move of a service to a generation, the *target*.
- **stable generation**: the target of the last rollout of a service that
  completed.
- **operator**: whoever runs the commands, the TUI or the web page.

## 3. Program and commands

One Go module, `github.com/fables-for-robots/great-machine`, one program:

| Command | What it does |
|---|---|
| `great-machine draal` | the controller |
| `great-machine zathras` | the init of an app container |
| `great-machine id` | prints the endpoint ID of the operator's key, creating the key if it is missing |
| `great-machine rollout [--parallel N] [--detach] SERVICE PATH [-- ARG...]` | pushes the closure and rolls it out |
| `great-machine rollback [--parallel N] [--detach] SERVICE` | rolls the stable generation out again |
| `great-machine status [SERVICE]` | lists services, or the zathrases of one |
| `great-machine logs [-f] SERVICE [ZATHRAS]` | prints the last 400 lines, and follows with `-f` |
| `great-machine shell SERVICE [ZATHRAS]` | a shell in the container of a zathras |
| `great-machine tui` | the terminal UI |

`ZATHRAS` is a name or the beginning of one. It may be left out when the
service has one zathras. A name that matches none, or more than one, ends
the command with the names there are.

`--parallel N` (`GREAT_MACHINE_PARALLEL`, default 1) is how many zathrases
are restarted at a time; 0 is all of them at once. `--detach` starts the
rollout and does not wait for it.

Every flag has an environment variable, and a flag wins over its variable.

### draal

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
| `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
| `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where the web page listens; empty for no web page |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |

At start draal logs its endpoint ID, which is all that a zathras or an
operator needs to reach it. Without `--operator` it warns that nobody can
act as an operator over iroh.

### zathras

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
| `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
| `--preload-url URL` | `GREAT_MACHINE_PRELOAD_URL` | `http://127.0.0.1:9889/v1/preload` | the preload endpoint of the sidecar |
| `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
| `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |

### Operator commands

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--key FILE` | `GREAT_MACHINE_KEY` | `great-machine/operator.key` in the user's configuration directory | the operator's key, created on first use |

`rollout` also takes `--jobs N` (`GREAT_MACHINE_JOBS`, default 5), the
number of store paths pushed at a time, and `--temp-dir DIR`
(`GREAT_MACHINE_TEMP_DIR`), where the temporary stores of a push are made.

## 4. Identity and authorization

Everything but the web page travels over iroh
([go-iroh](https://github.com/tmc/go-iroh) v0.3.0), on one ALPN,
`great-machine/1`. A peer is known by its endpoint ID, which is the public
half of its key and which the handshake proves.

- **draal** has a key in its data directory. Its endpoint ID is its address.
- **An operator** has a key file. `great-machine id` prints its endpoint ID,
  which draal is given with `--operator`.
- **A zathras** makes a new key every time it starts and keeps it in
  memory. Its endpoint ID is its identity for as long as the process lives,
  across reconnections. Pods cannot share a key: two endpoints with one key
  evict each other at the relay, and draal could not tell them apart.

Every request passes one function of draal before anything is done:

```go
// Authorize says whether peer may do op on service.
Authorize(peer EndpointID, op Operation, service string) error
```

| Operation | Allowed to |
|---|---|
| register as a zathras of a service | every peer |
| everything else | the endpoint IDs given with `--operator` |

A request that is refused is answered `unauthorized` and logged with the
peer and the operation.

This function is the one place that decides. Tokens of
[clamp](https://github.com/amber-store/clamp) are meant to replace the
allowlist later, with one verb and one path for every operation
(`PUT zathras/<service>`, `GET service/<service>`, `PUT rollout/<service>`,
`GET log/<service>`, `PUT shell/<service>`); nothing of that is built now.

**The web page has no authentication**, as a browser has no iroh key. It
listens on loopback unless told otherwise, and is reached through
`kubectl port-forward` or an SSH tunnel. On loopback it answers only to a
loopback address or `localhost` as the host name, and its WebSockets refuse
an `Origin` that is not the host they were asked on. **Whoever can reach
that port has a shell in every container.**

## 5. Protocol

A message is a CBOR map behind its length as four bytes, big-endian, of at
most 4 MiB. A request is the first message of a stream that the asking side
opens; one stream carries one request and what answers it. An answer is the
response of the operation or an error:

| Code | Meaning |
|---|---|
| `bad-request` | the request is malformed, or names something that cannot be |
| `unauthorized` | the peer may not do this |
| `not-found` | no such service, generation or zathras |
| `unavailable` | the zathras is not connected |
| `internal` | draal or the zathras failed |

### 5.1 zathras and draal

The zathras dials draal and keeps the connection. On it:

| Opened by | Request | Answer |
|---|---|---|
| zathras | `register`: service, name, version of the program, its state (5.3) | `registered`, after which the zathras sends a `report` on this stream whenever its state changes and at least every 2 seconds |
| draal | `prepare`: generation, binary, closure | `prepared`, or an error with the reason |
| draal | `activate`: generation, binary, arguments | `activated`, when the zathras has taken the generation as its own, or an error |
| draal | `logs`: follow or not | log lines (7.4), until the last one, or without end |
| draal | `shell`: columns, rows, `TERM` | a session (7.6) |

- A zathras that registers with an endpoint ID draal has a connection for
  already takes the place of that connection, which is closed.
- draal takes a zathras for gone when its connection ends or when no report
  has come for 10 seconds, and closes what is left of the connection.
- A zathras that has lost draal keeps what it runs and dials again: after
  1 second, then twice as long each time, up to 30 seconds, for ever. It
  registers with the state it has then.

### 5.2 Operator and draal

The operator dials draal and opens:

| Request | Answer |
|---|---|
| `store` | the jaccard-store server and the prefix |
| `rollout`: service, binary, arguments, closure, parallel | the rollout (its number, the generation) |
| `rollback`: service, parallel | the rollout |
| `watch` | the fleet (5.3): at once, and again whenever something changed, at most once a second |
| `rollouts`: service | the last 20 rollouts of the service, newest first |
| `history`: zathras | the samples (7.5) draal has of the zathras |
| `logs`: zathras, follow or not | log lines |
| `shell`: zathras, columns, rows, `TERM` | a session |

A zathras is named by its endpoint ID. The commands that take a name look
it up in the fleet.

### 5.3 State

The state of a zathras, as it reports it:

| Field | Meaning |
|---|---|
| `generation` | the generation it has taken as its own; none before the first `activate` |
| `binary`, `args` | what that generation runs |
| `phase` | `idle` (no generation), `running`, `stopping` (the service was told to end and has not ended) or `backoff` (the service has ended and waits to be started) |
| `pid`, `running_for` | of the service, while `running` |
| `restart_at` | while in `backoff` |
| `last_exit` | how the service ended last: exit status or signal, when, after how long, and for which generation |
| `restarts` | how often the service was started again after it had ended by itself, since the zathras started |
| `prepared` | the generations that were prepared since the zathras started |
| `preparing` | the generation being prepared, if one is |
| `sample` | the latest sample (7.5), if the platform gives one |

The fleet, as draal answers `watch`: every service that has a rollout in
the database or a zathras connected, with its stable generation, its last
rollout (number, target, state, reason, parallel, times, how many zathrases
are prepared, activated and awaited) and its zathrases, each with endpoint
ID, name, the time it connected and its state.

## 6. Rollout

### 6.1 The command

`great-machine rollout SERVICE PATH [-- ARG...]`:

1. **The binary.** A `PATH` inside `/nix/store` is taken as written. Any
   other path is resolved through its symbolic links, as `realpath` does,
   and has to end inside `/nix/store` (so `./result/bin/web` works). The
   binary has to be there. A Mach-O binary is refused: the pods run Linux.
2. **The closure.** `nix-store --query --requisites` of the store path the
   binary is in.
3. **The store.** draal is asked for the jaccard-store server and the
   prefix.
4. **The push.** The server is asked which of the references
   `prefix + name` it has. A store path never changes under its name, so
   one that is there is not pushed again. Every other path is imported into
   a temporary store, as `jaccard-store push-dir` imports a directory, and
   pushed with the `client` package of jaccard-store, `--jobs` at a time. A
   store path that is a single file is pushed as a reference whose root is
   the file. The first path that fails ends the command; nothing was rolled
   out.
5. **The rollout.** draal is sent the service, the binary, the arguments,
   the names of the closure and `--parallel`. It records a new generation
   and a rollout and answers at once.
6. **Following.** The command watches the fleet and shows the rollout until
   it has completed or halted. It exits with 0 when the rollout completed
   and with 1 when it halted or was superseded, having printed the reason.
   An interrupt, and `--detach`, end the watching and not the rollout.

Progress is shown as jaccard-store shows it: a line for every step with the
time it took and what came of it, a bar where the amount is known, and
plain lines where standard error is no terminal.

A rollout always makes a new generation, even of the same binary: rolling
out what runs already is how a service is restarted.

`great-machine rollback SERVICE` makes a rollout whose target is the stable
generation, and no new generation. It is refused when the service has no
stable generation, and when the last rollout completed, as there is
nothing to go back from.

### 6.2 The rollout in draal

A rollout is a row in the database: service, target, `parallel`, who asked
for it, and a state.

| State | Meaning |
|---|---|
| `preparing` | zathrases are fetching the closure; nothing was restarted |
| `activating` | zathrases are being moved to the target, some at a time |
| `completed` | every connected zathras runs the target; the target is the stable generation |
| `halted` | it failed, and the reason is recorded |
| `superseded` | another rollout of the service was begun before this one ended |

A service has at most one rollout that is `preparing` or `activating`. A new
rollout of the service supersedes it.

draal does not run a rollout as a sequence of steps. A **reconciler** looks
at the last rollout of a service and at what its zathrases report, and does
what is missing. It looks again whenever a zathras connects, reports or
goes, and whenever a rollout is made. A draal that was restarted, and a
zathras that was gone and is back, are therefore carried on with: nothing
of a rollout is kept in memory that the reports do not give back.

After it starts, draal waits 15 seconds before the reconciler does
anything, so that the zathrases are back before it decides.

**Preparing.** Every connected zathras that has not prepared the target is
sent `prepare`. The zathras posts the closure to the preload endpoint of
the sidecar and then checks that the binary is a file it may execute.

- When every connected zathras has prepared the target, the rollout is
  `activating`. With no zathras connected that is at once.
- A `prepare` that fails halts the rollout, with the name of the zathras
  and what the sidecar said.
- A rollout that is still `preparing` 30 minutes after it was made is
  halted.

**Activating.** For the reconciler a zathras is

- **done** when it runs the target and has for 30 seconds;
- **under way** when the target is its generation and it is not done;
- **failed** when the target is its generation and its phase is `backoff`,
  which a service enters only by ending within 30 seconds of a start, or
  when it refused `activate`;
- **waiting** when its generation is another one, or none.

Then:

- A zathras that failed halts the rollout, with its name and how its
  service ended.
- While fewer than `parallel` zathrases are under way, the waiting zathras
  that comes first by name is sent `prepare`, if it has not prepared the
  target, and then `activate`. With `parallel` 0 all of them are sent it.
- A zathras whose phase is `idle` has nothing to lose: it is sent `prepare`
  and `activate` at once and is not counted against `parallel`. It can fail
  like any other.
- When every connected zathras is done, the rollout is `completed`.
- A zathras that goes is no longer looked at. If it comes back it is
  waiting, under way or done by what it reports.

**No rollout under way.** A service that never had a rollout has nothing
to run, and its zathrases stay `idle`. When the last rollout of a service
completed, a
zathras whose generation is not the stable one (a new pod, or one that
missed a rollout) is sent `prepare` and `activate` for the stable
generation. When the last rollout halted, the service is frozen: only a
zathras that is `idle` is sent the stable generation, if there is one, and
no other zathras is touched until the operator rolls out or rolls back.

## 7. zathras

zathras is the first process of the app container. It runs the service,
keeps it running, and tells draal.

### 7.1 Running the service

- The service is started with the environment and the working directory of
  zathras, no standard input, and a process group of its own.
- **`activate`**: zathras checks that the binary is a file it may execute,
  and refuses otherwise, leaving what runs alone. Then it takes the
  generation as its own, stops the service if one runs, forgets any backoff
  and starts the new binary.
- **Stopping**: SIGTERM to the process group. If the service has not ended
  after `--stop-timeout` (2 minutes), SIGKILL.
- When the service has ended, whatever is left of its process group is
  killed.
- **A service that ends is started again**, whatever its exit status. If it
  ran for 30 seconds or longer, at once. If it ended sooner, after a delay:
  1 second the first time, then twice as long each time, up to 2 minutes. A
  run of 30 seconds or longer makes the next delay 1 second again.
- A binary that cannot be started counts as a service that ended at once.

### 7.2 As the first process

- zathras waits for every child of the container, its own and the orphans
  it inherits, so that none stays a zombie.
- On SIGTERM or SIGINT it stops the service as in 7.1, ends the shells and
  exits with 0. It starts nothing while it does. The pod's
  `terminationGracePeriodSeconds` has to be longer than `--stop-timeout`.

### 7.3 Preparing

`prepare` posts the store paths of the closure, one on a line, to the
preload endpoint. An answer of 200 is followed by the check of the binary;
any other answer is the error, with its body. draal ends a `prepare` that
has outlived the rollout by closing its stream, which ends the request.

### 7.4 Logs

- Standard output and standard error of the service are copied to those of
  zathras, so `kubectl logs` shows what it always shows.
- They are also cut into lines and kept in a ring of the last 400. A line
  is kept with its time and its stream. A line of more than 16 KiB is cut
  into several.
- zathras adds lines of its own to the ring, as a third stream, when it
  starts the service and when the service ends: the generation, the binary
  and the process ID; the exit status, the time it ran and when it is
  started again.
- The ring outlives the service: the last lines of a service that crashed
  are what one looks for.
- `logs` answers with the ring. With `follow` it goes on with every new
  line. A reader that is too slow loses lines and is told how many.

### 7.5 Samples

Every 2 seconds zathras reads, on Linux with cgroup v2:

| Figure | From |
|---|---|
| CPU, in cores | `usage_usec` of `/sys/fs/cgroup/cpu.stat`, over the time since the last reading |
| memory, in bytes, and its limit | `memory.current` and `memory.max` |
| disk read and written, in bytes per second | `rbytes` and `wbytes` of `io.stat`, over all devices |
| network received and sent, in bytes per second | `/proc/net/dev`, over all interfaces but the loopback |

The figures are those of the container, which is the service, zathras and
the shells. A sample goes out with the next report. draal keeps the last 15
minutes of every connected zathras in memory. Where a file is missing
(cgroup v1, another system) there are no samples, and the UIs say so.

### 7.6 Shell

`shell` starts `--shell` on a pseudo-terminal, in a session of its own, with
the environment of zathras and the `TERM` of the request. On its stream the
asking side sends input and changes of size, and zathras sends output and,
when the shell has ended, its exit status. A stream that ends ends the
shell: SIGHUP to its process group, then SIGKILL.

A shell is in the container of zathras: it sees the store, the files and
the processes the service sees.

## 8. draal

### 8.1 What it keeps

In `draal.sqlite`, through [sqlc](https://sqlc.dev) and `modernc.org/sqlite`,
as jaccard-store does:

| Table | Columns |
|---|---|
| `services` | name, stable generation, created |
| `generations` | service, number, binary, arguments, closure, created, created by (an endpoint ID) |
| `rollouts` | number, service, generation, parallel, state, reason, created, created by, finished |

A service gets its row with its first rollout. In memory only: the
zathrases that are connected, the last report of each and 15 minutes of
its samples. A zathras that has gone is forgotten.

draal is meant for a fleet of some hundreds of zathrases: the fleet is sent
whole to everyone who watches.

### 8.2 One interface for every way in

What an operator can do is one Go interface, `Controller`: `Store`,
`Rollout`, `Rollback`, `Watch`, `Rollouts`, `History`, `Logs`, `Shell`. The
handler of the iroh protocol and the HTTP API are both written against it,
so the TUI and the web page cannot differ in what they are able to show.

### 8.3 HTTP

| Path | What |
|---|---|
| `/api/watch` | WebSocket: the fleet as JSON, as `watch` sends it |
| `/api/services/{service}/rollouts` | the last 20 rollouts |
| `/api/zathras/{id}/history` | the samples |
| `/api/zathras/{id}/logs` | WebSocket: the ring, then what follows |
| `/api/zathras/{id}/shell?cols=&rows=` | WebSocket: binary messages are input and output, text messages are JSON (a change of size one way, the exit status the other) |
| everything else | the web page; a path that is no file is answered with `index.html` |

The HTTP API can roll nothing out and nothing back. A rollout needs the Nix
store of the laptop, and the page has no authentication.

## 9. Web page

A single-page application in [SolidJS](https://www.solidjs.com) with
TypeScript, built with Vite and embedded in the program with `go:embed`.
It follows the Fables for Robots design system.

| View | Shows |
|---|---|
| services | every service: stable generation, its zathrases by phase, the last rollout and its progress |
| a service | its zathrases as a table (name, phase, generation, time running, restarts, CPU and memory), the rollout under way with every zathras' part in it, and the last 20 rollouts |
| a zathras | its state in full; CPU, memory, disk and network over the last 15 minutes as charts; the last 400 lines, followed live; a shell in [xterm.js](https://xtermjs.org) |

The page holds one WebSocket to `/api/watch` and draws from what it
receives. It says so when the connection is lost, and connects again.

The sources are in `ui/`, a project of its own that a `go.mod` of its own
keeps out of the Go module. Its build is written to `web/dist`, which the
package `web` embeds. The Go module builds and is tested without that
build: draal then serves one line that says the page was not built.

## 10. Terminal UI

`great-machine tui` is a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
program that talks to draal as the other operator commands do. It has the
views of the web page: services, a service, a zathras with its state, its
figures as sparklines and its log, followed live. From a zathras, a key
opens the shell: the TUI gives the terminal to the session and takes it
back when the shell has ended or `Ctrl-]` was typed. `great-machine shell`
is the same session without the TUI around it.

## 11. Packages

Every package is at the top level of the module; there is no `internal/`.

| Package | Purpose | Depends on |
|---|---|---|
| `wire` | messages, framing, error codes | |
| `node` | keys, the endpoint of draal, dialing draal; over the `node` package of jaccard-store where that fits | go-iroh |
| `logring` | the ring of lines and its followers | |
| `stats` | samples from cgroup v2 and `/proc/net/dev` | |
| `reaper` | waits for every child and tells who asked | |
| `supervisor` | starts, stops and restarts the service; backoff | `reaper`, `logring` |
| `preload` | the client of the sidecar's preload endpoint | |
| `ptyshell` | shell sessions on a pseudo-terminal | `reaper` |
| `zathras` | the init: the above, and the connection to draal | `wire`, `node` |
| `db`, `db/dbq` | migrations, queries, what sqlc makes of them | |
| `fleet` | the connected zathrases, their reports and samples | `wire` |
| `rollout` | the reconciler | `db`, `fleet` |
| `authz` | `Authorize` and the allowlist | |
| `draal` | `Controller` and its implementation, the handler of the protocol | `db`, `fleet`, `rollout`, `authz`, `wire`, `node` |
| `operator` | the client of draal for commands and TUI | `wire`, `node` |
| `closure` | the closure of a store path, by `nix-store` | |
| `push` | store paths to jaccard-store | jaccard-store, amber-store core |
| `web` | the HTTP API and the embedded page | `draal` |
| `tui` | the terminal UI | `operator` |
| `term` | a remote shell session on the local terminal | |
| `cli` | the commands, with urfave/cli | |
| `cmd/great-machine` | `main` | `cli` |
| `e2e` | tests of the whole | |

`supervisor`, `logring`, `stats`, `rollout` and `fleet` know nothing of
iroh, and are tested without it.

## 12. Build and delivery

- **Flake.** `flake.nix` with `.envrc` (`use flake`), as the sibling
  repositories have. The shell has Go, sqlc and Node.
- **The Go build** is [gonixgo](https://github.com/draganm/gonixgo)'s
  `buildGoApplication`, with `CGO_ENABLED = 0`. It needs
  `--option allow-unsafe-native-code-during-evaluation true`.
- **The page** is built by a derivation of its own. The source that gonixgo
  builds is the repository with that build put at `web/dist`. Nothing that
  is generated is committed but what sqlc makes, which `sqlc diff` holds to
  its queries.
- **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
  arm64, is made by Nix of the same derivation: the program, a static
  busybox for `/bin/sh` and the shell's tools, and CA certificates. Its
  entrypoint is `great-machine`. It is the image of the app container of a
  pod, and of draal.
- **`deploy/pod.yaml`** is a whole pod: the sidecar as jaccard-nfs-nix-store
  documents it, and the app container with `great-machine zathras`,
  `/nix/store` mounted with `HostToContainer` propagation.
  **`deploy/draal.yaml`** runs draal with a volume for its data.
- **CI.** `test.yml` runs vet, the tests, `sqlc diff` and the type check
  and build of the page. `release.yml`, on a tag `v*`, builds and pushes
  the image as `:<tag>` and `:latest` and makes a GitHub release.

## 13. Errors

- **A push that fails** ends `rollout` before draal has heard of it.
- **draal is unreachable for an operator command**: the command fails with
  what the dial said.
- **draal is unreachable for a zathras**: 5.1. The service is not touched.
- **draal restarts during a rollout**: 6.2. The rollout goes on.
- **A zathras restarts** (the container was restarted): it comes back
  `idle` with a new endpoint ID and is sent a generation as 6.2 says.
- **The sidecar cannot fetch a path**: the `prepare` fails and the rollout
  halts before anything was restarted.
- **The new binary crashes**: the rollout halts at the first zathras, which
  goes on restarting it with backoff until the operator rolls out or back.
- **A shell or a log is asked of a zathras that is gone**: `unavailable`.
  One that goes during a session ends the session, and the UIs say so.

## 14. Tests

Tests use the package `testing` of the standard library.

- **`supervisor`** against real child processes, built from a test
  program that exits, hangs or ignores SIGTERM on request. The 30 seconds,
  the 2 minutes and the delays are values of the supervisor that the tests
  make short.
- **`rollout`** against a fleet of fake zathrases and a clock of the test:
  preparing, batches, `parallel` 0, a failure in each phase, superseding,
  rollback, a zathras that joins or goes at every point, and a reconciler
  that is made anew in the middle, as after a restart of draal.
- **`logring`, `stats`, `wire`, `authz`** as units; `stats` from files of
  the two formats as fixtures.
- **`ptyshell`** with a real shell: input, output, a change of size, the
  exit status, the end of the stream.
- **`push`** against a jaccard-store server in the test's process over a
  fake S3 bucket, as jaccard-nfs-nix-store's tests have it, with a store
  path that is a directory, one that is a file, and one that is there
  already.
- **`e2e`**: draal and several zathrases in one process over go-iroh on the
  loopback interface, with a fake preload endpoint and real child
  processes: a rollout from the operator client to `completed`; one that
  halts on a binary that exits; logs, a shell and the fleet through the
  operator client and through HTTP; a peer that is no operator, refused.
- **As the first process**: a test that runs zathras as PID 1 of a
  container and checks that orphans are reaped and that SIGTERM ends the
  service, run on Linux by a script as jaccard-nfs-nix-store has one.
- **The page**: type check and build. Its logic is in what it is sent.

## 15. To be settled first

The plan begins with these, as each can change something above:

1. **go-iroh.** That draal can open streams on a connection it accepted,
   and how soon a peer that vanished is noticed. Whether the `node` package
   of jaccard-store can bind and dial for `great-machine/1`, or
   great-machine needs its own.
2. **gonixgo.** That it builds a package that embeds a directory, from a
   source tree into which another derivation's output was put, and that
   the result runs on a static busybox image for both platforms.
3. **Store paths that are no directories.** That a single file, and a
   symbolic link, as a store path can be pushed and are served by the
   sidecar. If a symbolic link cannot, `rollout` refuses a closure that has
   one and names it.

## 16. Order of work

Three stages, each of which ends with something that works by itself:

1. zathras, draal and the operator commands: a rollout from the laptop to
   pods, with `status`, `logs` and `shell` on the command line.
2. The web page.
3. The terminal UI.

## 17. Not in this design

- Tokens of clamp, and any authentication of the web page.
- Health checks beyond a service that has run for 30 seconds.
- Environment and secrets of a service: they are those of the pod.
- Rollouts and rollbacks from the web page or the TUI.
- Removing a service from draal.
- More than one draal for a fleet.
