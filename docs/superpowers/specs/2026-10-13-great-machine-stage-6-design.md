# great-machine stage 6: who may do what

2026-10-13

This is a change to the design of
[`2026-10-09-great-machine-design.md`](2026-10-09-great-machine-design.md),
as stages 4 and [5](2026-10-13-great-machine-stage-5-design.md) left it.
What it does not mention stays as those documents say. A section of the
first is written as design §N, one of stage 5 as stage 5 §N, one of this
document as §N.

## 1. Purpose

Up to stage 5 draal knows one kind of peer that may act, the operators of
its command line, who may do everything; any peer at all may register as
a zathras of any service and fetch any store path; and the web page,
which draal serves itself, asks nobody who is looking, and gives a shell
in every container to whoever reaches its port.

Stage 6 says who may do what:

- **Three roles.** An admin does everything. A deployer pushes and rolls
  out, and looks at what it rolled out. A zathras fetches and reports.
- **A role is of a service.** A deployer and a zathras are granted for
  one service or for every one, and a key may hold several grants: one
  key can deploy `web` and `api` and nothing else.
- **Nothing is open.** A zathras registers with a statement that a key
  of its service's signed, and fetches while it is registered.
- **The page is the operator's.** draal serves no page and listens for
  nothing but iroh. `great-machine web` serves the page on the laptop and
  asks draal with the operator's key, as the terminal UI does: the page
  can do what that key may, and nothing without it.
- **Grants are managed** from the command line, from the page and from
  the terminal UI, by admins.

```
 laptop                                   cluster
 ┌──────────────────────┐                 ┌──────────────────────────────┐
 │ great-machine rollout│                 │ draal, one endpoint ID:      │
 │ great-machine tui    │── iroh, with ──▶│   great-machine/1            │
 │ great-machine web    │   the key of    │   amber/jaccard-store/1      │
 │   └ 127.0.0.1:8080 ◀─┼─ the operator   │ grants: who may do what      │
 │       the browser    │                 └──────────────▲───────────────┘
 └──────────────────────┘                                │ iroh, both protocols
                                                         │ from one endpoint
                                          ┌──────────────┴───────────────┐
                                          │ pod: zathras, with a new key │
                                          │   and the statement of the   │
                                          │   role key of its service    │
                                          └──────────────────────────────┘
```

It is done when:

- a key that was granted `deployer` for `web` rolls `web` out, reads its
  logs and opens a shell in it, and is refused all three for `api`, of
  which its `status`, its terminal UI and its page show nothing;
- a zathras whose role key was granted for `web` registers as `web` and
  fetches the store paths of `web`, and is refused as `api`, and a peer
  with no statement is refused as anything;
- `great-machine draal` has no `--web-addr` and binds no TCP port, and
  `great-machine web` prints a link with which the page opens and without
  which it answers nothing;
- an admin grants and revokes from `great-machine access`, from the page
  and from the terminal UI, and a key whose grant was revoked has lost
  its connections when the command returns.

## 2. Terms

- **role**: `admin`, `deployer` or `zathras`.
- **grant**: an endpoint ID, a role, and a service or every service. What
  draal decides by, and all it decides by.
- **role key**: a key that holds a `zathras` grant. It is given to the
  pods of a service, signs for them, and never connects to anything.
- **statement**: what a zathras registers with: the role key's ID and its
  signature over draal's endpoint ID and the zathras's own.
- **admin of the command line**: an endpoint ID given to draal with
  `--admin`. It is an admin that no request can make less.
- **the page**: the web page, served by `great-machine web`.
- **operator**: as before, whoever runs the commands, the terminal UI or
  the page. What an operator may do is now what its key was granted.

## 3. Grants

### 3.1 What draal keeps

A table in `draal.sqlite`, by a second migration:

| Column | |
|---|---|
| `id` | an endpoint ID |
| `role` | `admin`, `deployer` or `zathras` |
| `service` | the name of a service, or empty for every service |
| `note` | what a person wrote beside it; may be empty |
| `created_at`, `created_by` | when, and the endpoint ID of the admin who granted it |

The key of a row is the three of ID, role and service: an ID has as many
grants as it was given. `admin` is always of every service.

draal decides from a copy of the table in memory. The copy is read when
draal starts, and every change is written to the database first and
then put in the place of the copy, whole: `Authorize` reads nothing but
memory and has no error of a database to return. draal is the only
writer.

### 3.2 What a role may do

| | admin | deployer of S | zathras of S |
|---|---|---|---|
| register as a zathras of S, and report | | | by a statement (§4) |
| pull a store path of S | yes | yes | while registered for S |
| ask where the store of S is; push and list under the prefix of S | yes | yes | |
| delete from the store | yes | | |
| roll S out, roll it back, list its rollouts | yes | yes | |
| the logs, the samples and a shell of a zathras of S | yes | yes | |
| watch the fleet | all of it | its services | |
| grant, revoke and list grants; the store's page | yes | | |
| ask who it is (§7.1) | yes | yes | yes |

- A grant for every service counts for each. A deployer of every service
  is no admin: it deletes nothing and grants nothing.
- **A shell is a deployer's**, because a rollout is: whoever may run a
  program of its choosing in the containers of a service is not kept
  out of them by being refused a shell.
- A role key connects to nothing. If it did, it would be a peer with no
  grant that lets it ask anything.

### 3.3 The one place that decides

`Authorize(peer, operation, service)` stays the one function every
request passes (design §4), with operations added for what is new. Two
things are asked of it besides the grants of the peer itself:

- for a registration, whether the **signer** of the statement is a
  zathras of the service (§4.3);
- for a pull, whether the peer **is registered** as a zathras of the
  service: draal's fleet is asked.

A request that is of no service (a rollout always has one; a listing of
the whole store has none) is granted to an admin, and to a deployer of
every service where the operation is one a deployer may do at all.

### 3.4 Refusals

A refusal is `unauthorized`, logged with the peer, the operation and the
service, as before. What it says:

- to a peer that holds a grant, which role the operation takes and for
  which service: `… is no deployer of web and may not roll it out`;
- to a zathras that was refused, what is wrong with its statement and
  the command that mends it (§4.3): it is read in the log of a pod;
- nothing of what exists. A deployer of `web` that asks for the log of a
  zathras of `api` is answered `unavailable`, as for a zathras that is
  not connected: it does not learn that there is one.

## 4. A zathras and its role key

### 4.1 One key, one endpoint

A zathras makes one key when it starts, as before, and no second one:
the key of the store directory (stage 5 §3.4) goes. Whenever it dials
draal it binds one endpoint with that key, as before, and that endpoint
makes both connections: the one of great-machine's protocol, which is
the registration, and, when a pull needs it, one of the store's
protocol. The second lives no longer than the first: when the
connection to draal has ended, the endpoint is shut down and the next
dial binds another.

So a pull needs a zathras that is connected to draal. That is no new
bound: a pull is part of a `prepare`, which draal sends on that
connection. A pull that finds no connection has failed like any other,
and is tried again (stage 5 §3.4).

### 4.2 The statement

A zathras is given a role key, `--role-key FILE`, a key file like an
operator's. When it starts it signs, once:

```
great-machine zathras statement 1
<endpoint ID of draal, 64 hexadecimal characters>
<endpoint ID of the zathras, 64 hexadecimal characters>
```

three lines, each ended by a line feed, with the role key (Ed25519, as
every key of iroh is). `register` carries the endpoint ID of the role
key and the 64 bytes of the signature beside what it had.

- **The statement is worth nothing to anybody else.** It names the
  endpoint of the zathras, which the handshake proves, and the draal it
  was made for: whoever reads it, in draal's memory or anywhere, cannot
  register with it.
- **It does not expire**: it is as old as the key it names, which is
  gone with the process.
- **It names no service.** The grants of the role key say which services
  it may sign for, and the registration says which one this zathras is
  of.
- **zathras does not make the role key.** A file that is missing or is
  no key ends it at start: a new key would be one nobody granted
  anything. `great-machine id --key FILE` makes one, and prints its ID.

### 4.3 What draal does with a registration

In this order, and the first that fails is the answer:

1. The registration is well-formed, as before (design §4).
2. It has a statement. One without is refused: it is a zathras of before
   v0.4.0, or no zathras.
3. The signature is the role key's, over this draal's ID and the ID of
   the peer as the handshake proved it.
4. The role key holds a `zathras` grant for the service of the
   registration, or for every service. The refusal names the role key,
   the service, and the command: `great-machine access grant zathras ID
   --service SERVICE`.

A zathras that was refused keeps what it runs and dials again, as it
does when draal cannot be reached (design §5.1).

draal remembers, with the session of a zathras, which role key signed
for it. While the session is there, the endpoint ID of the zathras may
pull the store paths of its service, and no other's.

### 4.4 The role key in a pod

The role key of a service is the same in every pod of it: a Secret, of
which `deploy/pod.yaml` has an example, mounted as a file that root
alone reads (`defaultMode: 0400`).

It is in the container the service runs in. A service that is root reads
it, and can then sign for zathrases of its own service: it can register
as what it already is, and fetch what it already has. `--user` keeps the
service from the file. A role key that is granted for one service signs
for no other, which is why a service is given its own.

## 5. The store

Every request of the store's protocol is authorized before the store
sees it, as before (stage 4 §3.3), and now for a service: the one whose
prefix (`/services/SERVICE/nix/store/`) the reference is under.

| Request | Asked as | Of the service |
|---|---|---|
| `pull` | a pull | of the name |
| `push-start`, `push-upload` | a push | of the name |
| `push-commit` | a commit | none: it names an upload |
| `list` | a listing | of the prefix that is listed |
| `delete` | a deletion | admins alone |

- **A name or a prefix that is under no service's prefix** is of no
  service: such a request is an admin's, or a deployer's of every
  service. A prefix that is shorter than a service's own (`/services/we`)
  is under none.
- **A commit** is granted to whoever holds a deployer grant at all, or is
  an admin. The store itself answers a commit for an upload that the
  endpoint began, and no other.
- A refusal is an error of the store's protocol whose message begins
  with `unauthorized`, as before.
- **What this keeps apart is the names, and not the packs.** The store
  shares its packs between the services (stage 4): a push that names a
  root the store has is given a reference to it without an upload, the
  answer to the start of a push names the packs nearest to what is
  pushed, whoever's, and a reference that leans on a pack is pulled with
  that pack. So whoever may push for any service can read what any other
  service's references hold, and a zathras is handed what its service's
  references lean on. A role bounds what a key may do to a service, and
  keeps no closure secret from a deployer. Keeping the packs apart by
  service would take the store itself to know of services, which
  jaccard-store does not; it was found in the review of this stage, and
  is not done in it (`TestThePacksOfTheStoreAreSharedBetweenTheServices`
  holds what the store does).

## 6. Operators

- **Every request is decided when it is made**, by the grants as they are
  then.
- **`watch` answers a fleet of the services the key may see**: all of it
  for an admin and a deployer of every service, otherwise the services
  the key has a grant for. It is the same stream, with the others left
  out of every fleet. A key with no grant is refused.
- `status`, the terminal UI and the page show what `watch` answers, and
  so show nothing of a service the key was not granted.
- The commands are as they were. `rollout` still dials twice, one after
  the other: its key is in the table by itself.

## 7. Managing grants

### 7.1 Requests

Four requests are added to those of an operator (design §5.2):

| Request | Answer | For |
|---|---|---|
| `grants` | every grant, the admins of the command line among them and marked | admins |
| `grant`: ID, role, service, note | `granted` | admins |
| `revoke`: ID, role, service | `revoked` | admins |
| `me` | the endpoint ID of the peer and its grants | every peer |

- **A grant** is refused with `bad-request` unless the ID is an endpoint
  ID, the role one of the three, and the service a name of a service or
  empty; `admin` takes no service. The note is cleaned and cut at 200
  bytes, as what a zathras reports is. A grant that is there already is
  there with the new note.
- **A revocation** of a grant that is not there is `not-found`. An admin
  of the command line cannot be revoked, and **an admin cannot revoke
  its own `admin` grant**: once there is an admin, there is always one.
- Every grant and every revocation is logged by draal, with who asked.
  There is no history of them besides.

### 7.2 Commands

```
great-machine access list
great-machine access grant ROLE ID [--service NAME] [--note TEXT]
great-machine access revoke ROLE ID [--service NAME]
```

They take `--draal` and `--key` as every operator command does. Without
`--service` a grant is for every service. `list` prints a line for a
grant: the role, the service or `every`, the ID, the note, who granted
it and when.

### 7.3 A revocation ends what the grant was used for

When a grant of a key is revoked, draal closes, before it answers:

- every connection of that key, of either protocol;
- every session of a zathras that the key signed for, and the
  connections of those zathrases to the store.

They dial again, and find what is still granted. A connection that a
revocation need not have ended (the key had another grant that covered
it) is ended too: it costs a dial.

### 7.4 The first admin

`great-machine draal --admin ID` (`GREAT_MACHINE_ADMINS`; may be repeated,
and the variable takes several with commas between them) names the
admins of the command line. They are admins whatever the table says, are
listed with the grants, and no request revokes them: a table that is
empty, as at the first start, has an admin, and no mistake locks
everybody out.

The flag was `--operator` and the variable `GREAT_MACHINE_OPERATORS`;
both are still taken, and mean the same. Without an admin of the command
line and with no admin in the table, draal warns at start that nobody
can grant anything.

## 8. The page

### 8.1 draal serves none

`great-machine draal` loses `--web-addr`, its HTTP server, and the page
of the store under `/store/`. It binds its iroh endpoint and nothing
else. `deploy/draal.yaml` loses the variable and what it says of
`kubectl port-forward`.

### 8.2 `great-machine web`

An operator command:

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
| `--key FILE` | `GREAT_MACHINE_KEY` | as the other commands | the operator's key |
| `--addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where the page listens |

It serves the page that is built into the program, and the HTTP API
behind it, until it is interrupted, and prints one line when it listens:
the link of the page, with the ticket in it.

```
http://localhost:8080/?ticket=…
```

The link names `localhost` where the address is a loopback one, and the
port the page listens on.

- **It listens on loopback alone.** An `--addr` that is not a loopback
  address is refused: the page acts with the operator's key, and the
  ticket and the cookie travel in the clear. From another machine it is
  reached through a tunnel.
- **It dials draal when the page first asks for something**, with the
  operator's key, keeps the one connection, and dials again when it was
  lost. What the page asks while draal cannot be reached is answered
  `unavailable`, which the page shows as it shows a connection it lost.
  So the command starts, and the page loads, whether draal is there or
  not.
- **What the page may do is what the key may**: draal decides, as for
  every operator. The command decides nothing.

### 8.3 The ticket

The command makes two secrets when it starts, of 32 random bytes each:
the ticket, and what the cookie holds.

- A request for `/` with the ticket is answered with the cookie and a
  redirect to `/`, so that the ticket does not stay in the address.
- **Every other request needs the cookie**: without it the answer is
  `401`, and for a page, one line that says to open the link the command
  printed. That holds for the files of the page as for the API.
- The ticket is good for as long as the command runs, and the cookie
  with it: the link can be opened again, in another browser too. A
  command that is started again has a new ticket.
- The cookie is `HttpOnly`, `SameSite=Strict`, and has the port in its
  name, so that two commands on two ports do not take each other's.

Two things besides, against what else runs on the machine and in the
browser:

- **A request under another host name is refused** (`403`): the `Host`
  of a request has to be `localhost`, `127.0.0.1` or `[::1]` with the
  port the page listens on. A site that points a name of its own at the
  loopback address (design §4) is not answered.
- **A request that changes something, and every WebSocket, has to come
  from the page**: its `Origin` is the host it was asked on, or it is
  refused. A cookie is sent to every port of a host, so a page of
  another program on the same machine would otherwise act with it.

### 8.4 The API

As design §8.3 has it, and besides:

| Path | What |
|---|---|
| `GET /api/me` | the endpoint ID of the operator's key and its grants |
| `GET /api/grants` | every grant |
| `POST /api/grants` | grants: ID, role, service, note |
| `DELETE /api/grants?id=&role=&service=` | revokes |
| `/store/` and what is under it | the store's page (§8.5) |

A refusal of draal is passed on as it is, with the status that fits its
code (design §8.3).

### 8.5 The store's page

jaccard-store's admin page reads the database of the store, which is
draal's. draal answers it over iroh, to admins:

| Request | Answer |
|---|---|
| `store-page`: path | the status, the content type and the body that jaccard-store's admin page answers a `GET` of that path with |

`great-machine web` answers `/store/…` with it. The page is read-only,
as before, and an answer has to fit a message (4 MiB).

### 8.6 What the page shows

- **Who it is**: the header has the beginning of the operator's endpoint
  ID and its roles.
- **An Access view**, for admins: the grants as a table (role, service,
  ID, note, who granted it and when; those of the command line marked),
  a form that grants, and for every grant that can be revoked a button
  that does, after it was confirmed. A key that is no admin has no such
  view.
- **A key with no grant** is told so, with its ID, which is what an
  admin needs to grant it one.
- **A cookie that is no longer good** (the command was started again) is
  said: the page tells to open the link the command printed.

The made-up fleet of `web/demo` has made-up grants, and needs no ticket.

## 9. The terminal UI

From the view of the services, `a` opens **Access**: the grants, a line
for each as `great-machine access list` prints them.

- `g` grants: a form of four fields, the role (one of three, changed
  with the arrow keys), the ID, the service (empty for every service)
  and the note. `Enter` on the last field asks draal; `Esc` gives it up.
  What draal refuses is said in the form, which stays as it was filled.
- `x` revokes the grant under the cursor, after `y` to a line that names
  it. An admin of the command line has no `x`.
- A key that is no admin is told that the view is an admin's, with its
  own ID.
- What is typed into the form and what draal sends back is drawn through
  the function that takes control characters out, as everything is
  (design §10).

The header says who the key is, as the page's does.

## 10. The protocol

To design §5:

| | New |
|---|---|
| `register` | `statement`: the endpoint ID of the role key, and the signature |
| operator requests | `grants`, `grant`, `revoke`, `me`, `store-page` (§7.1, §8.5) |
| answers | `grants`, `granted`, `revoked`, `me`, `store-page` |

Nothing is taken away. A reader of before ignores what it does not know,
so a zathras of v0.4.0 registers with a draal of v0.3.0 (§11).

## 11. Upgrading from v0.3.0

This is a cut: a draal of v0.4.0 refuses every zathras that has no
statement, and every key that has no grant. In this order nothing stops:

1. **A role key for every service**: `great-machine id --key FILE` makes
   it and prints its ID; the file becomes a Secret.
2. **The pods**, to v0.4.0, with the Secret and `GREAT_MACHINE_ROLE_KEY`.
   A zathras of v0.4.0 registers with a draal of v0.3.0, which ignores
   the statement, and fetches from it.
3. **draal**, to v0.4.0. `GREAT_MACHINE_OPERATORS` is still read: who was
   an operator is an admin. `GREAT_MACHINE_WEB_ADDR` is read no longer.
4. **The grants**: `great-machine access grant zathras ID --service
   SERVICE` for every role key, and `deployer` for whoever is to deploy
   and is no admin.

Between 3 and 4 every zathras is refused. It keeps its service running
and dials again every 30 seconds at the most; the fleet is empty until
the grants are there, and nothing can be rolled out.

The page is no longer reached with `kubectl port-forward`: it is
`great-machine web` on the laptop.

## 12. Packages

To the table of design §11:

| Package | Purpose | Depends on |
|---|---|---|
| `authz` | the roles, the grants in memory, `Authorize` | `wire` |
| `web` | the HTTP API and the embedded page, in front of an operator's client; the ticket | `wire` |

Changed:

- `wire`: the statement and its text, `Role`, `Grant`, the new requests
  and answers, the service of a name of the store.
- `db`: the migration and the queries of the grants.
- `node`: a connection that was dialed dials its peer again from its own
  endpoint, for another protocol.
- `draal`: a registration with its statement; what a peer may ask, by its
  grants; the fleet a peer may see; the four requests of §7.1 and
  `store-page`; a revocation and the connections it ends. It no longer
  has a `Controller` for an HTTP API in its own process.
- `storeserver`: a request is asked for its service (§5); the connections
  of a peer are closed when asked; the admin page answers through draal.
- `storedir`: it is given what dials the store, and makes no key.
- `zathras`: the statement in its registration.
- `operator`: `Grants`, `Grant`, `Revoke`, `Me`, `StorePage`.
- `cli`: `access`, `web`; `--admin`; `--role-key`; draal without a page.
- `tui`, `ui`: the Access view, and who the key is.
- `demo`: made-up grants.

## 13. Errors

To design §13:

- **A key that was not granted what it asks**: `unauthorized`, with the
  role it takes (§3.4). The command prints it and ends with 1.
- **A zathras without a statement, with one that is not its own, or with
  a role key that was not granted the service**: refused (§4.3). It
  keeps its service running and dials again; draal's log and the pod's
  say why.
- **A role key file that is missing**: zathras ends at start.
- **A grant is revoked while it is used**: the connection ends (§7.3). A
  rollout that was being followed goes on in draal; the command that
  followed it says that the connection was lost.
- **`great-machine web` cannot listen** (the port is taken): it ends with
  the error. **draal cannot be reached**: the page loads and says so.
- **The link is opened without its ticket, or after the command was
  started again**: `401`, and what to do.

## 14. Tests

- **`authz`**: every row and column of §3.2, for one service and for
  every service; a key with several grants; a registration by its
  signer; a pull by who is registered; a change that is seen by the next
  question and by none before it.
- **`db`**: the migration on a database of v0.3.0; grants put, put again
  with another note, removed, and read back after the database was
  closed.
- **`wire`**: the text of the statement, signed and verified; one that
  was signed for another draal or another endpoint is not the peer's;
  the service of a name and of a prefix of the store, with those that
  are under none; a message with a field the reader does not know.
- **`node`**: a connection that was dialed dials again for another
  protocol, over real endpoints; both carry streams; the second ends
  with the first.
- **`draal`**: a registration with no statement, with one for another
  endpoint, with one of a role key without the grant, and with a good
  one; every request of an operator for a key with each role, for its
  own service and for another; the fleet as a deployer of one service
  sees it; a zathras of another service is `unavailable` to it; the four
  requests of §7.1 with what they refuse; a revocation that ends the
  connection of the key and the session of a zathras it signed for.
- **`storeserver`**: §5, request by request, for an admin, a deployer of
  the service, a deployer of another, a zathras that is registered for
  the service and one that is registered for another.
- **`storedir`**: its tests as they are, over a dial that is given.
- **`cli`**: `access` with its three sub-commands, against a draal of
  the test; `--admin` and its old name; a zathras without `--role-key`
  and with a file that is not there; `web` refuses an address that is
  not loopback, prints its link, and answers `401` without the ticket.
- **`web`**: the ticket and the cookie, the `Host` and the `Origin`
  (§8.3), each refused and each let through; the paths of §8.4 against a
  fake client; a draal that cannot be reached is `unavailable`.
- **`tui`**: the Access view as a function of the messages it gets, as
  the other views are tested: the list, the form, a refusal, a
  revocation that is confirmed and one that is not; a key that is no
  admin; what it draws at every size.
- **`ui`**: the type check and the unit tests of what the Access view
  works out itself.
- **`e2e`**: draal with grants. A deployer of `web` rolls `web` out and
  is refused `api`; a zathras of each of two services, each with its
  role key, fetches its own closure and is refused the other's; a grant
  that is revoked ends the watch of its key; the page, served by the
  code of `great-machine web` over an operator's client, shows a
  deployer its services and an admin the grants.
- **`scripts/test-page.sh`** asks `great-machine web` for the page, with
  the ticket of the link it prints.

## 15. Settled first

1. **One endpoint, two protocols.** Settled while this was written, with
   go-iroh v0.3.0 in one process over loopback: an endpoint that dialed
   holds a connection of great-machine's protocol and one of another to
   the same peer at a time, each carries streams, either is closed
   without the other noticing, and another is dialed afterwards. Over a
   relay it was not tried.
2. **A reader of before ignores a field it does not know.** Read in
   `wire`: the decoder returns no error for one. The plan runs it.
3. **jaccard-store's client over a connection great-machine dialed.**
   Read: the client takes whatever opens a stream, and says a connection
   of go-iroh does. The plan begins with it: a pull over a connection
   that §4.1's endpoint made.

## 16. Not in this stage

- A key that signs for the keys of the jobs of a CI, so that two jobs
  with one deployer's key do not evict each other.
- Grants that expire, and a history of grants.
- The page on an address that is not loopback, and TLS for it.
- Rolling out and rolling back from the page or the terminal UI.
- Tokens of clamp (design §4): the table is what decides.
- What stage 5 left: the release of v0.3.0 and its run on a cluster.

## 17. What else changes

The design document and the README are brought up to this at the end of
the stage: design §3 (the commands and the flags), §4 (rewritten: grants
in the place of the allowlist, the page's paragraphs gone), §5 (the
requests), §7.8, §8 (draal has no HTTP), §9, §10, §11, §13, §14 and §17;
the README's getting started, its tables of flags, "The web page", the
security notes, and the notes for the release.
