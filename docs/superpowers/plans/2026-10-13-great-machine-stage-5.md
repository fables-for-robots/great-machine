# great-machine, stage 5: a store without a mount — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** zathras pulls the store paths of a closure and writes them into `/nix/store` as files, and mounts nothing: its container needs no capability, and its store is built and tested on every system.

**Architecture:** A new package, `storedir`, takes the place of `storemount` behind `zathras.Preloader`. It pulls a reference into a pack cache with jaccard-store's client, writes its tree out beside its name with Core's `fstree`, and moves it there. With the mount gone the launcher is what is left of `--user`: no thread, no capability to drop, no `exec-in-place`.

**Tech Stack:** Go 1.27; jaccard-store v0.7.0 (`client`, `node`, and for tests `server`, `db`, `bucket/buckettest`); Amber-Store Core v0.10.0 (`packstore`, `fstree`, `key`); go-iroh v0.3.0; `golang.org/x/sys/unix` for `syncfs` and the bounding set; Docker for the tests that need root.

**Spec:** `docs/superpowers/specs/2026-10-13-great-machine-stage-5-design.md`, whole ("the spec" below). It changes `docs/superpowers/specs/2026-10-09-great-machine-design.md` ("the design") and stage 4's design; Task 7 brings those documents up to it.

This plan is shorter than those of stages 1 to 4, which spelled every line out for an implementer who had not read the code. It was written to be carried out by its author in the session that wrote the spec: it fixes the files, the interfaces, the tests by what each shows, and the order, and leaves the lines to the execution. What was run, and what it found, is in the ledger beside this file.

## Global Constraints

- Everything the plans of stages 1 to 4 constrain still holds: one module and one program, no `internal/` directory, no cgo, `log/slog`, the standard library's `testing` alone for Go tests, every waiting time a field of a `Config`, nothing built left behind, work on a branch (`stage-5`, which exists and holds the spec and this plan).
- **Versions**: Go 1.27.1, jaccard-store v0.7.0, Core v0.10.0, go-iroh v0.3.0, all as now. jaccard-nfs-nix-store leaves `go.mod`.
- **Nothing is pushed, tagged or released without the human partner's word.** This stage ends in a pull request and a release, v0.3.0: both are asked for.
- **draal, the protocol, the store in draal and the push are not changed.** A diff that touches `draal/`, `wire/`, `storeserver/`, `push/`, `authz/` or `node/` is a mistake.
- **The names**: the package is `storedir`; the pack cache is `packs/` under `--store-cache`; what is being written is `.great-machine-partial-NAME` in the store directory, beside its name; no flag is added or taken away, and `--store-cache` and `--no-store` keep their variables.
- **The times of a pull** are those of the spec's §3.4: 30 seconds for a dial, 1 second before a second attempt and twice as long each time up to 15 seconds, 2 minutes from the first failure, 1 hour for one attempt; twenty fetches at a time.
- **What is written out** is what the table of the spec's §3.3 says, letter for letter: `0555` for a directory, for a file with the execute bit of its owner and for a root that is the content of a file, `0444` for any other file, the user and the group of zathras, no setuid, setgid or sticky bit.
- **A container is never privileged and is given no capability** in a test or in `deploy/`.
- What zathras logs, refuses or returns is a sentence a person can act on, in the repository's own plain voice; doc comments likewise.

## Review Focus

What the spec implies and a person will meet, and the task whose tests pin it:

1. **A zathras that is killed while it writes a store path out, and started again.** The name is not in the store directory; what was written, read-only directories among it, is gone after the start; the next `prepare` fetches the path. Pinned in Task 2.
2. **A store directory that everybody may write to**, as the volume of a pod is. After the start it is zathras's alone, mode `0755`, and so is the cache. Pinned in Task 2.
3. **A closure with a store path that is one file, and one whose tree has symbolic links that point nowhere, or out of the store.** Both are written out as they are; a link is never followed. Pinned in Task 1.
4. **A pod left with the `securityContext` of v0.2.0.** zathras starts, and warns that a service that is root has `CAP_SYS_ADMIN`. Pinned in Task 3 (which capabilities the warning names).
5. **The bucket or draal away for a while in the middle of a `prepare`.** The fetch is tried again and succeeds; away for longer than the time of a pull, the `prepare` fails with the path. Pinned in Task 2.

Besides these: a name of a closure that begins with `.` is refused before the store is asked (Task 2); a reference with a FIFO in it is refused and leaves nothing under its name (Task 1); two `prepare`s of one closure fetch each store path once, and one that is withdrawn does not end the other's fetch (Task 2); a binary that is not there is a start that failed, with the error as its reason, and not a status of 127 (Task 3).

## How things are run

`go` is on the path of the flake's shell, which direnv enters; outside it, `nix develop .#bare --command go test ./...`. A step that says `go test ./x/` means that.

On Linux as root, for `--user`:

```bash
scripts/test-linux.sh                       # every package
scripts/test-linux.sh -run User -v ./launcher/
```

From Task 3 on that container has no capability beyond Docker's defaults. A task that touches `launcher`, `supervisor`, `ptyshell`, `cli/zathras*.go` or `e2e` is not done before the script has passed.

## File Structure

```
storedir/
  storedir.go        Config, Open, Close, NotFoundError: the directories, the pack cache, what a run was writing
  preload.go         Preload: the names of a closure, twenty fetches at a time, one fetch for a name
  pull.go            the connection to the store, a pull and its attempts
  write.go           a reference written out as files (the spec's §3.3)
  sync_linux.go      syncfs
  sync_other.go      every file as it is written
  *_test.go          a store in the test's process, as push/server_test.go has one
storemount/          gone
launcher/
  launcher.go        User, ParseUser, New, Start: the user of a process, and what it is started on
  caps.go            which capabilities are beyond a container's defaults
  caps_linux.go      the bounding set of the process
  caps_other.go      none
cli/zathras.go       opens and closes a storedir; the words of the flags
cmd/great-machine/   exec-in-place is not looked for
supervisor/, ptyshell/   what they say of a start; ptyshell's look at the shell's program goes
e2e/storedir_test.go the rollout with nothing standing in, on every system (was mount_linux_test.go)
deploy/pod.yaml      no capability; allowPrivilegeEscalation; a volume on /nix/store
scripts/test-linux.sh, .github/workflows/test.yml   a container without CAP_SYS_ADMIN
```

---

### Task 1: `storedir`: a reference written out

**Files:** Create `storedir/write.go`, `storedir/sync_linux.go`, `storedir/sync_other.go`, `storedir/write_test.go`.

**Interfaces:**
- Consumes: `fstree.CollectEntries`, `fstree.WriteContent`, `key.Key` of Core; for the tests `ingest.Objects` and `packstore`, as `push.one` uses them.
- Produces:
  - `func accept(root key.Key) error` — nil for a tree or the content of a file.
  - `func writeOut(ctx context.Context, get func(key.Key) ([]byte, error), root key.Key, partial string) error` — writes the reference at `partial`, which is not there and whose parent is; syncs; leaves nothing at `partial` when it fails.
  - `func removeTree(path string) error` — removes a tree whose directories may not be writable.

This task settles the first of the spec's §11: a tree with every kind of entry is imported as a push imports it, written out, and is what it was.

- [ ] Tests, each against a tree made in a temporary directory, imported with `ingest.Objects` into a packstore:
  - a directory with nested directories, a file with and one without an execute bit, an empty file, a file of several megabytes, a symbolic link to a file, one that points nowhere and one that points out of the tree: modes `0555` and `0444`, the targets, the times of the entries, the content, the owner of the process;
  - the root directory is `0555` and has the time 1;
  - a root that is one file: a file, `0555`, time 1, its content;
  - a file that is setuid and one that everybody may write: `0555`, `0444`;
  - a tree with a FIFO: an error that names the entry, and nothing at `partial`;
  - `accept` of a tree, of the content of a file, and of a key of another type;
  - a context that is done: the error of the context, and nothing at `partial`;
  - `removeTree` of a tree whose directories are `0555`.
- [ ] `go test ./storedir/`, failing; then the implementation; then passing, and `go vet ./storedir/`.
- [ ] Commit.

### Task 2: `storedir`: the store of a zathras

**Files:** Create `storedir/storedir.go`, `storedir/preload.go`, `storedir/pull.go`, `storedir/storedir_test.go`, `storedir/server_test.go`.

**Interfaces:**
- Consumes: Task 1; `client.New`, `(*client.Client).Pull`, `client.PullOptions.Accept`, `client.ErrNotFound`; jaccard-store's `node.Dial`; `packstore.Open`; `node.NewKey`, `node.ParseID`, `wire.StorePrefix`, `wire.ValidService`.
- Produces:

```go
const DefaultDir = "/nix/store"

type Config struct {
	Server  wire.ID          // the endpoint ID of draal
	Addrs   []netip.AddrPort // for tests: where the server is
	Service string
	Cache   string // --store-cache; the pack cache is packs/ in it
	Dir     string // the store directory; "": DefaultDir
	Jobs           int           // 0: 20 fetches at a time
	DialTimeout    time.Duration // 0: 30 seconds
	RetryWait      time.Duration // 0: 1 second
	RetryMax       time.Duration // 0: 15 seconds
	PullTimeout    time.Duration // 0: 2 minutes, from the first failure
	AttemptTimeout time.Duration // 0: 1 hour
	Log *slog.Logger
}

func Open(cfg Config) (*Store, error)
func (s *Store) Preload(ctx context.Context, names []string) error
func (s *Store) Close() error

type NotFoundError struct{ Path, Service string } // the words of storemount's
```

This task settles the second of the spec's §11, with its first test: of two store paths of which the second was pushed after the first and leans on it, the second is pulled with one pack into a cache that holds the first, and with two into an empty one. Should it not hold, the spec says what becomes of the cache, and this task is carried out so.

- [ ] Tests, against a jaccard-store server in the test's process (`server_test.go`, after `push/server_test.go`), store paths pushed with `push.Push` under `wire.StorePrefix`:
  - the pack that the cache saves (above);
  - a closure of a directory and of a store path that is one file is in the store directory, and is what was pushed;
  - a store path that is there is not asked of the store (a server that is stopped);
  - a store path the store lacks, and one pushed under another service's prefix alone: `*NotFoundError`, with the path and the service, at once and not after the time of a pull;
  - a name that is empty, begins with `.` or has a `/` in it is refused before the store is asked;
  - what a run was writing when it ended, a directory of mode `0555` among it, is gone after `Open`, and the store paths are as they were;
  - a store directory and a cache of mode `0777` are `0755` after `Open`;
  - a server that cannot be dialed for a while: the fetch succeeds; for longer than `PullTimeout`: an error with the path;
  - two `Preload`s of the same names fetch each store path once; one whose context is done returns, and the other gets its store paths;
  - no more than `Jobs` fetches at a time;
  - `Close` ends a `Preload` that waits, and a second `Close` returns what the first did; `Preload` after `Close` is an error.
- [ ] `go test ./storedir/`, failing; then the implementation; then passing, with `-race` and `-count=3`.
- [ ] Commit.

### Task 3: the launcher is what is left of `--user`

**Files:** Modify `launcher/launcher.go`, `launcher/caps.go`; create `launcher/caps_linux.go`, `launcher/caps_other.go`; delete `launcher/launcher_linux.go`, `launcher/launcher_other.go`; rewrite the tests of `launcher`; modify `cmd/great-machine/main.go` and its test, `ptyshell/ptyshell.go`, `supervisor/supervisor.go` (a comment), the `TestMain`s and the start tests of `cli`, `e2e`, `ptyshell` and `supervisor`, `cli/zathras.go` (the `Close` of the launcher), `scripts/test-linux.sh`, `.github/workflows/test.yml`.

**Interfaces:**
- Produces:

```go
type User struct{ UID, GID uint32 }
func ParseUser(text string) (User, error)
type Config struct {
	User *User
	Log  *slog.Logger
}
func New(cfg Config) (*Launcher, error)
func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error
```

`Start` sets the user of `Config` on `cmd`, gives its pipes and its terminal to that user, and calls `start`. There is no `Close`, no `ErrClosed`, no `Arg` and no `Replace`.

- [ ] Tests: everywhere, a command without a user is started as it is, with its arguments, its environment and its working directory; a user that is not this process's, asked of a process that is not root, is refused by `New`; which capabilities of a bounding set are beyond a container's defaults. On Linux as root: a process started as a user has the user, the group and no other, and opens `/dev/stdout` and its terminal anew. `supervisor`, `ptyshell`: a binary that is not there is a start that failed, with its error as the reason.
- [ ] The tests of the thread, of what it gave up, and of `exec-in-place` go.
- [ ] `go test ./...` and `scripts/test-linux.sh ./launcher/ ./supervisor/ ./ptyshell/ ./cmd/...`, passing.
- [ ] Commit.

### Task 4: zathras opens a `storedir`, and `storemount` goes

**Files:** Modify `cli/zathras.go`, `cli/zathras_test.go`, `cli/zathras_service_test.go`, `cli/zathras_linux_test.go`, `zathras/agent.go` (a comment), `go.mod`, `go.sum`, `flake.nix` (comments); delete `storemount/`.

**Interfaces:**
- Consumes: Task 2's `storedir.Config`, `storedir.Open`; Task 3's launcher.
- Produces: in `cli`, `type zathrasStore interface { zathras.Preloader; Close() error }` and `zathrasConfig.startStore func(storedir.Config) (zathrasStore, error)`.

- [ ] The tests of the command of a zathras: the store is opened before draal is dialed, asked for the closure first, and closed when the service has ended; a store that cannot be opened ends the command with its error; `--no-store` opens none.
- [ ] The words of `--store-cache`, of `--no-store` and of the command's description (the spec's §3.7 and §3.6).
- [ ] `git rm -r storemount`, `go mod tidy`; `go.mod` names neither jaccard-nfs-nix-store nor a package of Buildbarn.
- [ ] `go build ./... && go vet ./... && go test ./...`; `GOOS=linux go vet ./...`; `scripts/test-linux.sh`.
- [ ] Commit.

### Task 5: `e2e`: the rollout with nothing standing in, on every system

**Files:** Create `e2e/storedir_test.go`; delete `e2e/mount_linux_test.go`; modify `e2e/doc.go`, `e2e/harness_test.go` as needed.

- [ ] One rollout: the closure is pushed to draal's endpoint ID; the store of the zathras is a `storedir` over the directory that is the Nix store of the tests; the service runs from what was fetched; the store of another service is told that the store has no such path for it. It runs without root, on macOS and on Linux.
- [ ] `go test -count=1 ./e2e/` and `scripts/test-linux.sh ./e2e/`, passing.
- [ ] Commit.

### Task 6: the pod, the script and the build

**Files:** Modify `deploy/pod.yaml`, `deploy/draal.yaml` (the tag), `flake.nix` (comments), `.github/workflows/test.yml` (the image's step and its comments).

- [ ] `deploy/pod.yaml` as the spec's §5: the `securityContext` of §4.1, the volume `store` on `/nix/store`, no word of a privileged container.
- [ ] `nix build --option allow-unsafe-native-code-during-evaluation true` builds the program; `./result/bin/great-machine version`; the result is removed.
- [ ] Commit.

### Task 7: the documents

**Files:** Modify `docs/superpowers/specs/2026-10-09-great-machine-design.md`, `docs/superpowers/specs/2026-10-11-great-machine-stage-4-design.md` (a line at its top), `README.md`.

- [ ] The sections the spec's §14 names, each brought up to the spec.
- [ ] Commit.

### Task 8: on a cluster, by hand

After the pull request is merged and v0.3.0 is released, both on the human partner's word: `deploy/` in a namespace that enforces `baseline`. A rollout; a shell that shows no `CAP_SYS_ADMIN` and `NoNewPrivs: 1`; a container killed and started again, which runs the stable generation from the store it finds. The results go into the ledger.
