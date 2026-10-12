# great-machine stage 4: one endpoint, no sidecar

2026-10-11

This is a change to the design of
[`2026-10-09-great-machine-design.md`](2026-10-09-great-machine-design.md).
What it does not mention stays as that document says. A section of that
document is written as design §N, one of this document as §N.

## 1. Purpose

Up to stage 3 a fleet needs two things besides great-machine: a
jaccard-store server, which is a service of its own with an endpoint ID of
its own, and in every pod the jaccard-nfs-nix-store sidecar, a privileged
container that mounts the store for the app container. Stage 4 takes both
into the program:

- **draal serves the store.** The jaccard-store server runs in draal, on
  draal's iroh endpoint. Its endpoint ID is draal's: the one ID that a
  zathras or an operator is given is all they need, for the protocol of
  great-machine and for the store.
- **zathras mounts the store.** The NFS server of jaccard-nfs-nix-store
  runs in zathras, which mounts it on `/nix/store` of its own container.
  A pod is one container.
- **The service is not given what zathras needs to mount.** The container
  gets `CAP_SYS_ADMIN` and is not privileged, and zathras starts the
  service and the shells without that capability.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌──────────────────────────────┐
 │ great-machine rollout│── iroh ───────▶ │ draal, one endpoint ID:      │
 │   nix-store -qR      │  both protocols │   great-machine/1            │
 │ great-machine tui    │                 │   amber/jaccard-store/1      │
 └──────────┬───────────┘                 └──────────────▲───────────────┘
            │                                            │ iroh: great-machine/1,
            │ packs up                                   │ and pulls of the mount
            ▼                                            │
      ┌───────────┐                       ┌──────────────┴───────────────┐
      │ S3 bucket │ ◀──── packs down ──── │ pod, one container: zathras  │
      └───────────┘                       │   NFS on 127.0.0.1 ─▶ /nix/store
                                          │   └─ the service             │
                                          └──────────────────────────────┘
```

Pack bytes go between the clients and the bucket through pre-signed URLs
that draal hands out, as before: draal never carries them, and draal
itself reaches the bucket only to verify a pack and to clean up.

It is done when:

- draal, given a bucket and no jaccard-store server, takes the push of
  `great-machine rollout` on its own endpoint ID, and a pod of one
  container runs the rolled out binary out of `/nix/store`;
- `deploy/pod.yaml` has no sidecar and no privileged container, and its
  app container has `CAP_SYS_ADMIN` and AppArmor `Unconfined` and nothing
  else beyond a container's defaults;
- the service and a shell of a zathras have neither `CAP_SYS_ADMIN` nor a
  way to gain it in the container, which `/proc/self/status` in a shell
  shows;
- nothing listens in the pod for a list of store paths to fetch;
- every service has its own references in the store, and the `/nix/store`
  of a pod is made of its service's alone;
- a pod that ends leaves no mount on its node.

## 2. Terms

- **the store**: the jaccard-store server in draal, its database and its
  bucket.
- **the mount**: the NFS server in zathras, its cache, and its mount on
  `/nix/store`.
- **the launcher**: the thread of zathras that starts the service and the
  shells (§5).

## 3. draal serves the store

### 3.1 One endpoint, two protocols

draal's endpoint accepts two ALPNs: `great-machine/1` (design §4) and
`amber/jaccard-store/1`, jaccard-store's protocol. It is bound as before,
by jaccard-store's `node.Bind`, and given the second ALPN with go-iroh's
`SetALPNs`. A connection is handed by its ALPN: one of great-machine to
draal as before, one of jaccard-store to the store.

Two endpoints with one key evict each other at the relay, so the store
cannot have an endpoint of its own with draal's key: it is the same
endpoint, or another ID.

### 3.2 The store

The store is jaccard-store v0.7.0's `server` package, with the database
and the bucket of its own packages, as `jaccard-stored` puts them
together:

| What | Where |
|---|---|
| the database | `store.sqlite` in `--data`, beside `draal.sqlite` |
| the packs | the bucket of `--s3-bucket`, under `--s3-prefix` |
| the scratch space of verification | `great-machine-scratch`, a directory the store makes for itself in `--store-scratch`, which is `--data` unless told otherwise |
| the key | none of its own: draal's |

The settings jaccard-store's server has and draal has no flag for keep
jaccard-store's defaults: an upload has an hour, a URL is valid for an
hour, a part is 64 MiB. The sweeper runs every 30 seconds, as in
`jaccard-stored`.

jaccard-store's server empties its scratch directory whenever it starts.
That is why the store's is a directory of its own making, by a name
nothing else has: a `--store-scratch` that is the data directory, or one
above it, or `/`, would otherwise cost draal its key and its databases
at its next start, as a review of this stage's code showed.

Verification needs scratch space for `--store-max-pack-size` times
`--store-verify-jobs`, 32 GiB with the defaults. That does not belong on
the volume of draal's database: `deploy/draal.yaml` gives the scratch
space a volume of its own (§6).

draal does not serve the store with the server's own `Serve`, which takes
the whole endpoint. A connection of the store's ALPN is answered by
draal's code: every stream is one request, read with jaccard-store's
`wire.ReadRequest`, authorized (§3.3), answered with the server's
`Handle`, and written back with `wire.WriteFrame`. The remote ID that
`Handle` is given, and records as the uploader of a pack, is the peer's
endpoint ID.

### 3.3 Who may do what

The table of design §4 becomes:

| Operation | Allowed to |
|---|---|
| register as a zathras of a service | every peer |
| pull a reference from the store | every peer |
| everything else, the store's push, list and delete among it | the endpoint IDs given with `--operator` |

The store's operations are jaccard-store's: `pull` is the second row,
`push-start`, `push-upload`, `push-commit`, `list` and `delete` the third.
An operation draal does not know is refused.

Pulls are open because the mount of a zathras pulls with a key of its own
(§4.1), which draal does not know, and because the content of the store is
what the pods run. Until now everything of the store was open, to whoever
knew the server's ID: pushing and deleting are closed now.

A refusal is the store's answer with an error of jaccard-store's protocol,
code `bad_request` (it has no code for a refusal) and the message
`unauthorized: <operation> is for operators`, and is logged with the peer
and the operation, as a refusal of draal's own protocol is. A push begins
by asking what the store has, so the refusal that a push ends with, for
a key that is no operator's, names `list`. `great-machine rollout` does
not get that far with such a key: it first asks draal where the store
is, which is an operator's to ask, and prints draal's own refusal.

### 3.4 What draal accepts on the store's protocol

The bounds of design §4 for streams hold on this protocol too: the request has to
come within 5 seconds of the stream, and a connection has at most 64
streams being answered at a time; one more is closed without an answer. A
request that does not parse is closed without an answer, as
`jaccard-stored` does. Everything else is the server's to check, as it
does for any client.

A stream that was answered is kept until the peer has ended it, 2
seconds at most: closing a connection discards what its streams have not
delivered, so a request is not over before its answer was read.

When draal stops, the store's requests under way are given 10 seconds,
the grace `jaccard-stored` gives them, before the connections are closed.
A request ends with its context then. One that does not, as a
verification in the middle of a large pack does not until it is through,
has the 10 seconds once more and is left behind: draal is not held by it.
The database of the store is closed after that, and after the sweeper has
returned.

### 3.5 The store's page

jaccard-store's admin page, which shows what the store holds and what
sharing and compression save, is part of draal's web page under `/store/`.
It is read-only, and has the authentication of draal's page: none (design §4,
§9). The header of draal's page has a link to it. The demo of the page,
which has no store, answers there with a line that says so.

### 3.6 Flags

Of draal's flags (design §3), `--jaccard-server` and `--jaccard-prefix` are gone.
New:

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--s3-bucket NAME` | `GREAT_MACHINE_S3_BUCKET` | required | the bucket of the store |
| `--s3-prefix P` | `GREAT_MACHINE_S3_PREFIX` | none | put before the name of every object in the bucket |
| `--s3-endpoint URL` | `GREAT_MACHINE_S3_ENDPOINT` | AWS | an S3 service other than AWS: `http://` or `https://`, a host, a port and a path if it has them, and nothing else. It is in the URLs that clients are given, so the laptop and the pods have to reach it under that name |
| `--s3-region R` | `GREAT_MACHINE_S3_REGION` | the AWS SDK's, as `AWS_REGION` | the region of the bucket, which the URLs are signed for. One of the two has to say it: without a region draal ends at its start, and names the flag |
| `--s3-path-style` | `GREAT_MACHINE_S3_PATH_STYLE` | off | the bucket in the path of a URL, not in its host name |
| `--store-scratch DIR` | `GREAT_MACHINE_STORE_SCRATCH` | `--data` | where packs are verified, in `great-machine-scratch` there (§3.2) |
| `--store-verify-jobs N` | `GREAT_MACHINE_STORE_VERIFY_JOBS` | 2 | packs verified at once |
| `--store-max-pack-size SIZE` | `GREAT_MACHINE_STORE_MAX_PACK_SIZE` | `16GiB` | the largest pack, uncompressed |
| `--store-no-verify` | `GREAT_MACHINE_STORE_NO_VERIFY` | off | record a pack without reading its data, as jaccard-store's `--no-verify` |

The credentials of the bucket come from the AWS SDK's default chain
(`AWS_ACCESS_KEY_ID` and the rest), as in `jaccard-stored`. At start
draal logs its endpoint ID as before, and the bucket.

### 3.7 A prefix for every service

Every service has references of its own in the store: the reference of a
store path that was pushed for the service `web` is

```
/services/web/nix/store/<name>
```

The prefix is no setting. One function of the code (`wire.StorePrefix`)
makes it of the name of a service, which is a DNS label (design §2) and
so needs no escaping; draal and zathras both call it. `/services/` leaves
the rest of the names of the store free for anything else it may hold
later.

- **A pod sees its service's store paths.** The mount of a zathras looks a
  name up under its service's prefix (§4.1): a store path that was pushed
  for another service alone is not in its `/nix/store`.
- **The `store` request names the service.** Of an operator (design §5.2)
  it is now `store`: service, and is answered with draal's endpoint ID and
  that service's prefix. It is authorized as before, for the service it
  names, and a name that is no service name is `bad-request`. `rollout`
  asks for the service it rolls out, and pushes to what it is told. A
  service draal has not heard of yet has a prefix like any other: the
  first rollout of a service pushes before draal knows it.
- **A store path that two services run is pushed for each.** Its second
  reference costs one request and no upload: the push begins with the
  root of the content, and a server that has a pack of that root points
  the new reference at it (jaccard-store's `push-start` answers
  `Stored`). On the laptop the store path is still read and imported, to
  know its root, as for any reference the server does not have.
- **A rollback pushes nothing**, as before: the stable generation's
  closure is under the service's prefix since its own rollout.

### 3.8 Twenty store paths at a time

`rollout` pushes the store paths of a closure several at a time already,
`--jobs` of them (design §3, §6.1). The default of `--jobs` becomes 20,
from 5, and 32 is the most it takes: with more, the push's own streams
went beyond the 64 that draal answers of a connection at a time (§3.4),
and a rollout failed in its middle against a store that was well, as a
review of this stage's code showed. A push has two streams under way for
the moment in which it goes from one step to the next, hence half of 64.

- **On the laptop** every path that is being pushed is imported into a
  temporary store of its own, which holds it once more, uncompressed, with
  its pack beside it: up to 20 of them at once under `--temp-dir`. A
  laptop short of room gives a smaller `--jobs`.
- **On the connection** the pushes, and before them the 16 questions of
  what the store has, are streams of one connection to draal, within the
  64 that draal answers at a time (§3.4).
- **In draal** a push ends with the verification of its pack, 2 at a
  time by default (`--store-verify-jobs`): the commits of the others wait
  for their turn. Most store paths are small and verified at once; where
  large ones are many, more verification jobs take more scratch space
  (§3.2).

## 4. zathras mounts the store

### 4.1 The mount in the process

zathras runs jaccard-nfs-nix-store's `sidecar` package, which is that
project's command without its flags:

| Setting of `sidecar.Config` | Value |
|---|---|
| `Server` | the endpoint ID of draal (`--draal`) |
| `Prefix` | the prefix of the service of `--service` (§3.7) |
| `Cache` | `--store-cache` |
| `Dial` | jaccard-store's dial of draal's ID for the store's ALPN, with a key the mount makes at start and keeps in memory |
| `Listen` | `127.0.0.1:0`: loopback, a port the system picks |
| `Mount` | `/nix/store` |
| `PreloadListen` | empty: no preload endpoint |
| `MountOptions` | `sidecar.DefaultMountOptions`, which the command of the sidecar has as its default and the package has not: empty is the kernel's hard mount |
| everything else | the package's defaults |

- **The mount has a key of its own.** A zathras's own connection to draal
  is redialed with a new endpoint for every attempt (design §5.1), and the mount
  pulls on connections of its own, which come and go at other times. Two
  endpoints with the zathras's key would evict each other at the relay.
  The key of the mount is made anew at every start, as the zathras's is,
  and draal knows neither.
- **The NFS server listens on loopback, on a port the system picks.** The
  kernel's NFS client reaches its server over TCP, so the server listens
  somewhere; loopback keeps it in the pod. A fixed port is not needed:
  zathras mounts it itself, and is told the port.
- **The mount is in the container's own mount namespace.** It is made on
  `/nix/store` of the container, without a volume and without mount
  propagation, so it reaches no other container and not the node, and
  goes with the container. What jaccard-nfs-nix-store documents about a
  mount left on the node by a sidecar that was killed does not happen.
- `/nix/store` is made by zathras if it is not there; the image holds
  nothing in it (§9).
- **The cache** is a directory that outlives the container, an `emptyDir`
  of the pod (§6), so that a zathras that is started again finds what was
  fetched. A cache is of one server and one prefix, and jaccard-nfs-nix-store
  refuses another: a pod whose draal or whose service changes is a new
  pod.

### 4.2 Preparing

Design §7.3 becomes: `prepare` has the mount fetch the store paths of the
closure, in the process. The first path that cannot be fetched ends the
others, and is the error of the `prepare`, naming the path and saying
whether the store does not have it or it could not be fetched. All of
them fetched is followed by the check of the binary, as before.

Nothing in the pod listens for lists of store paths. jaccard-nfs-nix-store
has no call for this yet, only its HTTP endpoint; it is added there (§8).

### 4.3 Start and end

A zathras that does the work (design §7.2):

1. sets up the launcher (§5.2), and ends with an error if it cannot;
2. starts the mount, and ends with an error if it cannot: without a store
   nothing can run, and the kubelet starts the container again;
3. goes on as before: the health check, draal, the service.

The mount does not need draal to start. It needs draal when a path is
first named, as the sidecar needed the server.

On SIGTERM, SIGINT or SIGHUP it stops the service and ends the shells as
before (design §7.2), and then stops the mount, which unmounts and stops its
server in the order jaccard-nfs-nix-store's `Stop` keeps.

The figures of a zathras (design §7.5) are those of the container, which now
holds the mount: its CPU, memory and disk count with the service's. What
the sidecar's resources were is the app container's to have now.

### 4.4 Without a store of its own

`--no-store` starts no mount: `/nix/store` is whatever the container has,
and `prepare` checks the binary alone. It is for development on a machine
that has a store, for an operating system other than Linux, on which the
mount is not built (§9), and for the tests.

### 4.5 Flags

Of zathras's flags (design §3), `--preload-url` is gone. New:

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | what the mount has fetched |
| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | mount nothing (§4.4) |
| `--user UID[:GID]` | `GREAT_MACHINE_USER` | zathras's own | the user and the group the service and the shells run as (§5.3) |

## 5. What the service and the shells may do

### 5.1 The container

Mounting takes `CAP_SYS_ADMIN`. The app container gets it, and is not
privileged:

```yaml
securityContext:
  capabilities:
    add: ["SYS_ADMIN"]
  appArmorProfile:
    type: Unconfined     # the runtime's profile denies mount
```

A privileged container would have the host's devices, a `/sys` that can
be written and a `/proc` with nothing masked. Root with no capability at
all can still write `/proc/sysrq-trigger`, which reboots the node, or open
the node's disks, since only the owner of the file is checked. A
container that is not privileged keeps what the runtime does for every
container: `/proc` masked, `/sys` read-only, the devices of a container.
What `CAP_SYS_ADMIN` adds is taken away from the service and the shells
by the launcher.

Not all of it, as the execution of this stage found. A runtime's default
seccomp profile keeps some system calls for containers that were given
`CAP_SYS_ADMIN`: `unshare`, `setns`, `clone` for a new namespace and the
calls that mount among them, with `bpf`, `perf_event_open` and others.
It lets them through for every process of such a container, and most of
them the kernel then refuses a process that has not the capability
itself; making a user namespace it does not. The service and the shells,
started without the capability, cannot mount in the container; but they
can make a user namespace of their own, as root or as another user, and
are root in it, over what that namespace holds and over nothing of the
container (`unshare -Urm` and a `tmpfs` mounted there, seen in a
container of this stage; `Operation not permitted` in one of Docker's
defaults). That is a part of the kernel a container without the
capability does not reach. Nothing in this stage closes it. A seccomp
filter set by the launcher's thread, which `no_new_privs` allows it to
set, would; so should a profile of the pod's own that lets the calls
that mount through and none of the others.

`appArmorProfile` is a field of Kubernetes 1.30; before it, the
annotation `container.apparmor.security.beta.kubernetes.io/app:
unconfined` says the same. With the runtime's default seccomp profile
`mount` is allowed to a container that has `CAP_SYS_ADMIN`.

### 5.2 The launcher

Linux keeps capabilities, and whether a process may gain privileges, for
every thread, and a child is a copy of the thread that started it. zathras
starts the service and every shell from one thread, the launcher's: a
goroutine locked to its OS thread for the life of the process, which
before anything else does three things to its thread alone:

- drops `CAP_SYS_ADMIN` from the bounding set (`PR_CAPBSET_DROP`);
- takes it out of the inheritable set, and clears the ambient set;
- sets `no_new_privs` (`PR_SET_NO_NEW_PRIVS`).

A program root executes gets the bounding set and the inheritable set as
its capabilities, so the service and the shells have what the container
has, but `CAP_SYS_ADMIN`, and cannot gain it in the container: not by a
setuid program nor by file capabilities, and the mount is `nosuid`
besides. The other threads of zathras keep `CAP_SYS_ADMIN`, and mount
and unmount with it.

- **The thread stays the launcher's.** The Go runtime makes no new thread
  from a locked one, and ends a locked thread whose goroutine returns, so
  nothing of the rest of zathras runs on it. The goroutine never returns.
- **Every start goes through it.** The supervisor (design §7.1) and the shells
  (design §7.6) are given the launcher and hand it the start of a process, the
  `Start` of an `exec.Cmd` or the start of a shell on a pseudo-terminal,
  which it runs on its thread and whose error it returns. Nothing else of
  zathras starts a process, but the zathras that the first process starts
  (design §7.2), which needs `CAP_SYS_ADMIN` and is not started by the launcher.
- **Nothing is executed from the mount by zathras itself.** Go starts a
  process with `vfork`: the thread that starts it stands still until the
  child has executed its program, and until then the runtime cannot stop
  the world, which every garbage collection begins with, so a collection
  that begins meanwhile holds every other goroutine. A program on the
  mount is read through the NFS server of this same process, whose
  goroutines are among the held: the child waits for the server, the
  server for the collection, the collection for the child. This was seen
  (§13): of 300 starts of a program on the mount, with collections
  running, the process stood still within the first few and for good.

  So what the launcher's thread starts is the program itself,
  `/proc/self/exe`, which is a file of the image whatever is mounted, as

  ```
  great-machine exec-in-place DIR PATH ARGV0 [ARG...]
  ```

  Started so, the program does one thing: it changes to `DIR`, unless
  that is empty, and replaces itself with the program at `PATH`
  (`execve`), with the arguments from `ARGV0` on and its own environment.
  It is a process of its own by then, which waits for the mount as any
  program does and holds nobody. It is the same process as before: the
  process group, the session, the terminal, the user and the
  capabilities are those the launcher gave it. An `execve` that fails, or
  a working directory that cannot be changed to, is said on standard
  error, which is the service's, and the exit status is 127 for a program
  that is not there and 126 for any other failure: to the supervisor, a
  service that ended at once (design §7.1).

  The working directory is that process's to change to for the same
  reason as the execution: `os/exec` changes to one between the fork and
  the execution, and a directory of the store that the kernel has not
  been told of yet is asked of the server in this same process. A review
  of this stage's code showed that zathras stands still on that as well.

  One thing is not as it would be without the program in between: a
  signal that zathras itself was started ignoring, other than SIGHUP and
  SIGINT, is not ignored by what it starts. The Go program in between
  takes the others for itself, and the execution sets them back to their
  defaults. zathras ignores none itself.
- **A service cannot reach into zathras.** To trace a process, or read its
  memory, one needs every capability that process has, and the service
  lacks one that zathras has.
- **What the service can still do** is what it could before: signal
  zathras, which runs as the same user, and read and write what root may
  in the container, the cache of the mount among it. That is the service's
  own container; with `--user` it cannot. And one thing it could not
  before, with or without `--user`: a user namespace of its own (5.1).

### 5.3 Another user

`--user UID[:GID]` runs the service and the shells as that user, and with
the group, or with the group of the same number when none is given, and
no others. The launcher sets them as each process starts
(`SysProcAttr.Credential`). A process that is not root has no capability
after it executes a program, whatever the bounding set holds.

What such a process is started on, zathras made as root: the pipes of a
service's output, the terminal of a shell. The launcher gives the pipes
and the terminal to the user before the start, so that a service can
open `/dev/stdout` anew, as many are configured to, and a shell its own
terminal. For that the terminal of a shell is opened before the shell is
started, and not by what starts it. A review of this stage's code found
both refused.

That is the way where the container has to be privileged after all, as
on nodes with SELinux, which does not let a container that is not
privileged mount NFS: then the service is not root, and what is checked
by the owner of a file alone (`/proc/sysrq-trigger`, the node's disks) is
not its own.

### 5.4 What zathras refuses and warns about

- **It refuses to start** when `CAP_SYS_ADMIN` is in its bounding set and
  the launcher cannot drop it (zathras lacks `CAP_SETPCAP`), unless
  `--user` is given: the service would get the capability back.
- **It warns at start** when the service runs as root and the launcher's
  bounding set still holds a capability that a container does not get by
  default, as in a privileged container: the warning names them and
  `--user`.
- A zathras that is not root, and has no capability, has nothing to
  drop; the launcher sets `no_new_privs` alone. It cannot mount, and runs
  with `--no-store`.

The capabilities a container gets by default are those of containerd and
Docker: `CHOWN`, `DAC_OVERRIDE`, `FSETID`, `FOWNER`, `MKNOD`, `NET_RAW`,
`SETGID`, `SETUID`, `SETFCAP`, `SETPCAP`, `NET_BIND_SERVICE`,
`SYS_CHROOT`, `KILL` and `AUDIT_WRITE`.

## 6. The pod and draal's deployment

**`deploy/pod.yaml`** is one container, `app`, as before but for:

- no init container, no `store` volume and no startup probe;
- the `securityContext` of §5.1;
- the volume `cache`, an `emptyDir`, on `/var/cache/great-machine`;
- in a comment, the way of §5.3 for nodes where this is not enough:
  `privileged: true` and `GREAT_MACHINE_USER`.

The readiness probe and `terminationGracePeriodSeconds` stay.

**`deploy/draal.yaml`** loses `JACCARD_SERVER` and `JACCARD_PREFIX`, and
gets the bucket, its credentials from a Secret, and an `emptyDir` for the
scratch space on `GREAT_MACHINE_STORE_SCRATCH`, of a size for
`--store-max-pack-size` times `--store-verify-jobs`. Its volume for
`--data` now holds the store's database too, which is the only record of
the store (jaccard-store says so of its own): it is backed up with
`draal.sqlite`.

## 7. Upgrading from v0.1.0

draal's store begins empty: the references of the jaccard-store server
that v0.1.0 pushed to are not taken over. draal keeps its own database,
with the stable generation of every service, whose closure the new store
does not have.

So draal and every pod are changed together, and every service is then
rolled out once more, with the same binary if nothing else is to change:
that pushes its closure into the store. Until it is, a zathras that
starts is sent the stable generation, fails to prepare it with the path
the store does not have, and is sent it again every 30 seconds (design
§6.2): it stays `idle`. The README says this in its notes for the
release.

## 8. A change in jaccard-nfs-nix-store

jaccard-nfs-nix-store gets a call that its preload endpoint is made of:

```go
// Preload fetches the store paths of names, as the preload endpoint does
// a list: PreloadJobs at once; the first that cannot be fetched ends the
// others and is returned, naming the path, and is refs.ErrNotFound when
// the server does not have it.
func (s *Sidecar) Preload(ctx context.Context, names []string) error
```

The endpoint is then this call behind HTTP, and stays as it is for the
sidecar. The change is released as v0.3.0, which great-machine requires.

## 9. Build

- **Go 1.27.** jaccard-nfs-nix-store needs it, for Buildbarn's packages.
  `go.mod` says `go 1.27.1`, and the flake gives gonixgo `go_1_27`, as
  jaccard-nfs-nix-store's flake does.
- **The mount is for Linux alone.** Buildbarn's packages do not build for
  macOS. The package of the mount and the launcher have a Linux file and
  a file for every other system, on which `zathras` without `--no-store`
  ends at once and says so, and the launcher starts processes as before.
  The operator's commands and draal build everywhere.
- **The program is larger**: it carries Buildbarn's NFS server and what
  it needs, and the AWS SDK of the store. The image stays one image for
  draal and the pods.
- **The image** gets an empty `/nix/store`, the mount point, and nothing
  in it: the mount covers it, as the sidecar's did.

## 10. Packages

To the table of design §11:

| Package | Purpose | Depends on |
|---|---|---|
| `storeserver` | the store in draal: the jaccard-store server, its database, bucket and sweeper, the answering of the store's ALPN with authorization, and the admin page | jaccard-store, `authz`, `node` |
| `storemount` | the mount in zathras: jaccard-nfs-nix-store's sidecar, configured as §4.1, and `Preload` | jaccard-nfs-nix-store, jaccard-store |
| `launcher` | the thread that starts the service and the shells, and the program replacing itself with another (§5) | |

Changed:

- `node`: `ListenConfig` takes a handler for the connections of the
  store's ALPN, which the endpoint then accepts as well; `Accept` returns
  great-machine's connections alone.
- `authz`: the two operations of the store.
- `wire`: `StorePrefix`, and the service in the `store` request.
- `push`: 20 jobs when `Config` does not say; `cli`: `--jobs` 20 by
  default (§3.8).
- `draal`, `cli`, `operator`: the store, its flags, the prefix of a
  service (§3.7).
- `zathras`: `Config.Preload` is an interface with `Preload(ctx, names)`,
  which `storemount` satisfies; nil is `--no-store`.
- `supervisor`, `ptyshell`: a process is started through the launcher
  they are given; without one, as before.
- `web`: the store's page under `/store/`.
- `cmd/great-machine`: `exec-in-place` is looked for before anything else
  (§5.2).

Gone: `preload`, the client of the sidecar's endpoint.

## 11. Errors

To design §13:

- **The store does not have a path of a closure** (it was never pushed,
  §7): the `prepare` fails with the path, and the rollout halts before
  anything was restarted.
- **The bucket is not there, or cannot be reached**: draal asks the
  bucket nothing when it starts, so it starts. A push then fails, and
  `rollout` ends before draal heard of a rollout: at the upload, with the
  address the laptop could not reach, or with `internal` and the step
  that failed, whose cause is in draal's log.
- **A binary cannot be executed** (it is gone, or no program): zathras
  checks it at `prepare` and at `activate`, as before; one that fails
  after that is a service that ended at once with status 127 or 126 and
  a line on its standard error that says why (§5.2).
- **The mount cannot be made** (no `CAP_SYS_ADMIN`, AppArmor, SELinux):
  zathras ends at start with the error of `mount`, and says that the
  container needs what §5.1 gives it.
- **draal is down**: a path that was fetched is served on; a path that was
  not is an I/O error after the mount's pull timeout, as with a sidecar
  whose server is down. A service that runs needs nothing new: draal down
  still stops no service.

## 12. Tests

- **`storeserver`**: over a bucket in memory (jaccard-store's
  `buckettest`) and two real endpoints on loopback, a push of an operator
  and a pull of a stranger succeed; a push, a list and a delete of a
  stranger are refused, and the refusal is logged; the bounds of §3.4;
  draal stopping with a request under way.
- **`node`**: an endpoint with a store handler gives a connection of the
  store's ALPN to the handler and one of great-machine's to `Accept`;
  without a handler the store's ALPN is not accepted.
- **`authz`**: the two operations, for an operator and a stranger.
- **`wire`**: `StorePrefix` of a service, and the `store` request with
  its service.
- **`cli`**: `rollout` without `--jobs` pushes 20 at a time, and
  `GREAT_MACHINE_JOBS` still sets it.
- **`launcher`**, on Linux as root, in a container as `test-pid1.sh`
  runs one (a new script, `scripts/test-linux.sh`, runs every test that
  needs root and the kernel): a child started through it has no
  `CAP_SYS_ADMIN` in any set and `NoNewPrivs: 1`, and the rest of the
  process still mounts; with `--user`, the child's user and group and
  empty sets; the refusal and the warning of §5.4. Without root, the
  `no_new_privs` alone. Everywhere: a program started through it gets its
  arguments, `ARGV0` among them, its environment and its working
  directory; one that is not there ends with 127 and says so, and a
  working directory that is not there with 126. As another user it opens
  `/dev/stdout` and its terminal anew. The test binary is its own
  `exec-in-place`.
- **`storemount`**, on Linux as root, against a store in the test's
  process and a mount on a temporary directory: a path is fetched when it
  is named, `Preload` fetches a list and fails on a path the store lacks,
  a binary is executed from the mount through the launcher with a cache
  that is empty, and 300 times more while the garbage collector runs
  without pause (the one process serving what its child is executed
  from), every other time with a working directory in the mount that the
  kernel has not been told of, a store path pushed under another service's prefix alone is not
  found, and `Stop` leaves nothing mounted.
- **`e2e`**: draal with its store over a bucket in memory. The closures
  of rollouts of two services are pushed with the operator's key through
  draal's endpoint ID, each under its service's prefix, as the `store`
  request answers it; a store path the two have in common is uploaded
  once. They are fetched again with a stranger's key through the same ID.
  The
  zathrases of `e2e` run with a fake `Preload` in the place of the fake
  preload endpoint, as they run on macOS too.
- **`cli`**: the command of a zathras with a service in it, under a draal
  of the test: the service and a shell are started by the launcher
  (`no_new_privs`, the user of `--user`), the store is asked for the
  closure first, and it is stopped only when a service that ignores
  SIGTERM has had its stop timeout and is gone.
- **`e2e`, on Linux as root**: one rollout with nothing standing in. The
  closure is pushed to draal's endpoint ID; the store of the zathras is
  the mount, over the directory that is the Nix store of the tests,
  fetched from draal's store with a key draal does not know; the service
  is started from it by a launcher, and runs the binary of the mount
  without `CAP_SYS_ADMIN`; the mount of another service is told that the
  store has no such path for it.
- **On a cluster**, by hand, as the first runs of stage 3: `deploy/` on
  kind on x86_64, with a bucket of RustFS in the cluster. A rollout, a
  shell that shows the capabilities of §1, a zathras killed and started
  again with its cache, and a pod deleted leaving `findmnt -t nfs4` on
  the node empty. The results go into the plan's ledger.

## 13. Settled first

Four things could have changed what is above. They were tried while the
plan was written, on arm64, and one of them did: §5.2 has its
`exec-in-place` from the fourth.

1. **go-iroh.** Settled, in one process over loopback, with go-iroh
   v0.3.0: an endpoint bound by jaccard-store's `node.Bind` for
   great-machine's ALPN and then given both with `SetALPNs` accepts a
   dial for either, and `Conn.ALPN` says which; a dial for a third is
   refused in the handshake (`no application protocol`). `SetALPNs` has
   to be called before the first `Accept`.
2. **Go 1.27 and gonixgo.** The plan says what was built (its section
   "What was run").
3. **The mount without privileges.** Settled as far as a laptop goes: in
   a Docker container that is not privileged, with `CAP_SYS_ADMIN` and
   Docker's default seccomp profile, a process mounts the NFS server it
   runs, on a port the system picked, on its own `/nix/store`, executes
   from it, and has nothing mounted after `Stop`. Docker Desktop has no
   AppArmor: that `Unconfined` is needed, and enough, where there is one
   is still to be seen on a cluster (§12), and so is what a pod leaves on
   its node.
4. **The launcher.** Settled, and not as expected. A child started from a
   goroutine locked to its thread has the thread's bounding set, no
   inheritable or ambient capability and `no_new_privs`; as another user
   it has no capability at all; a thread of the rest still has
   `CAP_SYS_ADMIN`, mounts, and has no `no_new_privs`. But a program
   executed straight from the mount by the process that serves the mount
   stops that process: with an empty cache the first time it was tried,
   and with a full one within the first few of 300 starts while
   collections ran, until the test was killed after a minute, a goroutine
   "stopping the world" and the launcher's "running, locked to thread".
   It is no matter of `syscall.ForkLock`, which nothing that answers the
   mount takes. Through `exec-in-place` the same 300 starts took 22 ms at
   the longest under 3,600 collections, and a start with an empty cache
   38 ms.

## 14. Not in this stage

- Taking over the references of an existing jaccard-store server (§7).
- Removing what the store holds: a closure that no generation needs any
  more stays in the bucket, as before.
- Limiting pulls to the zathrases draal knows, or a pull of a service's
  references to the pods of that service: pulls are open (§3.3), and
  the prefixes keep the services' stores apart, not their secrets.
- A store shared by the pods of a node.
- Tokens of clamp (design §17).

## 15. What else changes

The design document and the README are brought up to this at the end of
the stage: design §1 (the picture), §3 (the flags, `--jobs` among them),
§4 (the table), §5.2 (the `store` request), §7.3, §7.6 (a shell lacks `CAP_SYS_ADMIN`), §11,
§12 and §13, and the
README's getting started, its tables of flags, what has to be there, the
network, and the security notes.
