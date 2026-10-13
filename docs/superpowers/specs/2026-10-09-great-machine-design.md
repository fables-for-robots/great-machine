# great-machine: design

2026-10-09

## 1. Purpose

great-machine rolls a program that was built with Nix on a laptop out to a
group of pods, and shows what those pods are doing.

A pod holds no image of the program. Its one container runs **zathras**,
an init that fetches what it is told to run into `/nix/store`, as plain
files, and starts it. Every zathras reports to **draal**, the
controller, which is also the store they fetch from. The operator talks
to draal with a key: from the command line to roll out, and through a web
page or a terminal UI to look. draal does for a key what the key was
granted, and nothing for a key that was granted nothing.

The store is a [jaccard-store](https://github.com/amber-store/jaccard-store)
server over an S3 bucket, inside draal. Up to stage 3 it was a server of
its own, and every pod had a sidecar,
[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store),
that mounted it on `/nix/store`. [The design of stage 4](2026-10-11-great-machine-stage-4-design.md)
is how the store came into draal, and that mount into zathras.
[The design of stage 5](2026-10-13-great-machine-stage-5-design.md) is
how the mount went: zathras pulls the store paths it is to run and
writes them out itself.
[The design of stage 6](2026-10-13-great-machine-stage-6-design.md) is who
may do what: the grants, the statement a zathras registers with, and the
page, which the operator serves. The three have what this document only
names of the store and of the grants.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌──────────────────────────────┐
 │ great-machine rollout│── iroh, with ──▶│ draal, one endpoint ID:      │
 │   nix-store -qR      │   the key of    │   great-machine/1            │
 │ great-machine tui    │   the operator  │   amber/jaccard-store/1      │
 │ great-machine web    │                 │ grants: who may do what      │
 │   └ 127.0.0.1:8080 ◀─┼─ the browser    └──────────────▲───────────────┘
 └──────────┬───────────┘                                │ iroh: both protocols,
            │ packs up                                   │ from one endpoint
            ▼                                            │
      ┌───────────┐                       ┌──────────────┴───────────────┐
      │ S3 bucket │ ◀──── packs down ──── │ pod, one container: zathras  │
      └───────────┘                       │   pack cache ─▶ /nix/store   │
                                          │   └─ the service             │
                                          └──────────────────────────────┘
```

It is done when:

- `great-machine rollout web /nix/store/<hash>-web/bin/web` on the laptop
  ends with every zathras of the service `web` running that binary, or with
  the rollout halted and a reason that names the zathras it failed on;
- a service that dies within 30 seconds of its start is restarted with
  exponential backoff, and a draal that is down or unreachable stops no
  service;
- the health check of a zathras passes while its service runs and at no
  other time, so that a pod is ready only then;
- the web page and the TUI both show every connected zathras with its
  status and its CPU, memory, disk and network figures, the last 400 lines
  its service wrote, and a shell in its container;
- a key does what it was granted and no more: a deployer of one service
  rolls that service out and is shown nothing of another, a zathras
  registers for the service its role key was granted, and a key that was
  granted nothing is refused everything.

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
- **operator**: whoever runs the commands, the TUI or the web page, with
  a key. What an operator may do is what its key was granted.
- **role**: `admin`, `deployer` or `zathras`.
- **grant**: an endpoint ID, a role, and a service or every service. What
  draal decides by, and all it decides by (4).
- **role key**: a key that holds a `zathras` grant. It is given to the
  pods of a service, signs for them, and connects to nothing.
- **statement**: what a zathras registers with: the endpoint ID of the
  role key and its signature over draal's endpoint ID and the zathras's
  own.

## 3. Program and commands

One Go module, `github.com/fables-for-robots/great-machine`, one program:

| Command | What it does |
|---|---|
| `great-machine draal` | the controller |
| `great-machine zathras` | the init of an app container |
| `great-machine id` | prints the endpoint ID of the operator's key, or of the key that `--key` names, creating the key if it is missing |
| `great-machine rollout [--parallel N] [--detach] SERVICE PATH [-- ARG...]` | pushes the closure and rolls it out |
| `great-machine rollback [--parallel N] [--detach] SERVICE` | rolls the stable generation out again |
| `great-machine status [SERVICE]` | lists services, or the zathrases of one |
| `great-machine logs [-f] SERVICE [ZATHRAS]` | prints the last 400 lines, and follows with `-f` |
| `great-machine shell SERVICE [ZATHRAS]` | a shell in the container of a zathras |
| `great-machine tui` | the terminal UI |
| `great-machine web` | serves the web page on the operator's machine, and prints its link |
| `great-machine access list` | lists who may do what (4) |
| `great-machine access grant ROLE ID [--service NAME] [--note TEXT]` | grants a role |
| `great-machine access revoke ROLE ID [--service NAME]` | revokes one |

`ZATHRAS` is a name, the beginning of one, or the beginning of an endpoint
ID, which tells apart two zathrases that were given the same name. It may
be left out when the service has one zathras. What matches none, or more
than one, ends the command with the names and endpoint IDs there are.

`--parallel N` (`GREAT_MACHINE_PARALLEL`, default 1) is how many zathrases
are restarted at a time; 0 is all of them at once. `--detach` starts the
rollout and does not wait for it.

Every flag has an environment variable, and a flag wins over its variable.

### draal

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run), `draal.sqlite` and the store's `store.sqlite` |
| `--admin ID` | `GREAT_MACHINE_ADMINS` | none | an endpoint ID that is an admin whatever the grants say (4); may be repeated, and the variable takes several with commas between them. The flag was `--operator` and the variable `GREAT_MACHINE_OPERATORS`: both are still taken, and mean the same |
| `--s3-bucket NAME` | `GREAT_MACHINE_S3_BUCKET` | required | the bucket of the store; with it `--s3-prefix`, `--s3-endpoint`, `--s3-region`, `--s3-path-style`, and `--store-scratch`, `--store-verify-jobs`, `--store-max-pack-size`, `--store-no-verify` ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.6) |
| `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |

At start draal logs its endpoint ID, which is all that a zathras or an
operator needs to reach it. With no admin, neither of the command line nor
among the grants, it warns that nobody can grant anything. It binds its
iroh endpoint and no other port: it serves no page.

### zathras

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
| `--role-key FILE` | `GREAT_MACHINE_ROLE_KEY` | required | the role key of its service, with which it signs its statement (4); zathras does not make the file |
| `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | where the pack cache is kept (7.8) |
| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | fetch nothing: `/nix/store` is whatever the machine has |
| `--user UID[:GID]` | `GREAT_MACHINE_USER` | zathras's own | the user and the group the service and the shells run as |
| `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
| `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |
| `--health-addr ADDR` | `GREAT_MACHINE_HEALTH_ADDR` | `:9890` | where the health check listens; empty for none |

### Operator commands

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--key FILE` | `GREAT_MACHINE_KEY` | `great-machine/operator.key` in the user's configuration directory | the operator's key, created on first use |

`rollout` also takes `--jobs N` (`GREAT_MACHINE_JOBS`, default 20), the
number of store paths pushed at a time, and `--temp-dir DIR`
(`GREAT_MACHINE_TEMP_DIR`), where the temporary stores of a push are made.

`web` also takes `--addr ADDR` (`GREAT_MACHINE_WEB_ADDR`, default
`127.0.0.1:8080`), where the page listens: a loopback address, and no
other (4).

`access grant` and `access revoke` name a role, `admin`, `deployer` or
`zathras`, and an endpoint ID. Without `--service` the grant is for every
service. `access list` prints a line for a grant: the role, the service or
`every`, the ID, the note, who granted it and when.

## 4. Identity and authorization

Everything but the packs of the store travels over iroh
([go-iroh](https://github.com/tmc/go-iroh) v0.3.0), and draal listens for
nothing else. The endpoint of draal accepts two ALPNs: `great-machine/1`,
which is the protocol of section 5, and `amber/jaccard-store/1`,
jaccard-store's own, for the store. A peer is known by its endpoint ID,
which is the public half of its key and which the handshake proves.
[The design of stage 6](2026-10-13-great-machine-stage-6-design.md) has
what this section says in full.

- **draal** has a key in its data directory. Its endpoint ID is its
  address, and that of the store.
- **An operator** has a key file. `great-machine id` prints its endpoint ID,
  which an admin grants a role, or which draal is given with `--admin`.
- **A zathras** makes a new key every time it starts and keeps it in
  memory. Its endpoint ID is its identity for as long as the process lives,
  across reconnections. Pods cannot share a key: two endpoints with one key
  evict each other at the relay, and draal could not tell them apart.
  It has that one key and no second: whenever it dials draal it binds one
  endpoint, which makes the connection of great-machine's protocol and,
  when a pull needs it, one of the store's (7.8).
- **A role key** is a key file that the pods of a service are given. It
  signs for them, as below, and connects to nothing.

### Grants

draal decides by grants, and by nothing else. A grant is an endpoint ID,
a role, and a service or every service; a key has as many grants as it
was given, so that one key can deploy `web` and `api` and nothing else.
An **admin** does everything. A **deployer** pushes and rolls out, and
looks at what it rolled out. A **zathras** fetches and reports.

| | admin | deployer of S | zathras of S |
|---|---|---|---|
| register as a zathras of S, and report | | | by a statement (below) |
| pull a store path of S | yes | yes | while registered for S |
| ask where the store of S is; push and list under the prefix of S | yes | yes | |
| delete from the store | yes | | |
| roll S out, roll it back, list its rollouts | yes | yes | |
| the logs, the samples and a shell of a zathras of S | yes | yes | |
| watch the fleet | all of it | its services | |
| grant, revoke and list grants; the store's page | yes | | |
| ask who it is | yes | yes | yes |

- An admin is of every service. A grant for every service counts for
  each; a deployer of every service is no admin, and deletes nothing and
  grants nothing.
- **A shell is a deployer's**, because a rollout is: whoever may run a
  program of its choosing in the containers of a service is not kept out
  of them by being refused a shell.
- **Nothing is open.** A peer that was granted nothing may ask who it is,
  and nothing else.

Every request passes one function of draal before anything is done:

```go
// Authorize says whether peer may do op on service.
Authorize(peer EndpointID, op Operation, service string) error
```

It reads a copy of the grants that draal keeps in memory, and nothing
else: the copy is read from the database when draal starts, and every
change is written to the database first and then put in the place of the
copy, whole. So the function has no error of a database to return. Two
things are asked of it besides the grants of the peer itself: for a
registration, whether the signer of the statement is a zathras of the
service; and for a pull, whether the peer is registered as a zathras of
the service, which draal's fleet says. A request that is of no service,
as a listing of the whole store is, is an admin's, and a deployer's of
every service where a deployer may do it at all.

A request that is refused is answered `unauthorized` and logged with the
peer, the operation and the service. A key that holds a grant is told
which role the operation takes, and for which service. Nobody is told
what exists: a deployer of `web` that asks for the log of a zathras of
`api` is answered `unavailable`, as for a zathras that is not connected,
and does not learn that there is one.

### The statement of a zathras

A zathras is given the role key of its service, `--role-key FILE`. When
it starts it signs with it, once, three lines:

```
great-machine zathras statement 1
<endpoint ID of draal, 64 hexadecimal characters>
<endpoint ID of the zathras, 64 hexadecimal characters>
```

`register` carries the endpoint ID of the role key and the signature.
draal refuses a registration unless, in this order, it is well-formed
(below), it has a statement, the signature is the role key's over this
draal's ID and the ID of the peer as the handshake proved it, and the
role key holds a `zathras` grant for the service of the registration.
The refusal says which of these failed, and for the last the command
that mends it: it is read in the log of a pod. A zathras that was
refused keeps what it runs and dials again (5.1).

- **The statement is worth nothing to anybody else**: it names the
  endpoint of the zathras and the draal it was made for. It does not
  expire, as it is no older than the key it names, which is gone with
  the process. It names no service: the grants of the role key say which
  services it signs for.
- **While a zathras is registered**, its endpoint ID may pull the store
  paths of its service, and no other's. draal remembers, with the
  session of a zathras, which role key signed for it.
- **zathras does not make the role key.** A file that is missing, or is
  no key, ends it at start: a new key would be one nobody granted
  anything. `great-machine id --key FILE` makes one, and prints its ID.
- **The role key is in the container the service runs in**, a Secret
  mounted as a file that root alone reads. A service that is root reads
  it, and can then sign for zathrases of its own service: it registers
  as what it already is, and fetches what it already has. `--user` keeps
  the service from the file, and a role key that is granted for one
  service signs for no other, which is why a service is given its own.

### Managing grants

Admins grant and revoke: with `great-machine access`, from the page and
from the terminal UI, which all ask draal the same requests (5.2).

- **The first admin.** `great-machine draal --admin ID` names the admins
  of the command line. They are admins whatever the table says, are
  listed with the grants, and no request revokes them: a table that is
  empty, as at the first start, has an admin, and no mistake locks
  everybody out. An admin cannot revoke its own `admin` grant either:
  once there is an admin, there is always one.
- **A revocation ends what the grant was used for.** Before draal
  answers it, it closes every connection of the key, of either protocol,
  and every session of a zathras that the key signed for, with the
  connections of those zathrases to the store. They dial again, and find
  what is still granted.
- A grant is refused unless the ID is an endpoint ID, the role one of
  the three, and the service a name of a service or none; its note is
  cleaned and cut at 200 bytes, as what a zathras reports is. Every
  grant and every revocation is logged by draal, with who asked. There
  is no history of them besides.

### What draal accepts from a peer

A zathras registers with a statement, and what it sends after that is
still not trusted to be small, well-formed or harmless to show: the role
key of a service is in the containers of that service.

- **A registration** is refused with `bad-request` unless the name is 1 to
  253 bytes of `A-Z a-z 0-9 . _ -`, beginning with a letter or a digit
  (a host name is), the version at most 64 bytes of printable ASCII, and
  the phase one of the four.
- **What a zathras reports** is bounded before draal keeps or shows it.
  Every string is made valid UTF-8 and its control characters become
  spaces. The binary is cut at 4096 bytes, the arguments at 128 of 1024
  bytes each, the error of the last exit at 1024 bytes. Of the prepared
  generations the 64 highest are kept. A report with a phase draal does
  not know ends the connection.
- **The reason of a failure** that a zathras gives is cleaned the same way
  and cut at 4096 bytes, so the reason of a rollout is bounded as well.
- **Samples.** Draal keeps of a zathras at most twice what one reporting
  every 2 seconds sends in 15 minutes, and ignores a sample that is not
  newer than the last it has.
- **Streams.** The first message of a stream has to come within 5 seconds.
  A connection has at most 64 streams that the peer opened being served
  at a time; one more is closed without an answer. A stream whose request
  was refused is closed 2 seconds after the refusal. A request with more
  than one of its fields set is refused.
- An answer that cannot be written, for another reason than the peer
  having gone, is logged by draal with the peer and the request.
- **The store's protocol** has the same bounds for its streams: a request
  within 5 seconds, 64 streams of a connection at a time. What a request
  holds is jaccard-store's server's to check. A request is authorized
  for the service whose prefix its reference is under, and a name or a
  prefix that is under no service's is of none
  ([stage 6](2026-10-13-great-machine-stage-6-design.md), §5). A refusal
  there is an error of that protocol whose message begins with
  `unauthorized`
  ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.3 and §3.4).

What is not bounded is the number of zathrases that register with the
statements of one role key. The fleet is sent whole, and enough
registrants that each report as much as they may make it larger than a
message can be: operators then get no fleet, and draal's log says why.

`Authorize` is the one place that decides. Tokens of
[clamp](https://github.com/amber-store/clamp) were once meant to take
that place from the allowlist of stages 1 to 5; the grants have it, and
nothing of clamp is built.

### The page and its ticket

draal serves no page. `great-machine web` serves it on the operator's
machine and asks draal with the operator's key, as the terminal UI does
(8.3, 9): the page can do what that key may, and draal decides. The
command decides nothing.

The command acts with the operator's key for whoever it answers, so it
listens on loopback alone, and answers nobody without a ticket:

- **The ticket.** The command makes two secrets when it starts, of 32
  random bytes each: the ticket, and what a cookie holds. It prints the
  link of the page with the ticket in it,
  `http://localhost:8080/?ticket=…`. A request for `/` with the ticket
  is answered with the cookie and a redirect to `/`, so that the ticket
  does not stay in the address. Every other request, for the files of
  the page as for the API, needs the cookie, and is answered `401`
  without it. The ticket is good for as long as the command runs, and
  the cookie with it; a command that is started again has a new one.
- **The host.** A request under another host name than `localhost`,
  `127.0.0.1` or `[::1]`, with the port the page listens on, is refused:
  a site that points a name of its own at the loopback address is not
  answered.
- **The origin.** A request that changes something, and every WebSocket,
  has to come from the page: its `Origin` is the host it was asked on,
  or it is refused. A cookie is sent to every port of a host, so a page
  of another program on the same machine would otherwise act with it.

Loopback is every user's of a machine. The ticket is what keeps another
user out, and whoever has the link has the page.

## 5. Protocol

A message is a CBOR map behind its length as four bytes, big-endian, of at
most 4 MiB. A request is the first message of a stream that the asking side
opens; one stream carries one request and what answers it. An answer is the
response of the operation or an error:

| Code | Meaning |
|---|---|
| `bad-request` | the request is malformed, or names something that cannot be |
| `unauthorized` | the peer may not do this |
| `not-found` | no such service, generation or grant |
| `unavailable` | the zathras is not connected; draal forgets one that has gone, so this is also the answer for one that never was, and it is the answer for a zathras of a service the key was not granted (4) |
| `internal` | draal or the zathras failed |

### 5.1 zathras and draal

The zathras dials draal and keeps the connection. On it:

| Opened by | Request | Answer |
|---|---|---|
| zathras | `register`: service, name, version of the program, its state (5.3), and its statement (4): the endpoint ID of the role key and the signature | `registered`, after which the zathras sends a `report` on this stream whenever its state changes and at least every 2 seconds |
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
  registers with the state it has then. One whose registration draal
  refused (4) does the same.

### 5.2 Operator and draal

The operator dials draal and opens what follows. Every request is decided
when it is made, by the grants of the key as they are then (4).

| Request | Answer |
|---|---|
| `store`: service | the endpoint ID of the store, which is draal's own, and the prefix of the service's references, `/services/SERVICE/nix/store/` |
| `rollout`: service, binary, arguments, closure, parallel | the rollout (its number, the generation) |
| `rollback`: service, parallel | the rollout |
| `watch` | the fleet (5.3) of the services the key may see: at once, and again whenever something changed, at most once a second |
| `rollouts`: service | the last 20 rollouts of the service, newest first |
| `history`: zathras | the samples (7.5) draal has of the zathras |
| `logs`: zathras, follow or not | log lines |
| `shell`: zathras, columns, rows, `TERM` | a session |
| `me` | the endpoint ID of the peer and its grants; every peer may ask |
| `grants` | every grant, the admins of the command line among them and marked |
| `grant`: ID, role, service, note | `granted`; a grant that is there already is there with the new note |
| `revoke`: ID, role, service | `revoked`, when what the grant was used for has ended (4) |
| `store_page`: path | the status, the content type and the body that jaccard-store's admin page answers a `GET` of that path with (8.3) |

A zathras is named by its endpoint ID. The commands that take a name look
it up in the fleet. The last four requests are an admin's.

Nothing was taken away when the requests of the grants and the statement
were added, and a reader ignores what it does not know: a zathras that
sends a statement registers with a draal of before that knows of none.

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

The fleet, as draal answers `watch`: every service that the key may see
(all of them for an admin and for a deployer of every service) and that
has a rollout in the database or a zathras connected, with its stable
generation, its last rollout (number, target, state, reason, parallel,
times, how many zathrases are prepared, activated and awaited) and its
zathrases, each with endpoint ID, name, the time it connected and its
state.

## 6. Rollout

### 6.1 The command

`great-machine rollout SERVICE PATH [-- ARG...]`:

1. **The binary.** A `PATH` inside `/nix/store` is taken as written, and
   refused when, through its symbolic links, it ends outside the store: a
   link to another store path is what a wrapper is, a link out of the
   store names a file that is in no closure. Any other path is resolved
   through its symbolic links, as `realpath` does, and has to end inside
   `/nix/store` (so `./result/bin/web` works). The binary has to be
   there. A Mach-O binary is refused: the pods run Linux.
2. **The closure.** `nix-store --query --requisites` of the store path the
   binary is in.
3. **The store.** draal is asked for the store of the service, which is
   draal's own endpoint ID and the prefix of the service, and the
   connection to draal is closed. The push dials draal again, for the
   store's protocol and with the operator's key as well, and two
   endpoints with one key put each other out at the relay: the key has
   one endpoint at a time.
4. **The push.** The store is asked which of the references
   `prefix + name` it has. A store path never changes under its name, so
   one that is there is not pushed again. Every other path is imported into
   a temporary store, as `jaccard-store push-dir` imports a directory, and
   pushed with the `client` package of jaccard-store, `--jobs` at a time,
   20 unless told otherwise. One that another service has pushed is
   imported and not uploaded: the store has a pack of that content and
   points the new reference at it. A
   store path that is a single file is pushed as a reference whose root is
   the file. A store path that is itself a symbolic link cannot be a
   reference (a link exists only as an entry of a directory), so a closure
   that has one is refused, with its name, before anything is pushed. The
   first path that fails ends the command; nothing was rolled out.
5. **The rollout.** draal is dialed again and sent the service, the
   binary, the arguments, the names of the closure and `--parallel`. It
   records a new generation and a rollout and answers at once. If draal
   cannot be reached now, the closure was pushed and nothing was rolled
   out, and the command says so: running it again pushes nothing. Draal
   refuses a closure that is empty, that does not hold the store path of
   the binary, or in which a name is empty, is `.` or `..`, or has a `/`
   in it.
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

After it starts, draal waits 45 seconds before the reconciler does
anything, so that the zathrases are back before it decides: a zathras
dials again after 30 seconds at most.

**Preparing.** Every connected zathras that has not prepared the target is
sent `prepare`. The zathras fetches the closure into its store (7.3) and
then checks that the binary is a file it may execute.

- When every connected zathras has prepared the target, the rollout is
  `activating`. With no zathras connected that is at once.
- A `prepare` that fails halts the rollout, with the name of the zathras
  and which store path it could not fetch, and why.
- A rollout that is still `preparing` 30 minutes after it was made is
  halted, and the reason names the zathrases that had not prepared.

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
- When every connected zathras is done, and none that left during the
  rollout is still being waited for, the rollout is `completed`.
- A zathras that goes is sent nothing and frees its place in the batch. If
  it comes back it is waiting, under way or done by what it reports.
- **A zathras that left during a rollout without being done holds the
  completion of the rollout for 2 minutes.** Without this, a fleet that
  loses draal for longer than the 10 seconds after which a zathras counts
  as gone would complete the rollout by its absence, and on its return
  every zathras would be moved to the new stable generation at once. A
  zathras that is back within the 2 minutes takes its place in the
  rollout again. One that stays away longer is gone, and the rollout
  completes over those who are there. Draal keeps who left in memory, and
  carries it over to a rollout that supersedes the one they left during.

**No rollout under way.** A service that never had a rollout has nothing
to run, and its zathrases stay `idle`. When the last rollout of a service
completed, a
zathras whose generation is not the stable one (a new pod, or one that
missed a rollout) is sent `prepare` and `activate` for the stable
generation. When the last rollout halted, the service is frozen: only a
zathras that is `idle` is sent the stable generation, if there is one, and
no other zathras is touched until the operator rolls out or rolls back.

This convergence is not paced: every zathras that is not on the stable
generation is moved at once. That is right for a new pod, and for one
that missed a rollout which has run on the others since. It is also what
happens to a fleet that returns after more than the 2 minutes above, or
after a restart of draal from which it stayed away longer than the 45
seconds: a known limit of this design.

A `prepare` or an `activate` that fails while no rollout is under way has
no rollout to halt. It is logged, and sent again after 30 seconds, for as
long as it fails: a new pod whose store cannot be reached for a while does
not stay `idle` for ever.

**A command that is not answered has failed.** Draal waits 30 minutes for
the answer to a `prepare` and 30 seconds for the answer to an `activate`,
which a zathras gives at once. Then it closes the stream, and the command
counts as one that failed, with a reason that says so: in a rollout it
halts the rollout, outside one it is sent again. **Every command that
fails is logged** by draal, with the zathras, the service, the command,
the generation and the reason.

## 7. zathras

zathras is the first process of the pod's container. It keeps the store
of its service in `/nix/store` (7.8), runs the service, keeps it
running, and tells draal.

### 7.1 Running the service

- The service is started with the environment and the working directory of
  zathras, no standard input, and a process group of its own. It is
  started by the launcher (7.8): as the user of `--user` where there is
  one, and otherwise as it is.
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
- A binary that cannot be started counts as a service that ended at once:
  its last exit has the error of the start and, for its status, -1.

### 7.2 As the first process

- Every child of the container is waited for, its own and the orphans it
  inherits, so that none stays a zombie. For this the program, as process
  1, starts itself again as a child and does nothing else but pass
  SIGTERM, SIGINT and SIGHUP on to it and wait for every child there is.
  The zathras that does the work is that child, and the container ends
  with its exit status.
- On SIGTERM, SIGINT or SIGHUP it stops the service as in 7.1, ends the shells,
  closes its store, which ends what is being fetched, and exits with 0.
  Nothing is unmounted. It starts nothing while it does.
  The pod's `terminationGracePeriodSeconds` has to be longer than
  `--stop-timeout`.

### 7.3 Preparing

`prepare` fetches the store paths of the closure into `/nix/store`,
twenty at a time. A store path that is there is there: a store path
never changes under its name, and the store is asked nothing. Any other
is pulled from draal's store into the pack cache, written out as files,
and moved to its name when all of it is on disk (7.8). The first that
cannot be fetched ends the others and is the error, which names the path
and says whether the store has none of that name for the service or why
it could not be fetched. All of them fetched is followed by the check of
the binary. draal ends a `prepare` that has outlived the rollout by
closing its stream, which ends the fetching. With `--no-store` nothing
is fetched and the binary alone is checked.

### 7.4 Logs

- Standard output and standard error of the service are copied to those of
  zathras, so `kubectl logs` shows what it always shows.
- They are also cut into lines and kept in a ring of the last 400. A line
  is kept with its time and its stream. A line of more than 16 KiB is cut
  into several, and bytes that are no valid UTF-8 are replaced, so that a
  service which writes anything at all leaves a log that can be read.
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

The figures are those of the container, which is the service, zathras
with what it fetches and writes out of the store, and the shells. A
sample goes out with the next report. draal keeps the last 15
minutes of every connected zathras in memory. Where a file is missing
(cgroup v1, another system) there are no samples, and the UIs say so.

### 7.6 Shell

`shell` starts `--shell` on a pseudo-terminal, in a session of its own, with
the environment of zathras and the `TERM` of the request, by the launcher
as the service is started (7.1). On its stream the
asking side sends input and changes of size, and zathras sends output and,
when the shell has ended, its exit status. A stream that ends ends the
shell: SIGHUP to its process group, then SIGKILL. That holds whatever
either side waits in at that moment: a session whose program takes no
input, with more typed than the stream holds, ends like any other when
the asking side closes, on draal and in the container.

A shell is in the container of zathras: it sees the store, the files and
the processes the service sees, as the user of the service and with what
the service has: the capabilities of the container when that user is
root, and none with `--user`.

### 7.7 Health check

zathras listens for HTTP on `--health-addr`, by default on port 9890 of
every address: the kubelet asks at the address of the pod, not on loopback.
`GET /healthz` answers by the phase of the service (5.3):

| Phase | Answer |
|---|---|
| `running` | `200`, with `running` as its body |
| `idle`, `stopping`, `backoff` | `503`, with the phase as its body |

- The check fails before the first `activate`, while the service is being
  stopped, while it waits to be started again after it ended, and while
  zathras shuts down.
- It passes as soon as the process was started, and says nothing about
  what the service does. A service that has a check of its own is probed
  at its own port instead.
- Whether draal can be reached changes nothing.
- **It is a readiness probe.** As a liveness probe it would have the
  kubelet restart the container of a service that stays down, and with the
  container go the backoff of zathras, its ring of lines and its identity.

### 7.8 The store and the launcher

[The design of stage 5](2026-10-13-great-machine-stage-5-design.md) has both in full (§3 and §4). In short:

- **The store directory.** `/nix/store` of the container is a directory
  that zathras fills, and nothing is mounted on it. A store path is
  pulled from draal's store, over a connection of the store's protocol
  that the endpoint of the zathras's connection to draal makes (4), and
  written out beside its name, as
  `/nix/store/.great-machine-partial-` and a number. It is moved to
  `/nix/store/NAME` when all of it is on disk: a store path under its
  name is a store path that is whole. Whatever in `/nix/store` begins
  with `.great-machine-partial-` is what an earlier run was writing when
  it ended, and is removed whenever zathras starts. A zathras that
  cannot open its store ends at start.
- **What is written out** is what a Nix store holds, and no more:
  directories and the files that can be executed with mode `0555`, every
  other file with `0444`, symbolic links as they are, all of it owned by
  zathras, with no setuid bit. A store path that is a single file gets
  an execute bit. A device, a FIFO or a socket is refused.
- **The pack cache**, `packs/` under `--store-cache`, keeps every object
  that was pulled, so that a pull downloads what the cache lacks. A
  store path that a rollout changed leans on the pack of the one before:
  a pod that has the one before downloads one pack for it, and a pod
  that is new two at the most. A pod's disk holds every store path
  twice, as packs and as files, and nothing is removed from either.
- **A pull that fails**, for another reason than the store having no
  such reference, is tried again for 2 minutes.
- **A pull needs a zathras that is connected to draal**, and registered:
  the second connection lives no longer than the first, and draal lets a
  zathras pull while it is registered (4). That is no new bound, as a
  pull is part of a `prepare`, which draal sends on that connection. A
  pull that finds no connection has failed like any other, and is tried
  again.
- **The store directory and the cache are zathras's alone to write.**
  It makes them so when it starts, whatever a volume came as: in a
  directory everybody may write to, a service that is another user
  could put a store path of its own in the place of one.
- **The container** is given no capability and keeps the AppArmor
  profile of the runtime. Its `allowPrivilegeEscalation: false` sets
  `no_new_privs` for every process in it.
- **The launcher** is what starts the service and every shell. With
  `--user` it starts them as that user, with the group and no other,
  and gives them the pipes and the terminal they are started on, so
  that they can open `/dev/stdout` or their terminal anew. Without
  `--user` it starts them as they are.
- **A service that is root is root in the container of zathras.** It can
  signal zathras and write what root may, the store directory and the
  pack cache among it. With `--user` it cannot.

## 8. draal

### 8.1 What it keeps

In `draal.sqlite`, through [sqlc](https://sqlc.dev) and `modernc.org/sqlite`,
as jaccard-store does:

| Table | Columns |
|---|---|
| `services` | name, stable generation, created |
| `generations` | service, number, binary, arguments, closure, created, created by (an endpoint ID) |
| `rollouts` | number, service, generation, parallel, state, reason, created, created by, finished |
| `grants` | endpoint ID, role, service (empty for every service), note, created, created by |

A service gets its row with its first rollout. The key of a grant is its
ID, its role and its service together: an ID has as many grants as it was
given. In memory only: the zathrases that are connected, the last report
of each, 15 minutes of its samples, and which role key signed for it. A
zathras that has gone is forgotten. In memory as well is the copy of the
grants that draal decides from (4).

Beside it, in `store.sqlite`, is what jaccard-store's server keeps of the
store: the references, the packs and which pack leans on which, and the
uploads under way. The packs are in the bucket. That database is the only
record of the store ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.2).

draal is meant for a fleet of some hundreds of zathrases: the fleet is sent
whole to everyone who watches, less the services a key may not see.

### 8.2 One way in

What an operator can do it asks over iroh, and draal has no other way
in: no HTTP, and no page. The commands, the terminal UI and the page ask
through one client (`operator`), each with the operator's key, so none
of them can do what another may not, and each is decided by the grants
of the key it asks with.

### 8.3 HTTP

The HTTP API is `great-machine web`'s, not draal's: the command serves
it on the operator's machine, in front of that client (4).

| Path | What |
|---|---|
| `/api/watch` | WebSocket: the fleet as JSON, as `watch` sends it |
| `/api/services/{service}/rollouts` | the last 20 rollouts |
| `/api/zathras/{id}/history` | the samples |
| `/api/zathras/{id}/logs` | WebSocket: the ring, then what follows |
| `/api/zathras/{id}/shell?cols=&rows=&term=` | WebSocket: binary messages are input and output, text messages are JSON (a change of size one way, the exit status the other) |
| `GET /api/me` | the endpoint ID of the operator's key and its grants |
| `GET /api/grants` | every grant |
| `POST /api/grants` | grants: ID, role, service, note |
| `DELETE /api/grants?id=&role=&service=` | revokes |
| `/store/` and what is under it | jaccard-store's admin page over the store's database, which draal answers with `store_page`: read-only, for admins, and an answer has to fit a message (4 MiB) |
| everything else | the web page; a path that is no file is answered with `index.html`, but for one under `/assets/`, which is not found |

Every request needs the cookie that the ticket gives, and is answered
`401` without it; one under another host name, and one that changes
something or opens a WebSocket from another origin, is refused (4).

The HTTP API can roll nothing out and nothing back: that is the command
line's.

A refusal of draal is passed on as it is: JSON with the code and the
message that the protocol over iroh has, and an HTTP status that fits
the code. What the page asks while draal cannot be reached is answered
`unavailable`. What is refused of a WebSocket is told as its last
message, since a page cannot read the answer to an upgrade that failed.
Every answer carries a content security policy that allows the page's
own origin alone.

## 9. Web page

A single-page application in [SolidJS](https://www.solidjs.com) with
TypeScript, built with Vite and embedded in the program with `go:embed`.
It follows the Fables for Robots design system. `great-machine web`
serves it, on the operator's machine and behind a ticket (4).

| View | Shows |
|---|---|
| services | every service: stable generation, its zathrases by phase, the last rollout and its progress |
| a service | its zathrases as a table (name, phase, generation, time running, restarts, CPU and memory), the rollout under way with where every zathras stands in it by its own report, and the last 20 rollouts |
| a zathras | its state in full; CPU, memory, disk and network over the last 15 minutes as charts; the last 400 lines, followed live; a shell in [xterm.js](https://xtermjs.org) |
| access | for admins: the grants as a table (role, service, ID, note, who granted it and when; those of the command line marked), a form that grants, and for every grant that can be revoked a button that does, after it was confirmed |

The page holds one WebSocket to `/api/watch` and draws from what it
receives, which is the fleet of the services its key may see. It says so
when the connection is lost, and connects again; it loads, and says the
same, while draal cannot be reached.

- **Who it is**: the header has the beginning of the operator's endpoint
  ID and its roles.
- For an admin the header has a link to the store's page, `/store/`,
  which is jaccard-store's own and not part of this application. A key
  that is no admin has no such link, and no access view.
- **A key with no grant** is told so, with its ID, which is what an
  admin needs to grant it one.
- **A cookie that is no longer good** (the command was started again) is
  said: the page tells to open the link the command printed.

The sources are in `ui/`, a project of its own that a `go.mod` of its own
keeps out of the Go module. Its build is written to `web/dist`, which the
package `web` embeds. The Go module builds and is tested without that
build: `great-machine web` then serves one line that says the page was
not built.

## 10. Terminal UI

`great-machine tui` is a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
program (version 2) that talks to draal as the other operator commands
do: it takes `--draal` and `--key` and no argument, and draal shows it
what its key was granted (4). It needs a terminal as its standard input
and its standard output, and says so with status 2 when either is none.
It ends with 0 when the user quits, with 130 when it is interrupted, and
with 1 and draal's refusal when draal refuses the key at the first dial.
It can roll nothing out and nothing back.

It has the views of the web page, one at a time: the services; a service
with its zathrases, the rollout under way and its last rollouts; a zathras
with its state, its figures of the last 15 minutes as sparklines and the
last 400 lines of its log, followed live; and access, as below. Its
header says who the key is, as the page's does. The keyboard alone
drives it.
Its colours are the terminal's own sixteen, so that it is light or dark
as the terminal is, and it has no theme of its own; `NO_COLOR` is
respected. It is made for 80 columns and 24 rows, uses more, draws down
to 50 columns and 12 rows, and says so in one line below that.

**Nothing that draal or a zathras sent reaches the terminal as it came.**
A log line is what a program in a container wrote, and on a terminal an
escape sequence can set the title, move the cursor over what is shown, or
be answered by the terminal as if it had been typed. Every name, state,
reason, refusal and log line is drawn through one function that takes
control characters and escape sequences out; a log line keeps its colours
by being read and drawn again in the terminal UI's own. The text of the
error the command prints when it ends has been through the same function.
The exception is a shell that the user opened: its output is the user's.

When the connection is lost the terminal UI says so and for how long,
keeps what it showed, dials again after 1 second and then after pauses
that double up to 30 seconds (a key asks at once), and is back on the
view it was on. A draal that takes the connection and does not answer
within a minute is given up and dialed again, like one that could not be
reached.

**The terminal is left as it was found** on every way out: the user
quits, the terminal UI is interrupted or told to end, it fails, a shell
ends or is left. A terminal UI that does not end within 5 seconds of
being told to is cut off, with the terminal restored, and says so. The
one thing it waits for without a bound is a terminal that takes no
output (a session over a network that has stalled): to end then would
leave the terminal as it is, so it ends when the terminal takes output
again or goes.

What a zathras may send is bounded where it is taken: a word (a name, a
phase, a reason, an argument, a refusal) is kept to 4 KiB and says how
much was left out, and of its arguments and of the generations it has
prepared 64 are shown. A column is never counted narrower than a terminal
draws it, so that no name pushes the rest of its row off the screen.

From a zathras that is connected, `s` opens the shell. The TUI asks draal
for the session and keeps its screen until it is there, saying that the
shell is being opened and that `Esc` gives it up; what draal refuses is
said there too. Then it leaves its screen, writes one line that says
whose shell it is, and gives the terminal to the session. It takes the
terminal back when the shell has ended or `Ctrl-]` was typed; either way
the session is closed, which hangs the remote shell up. From `s` until
the shell has ended the TUI acts on no other key: what is typed while
the shell is being opened is dropped, and while the shell has the
terminal every key is the shell's and the TUI draws nothing. (In the few
milliseconds in which the terminal changes hands a key may be dropped,
and one typed in the instant before the shell takes the terminal may be
the shell's or, rarely, be acted on by the TUI after the shell.) Afterwards it shows the
fleet as it is then, and says how the shell ended. What ends the TUI ends
the shell with it, and so does the connection to draal when it is lost,
and a zathras that goes. A shell that does not end with the TUI is left
behind after two seconds, and writes nothing to the terminal from then
on.

From the view of the services, `a` opens **access**: the grants, a line
for each as `great-machine access list` prints them. `g` grants, through
a form of four fields: the role (one of three, changed with the arrow
keys), the ID, the service (empty for every service) and the note.
`Enter` on the last field asks draal, `Esc` gives it up, and what draal
refuses is said in the form, which stays as it was filled. `x` revokes
the grant under the cursor, after `y` to a line that names it; an admin
of the command line has no `x`. A key that is no admin is told that the
view is an admin's, with its own ID. What is typed into the form, and
what draal sends back, is drawn through the function that takes control
characters out, as everything is.

`great-machine shell` is the same session without the TUI around it. Its
exit status is that of the shell. With a standard input that is no
terminal (`echo uptime | great-machine shell web`) the input is passed on
as it is, and at its end the shell is told that its input has ended.

**A session's read of the terminal can be ended.** When the session
returns, for whatever reason, nothing of it reads the terminal any more,
so that the TUI, which lives on, loses no key. As stage 1 left it, a
session on a terminal that ended for another reason than `Ctrl-]` left a
read of the terminal behind, which took the next thing that was typed and
dropped it; `great-machine shell` ends its process after the session, so
nothing was lost there. Likewise a client of draal that is closed ends
every watch, log and shell it started: the TUI dials again whenever the
connection was lost, for as long as it runs.

## 11. Packages

Every package is at the top level of the module; there is no `internal/`.

| Package | Purpose | Depends on |
|---|---|---|
| `wire` | messages, framing, error codes | |
| `node` | keys, and what a key signs; the endpoint of draal with the protocols it accepts; dialing draal, and dialing it again from the same endpoint for another protocol; over the `node` package of jaccard-store where that fits | go-iroh |
| `node/nodetest` | a network in memory, for tests without iroh | `node` |
| `logring` | the ring of lines and its followers | `wire` |
| `stats` | samples from cgroup v2 and `/proc/net/dev` | `wire` |
| `reaper` | as process 1: runs the program as its child and waits for every child | |
| `supervisor` | starts, stops and restarts the service; backoff | `logring`, `wire` |
| `testservice` | a program for the tests: exits, hangs or ignores SIGTERM on request | |
| `launcher` | starts the service and the shells as the user of `--user` | |
| `storedir` | the store of a zathras: pulls the store paths of a closure into a pack cache, over a connection it is given, and writes them out under `/nix/store` | jaccard-store, amber-store core, `node`, `wire` |
| `ptyshell` | shell sessions on a pseudo-terminal | |
| `health` | the HTTP health check | |
| `zathras` | the init: the above, and the connection to draal, with the statement it registers with | `wire`, `node` |
| `storeserver` | the store in draal: jaccard-store's server, its database and bucket, answered on draal's endpoint, every request authorized for its service; its admin page, answered through draal | jaccard-store, `authz`, `node` |
| `db`, `db/dbq` | migrations, queries, what sqlc makes of them: the services, the generations, the rollouts and the grants | |
| `fleet` | the connected zathrases, their reports and samples | `wire` |
| `rollout` | the reconciler | `db`, `fleet` |
| `authz` | the roles, the grants in memory, `Authorize` | `wire` |
| `draal` | the handler of the protocol: a registration with its statement, what a peer may ask and see by its grants, the requests that manage grants, a revocation and the connections it ends | `db`, `fleet`, `rollout`, `authz`, `wire`, `node` |
| `operator` | the client of draal for the commands, the TUI and the page | `wire`, `node` |
| `closure` | the closure of a store path, by `nix-store` | |
| `push` | store paths to the store | jaccard-store, amber-store core |
| `web` | the HTTP API and the embedded page, in front of an operator's client; the ticket | `wire` |
| `tui` | the terminal UI, over an interface that the client of `operator` satisfies | `wire`, `term`, `human` |
| `tui/tuitest` | a pseudo-terminal for the tests that run the terminal UI as a whole; part of no program | |
| `human` | how a size, a duration, an age, a rate and a time are written for a person | `wire` |
| `demo` | a made-up fleet with made-up grants, for developing the page (`web/demo`) and the terminal UI (`tui/demo`) | `wire` |
| `term` | a remote shell session on the local terminal | |
| `cli` | the commands, with urfave/cli | |
| `cmd/great-machine` | `main` | `cli` |
| `e2e` | tests of the whole | |

`supervisor`, `logring`, `stats`, `rollout` and `fleet` know nothing of
iroh, and are tested without it: `wire` is the messages and their
encoding, and has nothing of iroh in it either. `rollout` is one function
without state, which is given the last rollout of a service and what its
zathrases report, and answers what is to be done.

## 12. Build and delivery

- **Flake.** `flake.nix` with `.envrc` (`use flake`), as the sibling
  repositories have. The shell has Go, sqlc and Node. The Go is 1.27,
  which the NFS server of stage 4's mount (Buildbarn's) needed. Nothing
  needs it any more, and nothing is gained by going back.
- **The Go build** is [gonixgo](https://github.com/draganm/gonixgo)'s
  `buildGoApplication`, with `CGO_ENABLED = 0`. It needs
  `--option allow-unsafe-native-code-during-evaluation true`.
- **The page** is built by a derivation of its own. The source that gonixgo
  builds is the repository with that build put at `web/dist`. Nothing that
  is generated is committed but what sqlc makes, which `sqlc diff` holds to
  its queries, the lock file of the page's dependencies, and the hash of
  those dependencies in the flake.
- **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
  arm64, is made by Nix of the same derivation: the program, a static
  busybox for `/bin/sh` and the shell's tools, and CA certificates. Its
  entrypoint is `great-machine`. It is the image of the container of a
  pod, and of draal. A pod puts a volume over `/nix/store`, which the
  image has empty, and zathras fills it, so nothing in the image may be
  a link into its own store: the program, busybox and the certificates
  are copied in as files. gonixgo does not cross-compile, so each
  architecture is built on a runner of its own and the two are joined
  under one tag.
- **`deploy/pod.yaml`** is a whole pod: one container with
  `great-machine zathras`, no capability beyond a container's own and
  `allowPrivilegeEscalation: false`, a volume on `/nix/store` and one
  for the pack cache, the role key of its service from a Secret, as a
  file that root alone reads, and a readiness probe on the health check
  of zathras.
  **`deploy/draal.yaml`** runs draal with a volume for its data, the
  bucket with its credentials from a Secret, a volume for the scratch
  space of the store, and its first admin. It has no Service and no
  port: draal serves no page.
- **CI.** `test.yml` runs vet, the tests, `sqlc diff` and the type check,
  the unit tests and the build of the page, and the tests that need
  root, those of `--user`, in a container that has what a pod's
  container has (`scripts/test-linux.sh`). `release.yml`, on a tag `v*`,
  builds and pushes the image as `:<tag>` and `:latest` and makes a
  GitHub release.

## 13. Errors

- **A push that fails** ends `rollout` before draal has heard of it.
- **draal is unreachable for an operator command**: the command fails with
  what the dial said.
- **A key was not granted what it asks**: `unauthorized`, with the role
  the operation takes (4). The command prints it and ends with 1.
- **A zathras has no statement, one that is not its own, or a role key
  that was not granted its service**: draal refuses the registration (4).
  The zathras keeps its service running and dials again; draal's log and
  the pod's say why.
- **The role key file of a zathras is missing**: zathras ends at start.
- **A grant is revoked while it is used**: the connection ends (4). A
  rollout that was being followed goes on in draal; the command that
  followed it says that the connection was lost.
- **`great-machine web` cannot listen** (the port is taken): it ends with
  the error. **It cannot reach draal**: the page loads and says so.
- **The link of the page is opened without its ticket, or after the
  command was started again**: `401`, and what to do.
- **draal is unreachable for a zathras**: 5.1. The service is not touched.
- **draal restarts during a rollout**: 6.2. The rollout goes on.
- **A zathras restarts** (the container was restarted): it comes back
  `idle` with a new endpoint ID and is sent a generation as 6.2 says.
  What it had fetched is there where `/nix/store` is a volume of the
  pod, and is written out again from the pack cache where it is not.
- **A zathras cannot fetch a path**, or the store has none of that name
  for the service: the `prepare` fails with the path and the rollout
  halts before anything was restarted. A path that could not be fetched
  because draal or the bucket could not be reached is tried again for 2
  minutes first.
- **A path cannot be written out** (the disk is full, or the reference
  holds what a store does not): the `prepare` fails with the path and
  why, and what was written of it is removed.
- **The store of a zathras cannot be opened** (a directory cannot be
  made or written): zathras ends at start with the error.
- **zathras ends while it writes a store path**: the name is not in
  `/nix/store`, what was written is removed at the next start, and the
  next `prepare` fetches the path.
- **draal is down**: a service runs from files on the pod's own disk,
  and needs nothing of draal. Everything a generation runs from is
  fetched before it is started.
- **The new binary crashes**: the rollout halts at the first zathras, which
  goes on restarting it with backoff until the operator rolls out or back.
- **A shell or a log is asked of a zathras that is gone**, or of one of
  a service the key was not granted: `unavailable`. One that goes during
  a session ends the session, and the UIs say so.

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
- **`logring`, `stats`, `wire`, `health`** as units; `stats` from
  files of the two formats as fixtures, `health` with every phase.
- **Who may do what**, as [stage 6](2026-10-13-great-machine-stage-6-design.md)
  has it (§14). `authz`: every row and column of the table of section 4,
  for one service and for every service, a key with several grants, a
  registration by its signer, a pull by who is registered, and a change
  that the next question sees. `db`: grants put, put again with another
  note, removed, and read back. The statement, signed and verified, and
  one that was signed for another draal or another endpoint. `draal`: a
  registration with no statement, with one for another endpoint, with
  one of a role key without the grant, and with a good one; every
  request of an operator for a key with each role, for its own service
  and for another; the fleet as a deployer of one service sees it; the
  requests that manage grants with what they refuse; a revocation that
  ends the connection of the key and the session of a zathras it signed
  for.
- **`ptyshell`** with a real shell: input, output, a change of size, the
  exit status, the end of the stream.
- **`push`** against a jaccard-store server in the test's process over a
  fake S3 bucket, as jaccard-nfs-nix-store's tests have it, with a store
  path that is a directory, one that is a file, and one that is there
  already.
- **`storeserver`** as [stage 4](2026-10-11-great-machine-stage-4-design.md) has it
  (§12): the store over a bucket in memory and real endpoints. Since
  stage 6 request by request, for an admin, a deployer of the service, a
  deployer of another, and a zathras that is registered for the service
  or for another.
- **`storedir`, `launcher`** as [stage 5](2026-10-13-great-machine-stage-5-design.md) has them
  (§10). The store of a zathras on every system and without root,
  against a store in the test's process: a closure written out with its
  modes and its links, a store path that is there, the pack that the
  cache saves, a store path the store lacks, what a run left half
  written, a bucket that fails for a while. The launcher's `--user` on
  Linux as root, in a container that has what a pod's container has
  (`scripts/test-linux.sh`).
- **`e2e`**: draal, with its store over a bucket in memory, and several
  zathrases in one process over go-iroh on the loopback interface, with a
  fake in the place of the store of a zathras and real child processes:
  closures of two services pushed through draal's endpoint ID and pulled
  again by who may; one rollout with no fake at all, on every system and
  without root, into a store directory that is fetched from draal's
  store; a rollout from the operator client to `completed`; one that
  halts on a binary that exits; logs, a shell and the fleet through the
  operator client and through HTTP, which is the code of
  `great-machine web` over an operator's client; a deployer of one
  service that rolls it out and is refused another; a zathras of each of
  two services, each with its role key, that fetches its own closure and
  is refused the other's; a grant that is revoked, which ends the watch
  of its key; the health check of a zathras, which fails before the
  rollout, passes when the service runs and fails again when the service
  has exited.
- **As the first process**: a script that runs a small program, which
  enters as the program does, as process 1 of a container, and checks
  that the work is done by a child, that an orphan leaves no zombie, that
  SIGTERM reaches the child and that the container ends with the child's
  exit status. That a zathras which is told to end stops its service is
  tested with the supervisor and in the tests of the whole.
- **The page**: type check and build, and unit tests of what it works
  out itself: what it shows of numbers and times, the pauses between its
  attempts to connect, the lines it keeps of a log, the lines of the
  charts, where a zathras stands in a rollout, and what the access view
  works out. What is drawn is looked at against a made-up fleet behind
  the real API (`web/demo`). The API itself (`web`): the ticket and the
  cookie, the host and the origin, each refused and each let through,
  and a draal that cannot be reached.
- **The terminal UI**: its model without a terminal and without a
  network, as a function of the messages it gets: every key, the
  connection lost and back on every view, an answer that comes late; the
  access view with its list, its form, a refusal, a revocation that is
  confirmed and one that is not, and a key that is no admin.
  What it draws at every size of terminal down to one column and one
  row, and of a fleet whose every string is escape sequences, also by
  fuzzing. The program in a pseudo-terminal, for the terminal on every
  way out and around a shell. In `e2e`, in a pseudo-terminal against a
  real draal with real zathrases: the fleet, a rollout, a log, a shell,
  a zathras that goes while its shell is open, and draal lost and back.
  What is drawn is looked at against the made-up fleet (`tui/demo`).

## 15. To be settled first

The plan begins with these, as each can change something above:

1. **go-iroh.** Settled while the plan was written, against go-iroh
   v0.3.0 on loopback: both sides of a connection open streams and accept
   them, several at a time. A stream is not seen by the peer before
   something was written to it, which every request of this protocol
   does first. A peer that was killed is noticed after 6 to 9 seconds
   with an idle timeout of 6 seconds and a keep-alive of 2. The endpoint
   of draal is bound by the `node` package of jaccard-store; the dialing
   is great-machine's own code after that model, since jaccard-store's
   keeps the connection to itself. Over a relay, and on Linux, none of
   this was measured.
2. **gonixgo.** Settled in two halves. While the plan of stage 1 was
   written: it builds the program without cgo on macOS and on arm64
   Linux, and the image made of it runs. While the plan of stage 2 was
   written, with gonixgo v0.2.0 on macOS and on arm64 Linux: the page is
   built by a derivation of its own, a second derivation is the
   repository with that build at `web/dist`, and gonixgo builds the
   program from it with the directory embedded; the program and the image
   built so serve the page. gonixgo asks `go list` which files a package
   embeds while Nix evaluates, so both derivations are built during
   evaluation, on the machine that evaluates (import from derivation, as
   gonixgo's own tool is): an evaluation after a change of the page takes
   as long as the page's build, and the derivations of the program are
   the same whichever machine evaluated them.
3. **Store paths that are no directories.** Settled while the plan was
   written, against jaccard-store v0.7.0: a single file is imported and
   pushed by the same calls as a directory, and comes back identical. A
   symbolic link cannot be pushed as a link, since the import follows it,
   so `rollout` refuses a closure that has one (6.1). The mount of
   stage 4 served a single-file reference as a regular file with its
   content, and with an execute bit whether or not the file had one,
   since a root that is a file has no entry of a directory to keep a mode
   in: that was seen running on Linux in stage 4. Since stage 5 zathras
   writes such a store path out as a file with an execute bit, for the
   same reason (7.8).
4. **One endpoint, two protocols.** Settled while the design of stage 6
   was written, with go-iroh v0.3.0 in one process over loopback: an
   endpoint that dialed holds a connection of great-machine's protocol
   and one of another to the same peer at a time, each carries streams,
   either is closed without the other noticing, and another is dialed
   afterwards. Over a relay it was not tried.

## 16. Order of work

Six stages, each of which ends with something that works by itself:

1. zathras, draal and the operator commands: a rollout from the laptop to
   pods, with `status`, `logs` and `shell` on the command line.
2. The web page.
3. The terminal UI.
4. The store in draal and the mount in zathras: one endpoint ID, and a
   pod of one container ([its design](2026-10-11-great-machine-stage-4-design.md)).
5. The store of a zathras without a mount: plain files in `/nix/store`,
   and a container that is given no capability
   ([its design](2026-10-13-great-machine-stage-5-design.md)).
6. Who may do what: roles that are granted to a key for a service, a
   zathras that registers with a statement, and a page that the operator
   serves ([its design](2026-10-13-great-machine-stage-6-design.md)).

## 17. Not in this design

- Tokens of clamp: the grants are what decides.
- A key that signs for the keys of the jobs of a CI, so that two jobs
  with one deployer's key do not evict each other.
- Grants that expire, and a history of grants.
- The page on an address that is not loopback, and TLS for it.
- Checks of what a service does. draal and the health check of zathras go
  by whether its process runs, and for how long.
- Environment and secrets of a service: they are those of the pod.
- Rollouts and rollbacks from the web page or the TUI.
- Removing a service from draal, and removing anything from the store.
- Removing a store path or a pack from the disk of a pod: both grow
  until the pod goes.
- More than one draal for a fleet.
