# great-machine, stage 2: the web page — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** draal serves a web page that shows every service and every connected zathras with its state, its figures over the last 15 minutes and the last 400 lines of its log, and gives a shell in its container.

**Architecture:** A package `web` puts an HTTP API in front of `draal.Controller`, the interface the iroh protocol already answers from: JSON for what is asked once, WebSockets for the fleet, the logs and the shell. The page is a single-page application in SolidJS, built by Vite into `web/dist` and embedded in the program with `go:embed`. It has no authentication: it listens on loopback unless told otherwise, and answers under any host name. Before any of that, one task mends stage 1's streams, so that a shell that is closed from a browser tab is gone from the container.

**Tech Stack:** Go 1.26 with `github.com/coder/websocket`; SolidJS, TypeScript and Vite, with vitest for unit tests; xterm.js for the shell; Nix with gonixgo for the program and `buildNpmPackage` for the page.

**Spec:** `docs/superpowers/specs/2026-10-09-great-machine-design.md`, sections 3 (`--web-addr`), 4 (the page has no authentication), 7.6, 8.2, 8.3, 9, 12 and 14. The spec was brought in line with what this plan settled: that the page answers under any host name (section 4), the end of a shell session (7.6), what the HTTP API answers (8.3), the page's tests (14) and gonixgo's embedding (15). Stage 1 is merged; its plan and the ledger of its execution are beside this file. The terminal UI (stage 3) gets a plan of its own.

## Global Constraints

- Everything stage 1's plan constrains still holds: one module and one program, no `internal/` directory, no cgo, `log/slog`, the standard library's `testing` alone for Go tests, every waiting time a field of a `Config`, nothing built left behind, work on a branch.
- The HTTP API can roll nothing out and nothing back. It calls `Store`, `Rollout` and `Rollback` of the controller nowhere.
- The page has no authentication. `--web-addr` (`GREAT_MACHINE_WEB_ADDR`) is `127.0.0.1:8080` unless given, and empty means no web page. **The server answers under any host name, on every address**: it does not look at the `Host` of a request to decide whether to answer. A WebSocket is refused when its `Origin` is not the host it was asked on. That keeps a page of another site from opening a socket; it does not keep out a site that points a name of its own at draal's address (DNS rebinding). The person who ordered the project decided so: whoever can reach the port is trusted, and so is what they browse.
- **The page asks nothing of any other host.** Fonts, scripts, styles and images are in the build. It works under the content security policy the server sends, which allows no other origin and no inline script.
- The page is SolidJS with TypeScript in strict mode, built by Vite. It follows the Fables for Robots design system.
- The sources of the page are in `ui/`, which a `go.mod` of its own keeps out of the Go module. Its build goes to `web/dist`, which `web` embeds. **The Go module builds and its tests pass without that build**; draal then serves one line that says the page was not built.
- Nothing that is generated is committed but what sqlc makes, the lock file of the page's dependencies, and the hash of those dependencies in the flake.
- Every value of a zathras or of a rollout that the page shows is text to it: it is never put into the page as markup.

## Review Focus

What the spec implies and a person will meet, and the task whose tests pin it:

1. **The page reached under another name**, through a tunnel, a forward or a proxy that keeps the outside host name. It works as under `localhost`, its sockets included, and a page of another origin still gets no socket. Tasks 1 and 2.
2. **A zathras that goes while its log, its shell or its charts are open.** The page says that it is gone and stops asking; the socket ends with a reason and not in silence. Tasks 2 and 6 for the sockets, 9 and 10 for the page.
3. **draal restarted while the page is open.** The page says that the connection is lost, tries again with pauses that grow, and is back on the view it was on. Task 7.
4. **Nothing to show:** no service yet, a service without a zathras, a zathras without samples (no cgroup v2), a service without a rollout. Every view says what is missing; none is blank and none shows `NaN` or `undefined`. Tasks 8 and 9.
5. **A flood:** a service that logs thousands of lines a second, a shell that prints megabytes, a browser that reads slowly. The page stays usable and keeps a bounded number of lines; a slow browser holds up neither draal nor another browser; and a shell whose program takes no input, with far more typed than fits, is gone from the container when its tab is closed. Tasks 0, 2 and 10.

---

## File Structure

```
node/, zathras/,     Task 0 alone: a stream that is closed ends a Write that
draal/               waits, and a shell session ends on both sides
web/                 the HTTP API and the embedded page
web/dist/            the build of the page (ignored by git but for a placeholder)
web/demo/            a program for developing the page: a made-up fleet behind the real API
ui/                  the page: package.json, vite.config.ts, tsconfig.json, index.html, go.mod (a stub)
ui/src/api/          what the page asks draal: types, the fleet socket, logs, shell, history
ui/src/state/        the fleet as the page holds it
ui/src/views/        services, a service, a zathras
ui/src/components/   what the views are made of
ui/src/styles/       the design system's tokens and the page's styles
cli/draal.go         --web-addr
cli/web.go           the server beside the iroh endpoint, and its end
scripts/test-page.sh asks a program, or an image, for the page
e2e/                 tests of the whole through HTTP
flake.nix            the page's derivation, the source gonixgo builds, Node in the shells
```

## Interfaces

This section is the contract between the tasks. A task implements what is
written here for its part, exactly, and uses the other parts only through
what is written here.

### node and draal: a stream that is closed

Task 0 changes two promises of stage 1's code, on which the shell of the
page stands. Before it, a shell whose program took no input could not be
ended by the side that wanted to end it once more had been typed than the
stream holds: the `Write` that waited stayed, and so did the process in
the container.

```go
package node

type Stream interface {
	io.Reader
	io.Writer
	CloseWrite() error
	// Close ends the stream in both directions, and returns at once.
	//
	// When no Write of this side is under way, the end is orderly: what
	// this side wrote before is still delivered, and the peer reads
	// io.EOF after it.
	//
	// When a Write of this side has not returned, because the peer does
	// not read, the stream is broken off instead: that Write fails, what
	// this side wrote and the peer has not read may be lost, and the peer
	// reads an error and not io.EOF. A side that wants its last words
	// read lets its Write return before it calls Close.
	//
	// Either way a Write of the peer fails, at once or when it next
	// waits, a Read of this side fails, every later Write of this side
	// fails, and Done is closed on both sides. Close may be called after
	// CloseWrite and more than once.
	Close() error
	// Done is closed when this side can send no more on the stream: the
	// peer has closed it with Close, this side has ended what it sends
	// with CloseWrite or Close, or the connection has ended. It is how a
	// side that neither reads nor writes at the moment learns that the
	// peer has closed the stream: nothing else tells it.
	Done() <-chan struct{}
}
```

`draal.Shell.Close` ends the session, and the shell if it runs. It ends a
`Read` and a `Write` that wait, each with an error, and returns at once.
It may be called from another goroutine, and more than once.

A shell session ends on both sides when either side closes its stream,
whatever waits: the shell and the program in the foreground of its
terminal are gone, and neither draal nor the zathras keeps a goroutine of
it.

### web

```go
package web

// Config configures the handler.
type Config struct {
	// Controller is what the API answers from: a *draal.Draal.
	Controller draal.Controller
	// Dist is the build of the page. nil: the one that is embedded.
	Dist fs.FS
	// Each of these has a default when it is zero:
	PingEvery  time.Duration // how often an idle socket is pinged; 30 seconds
	WriteWait  time.Duration // how long one message to a browser may take; 10 seconds
	// MaxSockets is how many WebSockets are open at a time; 256.
	MaxSockets int
	Log        *slog.Logger
}

// New returns the handler of the API and of the page.
func New(cfg Config) http.Handler

// Built says whether the page is embedded in this program: whether
// index.html is among the embedded files. The placeholder that git
// tracks in web/dist is always among them and does not count.
func Built() bool

// IsLoopback says whether addr, as --web-addr has it (host:port), names
// a loopback address: an IP that is one, or localhost. An empty host
// (":8080") is every address and is not loopback. The command warns at
// its start about an address that is none; the handler does not ask.
func IsLoopback(addr string) bool
```

### The HTTP API

Every answer of the API that is no WebSocket is JSON. A refusal is

```json
{"error": {"code": "unavailable", "message": "…"}}
```

with the codes of `wire` and the status 400 for `bad-request`, 403 for
`unauthorized`, 404 for `not-found`, 503 for `unavailable`, 500 for
`internal`. The message of an `internal` refusal says nothing of what
went wrong, whoever made the refusal, draal or a zathras: that is in
draal's log. A refusal with a code that is none of these five is answered
as an internal one. A zathras is named by its endpoint
ID, 64 hexadecimal characters in lower case; anything else is 400. A
method that is not `GET` is 405, as a refusal with the code
`bad-request` and the header `Allow: GET`.

| Request | Answer |
|---|---|
| `GET /api/services/{service}/rollouts` | `{"rollouts": [RolloutInfo, …]}`, newest first, at most 20 |
| `GET /api/zathras/{id}/history` | `{"samples": [Sample, …]}`, oldest first |
| `GET /api/watch` (WebSocket) | text messages, each a `Fleet`; the first at once |
| `GET /api/zathras/{id}/logs?follow=1` (WebSocket) | text messages, each a `LogLine`; without `follow` the socket is closed after the ring |
| `GET /api/zathras/{id}/shell?cols=&rows=&term=` (WebSocket) | see below |
| any other path under `/api/` | 404 as a refusal |
| a path under `/assets/` that is no file | 404, a plain line |
| any other `GET` | the file of the page of that path, or `index.html` |

`rollouts` and `samples` are lists also when there is nothing in them,
never `null`.

`Fleet`, `Service`, `Zathras`, `State`, `Sample`, `Exit`, `RolloutInfo` and
`LogLine` are the types of `wire/messages.go`, encoded by `encoding/json`
with the names of their `json` tags: a time is RFC 3339 with nanoseconds,
and the zero time is `"0001-01-01T00:00:00Z"`; a duration is a number of
nanoseconds; an ID is a string.

**WebSockets.**

- The server refuses the upgrade with 403, as a refusal with the code
  `unauthorized`, when the request has an `Origin` whose host is not the
  request's `Host`. A request without an `Origin` is no browser's and is
  accepted.
- What is wrong with a request itself is refused before the upgrade with
  400: an ID that is none, a `follow` that is not `1`, `true`, `0`, `false` or empty,
  `cols` or `rows` outside 1 to 1000, a `term` that is not at most 64
  characters of `A-Za-z0-9._+-`, and a socket's path asked for without an
  upgrade. A page cannot read such an answer; it sees the socket fail.
- On every socket, a text message `{"error": {"code": …, "message": …}}` is
  the last one: the server closes after it. A request that draal refuses
  (a zathras that is not connected) is answered that way, after the
  upgrade, since a page cannot read the body of an upgrade that failed.
- `watch`: the server sends; what the page sends is ignored.
- `logs`: the server sends; a `LogLine` with `lost` set says that so many
  lines were lost. Without `follow` the server closes normally after the
  last line of the ring.
- `shell`: `cols`, `rows` and `term` are 80, 24 and `xterm-256color`
  when absent. The page sends binary messages (what is typed) and text
  messages `{"resize": {"cols": 120, "rows": 40}}`; a text message that is
  no such resize, or one outside 1 to 1000, is dropped. The server sends
  binary messages (what the shell prints) and, when the shell has ended,
  the text message `{"exit": 3}` and closes normally. A socket that the
  page closes ends the session and the shell. A message of the page may
  have 64 KiB; the page sends what is typed in messages of at most
  16 KiB. What is typed is held for the shell, up to 64 KiB; when more
  comes, the server reads no further until the shell has taken some, so
  that a paste waits in the browser. When the shell takes nothing for
  `WriteWait`, the socket is closed with 1009 and a reason that says so.
- Close codes: 1000 after an error message, after a log without `follow`
  and after `{"exit": N}`; 1001 when the server ends; 1009 for a message
  of the page that is too large and for a shell that does not take its
  input. A close with another code than 1000 has a reason that is a
  sentence for a person. A page that was dropped for being slow has its
  connection cut and sees 1006.
- The server pings an idle socket every `PingEvery`, and drops one that
  does not take a message within `WriteWait`. A browser that reads slowly
  loses its socket; it holds up nothing else.
- At most `MaxSockets` sockets are open at a time. One more is refused
  before the upgrade with 503 `unavailable`.
- `logs` cannot tell a log that ended from one that broke off: the
  controller's channel closes for both. A log without `follow` that broke
  off half way ends as a whole one does.
- The handler of every socket returns when the context of its request is
  done, within a second or two also when the page does not answer: it
  does not wait for a page to close its side. `http.Server` ends no connection that was taken over for a
  WebSocket, neither by `Shutdown` nor by `Close`: the command ends the
  sockets by ending that context.

**The page's files.** `index.html` is sent with `Cache-Control: no-cache`;
files under `/assets/`, whose names carry a hash, with a long `max-age` and
`immutable`. Every answer carries `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` (the paths of the
page have endpoint IDs in them) and the `Content-Security-Policy`

```
default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'
```

It allows the page's own origin alone; `connect-src 'self'` covers the
WebSockets to it. `'unsafe-inline'` is there for styles alone, because
xterm.js makes its styles from script. Nothing may come from a `data:`
or a `blob:` URI: the build of the page inlines no file. When the page is
not built, every path outside `/api/` is answered with status 501 and one
line of plain text that says so and how it is built.

**The host name** is not looked at. The page and the API are answered
under whatever `Host` a request has, on every address, so that a tunnel,
a forward or a proxy may reach draal under any name. What that leaves
open is in the Global Constraints, and is the person's decision.

### web/demo

```
go run ./web/demo [--addr 127.0.0.1:8080] [--dist DIR] [--empty]
```

serves the real handler of `web` over a made-up `draal.Controller`: four
services, among them one in the middle of a rollout that it moves along
(`web`), one that is halted with a reason (`worker`), one that has no
zathras (`empty`) and one whose zathrases registered and to which nothing
was rolled out yet (`fresh`); zathrases in every phase, some without
samples, one that goes and comes back; samples that move; logs that grow,
one of them a flood; a shell that is a real shell on this machine.
`--dist` serves a build of the page from a directory instead of the
embedded one. `--empty` serves a fleet without any service, as a draal
that was started for the first time. It is for developing the page and is
not part of the program.

### cli

`great-machine draal` gets

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where the web page listens; empty for no web page |

- The server starts after the iroh endpoint is bound and before `Serve`,
  and logs `web page` with `addr=` (what was bound) and `url=` (the host
  of the flag with the port that was bound; `localhost` when the flag
  names every address). An address that cannot be bound ends the command
  with status 1; one that is not `HOST:PORT` is a mistake of usage, with
  status 2.
- When the page is not built into the program, a warning at start says
  so.
- An address that is not loopback is allowed and warned about at start:
  the page has no authentication, and whoever reaches it has a shell in
  every container.
- The HTTP server has a time for the header of a request and one for a
  connection that is idle, and none that would cut a WebSocket.
- On shutdown the HTTP server is stopped first: the context of its
  requests is ended, which ends the sockets, and the command waits for
  the handlers, since they call into draal. Then draal ends as before.

### ui

- `ui/package.json` has the scripts `dev` (Vite's server, which passes
  `/api`, WebSockets included, on to `http://127.0.0.1:8080`), `check`
  (`tsc --noEmit`), `test` (`vitest run`: the unit tests,
  `ui/src/**/*.test.ts`, which run in Node without a document) and
  `build` (into `../web/dist`, or into the directory
  `GREAT_MACHINE_UI_DIST` names; it removes what an older build left and
  leaves the placeholder that git tracks there).
- Routes: `/` the services; `/s/:service` a service; `/s/:service/z/:id`
  a zathras, by its endpoint ID.
- `ui/src/api/types.ts` has the types of the API as TypeScript, one for
  each type above, with the names of the JSON.
- The page holds one WebSocket to `/api/watch` and draws from what it
  receives. The view of a zathras holds one more for its log and, while
  a shell is open, one for the shell; both are closed when the view is
  left. A shell is opened by a button and not by coming to the view: it
  is a process in the container.
- The page keeps the last 400 lines of a log. What is typed into a shell
  goes out in binary messages of at most 16 KiB.
- Where a zathras stands in a rollout is read from its own report
  against the generation of the rollout (not prepared, preparing,
  prepared, on the generation, ended on it); the fleet has no record of
  it. The counts of draal's last decision are shown beside.
- Everything the page works out itself is in files without JSX and has
  unit tests; what is drawn is checked by the type check, the build, and
  by looking at it against `web/demo`.

### flake

- `packages.<system>.ui`: the build of the page, by `buildNpmPackage`,
  with the hash of its dependencies (`npmDepsHash`) in the flake. After a
  change of `ui/package-lock.json`,
  `nix run nixpkgs#prefetch-npm-deps ui/package-lock.json` prints the new
  one.
- `packages.<system>.default`: the program, built by gonixgo from a
  derivation that is the repository with that build put at `web/dist`.
  Both are built while Nix evaluates, as gonixgo's own tool is.
- `devShells.*`: Node 24 beside Go and sqlc.

---

## Tasks

Each task is a file of its own in [`2026-10-09-great-machine-stage-2/`](2026-10-09-great-machine-stage-2/), with its complete
code, its tests and its commands. Whoever carries a task out reads this
file down to here, and the task's file. The tasks are done in the order
of their numbers; "needs" names the tasks a task stands on.

| # | Task | Delivers | Needs |
|---|---|---|---|
| 0 | [A stream that is closed](2026-10-09-great-machine-stage-2/task-00-stream-close.md) | in stage 1's code: `Close` of a stream ends a `Write` that waits, so that a shell session ends on both sides and leaves no process in the container | |
| 1 | [web: the handler](2026-10-09-great-machine-stage-2/task-01-web-handler.md) | the embedded page and its files, the headers, rollouts and history as JSON, refusals | |
| 2 | [web: the sockets](2026-10-09-great-machine-stage-2/task-02-web-sockets.md) | the fleet, a log and a shell over WebSockets; the origin rule; slow pages, the limit of sockets, input that waits for the shell | 0, 1 |
| 3 | [web/demo](2026-10-09-great-machine-stage-2/task-03-web-demo.md) | a made-up fleet behind the real API, for developing the page | 1, 2 |
| 4 | [The skeleton of the page and its build](2026-10-09-great-machine-stage-2/task-04-page-build.md) | `ui/`, the page's derivation, the source gonixgo builds, Node in the shells, CI | 1 |
| 5 | [cli](2026-10-09-great-machine-stage-2/task-05-cli.md) | `--web-addr`, the server beside the iroh endpoint and its end, `scripts/test-page.sh` | 1, 2, 4 |
| 6 | [e2e through HTTP](2026-10-09-great-machine-stage-2/task-06-e2e-http.md) | the fleet, a rollout, a log and a shell through HTTP against a real draal and real zathrases | 0, 1, 2 |
| 7 | [The page: its foundation](2026-10-09-great-machine-stage-2/task-07-page-foundation.md) | the design system's tokens and fonts, the frame, the routes, the types of the API, the fleet and its reconnecting, what is shown of numbers and times | 3, 4 |
| 8 | [The page: the services and a service](2026-10-09-great-machine-stage-2/task-08-page-services.md) | the first two views, with a rollout under way and the last rollouts | 7 |
| 9 | [The page: a zathras](2026-10-09-great-machine-stage-2/task-09-page-zathras.md) | its state in full and the four charts | 8 |
| 10 | [The page: the log and the shell](2026-10-09-great-machine-stage-2/task-10-page-log-shell.md) | the last 400 lines, followed; a shell in xterm.js | 9 |
| 11 | [Delivery](2026-10-09-great-machine-stage-2/task-11-delivery.md) | the image with the page, `deploy/draal.yaml`, the README | 5, 10 |

Three files that a task needs and that cannot be typed are in
[`2026-10-09-great-machine-stage-2/assets/`](2026-10-09-great-machine-stage-2/assets/): the lock file of the page's
dependencies as Task 4 and as Task 7 leave it, and the mark of the design
system. The tasks copy them from there.

## Decisions taken while this plan was written

The spec as it was approved did not have these. Each is in the spec now.
The person who ordered the project read them on 2026-10-09 before the
plan was carried out, made the first one differently from what the plan
proposed, and accepted the others.

| Decision | Why | What it costs |
|---|---|---|
| **The page answers under any host name, on every address.** The spec had: on loopback, only under a loopback address or `localhost`. | The person's decision, 2026-10-09, when this plan was read. A review had shown that the spec's rule left a draal on `0.0.0.0` open to any web site in the operator's browser (the site points a name of its own at draal's address, DNS rebinding, and its page is then of draal's origin), and the plan first answered with a stricter rule and a flag `--web-host`. The person chose no rule at all: a tunnel or a proxy under any name works with nothing to configure. | DNS rebinding reaches the page, on loopback too: a web site that is open in the browser of someone who can reach the port can read the fleet and open a shell. The README says so. |
| **Task 0 changes stage 1's streams**: `node.Stream.Close` breaks the stream off when a `Write` of its own side waits, and `node.Stream` has a new method `Done`. | A shell whose program took no input, with more typed than the stream holds, could not be ended: the process stayed in the container. A browser tab makes that easy to do. `Close` alone did not mend it: a side that neither reads nor writes was never told that the other had closed. | The interface of `node.Stream` grows by one method. A side that closes in the middle of its own `Write` may lose what the peer had not read; every request and answer of the protocol closes after its writes, and is as before. |
| **What is typed into a shell waits for the shell**: draal holds 64 KiB of it and reads no more from the page until the shell has taken some; after 10 seconds without any taken it ends the session. | The page's reader must never wait in a write to the shell (it would not see the page go), and a paste of megabytes into a program that reads must still arrive whole. | A program that takes no input for 10 seconds while more than 64 KiB wait loses its session, with a reason that says so. |
| **Limits of a server that may be reached by many**: 256 WebSockets at a time, 2 minutes for an idle connection, 2 minutes for an answer that is no WebSocket. | The page may be put on an address that is not loopback. | A 257th socket is refused. |
| **A path under `/assets/` that is no file is not found**, where the spec gave `index.html` for every path that is no file. | A page that is open since before a new build asks for scripts with old names, and a page of HTML in place of a script is an error that explains nothing. | None. |
| **The page shows where a zathras stands in a rollout by its own report**, beside the counts of draal's last decision. | The fleet has no record for each zathras of a rollout, and the spec's view asked for "every zathras' part". | The page cannot say which zathras draal counts as activated, only how many. |
| **Unit tests for the page, with vitest**, and three packages of fonts. No chart library: the charts are SVG of the page's own. | The spec had "type check and build" alone; the page works out more than was foreseen (pauses, rates across restarts of counters, the buffer of a log, the colours of a line). The fonts are the design system's, as files of the build, since the page may ask no other host. | Four dependencies more, all exact. |
| **`deploy/draal.yaml` and `deploy/pod.yaml` still name the image `v0.1.0`**, whose draal has no page. | Which tag the manifests name is decided with a release. | Whoever applies the manifests before the next release gets a draal without the page. |

## What was run while this plan was written

The code of the tasks is not a sketch. It was written and run in clones
of the repository, by task, and then put together into one tree that is
what the twelve tasks leave:

- `gofmt -l .` and `go vet ./...` are clean, and `go test ./...` passes
  in all 25 packages that have tests, on macOS (arm64) with Go 1.26.7; `node`, `zathras`,
  `draal`, `e2e`, `web` and `cli` also with `-race`, and `web` twenty
  times in a row with `-race` while the whole suite ran beside it.
- The page: `npm ci`, `npm run check`, `npm test` (107 tests in 10
  files) and `npm run build`, with Node 24.21.0.
- `nix build` on aarch64-darwin, with the page built by its own
  derivation and embedded by gonixgo; `scripts/test-page.sh` asked the
  program that came out for the page and got it.
- The code that was tested is, file for file, the text of the tasks. Where
  a task shows a file as it is before a later task changes it, that state
  was run too: Task 0 alone on top of `main`, Task 1 without Task 2, the
  page at each of its thirteen commits.
- Task 0's tests fail on the code before it for the reason they name,
  over go-iroh v0.3.0 on loopback and over `nodetest`, and pass after it;
  the new ones also ran on Linux (arm64, in a container).
- The Go side was then read by a reviewer who had not written it and who
  wrote tests that failed: the text of internal errors in the browser, a
  shell through DNS rebinding (which the person then decided to leave
  open: see the decisions above), a shell
  handler that never returned, a shutdown that waited for pages that do
  not answer, and smaller ones. All but the rebinding are mended in the
  tasks as they are here, and the reviewer's tests are among theirs in
  the form that holds the rule.
- The page was looked at in Chrome against `web/demo`, served by the Go
  handler under its content security policy: every view, light and dark,
  1440 and 390 pixels wide, with no error and no violation in the
  console. Seen there: the demo stopped and started under an open view
  (Review Focus 3); a zathras that goes and comes back under its open log
  and charts (2); every empty state (4); a log of a thousand lines a
  second and a shell that printed 88 MB, without a frame longer than
  50 ms (5).
- An image for arm64 was built by Nix in a container and its draal asked
  for the page, at the point where the page was its skeleton.

What was not run, and is seen for the first time when the tasks are
carried out:

- The tasks typed step by step from their files. Their code and their
  expected failures come from runs; the prose between was read.
- The page against a real draal in a browser, and through Vite's dev
  server. The API it uses is tested against a real draal in `e2e`.
- Firefox and Safari.
- The image with the whole page, the image for amd64, and both GitHub
  workflows with their new steps.
- `kubectl port-forward` to a pod, and a proxy in front of draal.
- The tests of `web` and `cli` on Linux, and anything over a relay.

One test of stage 1, `TestRolloutOfABinaryThatExitsHaltsAndIsRolledBack`,
failed once while two whole suites ran at the same time on one machine
(a second zathras had been moved to the bad generation), on the tree of
Task 0 alone. Twelve runs of it on `main` and twelve on that tree, under
load, then passed. It was not explained; it depends on a program that
exits after 10 ms being seen to exit within 3 seconds.

