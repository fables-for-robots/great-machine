# great-machine, stage 3: the terminal UI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `great-machine tui` shows in a terminal what the web page shows: every service and every connected zathras with its state, its figures and its log, followed live, and gives a shell in its container.

**Architecture:** A package `tui` is a Bubble Tea program over a small interface, `Operator`, which an `*operator.Client` satisfies: the terminal UI talks to draal over iroh as the other operator commands do, and needs nothing of the web page. Its model is a pure function of the messages it gets, so that it is tested without a terminal; the program around it dials draal, dials again when the connection is lost, and gives the terminal to a shell session with `term.Attach`. Before any of that, two things of stage 1 are mended that a program which lives on needs: a session's read of the terminal can be ended, and a client that is closed ends what it started.

**Tech Stack:** Go 1.26; Bubble Tea v2 (`charm.land/bubbletea/v2` v2.0.10) and Lip Gloss v2 (`charm.land/lipgloss/v2` v2.0.6), with `github.com/charmbracelet/x/ansi` v0.11.8 for the width of text as the renderer counts it, and no Bubbles; `term` and `operator` of stage 1; a pseudo-terminal (`creack/pty`, which the module has) for the tests of the whole.

**Spec:** `docs/superpowers/specs/2026-10-09-great-machine-design.md`, sections 3 (the commands), 4 (operators), 5.2, 7.4 to 7.6, 8.2, 10 and 14. Stages 1 and 2 are merged; their plans and the ledgers of their execution are beside this file.

## Global Constraints

- Everything the plans of stages 1 and 2 constrain still holds: one module and one program, no `internal/` directory, no cgo, `log/slog`, the standard library's `testing` alone for Go tests, every waiting time a field of a `Config`, nothing built left behind, work on a branch. One exception to the waiting times: the bounds of the terminal UI's own end (5 seconds until a program that does not end is cut off, 1 second more, 5 seconds for an `Operator` that does not return, 2 seconds for a shell that does not end with it) are variables of the package `tui` that only its tests change. They are no setting: a caller that could set one could set the cut before the shell's grace, and the order of the two is what keeps the terminal whole.
- The terminal UI can roll nothing out and nothing back. It calls `Store`, `Rollout` and `Rollback` of the client nowhere.
- It talks to draal as an operator: `--draal` and `--key`, as `status`, `logs` and `shell` do, and draal lets it in by its allowlist. It opens no port and reads nothing of the web page.
- **The terminal is left as it was found**, on every way out: the user quits, the context ends (SIGINT, SIGTERM, SIGHUP), the terminal UI panics, a shell ends or is detached. No raw mode, no alternate screen, no hidden cursor is left behind.
- **Nothing that draal or a zathras sent reaches the terminal as it came**, but through a shell that the user opened. Every name, state, reason, refusal and log line is drawn through one function that takes control characters and escape sequences out. A log line keeps its colours by being read and drawn again in the terminal UI's own colours.
- The colours are the terminal's own sixteen, so that the terminal UI is light or dark as the terminal is; it has no theme of its own and no switch. With `NO_COLOR` set it draws without colour.
- It works in a terminal of 80 columns and 24 rows, uses a larger one, draws down to 50 columns and 12 rows, and says so in one line when the terminal is smaller than that. No size, down to one column and one row, makes it fail.
- The keyboard alone drives it. It does not ask for the mouse.

## Review Focus

What the spec implies and a person will meet, and the task whose tests pin it:

1. **The terminal after the terminal UI**, on every way out, and after a shell that was opened from it: the next key typed goes to whoever reads the terminal next, and the terminal is in its usual mode. Pinned in Task 1 (the session's read), Task 7 (the terminal UI's own ways out: quit, a context that is done, a program that is stuck, a panic), Task 8 (every way out of a shell), Task 9 (the command) and Task 10 (against a real draal).
2. **Bytes of a zathras on the operator's terminal**: a log line, a name or a refusal with escape sequences in it moves no cursor, sets no title and clears nothing. Pinned in Task 4 (the one function, also by fuzzing), Task 6 (a fleet whose every string is escape sequences, on every view), Task 7 (the error that `Run` returns) and Task 8 (the line before a shell and the sentence after it).
3. **draal restarted, or the connection lost, while the terminal UI is open**: it says that the connection is lost and for how long, keeps what it showed, tries again with pauses that grow, and is back on the view it was on. Pinned in Task 5 (the model, on every view), Task 6 (what is said), Task 8 (with a shell open) and Task 10 (a real draal stopped and started).
4. **A zathras that goes while its view, its log or its shell is open**, and nothing to show: no service yet, a service without a zathras, a zathras without samples, a service without a rollout. Pinned in Task 5, Task 6 (every odd state is a sentence), Task 8 (a zathras that goes while its shell is open, or is gone when `s` is typed) and Task 10.
5. **A terminal that is small, or resized**, also while a shell is open; and a flood: a log of a thousand lines a second, a fleet of some hundred zathrases. Pinned in Task 1 (a size that changes while typing), Task 5 (every size from 1×1 to 100×40), Task 6 (three hundred zathrases; megabytes in a field), Task 7 (a flood of lines) and Task 8 (a resize and a flood while a shell is open).

## How things are run

On the machine this plan was written on, the system's `go` is older than
the module asks for, so every Go command is run in the flake's shell, from
the root of the repository:

```bash
nix develop .#bare --command go test ./...
```

On Linux, in a container (the tests of `term` and of the terminal UI use
pseudo-terminals, and behave differently enough there to be worth it):

```bash
docker run --init --rm -v "$PWD":/src:ro -w /src -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod golang:1.26 go test ./...
```

`--init` is needed. Without it `go` is process 1 of the container and
waits for no child but its own, so that a process whose parent has gone
stays as a zombie; a test of stage 2 that waits for the child of a shell
to be gone (`zathras`, `TestShellEndsWithItsStreamWhileInputWaits`) then
fails whenever the shell ends before its child. On the day this plan was
written it first passed without `--init` and later failed five times of
five, on `main` as on this plan's tree; with `--init` it passes. It is no defect of
zathras, which is process 1 in its container and waits for every child.

A test that starts a shell starts a real one on the machine it runs on.

---

## File Structure

```
term/                the read of the terminal can be ended (Task 1)
operator/            a client that is closed ends its watches and logs (Task 2)
demo/                the made-up fleet of web/demo, as a package both demos use
human/               how a size, a duration, an age and a generation are written; cli had it
web/demo/            the web page over it, as before
tui/                 the terminal UI: the model, what it draws, the program around it, the shell
tui/tuitest/         a pseudo-terminal for the tests that run the terminal UI as a whole; part of no program
tui/demo/            the terminal UI over the made-up fleet
cli/tui.go           great-machine tui
e2e/                 the terminal UI in a pseudo-terminal against a real draal
```

## Interfaces

This section is the contract between the tasks. A task implements what is
written here for its part, exactly, and uses the other parts only through
what is written here.

### term

`Attach` keeps its signature and everything its comment promises, but for
the paragraph on what it leaves behind, which becomes:

```go
// When Attach returns, for whatever reason, nothing of it reads in any
// more: what is typed next is read by whoever reads in next. A caller
// that lives on, as the terminal UI does, may read the terminal again at
// once.
func Attach(ctx context.Context, in *os.File, out io.Writer, s Session) (exit int, detached bool, err error)
```

This holds for a terminal and for a file that is none, and `Attach`
returns only when it is so. The terminal is in the mode it was in before,
as now, and it is restored after the reading has stopped.

How the read is made endable, in `term` itself and the same on macOS and
on Linux: `select(2)` on the file and on a pipe of `Attach`'s own; the
file is never made non-blocking, which would change it for every process
that shares it. While it is attached, a terminal is in a mode in which a
read returns at once with nothing when there is nothing (`VMIN` and
`VTIME` 0), so that another reader of the same terminal, which may take a
key first, cannot hold `Attach` up; a read of nothing is told from a
terminal that has gone by asking `poll` for a hangup.

What holds besides:

- `Attach` is to be the only reader of `in` while it runs. On a terminal
  another reader only takes keys away. In a file that is no terminal it
  can hold `Attach` up until the next input, which `Attach` then reads.
- `Attach` refuses a file whose descriptor is 1024 or more, which is what
  `select` can take.
- Nothing is written to the session or to `out` once `Attach` has
  returned, with one exception: when its context ended it, a `Write` that
  is under way is not waited for (a terminal that is held would hold
  `Attach` for ever), so one piece of the shell's output, 32 KiB at most,
  may still arrive on `out`, and nothing follows it.
- As before, the session is the caller's to close, and closing it ends
  what `Attach` may have left writing to it or reading from it.

### operator

`(*Client).Close` ends everything the client started, and returns when it
has ended: the channel of every `Watch` and of every `Logs` is closed and
their goroutines are gone, whatever their contexts say and whether or not
anyone receives, and a `Shell`'s `Read`, `Write`, `Resize` and `Wait`
return with an error in which `errors.Is` finds `ErrClosed`. `Close` may be called more than once and from another goroutine.

```go
// ErrClosed is the error of a request on a client that was closed, and of
// one that was under way when Close came: errors.Is finds it. It tells
// "this side closed the client" from "the connection was lost".
var ErrClosed = errors.New("the client is closed")
```

**Receive from the channel of a `Watch` or a `Logs`, or end its context.**
This is no change but a fact of the transport that the doc comments now
state, measured on go-iroh while this plan was written: what draal sends
and nobody receives stays in the connection, which takes 512 KiB of it for
one stream and about 768 KiB over all its streams, and these windows do
not grow while nobody reads. One watch or log that is fed and not read
holds up nothing else. Two hold up every other request of the client, a
shell's output among them. The terminal UI therefore reads its watch and
its log in goroutines that wait for nothing else, and keeps of them the
newest fleet and a bounded number of lines.

### human

`cli`'s functions that write a size in bytes, a time something took, an
age and a generation move to a package `human`, unchanged, so that the
terminal UI writes them as the commands do; `tui` cannot import `cli`,
which imports it. What it adds for the terminal UI (rates, CPU, a time, a
short ID, a store path as its name, an exit, a count with its noun)
returns a sentence for a person for every input: never the zero time as
a date, a negative duration, or a number that is none.

### demo

```go
package demo

// New returns the made-up controller of web/demo: four services
// (empty, fresh, web, worker), or none when empty is set, with a fleet
// that moves by itself once Run is called.
func New(empty bool) *Demo

// Run moves the fleet until ctx is done.
func (d *Demo) Run(ctx context.Context)
```

`*Demo` is a `draal.Controller`, as it is now in `web/demo`, with the same
fleet, the same times and the same endpoint IDs. `web/demo` (the program)
keeps its flags and what it serves.

### tui

```go
package tui

// Operator is what the terminal UI asks of draal. An *operator.Client is
// one, but for the type that its Shell returns: cli has the adapter.
type Operator interface {
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	Close() error
}

// Shell is a shell session: what term.Attach joins the terminal to, and
// Close, which ends it.
type Shell interface {
	term.Session
	Close() error
}

// Config configures the terminal UI.
type Config struct {
	// Dial connects to draal. It is called at the start, and again, with
	// pauses that grow, whenever the connection was lost. The terminal UI
	// closes what Dial returned.
	Dial func(ctx context.Context) (Operator, error)
	// In and Out are the terminal.
	In, Out *os.File
	// Draal is how the header names draal: the start of its endpoint ID.
	Draal string
	// Each of these has a default when it is zero:
	RetryFirst time.Duration    // the first pause before Dial is tried again; 1 second
	RetryMax   time.Duration    // the longest pause; 30 seconds
	Now        func() time.Time // time.Now
	Log        *slog.Logger     // nowhere: a terminal UI has no place to log to
}

// Run runs the terminal UI until the user quits, which is nil, or ctx is
// done, which is the error of ctx. The terminal is as it was when Run
// returns.
func Run(ctx context.Context, cfg Config) error

// RunDrawn is Run for the tests of the packages that run the terminal UI
// as a whole (cli, e2e): drawn is called with every view as it is handed
// to the renderer, which is what a person reads on the screen. What the
// renderer writes to the terminal is no screen: it writes what changed
// since the last frame, and only a terminal emulator could say what that
// adds up to.
func RunDrawn(ctx context.Context, cfg Config, drawn func(view string)) error
```

What draal refuses at the first dial, which is `Dial` and the first
`Watch`, with the code `unauthorized` ends `Run` with that error: the
operator is not on the allowlist, and trying again changes nothing. Once
draal has let the operator in, a later `unauthorized` is shown in the
header and tried again like any other loss. A draal that does not answer
is tried again from the start. A failure of the terminal UI itself (a
panic in the model, in a view, or in a goroutine of the program) ends
`Run` with an error that says so, with the terminal restored; nothing of
it is written to the terminal.

`Run` returns within a bounded time of `ctx` being done. The program is
asked to end; one that has not ended after five seconds is cut off, with
the terminal restored, and `Run` says so in its error, in which
`errors.Is` still finds the error of `ctx`. An `Operator` whose `Close`
does not return is waited for five seconds and then left, with a warning
to `Config.Log`. What is left behind in either case is the goroutine that
was stuck and what it holds; the process is about to end.

How the program ends is decided once: a program that has taken its
message to quit is ending by itself and is not cut off, and one that is
being cut off takes no message to quit until that is done. So the
terminal is given back by one of the two and never by both, and **nothing
is written to the terminal once `Run` has returned**. On the path of the
cut, which is the path of a failure, the sequences that reset the
terminal are written twice: Bubble Tea v2.0.10 has no way to end a
program that does not also close its renderer, and closing it without
first giving the terminal back closes the terminal under Bubble Tea's own
read. The second set changes nothing, but for a terminal on which the
program that started this one had pushed keyboard modes: it loses one
level of them. A program that took its message to quit and then does not
come back from drawing its last view is left to it after a further
second, and `Run`'s error says that the terminal may not be as it was.

**The text of every error that `Run` returns is safe to print**: where it
carries words of draal or of a zathras, they have been through the same
function as everything that is drawn. `errors.Is` sees through it, and
`errors.As` to a `*wire.Error` gives one whose message was made safe too.
A command prints the error itself, never what `errors.Unwrap` returns.

Of an `Operator` the terminal UI relies on this and nothing more: `Logs`
returns with the first line, with the end of the log, or with a refusal,
as `(*operator.Client).Logs` does (draal answers a log with its lines
alone, so before the first line nothing says that it was let in); every
call ends when its context ends or when `Close` was called.

The program is built with Bubble Tea's `tea.NewProgram` without
`tea.WithContext`: with it, Bubble Tea v2.0.10 closes its reader of the
terminal without waiting for it when the context ends.

**Views.** Three, as the web page has them, and one at a time fills the
terminal under a header of one line (the name, where one is, draal's ID,
and the state of the connection: connecting, live, or lost and since
when) and above a line of the keys that work here.

| View | Shows |
|---|---|
| services | every service: its stable generation, its zathrases by phase, its last rollout and how far it is |
| a service | its zathrases as a table (name, phase and since when, generation and whether it is the stable one, restarts, last exit, CPU and memory), the rollout under way with where every zathras stands in it by its own report and draal's counts, and its last rollouts |
| a zathras | its state in full; its figures over the last 15 minutes as sparklines with the value now (CPU, memory and its limit, disk read and written, network received and sent); the last 400 lines of its log, followed live |

**Keys.**

| Key | Does |
|---|---|
| `↑` `↓`, `k` `j` | move in a list; scroll a log |
| `Enter`, `→`, `l` | open what is chosen |
| `Esc`, `←`, `h`, `Backspace` | back; while a shell is being opened, `Esc` gives it up |
| `PgUp` `PgDn`, `g` `G` | a page, the start, the end; in a log `G` also follows it again |
| `s` | on a zathras: open a shell |
| `?` | the keys |
| `q`, `Ctrl-C` | quit |

**A shell** is opened with `s` on a zathras that is connected, in two
steps, so that **the terminal is given away only for a session that
exists**. First the terminal UI asks draal for the session and keeps the
terminal and its screen meanwhile: a notice says that the shell is being
opened and that `Esc` gives it up. What draal refuses, and a request that
fails, is a notice; the screen is never left for it. When the session is
there, the terminal UI leaves the alternate screen, writes one line that
says whose shell it is and that `Ctrl-]` leaves it, gives the terminal to
the session (`term.Attach`, through Bubble Tea's `tea.Exec`) and takes it
back when the shell has ended or `Ctrl-]` was typed; either way the
session is closed, which hangs the shell up. Back in the terminal UI a
notice of up to three lines says how the shell ended: its exit status,
that it was left with `Ctrl-]`, or the error.

**From `s` until the shell has ended, the terminal UI acts on no key** but
`Esc` while the shell is being opened, which gives the opening up; the
line of the keys says that one key then. A key typed while the shell is
being opened is dropped: it does nothing in the terminal UI, then or
after the shell, and it does not reach the shell. So `s` typed twice
opens one shell, and a `q` typed behind `s` quits nothing. What is typed
in the shell never reaches the terminal UI, and what is typed before `s`
never reaches the shell. One limit is known and accepted, at the two
instants at which the terminal changes hands: within a few milliseconds
of either, a key may be dropped, and a key typed in the instant before
the shell takes the terminal may be the shell's or, rarely, be acted on
by the terminal UI when the shell has ended (Bubble Tea's new reader of
the terminal starts before the terminal UI is told that the shell has
ended, and a key that its old reader still held is handed over behind
that). A connection that is lost between draal's answer and the giving
away leaves the screen for a moment and gives the terminal to nobody.

While the shell has the terminal, the terminal UI takes no key and draws
nothing: every key is the shell's. The watch of the fleet and the log are
still read, and what is kept of them is bounded, so that a shell that is
open for an hour holds nothing up; afterwards the view shows the fleet as
it is then and asks again for what it holds. What ends the terminal UI
ends the shell with it: the context of `Run`, the connection to draal,
and a zathras that goes each end the session, and a session that arrives
when nobody waits for it any more is closed and never joined to the
terminal. A shell that does not end with the context of `Run` (a session
that does not return) is left behind after two seconds, and after half of
the five seconds of the cut at the latest, with the terminal put back in
the mode it was given away in, so that the program ends in its usual way
and the cut never comes while a shell has the terminal. A panic of the
session ends the terminal UI as a panic of its own does, with the
terminal restored.

`tui/tuitest` is a pseudo-terminal for the tests of `tui`, `cli` and
`e2e`: it reads the screen from `RunDrawn`, and the bytes written to the
terminal for what the terminal is left in and for what a shell wrote. It
imports `testing`; no program imports it.

**What it holds.** One watch of the fleet for the whole program. On the
view of a service its rollouts, asked for when the view is opened and
again when the rollout's ID or state changes. On the view of a zathras its
history once, then the samples of the fleet, and one followed log; both
are ended when the view is left. It keeps 400 lines of a log, draws at
most some tens of times a second however fast lines come, and says how
many lines were lost when draal says so. From when a log is asked for
until its first line it says that no line has come yet; then that it is
followed live; then that it ended. After the connection came back, a line
is taken as new unless it is older than the newest line held, or of the
same time with the same stream and text as one that is held.

An answer is taken only when it is the answer to the newest request of
its kind, so that an older one that comes last changes nothing. A field
of words (a name, a reason, an argument, a refusal) is kept to 4 KiB and
says how much was left out; of the arguments 64 are kept. A sample that
is more than an hour ahead of the terminal UI's own clock is left out, so
that one wrong sample cannot move the window of the figures away from
every true one; a zathras whose clock is that far ahead has no
sparklines.

**When the connection is lost** (the watch's channel closes, or a request
fails with the connection): the header says lost and for how long, what
was shown stays and is marked as old, `Dial` is tried again after
`RetryFirst`, then after pauses that double up to `RetryMax`, and when it
succeeds the watch, and what the open view holds, are asked for again. A
key ends a pause at once.

### tui/demo

```
go run ./tui/demo [--empty]
```

runs the terminal UI over the made-up fleet of `demo`, with a real shell
on this machine as its shell, as `web/demo` has. It is for developing the
terminal UI and is not part of the program.

### cli

`great-machine tui` takes the flags of every operator command (`--draal`,
`--key`) and no argument. It needs a terminal as its standard input and
its standard output and says so, as a mistake of usage (status 2), when
either is none. It ends with status 0 when the user quits, and with 130
when it was interrupted, as the other commands do. What draal refuses at
the start (an operator that is not on the allowlist) ends it with that
refusal on standard error and status 1, after the terminal was restored.
It reads the operator's key once, before it takes the terminal, so that a
key that cannot be read is said on a terminal that was never touched.
When it was interrupted and `Run`'s error says more than that (the
terminal UI was cut off, the terminal may not be as it was, it failed),
the error is printed and the status is still 130.

---

## Tasks

Each task is a file of its own in [`2026-10-10-great-machine-stage-3/`](2026-10-10-great-machine-stage-3/), with its complete
code, its tests and its commands. Whoever carries a task out reads this
file down to here, and the task's file. The tasks are done in the order
of their numbers; "needs" names the tasks a task stands on.

| # | Task | Delivers | Needs |
|---|---|---|---|
| 1 | [term: a read that can be ended](2026-10-10-great-machine-stage-3/task-01-term.md) | in stage 1's code: when `Attach` returns, nothing of it reads the terminal any more, so that a program that lives on can read it again at once | |
| 2 | [operator: a client that is closed](2026-10-10-great-machine-stage-3/task-02-operator.md) | in stage 1's code: `Close` ends every watch, log and shell the client started, and returns when they have ended; `ErrClosed` | |
| 3 | [The made-up fleet as a package](2026-10-10-great-machine-stage-3/task-03-demo.md) | `demo`, which `web/demo` uses and the terminal UI's demo will | |
| 4 | [Text that is safe for a terminal](2026-10-10-great-machine-stage-3/task-04-text.md) | `human`; the one function everything drawn goes through; a log line read and drawn again; sparklines; where a zathras stands in a rollout; the buffer of a log | |
| 5 | [The model](2026-10-10-great-machine-stage-3/task-05-model.md) | the state and its `update`, with no terminal and no network: the three views and the keys, the connection and its pauses, what each view holds | 4 |
| 6 | [The views](2026-10-10-great-machine-stage-3/task-06-views.md) | what is drawn, at every size of terminal, with every odd state as a sentence | 4, 5 |
| 7 | [The program around the model, and the demo](2026-10-10-great-machine-stage-3/task-07-program.md) | `tui.Run`: the terminal as it was on every way out of the terminal UI's own; `go run ./tui/demo` | 3, 5, 6 |
| 8 | [The shell from the terminal UI](2026-10-10-great-machine-stage-3/task-08-shell.md) | `s` on a zathras: the terminal is the session's until the shell ends or `Ctrl-]` is typed; `tui.RunDrawn` and `tui/tuitest`, the pseudo-terminal of the tests of the whole | 1, 2, 7 |
| 9 | [`great-machine tui`](2026-10-10-great-machine-stage-3/task-09-cli.md) | the command, with the operator's flags | 2, 8 |
| 10 | [The terminal UI against a real draal](2026-10-10-great-machine-stage-3/task-10-e2e.md) | in a pseudo-terminal, over go-iroh, with real zathrases: the fleet, a rollout, a log, a shell, draal lost and back | 8 |
| 11 | [Delivery](2026-10-10-great-machine-stage-3/task-11-delivery.md) | the README; everything run once more | 9, 10 |

## Decisions taken while this plan was written

The spec as it was approved did not have these. Each is in the spec now
(section 10, and sections 11 and 14 where they name packages and tests).

| Decision | Why | What it costs |
|---|---|---|
| **Task 1 changes stage 1's `term.Attach`**: its read of the terminal is a `select(2)` on the file and a pipe of its own, and while it is attached the terminal is in a mode in which a read never waits (`VMIN` and `VTIME` 0). | The spec's section 10 names it as the prerequisite: a session that ended for another reason than `Ctrl-]` left a read behind that took the next key. A library for this (`cancelreader`, which Bubble Tea uses) cannot say when its read has ended, and a terminal made non-blocking is non-blocking for the shell that started the program too. The mode was added after a review showed that a second reader of the same terminal, taking a key first, could hold `Attach` up for ever. | `Attach` refuses a file whose descriptor is 1024 or more. When its context ends it, one piece of the shell's output (32 KiB at most) may still arrive after it returned. |
| **Task 2 changes stage 1's `operator.Client.Close`**: it ends every watch, log and shell of the client and waits for them; a new `ErrClosed`. | The terminal UI dials again and again over hours and closes the client each time; as stage 1 left it, the goroutines of a watch or a log stayed when nobody received. | None for the commands, which end their process. |
| **Bubble Tea v2 and Lip Gloss v2** (`charm.land/…`), and none of Bubbles. | The spec names Bubble Tea. Version 2 has the renderer that draws only what changed and `tea.Exec` for giving the terminal away. The lists, the table and the log are the terminal UI's own, as the web page's are: they are small, and every line they draw has to go through the one function that makes text safe. | Three direct dependencies more (`bubbletea`, `lipgloss`, `x/ansi`). |
| **The program is not given its context** (`tea.WithContext`); it is asked to quit when the context ends, and cut off after five seconds. | With its context, Bubble Tea v2.0.10 closes its reader of the terminal without waiting for it, and the race detector says so. | The code that ends the program is the terminal UI's own, with what the contract says of it above, the resets written twice on the path of the cut among it. |
| **Nothing that draal or a zathras sent is written as it came**, the text of the error that `Run` returns included; a log line's colours are read and drawn again, its other sequences are dropped. | A log line is what a program in a container wrote. On a terminal a sequence can set the title, move the cursor over what is shown, or on some terminals answer back as if it were typed. `great-machine logs` writes lines as they came, into what is usually a pipe or a pager; a terminal UI draws on the operator's screen. | A line's cursor movements and its hyperlinks are not shown. What a column of the table holds can stand one cell off for names in scripts whose width terminals disagree on (Devanagari, Myanmar, Hangul jamo); no line is ever wider than the terminal. |
| **The colours are the terminal's own sixteen**, with no theme and no switch; `NO_COLOR` is respected. | The person's word for the web page, "the theme should follow the system settings", applied to a terminal: the terminal's palette is the system setting there. | The terminal UI looks as good as the terminal's palette is. |
| **It draws down to 50 columns and 12 rows** and says so in one line below that. | 80×24 is what it is made for; half of that is where the table of a service can still say something. | Below 50×12 nothing is shown but the sentence. |
| **A log before its first line says "no line has come yet"**, not that draal is being asked. | draal answers a log with its lines alone: until the first line, nothing tells a log that was let in from one that waits. | A log that draal has not answered yet and a zathras that writes nothing look alike. |
| **Limits against what a zathras may send**: 4 KiB for a field of words, 64 arguments, a sample at most an hour ahead of the terminal UI's clock, scales of the figures clamped. | A review showed both: a zathras with megabytes in its name made every message cost a quarter of a second, and one sample far ahead moved the window of the figures away from every true sample of that zathras. | A zathras whose clock is more than an hour ahead has no sparklines. After a reconnect, a zathras whose clock was set back can lose up to 400 lines of its log from the view. |
| **`cli`'s writing of sizes and times becomes the package `human`**, and the made-up fleet of `web/demo` the package `demo`. | `tui` cannot import `cli`, which imports it; two demos want one fleet. | Two packages more. |
| **The terminal is given away only for a session that exists, and from `s` until the shell has ended no key is acted on** but `Esc`, which gives an opening up. | A review showed both sides of the moment the terminal changes hands. Bubble Tea carries out a request to give the terminal away some milliseconds after it was made, and a key typed in between was the terminal UI's: `s` twice opened two shells one after the other, `q` behind `s` quit the terminal UI under the shell. And as first built the terminal was given away before draal had answered: the user sat on their own screen with keys echoing, which later went to the shell, and only an interrupt of the whole terminal UI led out. | A key typed between `s` and the shell having the terminal is lost, whoever it was meant for. While a shell is being opened `q` quits nothing; `Esc` first. Within a few milliseconds of the terminal changing hands a key may be dropped, and one typed in the instant before the shell takes the terminal may be the shell's or be acted on after the shell. |
| **While a shell has the terminal the terminal UI takes no message at all** (Bubble Tea's `tea.Exec`), and a shell that does not end with the terminal UI is left behind after two seconds. | Giving the terminal away whole is what makes every key the shell's and none the terminal UI's. Nothing of the program can then end a shell, so the shell ends by its own context; one that does not must not keep the terminal from being restored. | Nothing is drawn behind a shell, and what happened meanwhile is seen when it ends. A session that was left behind is hung up by the end of the process. |
| **`tui.RunDrawn` and the package `tui/tuitest` are part of the module though only tests use them.** | The tests of `cli` and of `e2e` run the terminal UI as a whole and have to read its screen. Reading it out of the bytes the renderer writes failed about once in five runs on Linux: the renderer writes what changed, in pieces. A package of test helpers that three packages share cannot be a `_test.go` file. | One exported function that a program has no use for. `tuitest` imports `testing`; no program imports `tuitest`. |

## What was run while this plan was written

The code of every task was built and tested before its task file was
written: three writers worked test-first in clones of the repository, and
each task file was generated from the commits of its cycles, with every
"expected to fail" taken from a real run against the code as it was
before the step. The trees were then merged, and what the task files
hold was compared with the merged tree block by block.

**Independent reviews.** Each part was reviewed by an agent that had not
written it, which was told to break it and to prove what it found with a
failing test; each mend was reviewed again. What they found is mended in
the task files as they are here:

| Part | Found | What became of it |
|---|---|---|
| `term`, `operator` (Tasks 1, 2) | A second reader of the same terminal that takes a key first held `Attach` up for ever (2 runs of 5). A test of `Close` waiting that held nothing. A panic that was swallowed. | The terminal is in a mode in which a read never waits while it is attached. The test has a hook and fails 15 of 15 without the wait. |
| `tui` (Tasks 4 to 7) | Twelve findings, five with failing tests: a program that is stuck was never cut off (the timer was set behind a call that waits); a refusal of draal's was returned unclean to the command line; "asking draal" for ever in two places; an older answer that came last was taken; lines of one instant lost after a reconnect. No byte got past the function that makes text safe, also in fuzzing. | All mended, each mend taken out again by the reviewer and seen to fail a test. |
| the mends of `tui` | When the program ended by itself at the moment it was to be cut off, a whole frame was painted on the user's main screen after `Run` had returned (5 runs of 30). | How the program ends is decided once, under one lock; two tests hold the two orders without timing. |
| the shell, `cli`, `e2e` (Tasks 8 to 10) | No way to get a byte of draal's onto the terminal, no way out that left the terminal in another mode. But `s` typed twice opened two shells, `q` behind `s` quit the terminal UI under the shell, and the terminal was given away before draal had answered. | The shell is opened in two steps (see the contract). |
| the two steps | A session that draal had opened and nobody waited for was closed in a call that could wait for ever. A connection lost between draal's answer and the giving away still gave the terminal away. | Mended, and the two mends held by tests whose mutants the writer ran; they were not reviewed a third time. The limit at the instants the terminal changes hands is accepted and said in the contract. |

Besides, a test of `operator` that failed once on Linux under load led to
a fact of the transport that is now in the contract and in the doc
comments of `Watch` and `Logs`: two streams that are fed and not read
stall every stream of the connection (measured: one unread stream stops
at 575 KiB with other requests still answered; with two, none is).

**On the tree the task files add up to** (the reference this plan was
checked against), on arm64 macOS and on arm64 Linux in a container:
`gofmt -l .` prints nothing, `go vet ./...` is clean, and `go test ./...`
passes in all 30 packages on both. With the race detector: `term` and
`operator` 50 times on both, `operator` 100 times more on Linux and 120
times with six copies at once on one processor; `tui` 50 times on macOS
and 20 on Linux; the tests of the terminal UI in `cli` and in `e2e` 10
times on both, and 20 times on the tree before the last mend. `FuzzClean`
and `FuzzViews` ran for a minute each, and the reviewer's fuzzing of the
views for 90 seconds, with nothing found. The program was built by Nix
from that tree; it names `tui` in its help and says what it needs when
its input is no terminal.

The screens were captured from the demo in a terminal emulator at 80×24,
100×30 and 160×48, light and dark, and read.

**Not run:**

- x86_64 Linux, which is what the workflow on GitHub runs.
- The terminal UI in a real terminal window, by a person: Task 8's
  Step 10 and Task 11's last step ask for it.
- A change of the window's size while a shell is open, with the signal,
  in a test: Bubble Tea's own handling of the signal trips the race
  detector. It was seen to work once in the emulator.
- The image, and the release workflow.
- The terminal UI against a draal in a cluster.
