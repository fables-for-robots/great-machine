# great-machine, stage 6: who may do what — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** draal decides by grants, a role for a key and a service; a zathras registers with a statement of its service's role key; the page is served by `great-machine web` with the operator's key; and admins manage the grants from the command line, the page and the terminal UI.

**Architecture:** `authz` holds the grants in memory, written through to `draal.sqlite`, and answers `Authorize` for the peer, for the signer of a statement and for a zathras that is registered. A zathras has one key and one endpoint per dial, from which both protocols are dialed. draal loses its HTTP server; the `web` package moves in front of an operator's client and gets a ticket and a cookie. Three parts, each of which leaves software that works: draal, zathras and the command line (Tasks 1 to 8); the page (9 and 10); the terminal UI (11).

**Tech Stack:** Go 1.27; go-iroh v0.3.0 (`Endpoint.Connect` for a second protocol; Ed25519 by its `key` package); jaccard-store v0.7.0 (`client` over a connection of go-iroh, `admin`); sqlc and `modernc.org/sqlite`; SolidJS and Vite for the page; Bubble Tea 2 for the terminal UI.

**Spec:** `docs/superpowers/specs/2026-10-13-great-machine-stage-6-design.md`, whole ("the spec" below). It changes `docs/superpowers/specs/2026-10-09-great-machine-design.md`; Task 12 brings that document and the README up to it.

Like the plan of stage 5, this one fixes the files, the interfaces, the tests by what each shows, and the order, and leaves the lines to the execution, which its author carries out in the session that wrote the spec, with forks for the tasks that share no file with the others. What was run, and what it found, is in the ledger beside this file.

## Global Constraints

- Everything the plans of stages 1 to 5 constrain still holds: one module and one program, no `internal/` directory, no cgo, `log/slog`, the standard library's `testing` alone for Go tests, every waiting time a field of a `Config`, nothing built left behind, work on a branch (`stage-6`, which exists and holds the spec and this plan).
- **Nothing is pushed, tagged or released without the human partner's word.**
- **The names**, letter for letter: the roles `admin`, `deployer`, `zathras`; the requests `grants`, `grant`, `revoke`, `me`, `store_page` and their answers as `wire/access.go` has them; the statement's text as `wire.StatementText` writes it; the flags `--admin` (`GREAT_MACHINE_ADMINS`, with `--operator` and `GREAT_MACHINE_OPERATORS` still taken), `--role-key` (`GREAT_MACHINE_ROLE_KEY`), `--addr` of `great-machine web` (`GREAT_MACHINE_WEB_ADDR`, default `127.0.0.1:8080`); the commands `great-machine access list|grant|revoke` and `great-machine web`.
- **`Authorize` reads memory alone.** No request reads the database to be decided.
- **Nothing is open.** No operation is granted to a peer with no grant but `me`, and no test or flag turns the deciding off.
- **A refusal tells nothing of what exists** to a key that may not see it (the spec's §3.4).
- **draal binds no TCP port.** `great-machine web` listens on loopback alone.
- What draal, a zathras or a command logs, refuses or prints is a sentence a person can act on, in the repository's own plain voice; doc comments likewise.

## Review Focus

1. **A deployer of one service and what it learns of another**: the fleet it watches, the log and the shell of a zathras of another service (`unavailable`, as for one that is not there), a listing of the store with a prefix shorter than a service's. Pinned in Tasks 6 and 7.
2. **A statement in other hands**: one signed for another endpoint, for another draal, or by a role key that was granted another service. Each is refused, and says what is wrong. Pinned in Tasks 4 and 6.
3. **A grant that is revoked while it is used**: an open watch and an open shell of the key end, and the session of every zathras the key signed for. Pinned in Task 6, and in `e2e` in Task 8.
4. **The page without its ticket**: no cookie, a stale cookie, another `Host`, a `POST` or a WebSocket from another origin. Pinned in Task 9.
5. **The cut**: a registration without a statement is refused in words that say what to do, a draal with no admin says so at start, and `GREAT_MACHINE_OPERATORS` still makes admins. Pinned in Tasks 6 and 8.

Besides these: an admin cannot revoke its own `admin` grant, nor one of the command line (Task 6); a role key file that is missing ends a zathras at start (Task 5); a name of the store that is under no service's prefix is an admin's (Task 7); the page while draal cannot be reached (Task 9).

## File Structure

```
wire/access.go        Role, Grant, the requests of §7.1 and §8.5, Statement, StatementText, ServiceOfStore
db/                   migrations/0002_grants.sql, queries/grants.sql, and what sqlc makes of them; Grants, PutGrant, DeleteGrant
authz/                the Authority: the grants in memory, Authorize, what a peer may see
node/                 Again: another protocol from the endpoint of a connection that was dialed; Sign, Verify, LoadKey
zathras/              the statement in a registration
storedir/             Config.Dial in the place of a key of its own
storeserver/          a request and its service; Drop; Page
draal/                the registration, what a peer may ask and see, the requests of §7.1, a revocation
operator/access.go    Grants, Grant, Revoke, Me, StorePage
cli/access.go         great-machine access
cli/draal.go          --admin; no page
cli/zathras.go        --role-key; one endpoint for both protocols
cli/webcmd.go         great-machine web
web/                  in front of a Client; ticket.go
ui/                   who the key is; the Access view
tui/                  the Access view
demo/access.go        made-up grants
```

---

## Part 1: draal, zathras and the command line

### Task 1: the contract

**Files:** `wire/access.go`, `wire/messages.go`, `operator/access.go`, `demo/access.go`, with their tests.

What every later task builds against: the types of a grant and of the new requests with their JSON, the client's five calls, the made-up grants of the demo.

- [ ] The text of a statement; the roles; the service of a name of the store; a field the reader does not know.
- [ ] The five calls of the client against a draal of the test.
- [ ] Commit.

### Task 2: `db`: the grants

**Files:** `db/migrations/0002_grants.sql`, `db/queries/grants.sql`, `db/dbq/` (by `sqlc generate`), `db/store.go`, `db/store_test.go`.

**Produces:** `(*DB).Grants(ctx) ([]wire.Grant, error)`, `PutGrant(ctx, wire.Grant) error`, `DeleteGrant(ctx, id wire.ID, role wire.Role, service string) error` (`ErrNotFound` for one that is not there).

- [ ] The migration on a database of before; a grant put, put again with another note, removed, and read back after the database was closed and opened; `sqlc diff` clean.
- [ ] Commit.

### Task 3: `authz`: the authority

**Files:** `authz/authz.go` (rewritten), `authz/authz_test.go`.

**Produces:**

```go
type Config struct {
	Admins     []wire.ID                             // of draal's command line
	Registered func(wire.ID) (service string, ok bool) // a zathras that is registered; may be set later with SetRegistered
}
func New(cfg Config, grants []wire.Grant) *Authority
func (a *Authority) Authorize(peer wire.ID, op Operation, service string) error
func (a *Authority) Signer(by wire.ID, service string) error       // a role key that may sign for a zathras of the service
func (a *Authority) Sees(peer wire.ID) (every bool, services []string) // the services a key may watch
func (a *Authority) Holds(peer wire.ID) []wire.Grant              // what `me` answers
func (a *Authority) All() []wire.Grant                            // what `grants` answers
func (a *Authority) Replace(grants []wire.Grant)                  // after the database was written
```

and the operations: `OpStore`, `OpRollout`, `OpRollback`, `OpRollouts`, `OpHistory`, `OpLogs`, `OpShell`, `OpWatch`, `OpStorePull`, `OpStorePush`, `OpStoreCommit`, `OpStoreList`, `OpStoreDelete`, `OpAccess`, `OpStorePage`, `OpMe`.

- [ ] Every row and column of the spec's §3.2, for one service and for every service; several grants of one key; a request of no service; the signer; a pull by who is registered; `Replace` seen by the next question; the words of a refusal.
- [ ] Commit.

### Task 4: `node`: a second protocol, and a key that signs

**Files:** `node/dial.go`, `node/conn.go`, `node/key.go`, their tests.

**Produces:** `func Again(ctx context.Context, c Conn, alpn string) (Opener, error)`, where `Opener` is `OpenStreamConn(ctx) (net.Conn, error)` and `Close() error`, what jaccard-store's client is made over; `func LoadKey(path string) (Key, error)`, which makes no file; `func (k Key) Sign(text []byte) []byte`; `func Verify(id wire.ID, text, signature []byte) error`.

- [ ] Over real endpoints: a connection that was dialed dials again for the store's protocol, a pull of jaccard-store's client works over it (the third of the spec's §15), and it ends with the first. A connection that was not dialed here is refused. A signature is verified by the ID of its key and by no other; a file that is missing is an error and stays missing.
- [ ] Commit.

### Task 5: a zathras with one key and a statement

**Files:** `zathras/agent.go`, `storedir/storedir.go`, `storedir/pull.go`, `cli/zathras.go`, their tests.

**Consumes:** Task 4. **Produces:** `zathras.Config.Statement *wire.Statement`; `storedir.Config.Dial func(ctx context.Context) (node.Opener, error)` in the place of `Server`, `Addrs` and a key of its own; `--role-key`.

- [ ] A registration carries the statement; `storedir`'s tests as they are, over a dial that is given; the command signs for its own endpoint and the draal it was given, dials the store from the endpoint of its connection to draal, and ends at start without `--role-key` or with a file that is not there.
- [ ] Commit.

### Task 6: `draal`: by grants

**Files:** `draal/draal.go`, `draal/serve.go`, `draal/handler.go`, `draal/access.go` (new), their tests.

**Consumes:** Tasks 2 and 3. **Produces:** `draal.Config.Authz *authz.Authority`, `Config.Store` (what closes the connections of a peer to the store, and answers the page of the store).

- [ ] A registration: well-formed, with a statement, signed for this draal and this peer, by a role key of the service; each refusal in its words.
- [ ] Every request of an operator for a key with each role, for its own service and for another; a zathras of another service is `unavailable`; the fleet a deployer of one service watches.
- [ ] `me`, `grants`, `grant`, `revoke` and `store_page`, with what they refuse: no admin, an admin of the command line, an admin's own grant, a grant that is not there, a role and a service that are none.
- [ ] A revocation ends the connections of the key and the sessions of the zathrases it signed for, before it is answered.
- [ ] Commit.

### Task 7: `storeserver`: for a service

**Files:** `storeserver/storeserver.go`, `storeserver/storeserver_test.go`.

**Produces:** `(*Store).Drop(peer wire.ID)`, `(*Store).Page(path string) wire.StorePage`.

- [ ] The spec's §5, request by request, for an admin, a deployer of the service, a deployer of another, a zathras registered for the service and one registered for another; a name and a prefix under no service; a commit.
- [ ] Commit.

### Task 8: the command line, and the whole

**Files:** `cli/draal.go`, `cli/access.go`, `cli/app.go`, `e2e/harness_test.go`, `e2e/access_test.go`, the tests of `cli`.

- [ ] `--admin` and its old name; a draal with no admin warns; draal has no `--web-addr` and no page; `access list`, `grant` and `revoke`.
- [ ] `e2e`: a deployer of `web` rolls `web` out and is refused `api`; a zathras of each of two services fetches its own closure and is refused the other's; a revocation ends the watch of its key and the session of a zathras.
- [ ] `go test ./...`, `scripts/test-linux.sh`. Commit.

## Part 2: the page

### Task 9: `web` in front of an operator's client, and `great-machine web`

**Files:** `web/` (the handler over a `Client`; `ticket.go`), `cli/webcmd.go`, `cli/web.go`, `web/demo/`, `e2e/http_test.go`, `scripts/test-page.sh`, `.github/workflows/test.yml`.

**Consumes:** Task 1.

- [ ] The ticket and the cookie, the `Host` and the `Origin`, each refused and each let through; the paths of the spec's §8.4 against a fake client; a client that cannot reach draal is `unavailable`.
- [ ] The command refuses an address that is not loopback, prints its link, serves before draal is reached, and dials again after the connection was lost.
- [ ] Commit.

### Task 10: `ui`: who the key is, and Access

**Files:** `ui/src/`.

- [ ] The header, the Access view, a key with no grant, a cookie that is no longer good; `npm run check`, `npm test`, `npm run build`.
- [ ] Commit.

## Part 3: the terminal UI

### Task 11: `tui`: Access

**Files:** `tui/`, `cli/tui.go`, `tui/demo/`.

- [ ] The view as a function of the messages it gets: the list, the form, a refusal, a revocation that is confirmed and one that is not, a key that is no admin; what it draws at every size.
- [ ] Commit.

## The end

### Task 12: the documents and the pod

**Files:** `docs/superpowers/specs/2026-10-09-great-machine-design.md`, `README.md`, `deploy/pod.yaml`, `deploy/draal.yaml`.

- [ ] The sections the spec's §17 names; the Secret of a role key in `deploy/pod.yaml`; `deploy/draal.yaml` without the page.
- [ ] Commit.

### Task 13: on a cluster, by hand

With stage 5's, after the release, on the human partner's word.
