# great-machine stage 5: a store without a mount

2026-10-13

This is a change to the design of
[`2026-10-09-great-machine-design.md`](2026-10-09-great-machine-design.md)
and of [stage 4](2026-10-11-great-machine-stage-4-design.md). What it
does not mention stays as those documents say. A section of the first is
written as design §N, one of stage 4 as stage 4 §N, one of this document
as §N.

## 1. Purpose

Stage 4 gave every zathras a mount: the NFS server of
jaccard-nfs-nix-store in its own process, mounted on `/nix/store`, which
fetches a store path the first time it is named. Nothing is fetched that
way. A `prepare` fetches the whole closure of a generation before
anything of it is started (design §7.3), so a path is there by the time
it is named.

What the mount costs is paid all the same:

- the container has `CAP_SYS_ADMIN` and no AppArmor profile, and its
  namespace has to be at the Pod Security level `privileged`;
- a thread of zathras exists to keep that capability from the service,
  and what it does not keep from it, a user namespace of its own, stays
  open (stage 4 §5.1);
- zathras cannot execute a program from the mount it serves, and starts
  itself in between (`exec-in-place`, stage 4 §5.2);
- the mount is built for Linux alone, and is tested as root in a
  container.

Stage 5 takes the mount out. zathras pulls the store paths of a closure
and writes them into `/nix/store` as files.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌──────────────────────────────┐
 │ great-machine rollout│── iroh ───────▶ │ draal, one endpoint ID:      │
 │   nix-store -qR      │  both protocols │   great-machine/1            │
 │ great-machine tui    │                 │   amber/jaccard-store/1      │
 └──────────┬───────────┘                 └──────────────▲───────────────┘
            │                                            │ iroh: great-machine/1,
            │ packs up                                   │ and the pulls of a prepare
            ▼                                            │
      ┌───────────┐                       ┌──────────────┴───────────────┐
      │ S3 bucket │ ◀──── packs down ──── │ pod, one container: zathras  │
      └───────────┘                       │   pack cache ─▶ /nix/store   │
                                          │   └─ the service             │
                                          └──────────────────────────────┘
```

draal, the protocol of great-machine, the store in draal and the push of
`great-machine rollout` are not changed.

It is done when:

- a `prepare` leaves every store path of the closure in `/nix/store` as
  plain files, each of them whole or not there, and the service runs
  from them;
- `deploy/pod.yaml` gives its container no capability and leaves it the
  AppArmor profile of the runtime, and a shell of a zathras shows no
  `CAP_SYS_ADMIN` and `NoNewPrivs: 1` in `/proc/self/status`;
- a pod that holds a store path downloads one pack for the store path
  that a later rollout made of it;
- nothing of jaccard-nfs-nix-store or of Buildbarn is in `go.mod`, and a
  zathras with its store is built and tested on macOS as on Linux,
  without root;
- `great-machine exec-in-place` is gone.

## 2. Terms

- **the store**: the jaccard-store server in draal, its database and its
  bucket, as in stage 4.
- **the store directory**: `/nix/store` of the container of a zathras,
  which zathras fills.
- **the pack cache**: the objects zathras has pulled, in a packstore of
  [Amber-Store Core](https://github.com/amber-store/core), kept beside
  the store directory.
- **the launcher**: what starts the service and the shells as the user of
  `--user` (§4.2). It is no thread any more.

The mount of stage 4 is gone, and the word with it.

## 3. zathras fills the store directory

### 3.1 Where things are

| Where | What |
|---|---|
| `packs/` under `--store-cache` | the pack cache: every object that was pulled, for as long as the directory lives |
| `/nix/store/NAME` | a store path, written out |
| `/nix/store/.great-machine-partial-N` | a store path that is being written, `N` a number |

- **A name in the store directory is a store path that is whole.** A
  store path is written beside its name, as `.great-machine-partial-`
  and a number, and moved to its name when all of it is on disk (§3.3).
  The move is the record: there is no list of what was fetched. It is a
  number and not the name of the store path, which may be as long as a
  name can be.
- **What is being written is beside its name, and in no directory of its
  own.** A directory nobody may write to can be moved within its
  directory; into another only root moves it, since that move writes its
  `..`, and a zathras is to keep a store without being root.
- **What was being written is removed whenever zathras starts**: whatever
  in the store directory begins with `.great-machine-partial-` is what an
  earlier run was writing when it ended.
- **Both directories are one zathras's.** Nothing else writes to them,
  and no two zathrases share them.
- **zathras makes them writable by its own user alone**, mode `0755`,
  the store directory as stage 4 made the cache. A volume of a pod is
  made writable by everybody, and in such a directory a service that is
  another user could move a store path away and put its own in its
  place.
- **A store directory that holds something is used as it is.** zathras
  adds to it, and what is there under a name is taken for that store
  path. Nothing is mounted over it, so nothing is hidden.

The pack cache is kept so that a store path costs less the second time:
see §3.4. It is what the cache of stage 4 was, without the files that
the mount wrote out beside it: the files are in the store directory now.
A pod's disk holds every store path twice, once as packs and once as
files, as before.

### 3.2 Preparing

Design §7.3 becomes: `prepare` fetches the store paths of the closure
into the store directory, and then checks the binary, as before.

For every name of the closure:

1. A name that is empty, that begins with `.`, or that has a `/` in it is
   refused: it names no store path. draal refuses an empty name, `.`,
   `..` and one with a `/` in it already (design §6.1); any other that
   begins with `.` it lets through, and what is being written has such a
   name.
2. If `/nix/store/NAME` is there, the store path is there: a store path
   never changes under its name. The store is asked nothing.
3. Otherwise the reference `prefix + NAME` is pulled into the pack cache
   (§3.4), written out (§3.3), and moved to its name.

- **Twenty store paths are fetched at a time**, over all the `prepare`s
  that run, as the mount fetched twenty of one.
- **A name is fetched once at a time.** Two `prepare`s that need the same
  store path wait for the one fetch.
- **A `prepare` that is withdrawn stops waiting**, as before: draal closes
  its stream, or zathras is told to end. A fetch that nobody waits for
  any more is ended, and what it wrote is removed.
- **The first store path that cannot be fetched ends the others** of that
  `prepare` and is its error, which names the path and says whether the
  store has none of that name for the service or why it could not be
  fetched.

### 3.3 Writing a store path out

A reference has a root, which is a tree or the content of one file.

| In the reference | In the store directory |
|---|---|
| a directory | a directory, mode `0555` |
| a regular file with the execute bit of its owner | a file, mode `0555` |
| any other regular file | a file, mode `0444` |
| a symbolic link | a symbolic link with the same target |
| a root that is the content of a file | a file, mode `0555` |
| anything else: a device, a FIFO, a socket | refused: the fetch fails and names the entry |

- **This is what a Nix store holds, and no more.** The owner and the
  group are those of zathras, whatever the reference records: on a
  laptop whose Nix store belongs to its user the files would otherwise
  be that user's in the pod. No setuid, setgid or sticky bit is set, and
  no extended attribute.
- **The modification time is the one the reference records**, which in a
  Nix store is one second after the epoch. The root of a reference has
  none recorded, and is given that one.
- **A root that is the content of a file has no mode recorded.** It gets
  an execute bit, as the mount gave it (design §15).
- **Core's own extraction is not used.** It restores owners when it runs
  as root, with setuid bits and device nodes, and writes a tree out
  through a tar archive in between. great-machine reads the tree with
  Core's `fstree` and writes the files itself.
- **Nothing is written through what the tree holds.** A name of an entry
  that is empty, `.`, `..` or has a `/` in it is refused. Every directory
  is made by the one that writes the tree, and every file is made new: a
  symbolic link of the tree is made and never followed.
- **The move comes after the disk.** When the tree is written, the file
  system is synced (`syncfs` on Linux; where there is none, every file
  as it is written), the tree is moved to `/nix/store/NAME`, and the
  store directory is synced. A pod's volume outlives a node that lost
  its power, and a name in the store directory must not be a tree whose
  files did not reach the disk. A store directory that cannot be synced
  fails the fetch, with what the disk said, and leaves the store path
  under its name: it is whole, and was on disk before it was moved.
  Nothing is ever removed under the name of a store path.
- A directory is given its mode and its time when what is in it is
  written, so that a directory that cannot be written to does not stand
  in the way of its own content.

### 3.4 Pulling

- **The client is jaccard-store's**, over a connection that jaccard-store's
  `node.Dial` makes to draal's endpoint ID for the store's ALPN.
- **The key is the store directory's own**, made when zathras starts and
  kept in memory, as the mount's was (stage 4 §4.1): the connection of a
  zathras to draal is dialed with an endpoint of its own, and two
  endpoints with one key evict each other at the relay. One key for both
  is a matter of stage 6 (§13).
- **One connection serves every pull.** It is dialed when a pull first
  needs it, with 30 seconds for the dial, and closed when no fetch is
  under way. A pull that fails costs the connection it failed on; the
  next attempt dials again. A dial that fails has failed for every fetch
  that waited for it: a store that cannot be reached is not dialed once
  for each of them, one after another. A fetch that was ended does not
  wait for a dial.
- **A pull downloads what the pack cache lacks.** The store names the
  pack of the reference and that pack's parent. jaccard-store's client
  downloads nothing when the cache holds every object of the reference,
  the reference's own pack when that completes it, and the parent only
  when objects are still missing. A store path that a rollout changed
  leans on the pack of the one before, which a pod that ran the one
  before has: it downloads the difference. A pod that is new downloads
  two packs at the most for a store path.
- **Every object is checked against its key** before it is written to
  the cache, by jaccard-store's client.
- **A root that cannot be written out is refused before anything is
  downloaded**: one that is neither a tree nor the content of a file.
- **A reference the store does not have** is the error of stage 4: the
  store has no `/nix/store/NAME` for the service, and holds what the
  rollouts of that service have pushed. It is not tried again.
- **Any other failure is tried again**: after 1 second, then after twice
  as long each time up to 15 seconds, until 2 minutes have passed since
  the first failure. One attempt has an hour, and one that took longer
  has failed like any other. Then the fetch fails with what the last
  attempt said. These are the times of the mount, and
  values of the package that the tests make short.

### 3.5 Start and end

A zathras that does the work (design §7.2):

1. makes the launcher (§4.2);
2. opens its store: makes the store directory and the cache if they are
   not there, makes them its own to write (§3.1), removes what an
   earlier run was writing, and opens the pack cache. A zathras that cannot ends with
   the error: without a store nothing can run, and the kubelet starts
   the container again;
3. goes on as before: the health check, draal, the service.

draal is not dialed for the store before a `prepare` needs a store path
that is not there.

On SIGTERM, SIGINT or SIGHUP zathras stops the service and ends the
shells as before, and then closes its store: the fetches are ended, the
connection is closed, and the pack cache is closed. Nothing is
unmounted, and what a fetch was writing and could not remove goes at
the next start.

### 3.6 Without a store of its own

`--no-store` fetches nothing: `/nix/store` is whatever the machine has,
and `prepare` checks the binary alone. It is for development on a
machine that has a Nix store of its own, which zathras is not to write
to, and for the tests. It is no longer needed for a system other than
Linux, or for a zathras that is not root: such a zathras keeps a store
wherever it can write the two directories.

### 3.7 Flags

No flag is added and none is taken away.

| Flag | Variable | Default | Meaning now |
|---|---|---|---|
| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | where the pack cache is kept |
| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | fetch nothing (§3.6) |

## 4. What the service and the shells may do

### 4.1 The container

The container of a zathras is given nothing beyond what a container has:

```yaml
securityContext:
  allowPrivilegeEscalation: false
  seccompProfile:
    type: RuntimeDefault
```

- **No capability is added, and the AppArmor profile is the runtime's.**
  A namespace that enforces the Pod Security Standards admits the pod at
  the level `baseline`.
- **`allowPrivilegeEscalation: false` sets `no_new_privs`** for every
  process of the container, zathras among them, which gains nothing by
  executing a program: not by a setuid program and not by file
  capabilities. Stage 4 could not ask for it: a container with
  `CAP_SYS_ADMIN` is not given it. zathras does not set it itself.
- **The user namespace of stage 4 §5.1 is closed** by the runtime's
  seccomp profile, which keeps `unshare` and the calls beside it from a
  container without `CAP_SYS_ADMIN`.
- **A service that is root is root in the container of zathras.** It can
  signal zathras and write what root may: the store directory and the
  pack cache among it, as it could write the cache of the mount (stage 4
  §5.2). What it writes there it and the generations after it run from,
  in that pod and no other.
- **With `--user` it cannot.** The store directory and everything in it
  are zathras's, and nobody else's to write (§3.1, §3.3).

### 4.2 The launcher

The launcher starts the service and every shell, as before. What is left
of it is `--user`:

- With `--user UID[:GID]` it sets the user and the group of the process
  it starts, and no other group, and gives the pipes of a service and
  the terminal of a shell to that user before the start (stage 4 §5.3).
- Without `--user` it starts a process as it is.

Gone:

- **The thread.** A process is started from whichever thread starts it:
  there is no capability to keep from it.
- **What the thread gave up**: `CAP_SYS_ADMIN` out of the bounding set
  and the inheritable set, the ambient set, and `no_new_privs`, which
  the container has now (§4.1).
- **`exec-in-place`**, the program starting itself to replace itself
  with another, and with it the first thing `main` looked for. A program
  in the store directory is a file of a file system the kernel has:
  starting it holds nobody.
- **The refusal of stage 4 §5.4**: there is nothing to drop.

What follows from that:

- **A binary that cannot be started is a start that failed**, as before
  stage 4: to the supervisor a service that ended at once (design §7.1),
  whose last exit has the error and, for its status, -1. Nothing is
  written to the service's standard error, and there is no exit status
  127 or 126. A shell that cannot be started is the error of its
  `shell` request, as it was.
- **The working directory and the ignored signals** of what is started
  are what `os/exec` makes them: the difference of stage 4 §5.2 is gone.

### 4.3 What zathras warns about

zathras warns at start when the service runs as root and the bounding
set of zathras holds a capability that a container does not get by
default (the list of stage 4 §5.4), and names them and `--user`. That is
the warning of stage 4. It matters for a pod that was left with the
`securityContext` of stage 4: nothing drops `CAP_SYS_ADMIN` any more, so
a service of such a pod that is root has it. zathras refuses nothing.

## 5. The pod

**`deploy/pod.yaml`** is one container, `app`, as before but for:

- the `securityContext` of §4.1: no `capabilities`, no `appArmorProfile`;
- a second volume, `store`, an `emptyDir` on `/nix/store`, beside
  `cache` on `/var/cache/great-machine`. Both live as long as the pod:
  a container that is started again finds its store. Without `store` the
  store directory is the container's own, and a container that is
  started again writes every store path out anew from the cache, asking
  draal for each reference and downloading nothing;
- no word of a privileged container for nodes with SELinux (stage 4
  §5.3): nothing is mounted.

The readiness probe and `terminationGracePeriodSeconds` stay.

**`deploy/draal.yaml`** changes in its tag alone.

## 6. Upgrading from v0.2.0

Nothing of the protocol and nothing of the store changes, so draal and
the pods need not change in one step, and no service is rolled out
again: a pod that is new is sent the stable generation, as any new pod
is, and fetches it from the store that v0.2.0 pushed to.

The image and the `securityContext` of a pod change together:

- **The image of v0.3.0 with the `securityContext` of v0.2.0** runs, and
  warns (§4.3): a service that is root has `CAP_SYS_ADMIN`.
- **The image of v0.2.0 with the `securityContext` of v0.3.0** cannot
  mount, and ends at its start.

A cache that a pod of v0.2.0 kept is not used and not removed: the pack
cache is a directory of its own in it (§3.1). In a Deployment a pod of
v0.3.0 is a new pod, with a new volume.

The README says this in its notes for the release.

## 7. Build

- **`go.mod` loses jaccard-nfs-nix-store**, and with it Buildbarn's
  packages and what they need. Core and jaccard-store stay.
- **Go stays 1.27**: nothing needs it any more, and nothing is gained by
  going back.
- **Of what this stage touches, two small files are for Linux alone**:
  the launcher's, which reads the bounding set for the warning of §4.3,
  and the `syncfs` of `storedir` (§3.3). The store of a zathras is built
  and tested on every system.
- **The program is smaller** by the NFS server.
- **The image is not changed.** Its `/nix/store` stays empty: a volume
  is put over it (§5), so the program, busybox and the certificates
  still must not be links into it.

## 8. Packages

To the table of design §11:

| Package | Purpose | Depends on |
|---|---|---|
| `storedir` | the store of a zathras: pulls the store paths of a closure into a pack cache and writes them out under `/nix/store`; `Preload` | jaccard-store, Core, `node`, `wire` |
| `launcher` | starts the service and the shells as the user of `--user` | |

Gone: `storemount`.

Changed:

- `cli`: zathras opens and closes a `storedir` in the place of the
  mount; the words of `--store-cache`, of `--no-store` and of the
  command.
- `cmd/great-machine`: `exec-in-place` is not looked for.
- `supervisor`, `ptyshell`: what they say of a start through a launcher,
  which they are given or not, as before. `ptyshell` loses the look it
  took at the shell's program before such a start: the start itself says
  now that the program is not there.
- `e2e`, and the tests of `cli`, `supervisor` and `ptyshell`: §10.

`zathras` is not changed: it knows its store as `Preloader`, which
`storedir` satisfies as `storemount` did.

## 9. Errors

To design §13 and stage 4 §11:

- **The store does not have a path of a closure**: as before. The
  `prepare` fails with the path, and the rollout halts before anything
  was restarted.
- **A path cannot be fetched** (draal or the bucket cannot be reached):
  it is tried again for 2 minutes (§3.4), and then the `prepare` fails
  with the path and what the last attempt said.
- **A path cannot be written out** (the disk is full, the reference
  holds what a store does not, §3.3): the `prepare` fails with the path
  and why. What was written of it is removed.
- **The store cannot be opened** (a directory cannot be made or
  written): zathras ends at start with the error.
- **zathras ends while it writes a store path**: the name is not in the
  store directory, and what was written is removed at the next start.
  The next `prepare` fetches it, from the cache if the pull had ended.
- **A binary cannot be executed**: zathras checks it at `prepare` and at
  `activate`, as before; one that fails after that is a start that
  failed (§4.2).
- **draal is down**: a service runs from files on the pod's own disk and
  needs nothing of draal. The I/O error of stage 4 for a path that was
  not fetched is gone: everything a generation runs from is fetched
  before it is started.

## 10. Tests

- **`storedir`**, on every system and without root, against a store in
  the test's process over a bucket in memory and real endpoints on
  loopback, as `storeserver` is tested:
  - a closure of a directory with nested directories, files with and
    without an execute bit and symbolic links, and of a store path that
    is one file, is written out with the modes, the targets and the
    times of §3.3, and its content is that of what was pushed;
  - a store path that is there is not asked of the store;
  - a store path that was pushed after one it leans on is pulled with
    one pack into a cache that holds the other, and with two into an
    empty one;
  - a store path the store lacks is the error of §3.4, and so is one
    that was pushed under another service's prefix alone;
  - a reference with a FIFO in it is refused, and leaves nothing under
    its name; a file that was setuid where it was pushed from is not
    where it is written out;
  - a name that begins with `.` is refused;
  - what a run was writing when it ended, a directory that cannot be
    written to among it, is gone after the next start, and the store
    paths are as they were;
  - a store directory and a cache that everybody may write to are
    zathras's alone after the start;
  - a bucket that fails for a while is tried again and the fetch
    succeeds; one that fails for longer than the time of §3.4 is the
    error, with the path;
  - two `Preload`s of the same names fetch each store path once; one
    that is canceled returns, and the other gets its store paths;
  - closing ends the fetches.
- **`launcher`**: everywhere, a command without `--user` is started as it
  is, with its arguments, its environment and its working directory; and
  which capabilities of a bounding set the warning of §4.3 names. On Linux
  as root (`scripts/test-linux.sh`), a process started as `--user` has
  the user, the group and no other, and opens `/dev/stdout` and its
  terminal anew. The tests of what the thread gave up, and of a program
  started 300 times from the mount, go with what they tested.
- **`supervisor`, `ptyshell`**: a binary that is not there is a start
  that failed, with its error as the reason.
- **`cli`**: the command of a zathras with a service in it, under a draal
  of the test: the store is asked for the closure first, and is closed
  when the service has ended.
- **`e2e`**: the rollout with nothing standing in, which stage 4 ran on
  Linux as root alone, runs on every system and without root. The
  closure is pushed to draal's endpoint ID; the store of the zathras is
  a `storedir` over the directory that is the Nix store of the tests,
  fetched from draal's store; the service runs from it; the store of
  another service is told that there is no such path for it. The other
  tests of `e2e` keep their fake `Preload`.
- **`scripts/test-linux.sh`** runs its container without `CAP_SYS_ADMIN`
  and with Docker's AppArmor profile: what still needs root is `--user`.
- **On a cluster**, by hand, as stage 4: `deploy/` in a namespace that
  enforces `baseline`. A rollout; a shell that shows what §1 says of
  `/proc/self/status`; a container killed and started again, which runs
  the stable generation from the store it finds. The results go into the
  plan's ledger.

## 11. Settled first

Two things could change what is above. Both were read in the code they
depend on while this was written, and neither was run: the plan begins
with them.

1. **Core gives what §3.3 writes.** Read in Core v0.10.0:
   `fstree.CollectEntries` gives every entry of a directory its name,
   its mode with its type, its modification time, the key of its
   content and the target of a link, and `fstree.WriteContent` writes
   the content of a file from its key, which is how jaccard-nfs-nix-store
   writes its plain files. To be run: a tree with every kind of entry is
   imported, as a push imports it, and written out, and is what it was.
2. **The pack cache saves a pack.** Read in jaccard-store v0.7.0: a pull
   imports the reference's own pack first, and the parent only when
   objects are still missing after it. To be run: of two store paths of
   which the second was pushed after the first and leans on it, the
   second is pulled with one pack into a cache that holds the first.
   Should the store give store paths no parent, or should the cache not
   save the pack, the cache buys nothing for its half of the disk: every
   store path is then pulled into a packstore of its own that is removed
   when the store path is written out, and `--store-cache` is where
   those are made.

## 12. Not in this stage

- Removing a store path or a pack: the store directory and the pack
  cache of a pod grow until the pod goes, as the cache of the mount did.
- A store shared by the pods of a node (stage 4 §14).
- Taking capabilities away from the container beyond its defaults, and a
  zathras that is not root in a pod at the level `restricted`.
- One key for both connections of a zathras, limiting who may pull, and
  everything else of access control: §13.

## 13. What follows: access control

Stage 6 is to say who may do what. It has no design yet. What was agreed
when this stage was split off from it, so that it is not lost:

- **Three roles.** An admin does everything; a deployer pushes and rolls
  out; a zathras pulls and reports.
- **Endpoint IDs with their roles are in `draal.sqlite`**, and draal
  answers from a copy in memory, which it writes together with the
  database: `Authorize` has no error of a database to return.
- **A zathras does not share a key with another**: the relay keeps one
  session for an endpoint ID, draal's own socket one path state, and
  draal one session of the fleet. It proves its role with a statement
  that a role key signed, "at draal D, endpoint E acts for me". Every
  pod of a service is given that role key, and its ID is what the table
  holds.
- **One key for a zathras.** Both protocols are dialed from one endpoint,
  so the key of §3.4 goes. To be settled first there: that go-iroh holds
  two connections of different ALPNs from one endpoint to one peer, and
  that an endpoint that dials outlives a draal that was started again.

Still open: that revoking a role ends the connections it was used on;
the first admin, and the last one being removed; the requests and the
commands that grant, revoke and list; which role may open a shell, and
which may delete from the store; whether a role is of one service; the
web page, which has no authentication; the key of a deployer in two jobs
of a CI at once; and what becomes of the tokens of clamp (design §4).

## 14. What else changes

The design document, stage 4's and the README are brought up to this at
the end of the stage:

- the design: its opening and §1 (the picture), §3 (the words of the
  flags), §4 (the second key is the store directory's), §7.1, §7.2 (what
  is closed at the end), §7.3, §7.5 (what the figures count), §7.6 (a
  shell has what the container has), §7.8, §11, §12, §13, §14, §15 and
  §16 (a fifth stage);
- stage 4's design: a line at its top that says its §4 and §5 are
  replaced by this document;
- the README: getting started, the pod and what it has to be given, the
  tables of flags, the security notes, and the notes for the release.
