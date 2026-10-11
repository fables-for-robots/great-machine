# great-machine, stage 4: one endpoint, no sidecar — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** draal is the store of the fleet, under its own endpoint ID, and a pod is one container whose zathras mounts that store on `/nix/store`: no jaccard-store server to run, no sidecar, no privileged container.

**Architecture:** draal's iroh endpoint accepts a second protocol, jaccard-store's, and hands its connections to a package, `storeserver`, that answers them with jaccard-store's own server over an S3 bucket, after asking draal's `authz` (a pull is anyone's, a push an operator's). zathras runs jaccard-nfs-nix-store's NFS server in its own process (`storemount`), mounts it on `/nix/store` of its own container, and fetches a rollout's closure by a call instead of a request. Because the container now has `CAP_SYS_ADMIN`, the service and the shells are started from one thread that has dropped it (`launcher`), and because a process that serves a mount cannot execute from it, what that thread starts is the program itself, which then replaces itself with the service.

**Tech Stack:** Go 1.27 (Buildbarn's NFS server needs it); jaccard-store v0.7.0 (`server`, `db`, `bucket`, `admin`, `client`, `node`, `wire`), which the module has; jaccard-nfs-nix-store v0.3.0 (`sidecar`, `refs`, `jstest`), new, and released by Task 1 of this plan; go-iroh v0.3.0; `golang.org/x/sys/unix` for capabilities and `prctl`; the kernel's NFSv4.1 client; Docker for the tests that need root.

**Spec:** `docs/superpowers/specs/2026-10-11-great-machine-stage-4-design.md`, whole. It changes `docs/superpowers/specs/2026-10-09-great-machine-design.md` ("the design" below; the last task brings that document up to it). Stages 1 to 3 are merged; their plans and the ledgers of their execution are beside this file.

## Global Constraints

- Everything the plans of stages 1 to 3 constrain still holds: one module and one program, no `internal/` directory, no cgo, `log/slog`, the standard library's `testing` alone for Go tests, every waiting time a field of a `Config`, nothing built left behind, work on a branch (`stage-4`, which exists and holds the spec and this plan).
- **Go 1.27.1**: `go 1.27.1` in `go.mod`, `go_1_27` in the flake, from Task 2 on.
- **Versions**: jaccard-store v0.7.0 as now; jaccard-nfs-nix-store v0.3.0, which does not exist before Task 1 is released; go-iroh v0.3.0 as now.
- **Nothing is pushed, tagged or released without the human partner's word.** Task 1 ends in a release of another repository, and this stage in a pull request: both are asked for.
- **Nothing is executed from the mount by zathras itself.** Every process a zathras starts, but the zathras the first process starts, is started through a `launcher.Launcher`, as `great-machine exec-in-place DIR PATH ARGV0 [ARG...]`. A direct `exec.Command(binary).Start()` of something under `/nix/store` in a zathras stops the zathras for good, and so does a working directory there that `os/exec` changes to (the spec's §5.2 and §13 say how both were seen).
- **A container is never privileged** in a test or in `deploy/`: `CAP_SYS_ADMIN` and no AppArmor profile (`--cap-add SYS_ADMIN --security-opt apparmor=unconfined` for Docker). That this suffices is part of what the tests show.
- **Nothing in a pod takes requests to fetch.** There is no preload endpoint; the NFS server listens on `127.0.0.1`, on a port the system picks.
- **The prefix of a service's references** is `/services/<service>/nix/store/` and is made by `wire.StorePrefix` alone: no flag, no variable, no second place that spells it.
- **The names**: the ALPN of the store is jaccard-store's, `amber/jaccard-store/1`; the first argument of the program replacing itself is `exec-in-place`; the flags and their variables are those of the spec's §3.6 and §4.5, letter for letter; a refusal of the store reads `unauthorized: <operation> is for operators`.
- **The mount and what the launcher drops are for Linux alone.** Files that need Linux have the build tag or the suffix; the operator's commands and draal build and are tested on macOS as before. A test that needs Linux and root is skipped anywhere else with a message that names `scripts/test-linux.sh`.
- What draal or a zathras logs, refuses or returns is a sentence a person can act on, in the repository's own plain voice; doc comments likewise.

## Review Focus

What the spec implies and a person will meet, and the task whose tests pin it:

1. **A program started from the store by the zathras that serves the store, while the garbage collector runs.** It starts, every time, within milliseconds; nothing waits for ever. Pinned in Task 10 (300 starts under collections that never pause, and a start with an empty cache), with the launcher's own tests in Task 8.
2. **A store path the store does not have for this service**: the first rollout after the upgrade, a path that was pushed for another service alone, a name that was mistyped. The operator reads the path, and that the store has none of that name for the service, in the reason of the rollout; nothing was restarted. Pinned in Task 10 (the mount), Task 11 (the reason as it travels) and Task 7 (the prefix a rollout pushes under).
3. **The mount cannot be made**: the container lacks the capability, the system is not Linux, the cache was filled for another service. zathras ends at its start with a sentence that says what the container needs, and nothing stays mounted or listening behind it. Pinned in Task 10 and Task 11, and on a cluster in Task 14.
4. **Someone who is no operator pushes, lists or deletes; and draal is stopped while a push is under way.** The first reads `unauthorized: list is for operators` where `great-machine rollout` prints its error (a push begins by asking what the store has), and `unauthorized: push-start is for operators` for a client that pushes without asking; the second ends the push with an error, and draal still ends in its own time, also when a request of the store does not end. Pinned in Task 5 (the store), Task 6 (what a push says, and draal's end) and Task 7 (through draal's endpoint ID).
5. **A service that is not there when it is started, a `--user` that cannot be, and a service of `--user` that opens `/dev/stdout`.** The first is a service that ended at once: status 127, the reason in its log, the supervisor backing off as for any other. The second ends the command line before anything is started, and says what a user is written as. The third works, and so does a shell that opens its own terminal: what a process of another user is started on is given to that user. Pinned in Task 8, Task 9 and Task 11.

Besides these: a `--store-scratch` that is the data directory, or `/`, costs draal nothing, since the store empties a directory of its own making alone (Task 5); a rollout with more `--jobs` than the store answers streams of is refused with the bound (Task 6); a peer that opens streams of the store's protocol and says nothing, or opens too many, costs draal nothing lasting (Task 5); a draal whose bucket has no region says which flag gives it one, and one whose bucket is not there starts and says so at the first push (Tasks 5 and 6); and zathras told to stop while a prepare is under way ends the prepare and unmounts (Task 11).

## How things are run

Every Go command of this plan is run in the flake's shell, from the root of the repository, which has Go 1.27 from Task 2 on:

```bash
nix develop .#bare --command go test ./...
```

A step that says `Run: go test ./x/` means that. A step that names several commands means them in one such shell (`nix develop .#bare --command bash -c '…'`).

**On Linux, as root, from Task 8 on:**

```bash
scripts/test-linux.sh                              # every package
scripts/test-linux.sh -run Mount -v ./storemount/  # the arguments are go test's
```

That is `go test` in a `golang:1.27` container with `--init`, `CAP_SYS_ADMIN` and no AppArmor profile, which is what the container of a pod has (`deploy/pod.yaml`), and not privileged. The tests that need Linux and root (what the launcher's thread gives up; the store that is mounted) skip themselves anywhere else, so a run on a Mac says nothing about them: **a task that touches `launcher`, `storemount`, `supervisor`, `ptyshell`, `cli/zathras*.go` or `e2e` is not done before the script has passed.** Before Task 8 the tree is run on Linux as Task 2 shows it.

The script keeps Go's build cache in a Docker volume, `great-machine-go-build`. The tests of the mount build programs with the NFS server in them: the machine this plan was written on had 4 GB of Docker's disk left, and that was enough.

**A `diff` block** in a step is the change to make, in the form `git diff` prints it against the tree as the steps before left it. Make the change by hand or save the block to a file and run `git apply` on it; either way the file has to end up as the block says. A block that does not apply means that an earlier step was not carried out as written: find out which before going on.

**A test starts real things**: the tests of the supervisor and the shells start child processes, those of `node`, `storeserver` and `e2e` bind UDP ports on loopback, and those of `storemount` mount. None needs the internet.

**Task 1 is in another repository** and is run with that repository's own commands, which its task names.

---

## File Structure

```
(jaccard-nfs-nix-store, Task 1)
preload/             Fetch and Failure, which the endpoint is made of
sidecar/             (*Sidecar).Preload, ErrStopped

(great-machine)
wire/                StorePrefix; the service in the store request
authz/               the two operations of the store
node/                an endpoint that accepts other protocols, and gives their connections away
storeserver/         the store in draal: jaccard-store's server behind authz, on connections node accepted
draal/               Config.ID in the place of Config.Store; Store(ctx, service)
operator/, push/     Store(ctx, service); 20 jobs; "the store"
web/                 the store's page under /store/
ui/                  a link to it in the header
launcher/            the thread that starts the processes of a zathras, and exec-in-place
supervisor/, ptyshell/   a Start that is given to them
storemount/          the store of a zathras: jaccard-nfs-nix-store's sidecar in the process, mounted
zathras/             Preloader
preload/             gone
cli/draal.go         the flags of the bucket and of the store; the order of draal's start and end
cli/zathras.go       --store-cache, --no-store, --user; the order of a zathras's start and end
cmd/great-machine/   exec-in-place is looked for first
e2e/                 the store through draal; a rollout into a mounted store (Linux, root)
scripts/             test-linux.sh; test-page.sh with a bucket
deploy/              a pod of one container; a draal with a bucket
flake.nix            Go 1.27; an image with an empty /nix/store
```

## Interfaces

This section is the contract between the tasks. A task implements what is written here for its part, exactly, and uses the other parts only through what is written here.

### jaccard-nfs-nix-store v0.3.0 (Task 1)

```go
// package preload

// Fetch fetches the store paths of the names with fetch, as many at once
// as there are jobs; fewer than one job means DefaultJobs. The first name
// that cannot be fetched ends the others and is returned as a *Failure.
// When ctx ends Fetch returns an error that is ctx's.
func Fetch(ctx context.Context, fetch Fetcher, jobs int, names []string) error

// Failure is the store path of a list that could not be fetched, and why.
type Failure struct {
	Name string // without /nix/store/
	Err  error  // refs.ErrNotFound for a path the server does not have
}
func (f *Failure) Path() string  // /nix/store/<name>
func (f *Failure) Error() string
func (f *Failure) Unwrap() error

// package sidecar

// ErrStopped is what Preload returns for a sidecar that was stopped,
// before the call or while it ran.
var ErrStopped = errors.New("sidecar: stopped")

// Preload fetches the store paths of names as the preload endpoint
// fetches a list, Config.PreloadJobs at once, whether or not there is an
// endpoint. It ends with ctx, whose error it then returns, and with the
// sidecar.
func (s *Sidecar) Preload(ctx context.Context, names []string) error
```

`sidecar.Config.MountOptions` has no default of its own: empty is no options, which is the kernel's hard mount. Whoever starts a sidecar in a program passes `sidecar.DefaultMountOptions`.

### wire and authz (Task 3)

```go
// StorePrefix returns "/services/<service>/nix/store/".
func StorePrefix(service string) string

// StoreRequest asks where the closures of a service are to be pushed.
type StoreRequest struct {
	Service string `json:"service"`
}
```

`StoreInfo` stays `{Server ID; Prefix string}`: the server is draal itself, the prefix the service's.

```go
OpStorePull  Operation = "store-pull"  // a pull of the store's protocol: every peer
OpStoreWrite Operation = "store-write" // push-start, push-upload, push-commit, list, delete: operators
```

`Allowlist.Authorize` returns nil for `OpRegister` and `OpStorePull` always. The service of the two is `""`. `OpStore`, the `store` request, is asked with the service it names.

### draal.Controller, operator, push (Tasks 3 and 6)

```go
// Store returns where the closures of a service are pushed to: draal's own
// endpoint ID and the prefix of the service. A name that is no name of a
// service is a bad-request.
Store(ctx context.Context, service string) (wire.StoreInfo, error)
```

- `draal.Config.ID wire.ID` (Task 6) is what `Store` answers as the server; until Task 6 it is `Config.Store.Server`.
- `operator.Client.Store(ctx context.Context, service string) (wire.StoreInfo, error)`.
- `push.Config.Jobs` 0 is 20. `push.MaxJobs` is 32 (Task 6): `Push` refuses more, and so does `--jobs`. What `push` says of where it pushes: "the store".

### node (Task 4)

```go
type ListenConfig struct {
	Key   Key
	Bind  netip.AddrPort
	Local bool
	Log   *slog.Logger
	// Handlers are for the protocols the endpoint accepts besides
	// great-machine's own: by its ALPN, what is given every connection
	// that is accepted for it. A handler is called in a goroutine of its
	// own and returns when it is done with the connection, which is then
	// closed: it need not close it. Close of the listener closes the
	// connections of handlers too.
	//
	// Such a connection is handed over by Accept, which goes on to the
	// next: it is taken only while Accept is called.
	Handlers map[string]func(Conn)
}
```

`Listen` refuses a handler for `wire.ALPN`, for `""`, and a nil one. `Accept` returns the connections of `wire.ALPN` alone.

### storeserver (Task 5)

```go
const ALPN = "amber/jaccard-store/1"

type Config struct {
	Dir          string           // required: holds store.sqlite
	Bucket       bucket.Config    // the S3 bucket (jaccard-store/bucket)
	Objects      server.Bucket    // for tests: the bucket itself; then Bucket is not looked at
	Scratch      string           // where the store makes its own great-machine-scratch, which alone it empties; "": Dir
	VerifyJobs   int              // 0: 2
	MaxPackBytes int64            // 0: 16 GiB
	NoVerify     bool
	Authz        authz.Authorizer // required
	RequestWait  time.Duration    // 0: 5 seconds, for the request of a stream
	AnswerWait   time.Duration    // 0: 2 seconds, for the peer to end a stream that was answered
	MaxStreams   int              // 0: 64 streams of a connection answered at a time
	StopGrace    time.Duration    // 0: 10 seconds, for the requests under way when Stop is called
	SweepEvery   time.Duration    // 0: 30 seconds
	Log          *slog.Logger     // nil: slog.Default()
}

func Open(ctx context.Context, cfg Config) (*Store, error) // asks the bucket nothing
func (s *Store) ServeConn(conn node.Conn)                  // what Handlers has for ALPN
func (s *Store) Admin() http.Handler                       // asked with its path prefix taken off
func (s *Store) Stop()                                     // may be called more than once
func (s *Store) Close() error                              // Stop, the sweeper, the database
```

A refusal reads `unauthorized: <operation> is for operators`, as an error of jaccard-store's protocol with the code `bad_request`; an operation the store does not know, `the store answers no such request`.

### draal's command and the page (Task 6)

`--s3-bucket` (required), `--s3-prefix`, `--s3-endpoint`, `--s3-region`, `--s3-path-style`, `--store-scratch`, `--store-verify-jobs`, `--store-max-pack-size`, `--store-no-verify`; the variable of each is `GREAT_MACHINE_` and the name in capitals with underscores. `--jaccard-server` and `--jaccard-prefix` are gone.

`runDraal`, in this order: the data directory, the key, draal's database, the store (`storeserver.Open`, `Dir` the data directory), the listener (`Handlers: {storeserver.ALPN: store.ServeConn}`), the web listener, `draal.New` with `ID: listener.ID()`, the page with `Store: store.Admin()`, `d.Serve`. At the end: the page; `d.Serve` returns; `store.Stop()`; the listener; `store.Close()`; draal's database.

`web.Config.Store http.Handler` is served under `/store/`; nil: a `not-found`.

### launcher (Task 8)

```go
const Arg = "exec-in-place" // great-machine exec-in-place DIR PATH ARGV0 [ARG...]; DIR may be empty

func Replace(args []string) (status int, was bool) // first thing in main, and in a TestMain

type User struct{ UID, GID uint32 }
func ParseUser(text string) (User, error) // "UID" or "UID:GID"

type Config struct {
	User *User        // nil: the user of this process
	Log  *slog.Logger // nil: slog.Default()
}

var ErrClosed = errors.New("launcher: closed")

func New(cfg Config) (*Launcher, error)
func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error
func (l *Launcher) Close()
```

`Start` takes the working directory out of `cmd` and gives it to `exec-in-place`, and with a `User` it makes that user the owner of the standard streams of `cmd` that are pipes or terminals. Both have to be the command's when `Start` is called: a terminal that `start` itself opens is not seen.

### supervisor and ptyshell (Task 9)

Both configs get the same field, and neither imports `launcher`:

```go
// Start, if set, starts a process in the place of cmd.Start: it is given
// the command and what starts it, and returns what that returned. It is
// (*launcher.Launcher).Start. nil: the process is started as it is.
Start func(cmd *exec.Cmd, start func() error) error
```

`ptyshell` opens the terminal of a shell before it starts the shell, so that the command has it as its standard streams when `Start` is given the command.

### storemount (Task 10)

```go
const DefaultDir = "/nix/store"

type Config struct {
	Server  wire.ID          // required: draal's endpoint ID, which is the store's
	Addrs   []netip.AddrPort // for tests: where the server is
	Service string           // required: its prefix is what the mount is made of
	Cache   string           // required: what was fetched; of one server and one service
	Dir     string           // "": DefaultDir
	Log     *slog.Logger     // nil: slog.Default()
}

func Start(cfg Config) (*Mount, error)                             // dials nobody; on another system than Linux it fails
func (m *Mount) Preload(ctx context.Context, names []string) error // names without /nix/store/
func (m *Mount) Stop() error

// NotFoundError is the error of Preload for a store path that the store
// has none of under the prefix of the service.
type NotFoundError struct {
	Path    string // /nix/store/<name>
	Service string
}
```

### zathras and its command (Task 11)

```go
// Preloader fetches the store paths of a closure before anything of it is
// used: *storemount.Mount.
type Preloader interface {
	Preload(ctx context.Context, names []string) error
}
```

`zathras.Config.Preload` is a `Preloader`; nil: nothing is fetched. `--store-cache DIR` (`GREAT_MACHINE_STORE_CACHE`, `/var/cache/great-machine`), `--no-store` (`GREAT_MACHINE_NO_STORE`), `--user UID[:GID]` (`GREAT_MACHINE_USER`); `--preload-url` is gone.

`runZathras`, in this order: the launcher; the store, unless `--no-store`; the health check's listener; the key; the supervisor, with `Start: l.Start`; the agent, with the store as its `Preload` and `Start: l.Start` in its `Shell`. At the end: the agent and the supervisor; the store; the health check.

## Tasks

Each task is a file of its own in [`2026-10-11-great-machine-stage-4/`](2026-10-11-great-machine-stage-4/). A task is done when its last step is, and its tests pass where they run: on this machine, and from Task 8 on through `scripts/test-linux.sh` as well.

| # | Task | What it leaves | Needs |
|---|---|---|---|
| 1 | [jaccard-nfs-nix-store: a sidecar that preloads by a call](2026-10-11-great-machine-stage-4/task-01-nfs-nix-store.md) | in another repository: `preload.Fetch`, `(*Sidecar).Preload`; a release, v0.3.0, which is asked for | |
| 2 | [Go 1.27](2026-10-11-great-machine-stage-4/task-02-go.md) | the module and the flake on the Go that the mount's packages need | |
| 3 | [A prefix for every service, and who may do what with the store](2026-10-11-great-machine-stage-4/task-03-prefix.md) | `wire.StorePrefix`; the service in the `store` request; `authz`'s two operations; 20 jobs | 2 |
| 4 | [`node`: an endpoint for two protocols](2026-10-11-great-machine-stage-4/task-04-node.md) | `ListenConfig.Handlers` | 2 |
| 5 | [`storeserver`: the store in draal](2026-10-11-great-machine-stage-4/task-05-storeserver.md) | jaccard-store's server behind `authz`, on connections that `node` accepted | 3, 4 |
| 6 | [draal serves the store](2026-10-11-great-machine-stage-4/task-06-draal.md) | the flags, the wiring, the order of draal's end, the store's page | 5 |
| 7 | [`e2e`: the store through draal](2026-10-11-great-machine-stage-4/task-07-e2e-store.md) | two services pushed under one endpoint ID, pulled by a stranger | 6 |
| 8 | [`launcher`: what starts the processes of a zathras](2026-10-11-great-machine-stage-4/task-08-launcher.md) | the thread, `exec-in-place`, `scripts/test-linux.sh` | 2 |
| 9 | [The supervisor and a shell start through what they are given](2026-10-11-great-machine-stage-4/task-09-start.md) | `Config.Start` in both | 8 |
| 10 | [`storemount`: the store of a zathras](2026-10-11-great-machine-stage-4/task-10-storemount.md) | the mount, on Linux; the module needs jaccard-nfs-nix-store | 1, 3, 8 |
| 11 | [zathras mounts its store, and starts everything through its launcher](2026-10-11-great-machine-stage-4/task-11-zathras.md) | `great-machine zathras` as a pod runs it; no package `preload` | 9, 10 |
| 12 | [`e2e`: a rollout into a store that is really mounted](2026-10-11-great-machine-stage-4/task-12-e2e-mount.md) | the one test with no stand-in, on Linux as root | 7, 11 |
| 13 | [Delivery](2026-10-11-great-machine-stage-4/task-13-delivery.md) | `deploy/`, the image, CI, the README, the design | 12 |
| 14 | [On a cluster, by hand](2026-10-11-great-machine-stage-4/task-14-cluster.md) | what only a cluster shows, in the ledger | 13 |

Tasks 3 to 7 are draal's side and Tasks 8 to 11 the zathras's: the two share no file but `go.mod`, `cli/fake_test.go`, `cli/commands_test.go` and the harness of `e2e`, and the steps of this plan are written for the order of the table. Task 1 can be done at any time before Task 10, and its release takes a person: begin with it.

## Decisions taken while this plan was written

Each of these the spec left open or got wrong, and each is in the spec now where it changes it.

1. **Nothing is executed from the store by the zathras that serves it.** The spec had the launcher's thread start the service straight from `/nix/store`. Tried, it stopped the process: Go starts a child with `vfork`, the starting thread stands still until the child has executed, the runtime cannot stop the world meanwhile, and a garbage collection that begins then holds the goroutines that serve the very file the child is executing. The launcher therefore starts the program itself, a file of the image, as `exec-in-place`, and that process becomes the service. The launcher's thread, which the human partner chose over a helper that drops privileges, stays what drops them: the helper that came back drops nothing, it only executes. (The spec's §5.2 and §13.)
2. **The launcher goes through `exec-in-place` on every system**, also where it has no thread and nothing is mounted: one way to start a process, tested wherever the tests run.
3. **A shell that is not there stays an error of its start.** Through a launcher it would be a session that says one line and ends; `ptyshell` looks at the file first.
4. **A stream of the store that was answered is kept until its peer ends it**, 2 seconds at most, and **a request that does not end with its context has the grace twice and is left behind**: both so that draal's end is neither early for a client nor held by one. (The spec's §3.4.)
5. **draal asks its bucket nothing when it starts**, as jaccard-store's own server does not: a bucket that is not there shows in the first push. A region it does need at once, and says which flag gives one. (The spec's §11.)
6. **The refusal a rollout prints for a key that is no operator's names `list`**, since a push begins by asking what the store has. (The spec's §3.3.)
7. **A handler's connection is closed by `node` when the handler returns**, and connections of the store's protocol are taken only while draal accepts. The second is harmless: between the end of `Serve` and the stop of the store nobody is let in.
8. **The store of the harness of `e2e` is on request**, because the bucket in memory sets the environment of its test and ten tests of `e2e` are parallel.
9. **One test has no stand-in** (Task 12), which the spec did not ask for: the mount, pulling from draal's own store with a key draal does not know, and a service started from it by a launcher. Without it no test would have the two sides of the stage meet.
10. **`deploy/` and the README name v0.2.0**, the release that this stage is, which exists once it is tagged; and the region of the bucket is in `deploy/draal.yaml`, because draal does not start without one.
11. **The page gets a link to the store's page**, and the demo of the page a line in its place. `scripts/test-page.sh`, which CI asks the built program and the image for the page with, starts its draal with a bucket and a region and no credentials, and asks for the store's page too.
12. **The release of jaccard-nfs-nix-store is a step of this plan** (Task 1) and a person's to make. Until it exists the module can require a pushed commit of that branch, never a `replace` to a directory, which neither CI nor the Nix build can follow.

And what two reviews found (below), each mended and in the spec:

13. **The store empties a directory of its own making alone**, `great-machine-scratch` in `--store-scratch`. jaccard-store's server empties its scratch directory when it is made; a `--store-scratch` that was the data directory cost draal its key and both databases at its next start. (The spec's §3.2.)
14. **`--jobs` is 32 at the most.** More than 64 jobs made the push's own streams go beyond what the store answers of a connection, and a rollout failed in its middle against a store that was well. Half of 64, because a push has two streams under way as it goes from one step to the next. (The spec's §3.8.)
15. **The working directory of what a launcher starts is changed to by `exec-in-place`**, whose arguments are therefore `DIR PATH ARGV0 [ARG...]`: a working directory in the store stood the process still as an execution from it did. No zathras gives its service a working directory today; the supervisor and the shells can be given one. (The spec's §5.2.)
16. **A launcher with a user gives the user what a process is started on**, the pipes and the terminal, and a shell's terminal is opened before the shell is started so that the launcher sees it: a service of `--user` could not open `/dev/stdout`, nor a shell its terminal. (The spec's §5.3.)
17. **An endpoint has a host and nothing that is no address; a size is digits; a scratch directory of blanks is none.**

## What was run while this plan was written

**The code of every task was built and tested before its task file was written.** The four things the spec wanted settled first were tried in a scratch module (the spec's §13 has what came of each). Then two writers worked test-first in clones of the repository and of jaccard-nfs-nix-store, one on draal's side (Tasks 3 to 7) and one on the zathras's (Tasks 1 and 8 to 11), against the contract above; the main lane wrote Tasks 2 and 12 to 14, reviewed both, and mended what the review found (the second grace of `Stop`, the region, `scripts/test-page.sh`, the demo's line, stale wording). The commits were then replayed in the order of this plan's tasks on a fresh branch, without a conflict and to the same tree, and **each task file was generated from the commits of its cycles**: every "expected to fail" is the output of a real run against the tree as the step before left it.

**On the tree the task files add up to**, on arm64 macOS and on arm64 Linux:

- `gofmt -l .` prints nothing; `go vet ./...` is clean for macOS and for Linux; the module builds for `linux/amd64` without cgo.
- `go test ./...` passes in all 33 packages on macOS, and twice in a row in all 33 on Linux as root, in a container that is not privileged and has `CAP_SYS_ADMIN` and no AppArmor profile (the options of `scripts/test-linux.sh`).
- **At the end of every task, in the order of the plan**, the tree was checked out and `gofmt`, `go vet` for both systems and `go test ./...` were run on macOS: clean and passing after each of Tasks 3 to 13 (30 to 34 packages); and from Task 8 on the whole was run on Linux as root as well, passing after each.
- **The plan was carried out from its own text** by a program: every "Create", "Change" and "Delete" of every task file, in the order of the steps, on a fresh tree of each repository. After every task the tree was the one the tests ran on, to the byte. (`go.mod` and `go.sum`, which the steps make with `go get` and `go mod tidy`, were taken from the reference.)
- In a row, on that tree: the test of the whole without a stand-in (Task 12) 15 times; the store's tests 15 times on macOS and 10 on Linux; the launcher's 20 times as root; the mount's 5 times; the command with a service in it (Task 11) 10 times on each system; a push of 90 store paths with the most jobs there are, 32, 15 times on macOS and 10 on Linux, never beyond the store's 64 streams.
- A program started from the mount by the process that serves it, with the garbage collector running without pause: 300 starts, every other one in a working directory of the mount that the kernel had not been told of, the longest between 9 and 63 ms over ten runs under some 3,400 collections each, and a start with an empty cache in 22 to 27 ms. Without `exec-in-place` the same test stood still within its first starts until it was killed, and so it did with `exec-in-place` and the working directory left to `os/exec`.
- The mutant of the command that the second review made fails the test of Task 11 on macOS (the store stopped under a service that runs) and on Linux as root (that, and a service and a shell that can gain privileges and are not the user that was asked for).
- The launcher's two tests for a process that is not root pass as user 65534.
- jaccard-nfs-nix-store with Task 1: its own tests on macOS, and all 11 packages on Linux by its own script.
- The page: its type check, its 116 tests and its build; the Go packages that serve it with it built; `scripts/test-page.sh` against a program with the page in it, started without credentials. The link in the header was looked at in a browser at 1100 and at 430 pixels, and followed.
- `deploy/pod.yaml` and `deploy/draal.yaml` are valid for `kubeconform -strict`, and `test.yml` for `actionlint`.
- The program as Nix builds it on macOS with Go 1.27 (Task 2's tree).

**Independent reviews.** Each writer's part was first reviewed by the main lane, which had not written it. Then each side was reviewed by an agent that had written none of it and was told to break it, and to prove what it found with a failing test:

| Side | Found | What became of it |
|---|---|---|
| draal's (Tasks 3 to 7) | No way for a peer that is no operator to write, and no race, hang or leak in the store's stop, under the race detector with 50 connections of 8 streams each. But: a `--store-scratch` that was the data directory made draal delete its key and both databases at every start; `--jobs` above 64 failed a rollout against a store that was well; an endpoint without a host, with a user or a query, and a size with a sign, were taken; one assertion on the log could not fail for three of five operations. Minor, and left: the write of an answer has no time bound of its own, as draal's own protocol has none. | The first four are mended, each with the test that failed first (Tasks 5 and 6). The last is in "Left as it is" below. |
| the zathras's (Tasks 8 to 11) | No way for a process a zathras starts to have `CAP_SYS_ADMIN`. But: a working directory in the store stood the process still, as an execution from the store did before `exec-in-place` (not reachable through the command, which gives no working directory); a service of `--user` could not open `/dev/stdout`, nor a shell its own terminal; and a mutant of the command that gave its launcher to nobody, forgot `--user` and stopped its store at once passed every test, because none started a service. Minor, and left: a signal that zathras was started ignoring is not ignored by what it starts; the checks of a binary cannot be cancelled. | The first three are mended (Tasks 8 and 9), with a test of the 300 starts that stands still without the mend (Task 10). The command has a test with a service in it now, which the mutant fails on macOS and on Linux (Task 11). The two minor ones are in "Left as it is". |

**What the reviews found is in the task files as cycles of their own**, each saying that a review found it, behind the cycles that were there before. The code of three steps is therefore written once and revised by a later step of the same task: the arguments of `exec-in-place` (Task 8), the scratch directory of the store (Task 5), and how the terminal of a shell is opened (Task 9). They were not folded into the earlier steps because every "expected to fail" of this plan is the output of a real run, and folding would have meant making those up.

**Left as it is**, known:

- The store has no time bound on the writing of an answer: a peer that takes none keeps its stream until the connection ends, and `Stop` gives it the whole grace. draal's own protocol is the same, and jaccard-store's client always reads.
- A signal that zathras itself was started ignoring, other than SIGHUP and SIGINT, is not ignored by its service: the Go program in between takes it. zathras ignores none, and a container starts its first process ignoring none.
- `prepare` and `activate` look at the binary with a `stat` that cannot be cancelled. On a store path that is not fetched yet that waits for the fetch, two minutes at the most, and a zathras that is told to end meanwhile ends that much later. A rollout fetches before it looks.
- The harness of `e2e` puts draal and its store together by hand, as the command does; that the command does it rightly is held by the tests of `cli`.
- The launcher's thread is never ended by the command but with the process.

**Not run:**

- **x86_64**, on which GitHub's workflow runs.
- **A kernel with AppArmor.** Docker Desktop has none. That `CAP_SYS_ADMIN` and no profile suffice to mount there is first seen by the step "linux, as root" of `test.yml`, and on a cluster by Task 14.
- **The Nix build for Linux with the mount's packages in it, and the image.** The Docker disk of the machine had no room for a Nix store. The module was cross-compiled for Linux with `go build`, and jaccard-nfs-nix-store's own flake builds the same packages with gonixgo and Go 1.27. Tasks 10 and 13 have the step.
- **`scripts/test-linux.sh` itself**, on the tree with jaccard-nfs-nix-store beside it: the script mounts the repository alone. Every run on Linux used its options and its image with the directory above mounted as well.
- **A cluster**: a pod of one container, what it leaves on its node, the bucket reached from a laptop and from pods, iroh between the two. Task 14.
- **SELinux**, and Kubernetes before 1.30: the README and `deploy/pod.yaml` say what is meant for each, and nothing here has seen it.
- **A release of jaccard-nfs-nix-store**: nothing was pushed or tagged.
