### Task 8: The shell from the terminal UI

The key `s` on the view of a zathras that is connected opens a shell in its container, in two steps, so that **the terminal is given away only for a session that exists**. First the terminal UI asks its `Operator` for a session and says so, keeping the terminal and its screen. When draal has opened the session, it gives the terminal to `term.Attach` with `tea.Exec`, and takes it back when the shell has ended or `Ctrl-]` was typed. Either way the session is closed, and back in the terminal UI one line says how the shell ended. **From `s` until the shell has ended the terminal UI acts on no key but `Esc`, which gives up a shell that draal has not opened yet.** Before all that, one small package: the pseudo-terminal that the tests of the whole run a terminal UI in (`tui/tuitest`), which the tests of Tasks 9 and 10 use, and of which this task's tests use one function.

**Review Focus pinned here:**
- **1, in full for a shell** (the terminal, and whose a key is, on every way into a shell and out of it):
  - while draal is asked: `TestKeysTypedWhileAShellIsOpened` (the terminal is the terminal UI's, nothing typed is echoed, acted on or passed on to the shell), `TestKeyTypedRightBehindS` (`ss` asks for one shell, `sq` does not quit), `TestEscGivesUpAShellThatIsOpened`, `TestEscWithASessionWhoseCloseDoesNotReturn`, `TestContextEndsWhileDraalIsAskedForAShell`, `TestSessionThatIsHeldWhenRunEnds` (the session is closed, and a `Close` that does not return does not hold `Run` up);
  - as the terminal is given away: `TestKeyHeldWhenTheTerminalIsGivenAway` (a key that Bubble Tea's reader holds then is dropped, not acted on after the shell);
  - while the shell has it and after: `TestShellHasTheTerminalAndGivesItBack` (every key typed during the shell reaches the session and none the program, the keys typed after it reach the program in order, nothing but the shell wrote to the terminal meanwhile), `TestKeysTypedWhileTheTerminalChangesHands` (the moment between `Attach` and Bubble Tea), `TestShellLeftWithCtrlRightBracket` (`Ctrl-]`, and three shells in a row);
  - the other ways out: `TestShellEndsWithTheContext`, `TestShellThatDoesNotEndWithTheContext` (the program ends by itself all the same, before it would be ended by force), `TestShellIsLeftBeforeTheKill`, `TestShellPanics`.

  Each test that ends a program compares the mode of the pseudo-terminal before and after, checks that the alternate screen was left and the cursor shown last, that what is typed afterwards reaches the next reader of the terminal, and that no goroutine is left.
- **3, with a shell**: `TestConnectionLostWhileAShellIsOpen`, `TestConnectionLostWhileAShellIsOpened`, `TestConnectionLostBetweenTheAnswerAndTheShell`.
- **4, the shell's part** (a zathras that goes while its shell is open): `TestShellRefusedAndShellOfAZathrasThatGoes`, and `TestNoShellOnAZathrasThatIsGone`.
- **5, the shell's part**: `TestTerminalResizedWhileAShellIsOpen` for the terminal UI's half of a resize, and Step 10 in a real terminal for the whole (that the session is told of the new size is `term`'s own test, Task 1); `TestWatchAndLogAreReadWhileAShellIsOpen` for a flood while a shell is open.
- **2** for the one new thing that draal's words reach: `TestShellSentence` (a refusal with an escape sequence in it).

**Files:**
- Create: `tui/drawn.go` (`RunDrawn`)
- Create: `tui/tuitest/tuitest.go`, `tui/tuitest/dup_linux.go`, `tui/tuitest/dup_other.go`
- Test: `tui/tuitest/tuitest_test.go`
- Modify: `tui/shell.go` (the whole file: the two steps, the line that says how a shell ended, and how the program gives the terminal away)
- Modify: `tui/model.go` (the model holds how far a shell is; while one is asked for or open, no key is the model's; a connection that is lost gives up a shell that is being opened)
- Modify: `tui/run.go` (the runner knows the context of `Run` and holds the shell that waits; it carries out the three effects; `Update` ends the program when something failed while a shell had the terminal)
- Modify: `tui/view.go` (`noticeLines`: what is said once may take three lines; `keys`: the one key that works while a shell is being opened)
- Test: `tui/shell_test.go`
- Modify: `tui/run_test.go` (the fake `Operator` can be given shells; the pseudo-terminal is given up without a `Close`), `tui/model_test.go`, `tui/scenes_test.go`, `tui/view_test.go` (what `s` does now)

**Interfaces:**
- Consumes: `term.Attach(ctx, in, out, s) (exit int, detached bool, err error)` and `term.Size(f) (cols, rows uint16)` as Task 1 leaves them (when `Attach` returns nothing of it reads the terminal any more; while it runs nothing else may read the terminal; it returns when its context is done); `tui.Operator`, `tui.Shell`, `tui.Config` (the contract); of Tasks 5 to 7: `(*model).shellKey` (which refuses when the zathras is gone or the connection is not live, and otherwise calls `openShell`), `(*model).key`, `(*model).take`, `(*model).lost`, `(*model).again`, `m.notice`, `m.seen`, `named`, `clean`, `refusal`, `wrap`, `line`, `(*runner).current(epoch) (*conn, Operator)`, `(*runner).spawn`, `(*runner).do`, `(*runner).close`, `r.cfg`, `r.ctx`, `conn.ctx`, `program.do`, `run(ctx, cfg, drawn, opts...)`, `killAfter`, `closeAfter`, `filterForTests`; in the tests `startRun`, `fakeOperator`, `oneOperator`, `await`, `waitFor`, `goroutinesLeft`, `terminal` with `wait`, `shown`, `written`, `typeKeys`, `end`, `stop`, `quietBehindTheMark`, `returnedMark`, and the fixtures. Bubble Tea v2.0.10: `tea.Exec(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd`.
- Produces:
  - In the model, `m.sh shellState{stage shellStage, n int, name string}` with the stages `shellNone`, `shellOpening`, `shellOpen`. The model holds the number of the request, never the session.
  - `effShell{epoch int, zathras wire.ID, n int}`: ask draal for a session; answered by `shellOpenedMsg{n int, err error}`, with the session held by the runner.
  - `effGiveUp{n int}`: nobody waits for that shell any more; the runner ends the request and closes a session that is there or comes yet.
  - `effAttach{n int, name string}`: give the terminal to the session of that number; answered by `shellEndedMsg{opened bool, exit int, detached bool, err error}`. `name` is how the line above the shell names the zathras, clean.
  - `func shellSentence(msg shellEndedMsg) string`, the one line: `The shell ended.` / `The shell ended with status 3.` / `The shell was ended by a signal.` / `The shell was left with Ctrl-], which hung it up.` / `The shell ended: <why>` / `No shell was opened: <why>`, where `<why>` is the sentence of draal's refusal when the error is one (`errors.As` to `*wire.Error`), and the whole error otherwise; clean in either case. The other lines: `Opening a shell on NAME … esc gives it up`, `The shell was given up before draal had opened it.`, `No shell was opened: the connection to draal was lost.`
  - On the terminal's own screen, when the terminal is given to the session, the line `great-machine: a shell on NAME. Ctrl-] leaves it.` (Tasks 9 to 11 name it.)
  - `var shellGrace = 2 * time.Second`: how long a shell has to give the terminal back once the context of `Run` is done; and `func graceOfShell() time.Duration`, which is that and at most half of `killAfter`.
  - `func tui.RunDrawn(ctx context.Context, cfg Config, drawn func(view string)) error`: `Run`, with every view told to `drawn` as it is handed to the renderer. **It is not in the contract's `tui` section: it is there for the tests of `cli` and `e2e`**, which cannot reach `run`.
  - `package tuitest` (import path `github.com/fables-for-robots/great-machine/tui/tuitest`; it does not import `tui`, so the tests of `tui` may use it):
    - `const Deadline = 60 * time.Second`
    - `func Open(t testing.TB, cols, rows uint16) *Terminal`: a pseudo-terminal, given up when the test ends once no goroutine of a terminal UI is left (the test fails if one stays). A test that uses one is not parallel.
    - `(*Terminal).File() *os.File` (the `In` and `Out` of a `tui.Config`), `Drawn(view string)` (what `RunDrawn` is given), `Screen() string`, `Wait(words ...string) string`, `WaitFor(what string, cond func(screen string) bool) string`, `Type(keys string)`, `Resize(cols, rows uint16)`, `Written() string`, `WaitWritten(from int, text string) int`, `Usual() bool`, `OnAlternateScreen() bool`, `Restored()` (the checks of Review Focus 1 on a terminal UI that has ended).
    - `func GoroutinesLeft() []string`, `func Release(t testing.TB, f *os.File)`.

**How it is made, and why:**
- **Two steps, and why.** `tea.Exec` gives the terminal away at once, and a session takes a round trip to draal and to the zathras. If the terminal were given away with the request, it would stand on the user's own screen in its usual mode for as long as draal takes, with nothing said: keys would echo there and go to the shell later, `Ctrl-]` would do nothing, and a draal that never answers would leave only a signal as the way out. So the request is made while the terminal UI has the terminal (`effShell`, carried out by the runner in a goroutine), the session is held by the runner, and the terminal changes hands when the model, told that the session is there (`shellOpenedMsg`), asks for it (`effAttach`). A refusal is a line in the terminal UI, which never leaves its screen for it. A session that comes when nobody waits (the opening was given up with `Esc`, the connection was lost, `Run` is ending) is closed by the runner and never attached: the model does not even read its message.
- **Whose a key is.** From `s` until `shellEndedMsg` the model acts on no key (`shellTakesKey`), but on `Esc` while the shell is being opened. So `ss` asks for one shell and `sq` does not quit, whichever way the keys and draal's answer interleave. What is typed while the terminal UI has the terminal is dropped: it is not the terminal UI's, since a shell was asked for, and not the shell's, which was not there. In each moment:
  - *between `s` and draal's answer*: read by Bubble Tea, dropped by the model (`Esc` gives the shell up);
  - *between the answer and `tea.Exec`*: the same; the model is at `shellOpen`;
  - *held by Bubble Tea's reader when the terminal is given away* (read, and not yet handed to a program that is busy): it is handed over when the program takes messages again, behind the shell, and **before** `shellEndedMsg`, since it has waited longer to be sent and a Go channel serves waiting senders in their order: the model drops it. `TestKeyHeldWhenTheTerminalIsGivenAway` holds that order;
  - *typed until the shell has the terminal, and not read by Bubble Tea* (its reader is cancelled first): it stays in the terminal and is the shell's;
  - *while the shell has the terminal*: the shell's, but `Ctrl-]`;
  - *between the shell and the terminal UI* (after `Attach` has returned, before Bubble Tea has the terminal again): the terminal is in its usual mode and echoes it; Bubble Tea reads it afterwards. An `Enter` typed then arrives as a line feed, which Bubble Tea takes for `Ctrl-J`;
  - *once the view is drawn again*: the terminal UI's, in order.
- **The limit, which is accepted and has no test** (a test of it would be one of timing). A key typed while the shell has the terminal never reaches the terminal UI, and a key typed before `s` never reaches the shell. **Within a few milliseconds of the terminal changing hands, in either direction, a key may be dropped**: the model is told of the shell's end by a message, Bubble Tea's new reader is started before that message is sent, and a key it hands over first is one of those the model drops (seen in one of ten tries on macOS and in more than half on Linux for a key typed with no pause at all behind the shell's end, and never with 5 ms between). **And a key typed in the instant before the shell takes the terminal may be the shell's or, rarely, be acted on by the terminal UI when the shell has ended**: when Bubble Tea has read it behind another key that it could not hand over before the terminal went, it is sent behind that one, and whether it or `shellEndedMsg` comes first is not fixed. Nothing in Bubble Tea tells such a key from one typed after the shell. (It is not true that no key typed for the terminal UI can reach the shell: an `Esc` typed to give a shell up, in the instant in which the terminal goes, is the shell's.)
- **A session nobody waits for is closed in a goroutine of the runner** (`(*runner).drop`), never by `Update` and never before the bounded wait at the end of `Run`: a `Close` that does not return holds up neither the terminal UI's keys nor `Run`, which waits `closeAfter` for it and no more. Inside `tea.Exec` the session is closed by the goroutine of the shell, before the terminal is taken back.
- **The connection lost between draal's answer and the terminal changing hands**: the model is at `shellOpen` and has asked for the terminal; the command finds the session's context done, writes no line, does not attach, and the line says `No shell was opened: the connection to draal was lost.` The terminal leaves the terminal UI's screen for that and comes back at once: accepted.
- **`tea.Exec` runs inside Bubble Tea's event loop**: it releases the terminal (cancels its own reader of the terminal and waits for it, stops the renderer, leaves the alternate screen, shows the cursor, restores the mode it found at its start), calls the command's `Run`, takes the terminal back (raw again, a new reader, the alternate screen, a full repaint), and sends what the callback returns as a message. So when `Run` is called the terminal is in its usual mode and on its own screen, which is what `term.Attach` expects and what it leaves. `SetStdin` and `SetStdout` would hand the command the program's own input and output: the command takes `cfg.In` and `cfg.Out`, which are the same files.
- **While the shell has the terminal the event loop is blocked**: `Update` is not called, nothing is drawn, and every `Send` waits. The runner of Task 7 stands that (the watch is read on and the newest fleet kept, a followed log is read on and its newest 400 lines kept, the ticker drops its ticks), and it has to: the shell is a stream of the same connection, and streams of it that nobody reads would stall it. After the shell the model gets, in no fixed order, the message that the shell has ended and what waited: one or two older fleets and then the newest, a batch of the log. `shellEnded` asks anew for what the open view holds (`again`), with a new number for the log, so that a batch of the old one is dropped, and `logBuffer.resume` keeps a line from being drawn twice when draal sends the ring again. A connection that was lost meanwhile is said by the `lostMsg` that waited; when it is taken first, `again` asks for nothing.
- **The context that ends `Run` ends the shell.** Nothing else could: the program takes no message, the request to quit among them. `Attach` is given a context that is done when the context of `Run` is, or when the connection the shell was asked over is hung up. A request for a session that is under way then is ended by its connection's context at the end of `Run`, like any other request; an `Operator` that does not honour it is left behind after `closeAfter` (Task 7), and its session, should it come, is closed.
- **Inside `tea.Exec` the shell is a goroutine that the command waits for**, and not the command itself, so that the command can return without it. `Attach` returns at once when its context is done. What may not return is a session whose `Resize` does not return at the start of `Attach`, with the terminal already raw. `graceOfShell()` after the context of `Run` is done the command puts the terminal back in the mode it was given away in and returns; Bubble Tea takes the terminal back and the program quits in its usual way, with the terminal as it was. The goroutine is left to the end of `Run`, which waits `closeAfter` for it. **Accepted:** when such a shell comes loose later, `Attach` puts the terminal's mode back once more as it returns (the usual mode, which it found), possibly after `Run` has returned. The `Shell` of the real client cannot wait in `Resize` there, since nothing was sent on its stream before; it takes an `Operator` that misbehaves, and the process ends right after `Run`.
- **The kill of Task 7 must never come while a shell has the terminal**: it would draw the terminal UI over the shell and set the terminal's mode under `term.Attach`, and during `tea.Exec` the program's quit is not taken, so nothing would hold the kill back. So a shell is left well before it: after `graceOfShell()`, which is `shellGrace` and never more than half of `killAfter`, whatever the two are set to. `TestShellIsLeftBeforeTheKill` holds the two together, and `TestShellThatDoesNotEndWithTheContext` that such a program has ended by itself, with the plain error of its context, before the kill would fire.
- **A panic while the shell has the terminal** (of the session, or of `Attach`, which restores the terminal and panics on) is recovered in the goroutine of the shell and becomes `shellCrashedMsg`, which `Update` takes itself and never hands to the model: the program quits as after a panic of its own (Task 7), and `Run` returns `the terminal UI failed, and the terminal was restored: panic: …`. The same when Bubble Tea could not give the terminal away or take it back.
- **The session is closed on every way out** (the deferred `Close` in `attach`; `giveUp`; the end of `Run`), which hangs the shell up: a shell nobody is joined to must not stay in a container.
- **The line above the shell** is written on the terminal's own screen when the terminal has been given away: what the shell writes lands there, under it, and stays in the terminal's scrollback when the terminal UI has ended.
- **`TERM`** of the request is that of the environment, and the size that of the terminal when draal is asked, as `great-machine shell` has them; `Attach` sends the size again when it begins.

**The three traps of the race detector in Bubble Tea 2.0.10, and how the tests stand to them.** None of them is in code of this repository, and each is reported on every run that meets it.
1. `tea.WithContext` ends the program by closing its reader of the terminal under the read. Not used: `run` asks the program to quit (Task 7). The tests here end programs through `run` alone.
2. A `SIGWINCH` while a command of `tea.Exec` runs makes Bubble Tea write the size it holds from two goroutines. `TestTerminalResizedWhileAShellIsOpen` changes the size of the pseudo-terminal **without the signal** (a test's process is not in the foreground of its pseudo-terminal, so none is sent), and holds what is the terminal UI's own: after the shell, the view is drawn for the size the terminal has then. It does not hold that the session is told (that is `term`'s test), and no test in this process sends the signal during a shell. Step 10 does both at once, by hand, in a real terminal. (Two shells back to back, which `ss` used to open, met the same race; one shell is asked for now.)
3. After a context that ended or a panic around a shell, a goroutine of Bubble Tea (`checkResize`, started when the terminal is taken back) reads the descriptor of the output file and ends, and nothing orders that read before a `Close` of the file by the test: the detector reported it once in some two hundred runs, also when the test waited until no goroutine of Bubble Tea was left, since a goroutine that has ended orders nothing. The tests therefore never close the file the terminal UI ran on: `tuitest.Release` points its descriptor at `/dev/null`, which closes the terminal and writes nothing to the file, and leaves the file to the garbage collector. `TestShellEndsWithTheContext`, `TestShellThatDoesNotEndWithTheContext` and `TestShellPanics` are the tests that meet this, and they do end programs around a shell: nothing is avoided.

**The tests wait on conditions, not on time.** Keys that the model drops show in nothing that is drawn; `keysSeen` counts the keys the program takes (through `filterForTests` of Task 7), and a test waits for that count. `TestKeyHeldWhenTheTerminalIsGivenAway` holds the program at draal's answer with the same filter, and waits until a goroutine of Bubble Tea's reader stands in its send of the key (it reads the stacks) before it lets the program go on.

- [ ] **Step 1: Write the failing test of the pseudo-terminal of the tests**

Create `tui/tuitest/tuitest_test.go`:

```go
package tuitest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/tui/tuitest"
	"github.com/fables-for-robots/great-machine/wire"
)

// emptyDraal is a draal without a service, and without anything else.
type emptyDraal struct{}

func (emptyDraal) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	fleets := make(chan wire.Fleet, 1)
	fleets <- wire.Fleet{}
	go func() {
		<-ctx.Done()
		close(fleets)
	}()
	return fleets, nil
}

func (emptyDraal) Rollouts(context.Context, string) ([]wire.RolloutInfo, error) { return nil, nil }

func (emptyDraal) History(context.Context, wire.ID) ([]wire.Sample, error) { return nil, nil }

func (emptyDraal) Logs(context.Context, wire.ID, bool) (<-chan wire.LogLine, error) {
	return nil, wire.Errorf(wire.CodeUnavailable, "there is no zathras")
}

func (emptyDraal) Shell(context.Context, wire.ID, wire.ShellRequest) (tui.Shell, error) {
	return nil, wire.Errorf(wire.CodeUnavailable, "there is no zathras")
}

func (emptyDraal) Close() error { return nil }

// A terminal UI runs in a Terminal: what it draws is the screen, keys
// reach it, a size that changes is drawn, and when it has ended the
// terminal is as it was. And a terminal that was left in another mode
// is told apart from one that was restored.
func TestTerminal(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	term := tuitest.Open(t, 80, 24)
	if !term.Usual() || term.OnAlternateScreen() || term.Written() != "" {
		t.Fatalf("a terminal that was just opened is not in its usual mode, or was written to: %q", term.Written())
	}
	ended := make(chan error, 1)
	go func() {
		ended <- tui.RunDrawn(t.Context(), tui.Config{
			Dial: func(context.Context) (tui.Operator, error) { return emptyDraal{}, nil },
			In:   term.File(), Out: term.File(), Draal: "3f9a2c1b",
		}, term.Drawn)
	}()
	screen := term.Wait("draal 3f9a2c1b · ● live", "draal knows no service yet.")
	if rows := strings.Split(screen, "\n"); len(rows) != 24 {
		t.Errorf("the screen has %d rows, want 24:\n%s", len(rows), screen)
	}
	if term.Usual() {
		t.Error("the terminal is in its usual mode while a terminal UI runs in it")
	}
	term.WaitWritten(0, "\x1b[?1049h")
	if !term.OnAlternateScreen() {
		t.Error("the terminal UI is not on the alternate screen")
	}
	term.Type("?")
	term.Wait("open what is chosen")
	term.Type("?")
	// Bubble Tea is told of a new size by a signal, which a test's
	// process does not get from its pseudo-terminal: it asks the terminal
	// for its size when it takes the terminal back from a shell, and at
	// its start.
	term.Resize(100, 30)
	term.Type("q")
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("RunDrawn = %v, want nil when the user quits", err)
		}
	case <-time.After(tuitest.Deadline):
		t.Fatal("timed out waiting for the terminal UI to end")
	}
	term.Restored()
	if left := tuitest.GoroutinesLeft(); len(left) != 0 {
		// Open's cleanup waits for them; here it is enough that they are
		// told: a stack of a goroutine names the package it is of.
		for _, g := range left {
			if !strings.Contains(g, "bubbletea") && !strings.Contains(g, "great-machine/tui.") && !strings.Contains(g, "cancelreader") && !strings.Contains(g, "ultraviolet") && !strings.Contains(g, "great-machine/term.") {
				t.Errorf("a goroutine that is none of the terminal UI is counted as one:\n%s", g)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/tuitest/`
Expected: FAIL, there is no such package yet:

```
github.com/fables-for-robots/great-machine/tui/tuitest: no non-test Go files in tui/tuitest
FAIL	github.com/fables-for-robots/great-machine/tui/tuitest [build failed]
FAIL
```

- [ ] **Step 3: Write `RunDrawn` and the package `tuitest`**

Create `tui/drawn.go`:

```go
package tui

import "context"

// RunDrawn is Run for the tests of the packages that run the terminal UI
// as a whole: drawn is called with every view, as it is handed to the
// renderer. That is what a person reads on the screen once the terminal
// has caught up. What the renderer writes to the terminal is no screen:
// it writes what has changed since the last frame, in pieces, and only a
// terminal emulator could say what that adds up to.
//
// drawn is called from the goroutine that draws, one call at a time, and
// must not wait.
func RunDrawn(ctx context.Context, cfg Config, drawn func(view string)) error {
	return run(ctx, cfg, drawn)
}
```

Create `tui/tuitest/tuitest.go`:

```go
// Package tuitest is for the tests that run the terminal UI as a whole,
// in a pseudo-terminal: those of the command line and those against a
// real draal. It is part of no program.
//
// A test reads two things. The screen is what the terminal UI drew last
// (tui.RunDrawn tells it): words are looked for there. The bytes that
// were written to the pseudo-terminal are looked at for what a terminal
// is left in, and for what a shell wrote, which lands there as it came.
//
// A Terminal leaves one file descriptor to the garbage collector when
// its test ends (see Release), which closes it when it gets to it. With
// a very large -count the descriptors of a test process may so run past
// 1023 for a while, and term.Attach refuses a terminal whose descriptor
// is 1024 or more: a test that fails with that needs a smaller -count,
// and nothing else.
package tuitest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	xterm "golang.org/x/term"
)

// Deadline is how long a Terminal waits for something that is to happen.
const Deadline = 60 * time.Second

// The sequences by which a terminal is told to show its alternate
// screen and to leave it, and to hide its cursor and to show it.
const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	cursorOff    = "\x1b[?25l"
	cursorOn     = "\x1b[?25h"
)

// Terminal is a pseudo-terminal that a terminal UI under test runs in,
// with everything that was written to it and the view that was drawn
// last.
type Terminal struct {
	t      testing.TB
	master *os.File
	slave  *os.File
	before *xterm.State

	mu     sync.Mutex
	out    bytes.Buffer
	screen string
}

// Open opens a pseudo-terminal of cols columns and rows rows. It is
// given up when the test ends, once no goroutine of the terminal UI is
// left: the test fails if one stays.
//
// A test that uses a Terminal must not run in parallel with another:
// the goroutines that are looked for are those of the whole process.
func Open(t testing.TB, cols, rows uint16) *Terminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("opening a pseudo-terminal: %v", err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	before, err := xterm.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatalf("reading the mode of the pseudo-terminal: %v", err)
	}
	term := &Terminal{t: t, master: master, slave: slave, before: before}
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			term.mu.Lock()
			term.out.Write(buf[:n])
			term.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		// The terminal is closed only when nothing of the terminal UI is
		// left: a goroutine of Bubble Tea may ask the terminal for its
		// size for a moment after the program has ended.
		var left []string
		for end := time.Now().Add(Deadline); ; time.Sleep(5 * time.Millisecond) {
			if left = GoroutinesLeft(); len(left) == 0 {
				break
			}
			if time.Now().After(end) {
				t.Errorf("%d goroutines of the terminal UI are left:\n%s", len(left), strings.Join(left, "\n\n"))
				break
			}
		}
		Release(t, slave)
		master.Close()
		select {
		case <-read:
		case <-time.After(Deadline):
			t.Error("timed out waiting for the pseudo-terminal to be read to its end")
		}
	})
	return term
}

// Release gives up a terminal that a terminal UI ran on: f, the
// terminal's own file, is a file of nothing from here on, and the
// terminal is closed, as far as f kept it open.
//
// f is not closed, but left to the garbage collector. A goroutine of
// Bubble Tea that has ended (the one that asks the terminal for its size
// when it is taken back from a shell) may have read the descriptor of f
// last without anything that orders that read before a Close of the
// test, and the race detector reports the two as a race. Nothing is
// written to f here, and what the collector closes it orders behind
// everything.
func Release(t testing.TB, f *os.File) {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Errorf("opening %s: %v", os.DevNull, err)
		return
	}
	defer null.Close()
	if err := dup(int(null.Fd()), int(f.Fd())); err != nil {
		t.Errorf("giving up the pseudo-terminal: %v", err)
	}
}

// GoroutinesLeft returns the stacks of the goroutines that are of a
// terminal UI: of the package tui, of what joins a terminal to a shell,
// of Bubble Tea and of what reads the terminal for it.
func GoroutinesLeft() []string {
	buf := make([]byte, 8<<20)
	var left []string
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		for _, of := range []string{"great-machine/tui.", "great-machine/term.", "charm.land/bubbletea", "charmbracelet/ultraviolet", "muesli/cancelreader"} {
			if strings.Contains(g, of) {
				left = append(left, g)
				break
			}
		}
	}
	return left
}

// File returns the terminal: what a terminal UI is given as its input
// and its output.
func (term *Terminal) File() *os.File { return term.slave }

// Drawn takes a view: it is what tui.RunDrawn is given.
func (term *Terminal) Drawn(view string) {
	term.mu.Lock()
	term.screen = view
	term.mu.Unlock()
}

// Screen returns what a person reads on the screen now: the view that
// was drawn last, without its colours.
func (term *Terminal) Screen() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return ansi.Strip(term.screen)
}

// Wait waits until the screen says all of the words, and returns the
// screen that does. A sentence may be broken over lines.
func (term *Terminal) Wait(words ...string) string {
	term.t.Helper()
	return term.WaitFor(fmt.Sprintf("say %q", words), func(screen string) bool {
		said := strings.Join(strings.Fields(screen), " ")
		for _, w := range words {
			if !strings.Contains(said, w) {
				return false
			}
		}
		return true
	})
}

// WaitFor waits until the screen is one that cond holds of, and returns
// it. what says what is waited for, behind "the screen to".
func (term *Terminal) WaitFor(what string, cond func(screen string) bool) string {
	term.t.Helper()
	for end := time.Now().Add(Deadline); ; time.Sleep(5 * time.Millisecond) {
		screen := term.Screen()
		if cond(screen) {
			return screen
		}
		if time.Now().After(end) {
			term.t.Fatalf("timed out waiting for the screen to %s. It says:\n%s", what, screen)
		}
	}
}

// Type types into the terminal.
func (term *Terminal) Type(keys string) {
	term.t.Helper()
	if _, err := term.master.WriteString(keys); err != nil {
		term.t.Fatalf("typing: %v", err)
	}
}

// Resize gives the terminal another size. No signal comes with it: the
// test's process is not in the foreground of this terminal.
func (term *Terminal) Resize(cols, rows uint16) {
	term.t.Helper()
	if err := pty.Setsize(term.master, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		term.t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
}

// Written returns every byte that was written to the terminal so far.
func (term *Terminal) Written() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.out.String()
}

// WaitWritten waits until text was written to the terminal behind the
// place from, and returns the place behind it.
func (term *Terminal) WaitWritten(from int, text string) int {
	term.t.Helper()
	for end := time.Now().Add(Deadline); ; time.Sleep(5 * time.Millisecond) {
		written := term.Written()
		if at := strings.Index(written[min(from, len(written)):], text); at >= 0 {
			return from + at + len(text)
		}
		if time.Now().After(end) {
			term.t.Fatalf("timed out waiting for %q to be written to the terminal. Since then this was written:\n%q", text, written[min(from, len(written)):])
		}
	}
}

// Usual says whether the terminal is in the mode it was opened in: the
// usual mode of a terminal, in which it echoes and hands on whole lines.
func (term *Terminal) Usual() bool {
	term.t.Helper()
	now, err := xterm.GetState(int(term.slave.Fd()))
	if err != nil {
		term.t.Fatalf("reading the mode of the pseudo-terminal: %v", err)
	}
	return reflect.DeepEqual(now, term.before)
}

// OnAlternateScreen says whether the last the terminal was told about
// its alternate screen is to show it.
func (term *Terminal) OnAlternateScreen() bool {
	out := term.Written()
	return strings.LastIndex(out, altScreenOn) > strings.LastIndex(out, altScreenOff)
}

// Restored checks that the terminal is as it was before the terminal UI
// ran in it, which has ended: it is in its usual mode, the alternate
// screen was entered and is left, the cursor is shown, the mouse was
// never asked for, and what is typed now is read by whoever reads the
// terminal next.
func (term *Terminal) Restored() {
	term.t.Helper()
	if !term.Usual() {
		term.t.Error("the terminal is in another mode than before the terminal UI")
	}
	// What was written is read to its end before it is judged.
	var out string
	for end := time.Now().Add(Deadline); ; time.Sleep(5 * time.Millisecond) {
		out = term.Written()
		if strings.LastIndex(out, altScreenOff) > strings.LastIndex(out, altScreenOn) && strings.LastIndex(out, cursorOn) > strings.LastIndex(out, cursorOff) {
			break
		}
		if time.Now().After(end) {
			term.t.Errorf("the terminal was not restored: the alternate screen is not left, or the cursor is not shown. The end of what was written:\n%q", out[max(len(out)-400, 0):])
			break
		}
	}
	if !strings.Contains(out, altScreenOn) {
		term.t.Error("the alternate screen was never entered")
	}
	for _, mode := range []string{"\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h", "\x1b[?1006h"} {
		if strings.Contains(out, mode) {
			term.t.Errorf("the mouse was asked for: %q", mode)
		}
	}
	term.Type("next\n")
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(term.slave, buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if s != "next\n" {
			term.t.Errorf("the next reader of the terminal read %q, want what was typed", s)
		}
	case <-time.After(Deadline):
		term.t.Error("what was typed after the terminal UI ended did not reach the next reader of the terminal")
	}
}
```

Create `tui/tuitest/dup_linux.go` (Linux on arm64 has no `dup2`):

```go
package tuitest

import "syscall"

// dup makes the descriptor to a copy of from, closing what to was.
func dup(from, to int) error {
	return syscall.Dup3(from, to, 0)
}
```

Create `tui/tuitest/dup_other.go`:

```go
//go:build !linux

package tuitest

import "syscall"

// dup makes the descriptor to a copy of from, closing what to was.
func dup(from, to int) error {
	return syscall.Dup2(from, to)
}
```

- [ ] **Step 4: Run the test to see it pass**

Run: `nix develop .#bare --command go test -race -count=20 ./tui/tuitest/`
Expected: `ok`

```bash
git add tui/drawn.go tui/tuitest
git commit -m "TUI: a pseudo-terminal for the tests that run the terminal UI as a whole, and Run with every view told"
```

- [ ] **Step 5: Write the failing tests of the shell**

Create `tui/shell_test.go`:

```go
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/wire"
)

// The line that says how a shell ended, for every way it can end.
func TestShellSentence(t *testing.T) {
	refused := wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected")
	for _, tt := range []struct {
		name string
		msg  shellEndedMsg
		want string
	}{
		{"it ended well", shellEndedMsg{opened: true}, "The shell ended."},
		{"it ended with a status", shellEndedMsg{opened: true, exit: 3}, "The shell ended with status 3."},
		{"a signal ended it", shellEndedMsg{opened: true, exit: -1}, "The shell was ended by a signal."},
		{"it was left", shellEndedMsg{opened: true, detached: true}, "The shell was left with Ctrl-], which hung it up."},
		{"draal refused it", shellEndedMsg{err: fmt.Errorf("operator: shell: %w", refused)}, "No shell was opened: zathras 20ab12cd is not connected"},
		{"draal could not be asked", shellEndedMsg{err: errors.New("operator: shell: the connection ended")}, "No shell was opened: operator: shell: the connection ended"},
		{"its zathras went", shellEndedMsg{opened: true, err: fmt.Errorf("term: reading from the shell: %w", wire.Errorf(wire.CodeUnavailable, "the zathras has disconnected"))},
			"The shell ended: the zathras has disconnected"},
		{"the session failed", shellEndedMsg{opened: true, err: errors.New("term: reading from the shell: the stream was reset")},
			"The shell ended: term: reading from the shell: the stream was reset"},
		// Review Focus 2: what a refusal says is drawn as text.
		{"a refusal with a title in it", shellEndedMsg{err: wire.Errorf(wire.CodeUnavailable, "no \x1b]0;pwned\x07shell\x1b[2J here")}, "No shell was opened: no shell here"},
	} {
		if got := shellSentence(tt.msg); got != tt.want {
			t.Errorf("%s: the line is %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A shell is opened in two steps. The key asks draal for a session and
// says so; draal's answer asks for the terminal to be given to it, by
// the name the zathras is drawn with; the message that the shell has
// ended is said in one line, which the next key takes away, and what the
// view holds is asked for anew. From the key until the end no key is
// acted on but Esc, which gives up a shell that draal has not opened.
func TestShellKeyAndItsEnd(t *testing.T) {
	m, _ := onZathras(80, 24, "web", 0)
	m.seen.Name = "web-\x1b[2J7d9f"
	id := m.logID
	wantEffects(t, "s", press(m, "s"), effShell{epoch: 1, zathras: idOf(0x20), n: 1})
	opening := "Opening a shell on web-7d9f … esc gives it up"
	if m.notice != opening || !strings.Contains(plain(m.view()), " "+opening) {
		t.Errorf("while draal is asked the line is %q, and the view:\n%s", m.notice, plain(m.view()))
	}
	// The line of the keys names the one key that does something now.
	if rows := strings.Split(plain(m.view()), "\n"); strings.TrimSpace(rows[len(rows)-1]) != "esc give the shell up" {
		t.Errorf("while draal is asked the keys are said as %q", rows[len(rows)-1])
	}
	// Keys meanwhile: none is acted on, none takes the line away, and q
	// does not quit.
	at := m.logTop
	wantEffects(t, "keys while the shell is opened", press(m, "s", "q", "ctrl+c", "?", "k", "left", "enter", "G"))
	if m.notice != opening || m.screen != screenZathras || m.help || m.logTop != at || m.sh.n != 1 {
		t.Errorf("keys were acted on while a shell was opened: the line is %q, on %v, help %v", m.notice, m.screen, m.help)
	}
	// An answer to another request is not read.
	wantEffects(t, "the answer to a shell nobody asked for", m.update(shellOpenedMsg{n: 7}))
	wantEffects(t, "draal has opened the session", m.update(shellOpenedMsg{n: 1}), effAttach{n: 1, name: "web-7d9f"})
	if m.notice != "" {
		t.Errorf("when the terminal is given away the line is %q", m.notice)
	}
	// Keys that come before the model is told of the shell's end (one
	// that Bubble Tea's reader held when it gave the terminal away) are
	// dropped as well, Esc among them.
	wantEffects(t, "keys while the shell is open", press(m, "q", "esc", "s"))
	wantEffects(t, "the shell has ended", m.update(shellEndedMsg{opened: true, exit: 3}),
		effHistory{epoch: 1, zathras: idOf(0x20)}, effFollow{epoch: 1, zathras: idOf(0x20), id: id + 1})
	if m.notice != "The shell ended with status 3." {
		t.Errorf("after the shell the line is %q", m.notice)
	}
	if !strings.Contains(plain(m.view()), " The shell ended with status 3.") {
		t.Errorf("the line is not drawn:\n%s", plain(m.view()))
	}
	press(m, "j")
	if m.notice != "" {
		t.Errorf("after the next key the line is still %q", m.notice)
	}
	// And the keys are the model's again: the next s is the second shell.
	wantEffects(t, "s again", press(m, "s"), effShell{epoch: 1, zathras: idOf(0x20), n: 2})

	// Esc gives the opening up: the program is told, the line says so,
	// the view stays, and draal's answer, when it comes, is not read.
	wantEffects(t, "esc while the shell is opened", press(m, "esc"), effGiveUp{n: 2})
	if m.notice != "The shell was given up before draal had opened it." || m.screen != screenZathras {
		t.Errorf("after esc the line is %q, on %v", m.notice, m.screen)
	}
	wantEffects(t, "the answer to a shell that was given up", m.update(shellOpenedMsg{n: 2}))
	wantEffects(t, "the keys are the model's again", press(m, "esc"), effUnfollow{})

	// draal refuses: its sentence, and no terminal is asked for.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	wantEffects(t, "draal refuses", m.update(shellOpenedMsg{n: 1, err: fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected"))}))
	if m.notice != "No shell was opened: zathras 20ab12cd is not connected" || m.sh.stage != shellNone {
		t.Errorf("after a refusal the line is %q", m.notice)
	}

	// The connection is lost while draal is asked: the shell is given
	// up with the connection, and says so.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	effs := m.update(lostMsg{epoch: 1})
	if len(effs) == 0 || effs[0] != (effGiveUp{n: 1}) || m.notice != "No shell was opened: the connection to draal was lost." || m.sh.stage != shellNone {
		t.Errorf("the connection lost while a shell is opened: the model wants %+v and says %q", effs, m.notice)
	}

	// The connection was lost while the shell was open, and the model
	// was told first: the line is said all the same, and nothing is
	// asked of a connection that is gone.
	m, _ = onZathras(80, 24, "web", 0)
	press(m, "s")
	m.update(shellOpenedMsg{n: 1})
	m.update(lostMsg{epoch: 1})
	wantEffects(t, "the shell has ended without a connection", m.update(shellEndedMsg{opened: true, err: errors.New("the stream was reset")}))
	if m.notice != "The shell ended: the stream was reset" || m.conn != connLost {
		t.Errorf("the line is %q and the connection %v", m.notice, m.conn)
	}

	// What a shell failed with may be long: it is said in three lines at
	// most, and the view keeps to the size of the terminal.
	m.update(shellEndedMsg{opened: true, err: errors.New(strings.Repeat("the stream was reset by the peer; ", 12))})
	if got := len(m.noticeLines()); got != 3 {
		t.Errorf("a long error is said in %d lines, want 3", got)
	}
	if rows := strings.Split(plain(m.view()), "\n"); len(rows) != 24 || !strings.HasSuffix(strings.TrimSpace(rows[22]), "…") {
		t.Errorf("with a long error the view has %d rows, and its last line of it is %q", len(rows), rows[min(22, len(rows)-1)])
	}
}

// fakeShell is a session the test plays the shell of.
type fakeShell struct {
	out  chan []byte   // what the shell writes
	end  chan struct{} // closed by finish: the shell has ended
	echo bool          // what is typed is written back

	// How it ends, set before finish.
	exit   int
	fail   error // what Read returns in place of the end
	panics bool  // Read panics

	// hold, when set, is waited for by Close: the test is then between
	// term.Attach, which has returned, and Bubble Tea, which has not yet
	// taken the terminal back.
	hold chan struct{}
	// stuck, when set, is waited for by Resize, whatever else happens: a
	// session that does not return.
	stuck   chan struct{}
	closing chan struct{} // closed when Close is called
	closed  chan struct{} // closed when Close returns

	mu    sync.Mutex
	input []byte
	sizes [][2]uint16
	once  sync.Once
	ended sync.Once
}

func newFakeShell() *fakeShell {
	return &fakeShell{out: make(chan []byte, 64), end: make(chan struct{}), closing: make(chan struct{}), closed: make(chan struct{})}
}

func (f *fakeShell) Read(p []byte) (int, error) {
	// What was written before the end is read before it.
	select {
	case b := <-f.out:
		return copy(p, b), nil
	default:
	}
	select {
	case b := <-f.out:
		return copy(p, b), nil
	case <-f.end:
		switch {
		case f.panics:
			panic("the session went wrong")
		case f.fail != nil:
			return 0, f.fail
		}
		return 0, io.EOF
	case <-f.closed:
		return 0, errors.New("the session is closed")
	}
}

func (f *fakeShell) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.input = append(f.input, p...)
	f.mu.Unlock()
	if f.echo {
		select {
		case f.out <- append([]byte(nil), p...):
		case <-f.closed:
		}
	}
	return len(p), nil
}

func (f *fakeShell) Resize(cols, rows uint16) error {
	f.mu.Lock()
	f.sizes = append(f.sizes, [2]uint16{cols, rows})
	f.mu.Unlock()
	if f.stuck != nil {
		<-f.stuck
	}
	return nil
}

func (f *fakeShell) Wait(ctx context.Context) (int, error) {
	select {
	case <-f.end:
		return f.exit, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (f *fakeShell) Close() error {
	f.once.Do(func() {
		close(f.closing)
		if f.hold != nil {
			<-f.hold
		}
		close(f.closed)
	})
	return nil
}

// finish ends the shell.
func (f *fakeShell) finish() { f.ended.Do(func() { close(f.end) }) }

// got returns what was typed into the shell.
func (f *fakeShell) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

// attached waits until the terminal is joined to the shell: term.Attach
// has made it raw and has sent its size.
func (f *fakeShell) attached(t *testing.T) [2]uint16 {
	t.Helper()
	var size [2]uint16
	await(t, "the terminal to be joined to the shell", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.sizes) == 0 {
			return false
		}
		size = f.sizes[0]
		return true
	})
	return size
}

// isClosed waits until the session is closed.
func (f *fakeShell) isClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the session to be closed")
	}
}

// shellsOf makes op answer Shell with the sessions, one after the other,
// and returns where the requests are noted.
func shellsOf(op *fakeOperator, sessions ...*fakeShell) *[]wire.ShellRequest {
	var mu sync.Mutex
	reqs := new([]wire.ShellRequest)
	op.shell = func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(sessions) == 0 {
			return nil, wire.Errorf(wire.CodeUnavailable, "the test has no shell left")
		}
		*reqs = append(*reqs, req)
		sh := sessions[0]
		sessions = sessions[1:]
		return sh, nil
	}
	return reqs
}

// onZathrasOfRun brings a terminal UI that runs to the view of the first
// zathras of web, with its log asked for, and returns the channel its
// log is fed with.
func onZathrasOfRun(t *testing.T, term *terminal, op *fakeOperator) chan wire.LogLine {
	t.Helper()
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jjll")
	term.wait("20ab12cd34ab12cd")
	return op.linesOf(t, idOf(0x20))
}

// shellLine is what is written above a shell of the first zathras of
// web.
const shellLine = "great-machine: a shell on web-7d9f8b6c4-2xkqp. Ctrl-] leaves it."

// mode returns the mode the terminal is in.
func (term *terminal) mode() *xterm.State {
	term.t.Helper()
	state, err := xterm.GetState(int(term.slave.Fd()))
	if err != nil {
		term.t.Fatalf("reading the mode of the pseudo-terminal: %v", err)
	}
	return state
}

// waitWritten waits until text was written to the terminal behind the
// place from, and returns where it ends.
func (term *terminal) waitWritten(from int, text string) int {
	term.t.Helper()
	at := -1
	await(term.t, fmt.Sprintf("%q to be written to the terminal", text), func() bool {
		at = strings.Index(term.written()[from:], text)
		return at >= 0
	})
	return from + at + len(text)
}

// nextReader checks that what is typed after the terminal UI has ended
// is read by whoever reads the terminal next: Review Focus 1.
func (term *terminal) nextReader() {
	term.t.Helper()
	term.typeKeys("next\n")
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(term.slave, buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if s != "next\n" {
			term.t.Errorf("the next reader of the terminal read %q, want what was typed", s)
		}
	case <-time.After(deadline):
		term.t.Error("what was typed after the terminal UI ended did not reach the next reader of the terminal")
	}
}

// Review Focus 1, a shell from its key to its end: the terminal is the
// shell's while it is open, and the terminal UI's again afterwards. No
// key is lost on either side: what is typed during the shell reaches the
// session, all of it and nothing of it the program, and what is typed
// after it reaches the program, in order. Meanwhile the fleet and the
// log are read, and afterwards the view shows the newest fleet and no
// line twice.
func TestShellHasTheTerminalAndGivesItBack(t *testing.T) {
	t.Setenv("TERM", "xterm-test")
	op := newFakeOperator()
	sh := newFakeShell()
	sh.exit = 3
	reqs := shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	lines := onZathrasOfRun(t, term, op)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Second) }
	lines <- wire.LogLine{At: at(1), Stream: "stdout", Text: "said before the shell"}
	term.wait("said before the shell")

	mark := len(term.written())
	term.typeKeys("s")
	if size := sh.attached(t); size != [2]uint16{80, 24} {
		t.Errorf("the session was told the size %v, want 80 by 24", size)
	}
	if want := (wire.ShellRequest{Zathras: idOf(0x20), Cols: 80, Rows: 24, Term: "xterm-test"}); len(*reqs) != 1 || (*reqs)[0] != want {
		t.Errorf("draal was asked for %+v, want %+v", *reqs, want)
	}
	if reflect.DeepEqual(term.mode(), term.before) {
		t.Error("the terminal is in its usual mode while a shell is joined to it")
	}
	// The line that says whose shell this is stands on the terminal's own
	// screen: the alternate screen was left before it.
	begun := term.waitWritten(mark, shellLine)
	if out := term.written()[:begun]; strings.LastIndex(out, "\x1b[?1049l") < mark || strings.LastIndex(out, "\x1b[?1049h") > strings.LastIndex(out, "\x1b[?1049l") {
		t.Errorf("the alternate screen was not left before the shell: %q", out[mark:])
	}

	// 900 bytes are typed, among them the keys of the terminal UI, Ctrl-C
	// and sequences of keys: all of them reach the session.
	typed := strings.Repeat("xyzq\x03\x1b[A", 100)
	term.typeKeys(typed)
	await(t, "what was typed to reach the session", func() bool { return sh.got() == typed })
	sh.out <- []byte("OUTPUT-OF-THE-SHELL\r\n")
	term.waitWritten(begun, "OUTPUT-OF-THE-SHELL\r\n")

	// Meanwhile the program takes nothing, and the fleet and the log are
	// read all the same.
	for restarts := range 3 {
		f := fixtureFleet()
		f.Services[2].Zathrases[0].State.Restarts = uint64(restarts + 1)
		select {
		case op.fleets <- f:
		case <-time.After(deadline):
			t.Fatal("a fleet was not read while the shell was open")
		}
	}
	await(t, "the fleets to be read", func() bool { return len(op.fleets) == 0 })
	select {
	case lines <- wire.LogLine{At: at(2), Stream: "stdout", Text: "said during the shell"}:
	case <-time.After(deadline):
		t.Fatal("a line of the log was not read while the shell was open")
	}

	// The shell ends. Until then nothing but the shell wrote to the
	// terminal: no key was echoed, and the terminal UI drew nothing.
	sh.finish()
	term.wait("The shell ended with status 3.")
	sh.isClosed(t)
	back := term.waitWritten(begun, "\x1b[?1049h") - len("\x1b[?1049h")
	out := term.written()
	if got := strings.NewReplacer("\r", "", "\n", "").Replace(out[begun:back]); !strings.HasPrefix(got, "OUTPUT-OF-THE-SHELL") || strings.ContainsAny(strings.TrimPrefix(got, "OUTPUT-OF-THE-SHELL"), "xyzq") {
		t.Errorf("while the shell was open, this was written to the terminal behind its line: %q", out[begun:back])
	}
	if got := sh.got(); got != typed {
		t.Errorf("the session got %d bytes, and %d were typed during the shell", len(got), len(typed))
	}

	// Back in the terminal UI: the newest fleet, and what the view holds
	// asked for again. The log comes again from its start, as draal sends
	// it, and no line of it is drawn twice: not the one from before the
	// shell, and not the one that was read while the shell was open,
	// whether the program took it afterwards or not.
	term.wait("3 restarts since this zathras started")
	var again chan wire.LogLine
	await(t, "the log to be asked for again", func() bool {
		op.mu.Lock()
		defer op.mu.Unlock()
		again = op.lines[idOf(0x20)]
		return again != lines
	})
	for n, text := range []string{"said before the shell", "said during the shell", "said after the shell"} {
		again <- wire.LogLine{At: at(n + 1), Stream: "stdout", Text: text}
	}
	term.wait("said after the shell")
	for _, text := range []string{"said before the shell", "said during the shell", "said after the shell"} {
		if n := strings.Count(term.shown(), text); n != 1 {
			t.Errorf("after the shell the log says %q %d times:\n%s", text, n, term.shown())
		}
	}
	op.mu.Lock()
	asked := strings.Join(op.asked, ", ")
	op.mu.Unlock()
	if strings.Count(asked, "history 20") != 2 || strings.Count(asked, "logs 20") != 2 {
		t.Errorf("draal was asked for: %s; want the samples and the log twice, before the shell and after it", asked)
	}

	// Keys again, in order: back, down, open is the second zathras of
	// web. In any other order, or with one of them lost, it is not.
	term.typeKeys("hjl")
	term.wait("21ab12cd34ab12cd")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// Review Focus 1, the moment between the shell and the terminal UI:
// term.Attach has returned and Bubble Tea has not yet taken the terminal
// back. The terminal is in its usual mode then. What is typed then is
// not the shell's, and it is read by the terminal UI, which acts on it
// when it has been told that the shell has ended, and drops it when the
// key comes before that: the two are sent to the program at about the
// same time, and which comes first is not fixed. So the test types keys
// that do nothing either way, and holds what is fixed: the program takes
// them, the session does not, and the keys after them are acted on in
// order.
func TestKeysTypedWhileTheTerminalChangesHands(t *testing.T) {
	seen := keysSeen(t)
	op := newFakeOperator()
	sh := newFakeShell()
	sh.hold = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(sh.hold) }) }
	defer release()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	before := seen()
	term.typeKeys("s")
	sh.attached(t)
	term.typeKeys("in")
	await(t, "what was typed to reach the session", func() bool { return sh.got() == "in" })
	sh.finish()
	select {
	case <-sh.closing:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the session to be closed")
	}
	if !reflect.DeepEqual(term.mode(), term.before) {
		t.Error("between the shell and the terminal UI the terminal is not in its usual mode")
	}
	term.typeKeys("xyz")
	release()
	await(t, "the keys to be taken by the program", func() bool { return seen() == before+4 })
	if got := sh.got(); got != "in" {
		t.Errorf("the session got %q, want what was typed during the shell alone", got)
	}
	// Back, down, open is the second zathras of web. In any other order,
	// or with one of them lost, it is not.
	term.typeKeys("hjl")
	term.wait("21ab12cd34ab12cd")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// Ctrl-] leaves a shell that runs on: the session is closed, which hangs
// the shell up, and the line says so. Then two more shells from the same
// view, the last of them left as well.
func TestShellLeftWithCtrlRightBracket(t *testing.T) {
	op := newFakeOperator()
	first, second, third := newFakeShell(), newFakeShell(), newFakeShell()
	for _, sh := range []*fakeShell{first, second, third} {
		sh.echo = true
	}
	shellsOf(op, first, second, third)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)

	mark := len(term.written())
	term.typeKeys("s")
	first.attached(t)
	term.typeKeys("ls")
	mark = term.waitWritten(term.waitWritten(mark, shellLine), "ls")
	term.typeKeys("\x1d")
	term.wait("The shell was left with Ctrl-], which hung it up.")
	first.isClosed(t)
	if got := first.got(); got != "ls" {
		t.Errorf("the session got %q, want what was typed before Ctrl-]", got)
	}

	term.typeKeys("s")
	second.attached(t)
	term.typeKeys("exit")
	mark = term.waitWritten(term.waitWritten(mark, shellLine), "exit")
	second.finish()
	term.wait("The shell ended.")
	second.isClosed(t)

	term.typeKeys("s")
	third.attached(t)
	term.typeKeys("q")
	term.waitWritten(term.waitWritten(mark, shellLine), "q")
	term.typeKeys("\x1d")
	term.wait("The shell was left with Ctrl-], which hung it up.")
	third.isClosed(t)

	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// A shell that draal refuses is said as draal's sentence, and nothing is
// written above a shell that is none. Review Focus 4: a zathras that
// goes while its shell is open ends the shell with what the session
// says, and its view says that it is gone.
func TestShellRefusedAndShellOfAZathrasThatGoes(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.fail = fmt.Errorf("operator: the shell session failed: %w", wire.Errorf(wire.CodeUnavailable, "zathras web-7d9f8b6c4-2xkqp has disconnected"))
	refuse := true
	var mu sync.Mutex
	op.shell = func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
		mu.Lock()
		defer mu.Unlock()
		if refuse {
			refuse = false
			return nil, fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 20ab12cd is not connected"))
		}
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)

	// The refusal is said in the terminal UI, which never left its
	// screen for it.
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("No shell was opened: zathras 20ab12cd is not connected")
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	term.typeKeys("?")
	term.wait("Log ·")
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") || strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("for a shell that was refused the terminal UI left its screen: %q", out)
	}

	mark = len(term.written())
	term.typeKeys("s")
	sh.attached(t)
	term.waitWritten(mark, shellLine)
	// The zathras goes: draal says so in the fleet and ends the session.
	gone := fixtureFleet()
	gone.Services[2].Zathrases = gone.Services[2].Zathrases[1:]
	op.fleets <- gone
	sh.finish()
	term.wait("The shell ended: zathras web-7d9f8b6c4-2xkqp has disconnected", "This zathras is not connected to draal any more")
	sh.isClosed(t)
	// It has no shell now, and the key says so.
	term.typeKeys("s")
	term.wait("This zathras is not connected: there is no container to open a shell in.")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, with a shell open: the connection to draal is lost.
// The session ends with it, and back in the terminal UI the header says
// that the connection is lost.
func TestConnectionLostWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.fail = errors.New("operator: the shell session failed: the connection was closed")
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	close(op.fleets)
	sh.finish()
	term.wait("The shell ended: term: reading from the shell: operator: the shell session failed: the connection was closed",
		"✖ lost for", "The connection to draal is lost since")
	sh.isClosed(t)
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Fatal("the connection that was lost was not closed")
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 1: the context ends while a shell that never ends has the
// terminal. The shell ends with it, the session is closed, Run returns
// the error of the context, and the terminal is as it was.
func TestShellEndsWithTheContext(t *testing.T) {
	// Nothing but the context ends this shell: the program is not ended
	// by force, and the shell is not left behind.
	grace, kill := shellGrace, killAfter
	shellGrace, killAfter = time.Hour, time.Hour
	t.Cleanup(func() { shellGrace, killAfter = grace, kill })
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	term.typeKeys("sleep 1000\r")
	await(t, "what was typed to reach the session", func() bool { return sh.got() == "sleep 1000\r" })
	term.stop()
	if err := term.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	sh.isClosed(t)
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Error("the connection was not closed")
	}
	term.nextReader()
}

// Review Focus 1: the context ends, and the shell does not end with it:
// the session does not return from being told the size, at the start of
// term.Attach, with the terminal already in the mode of a shell. Run
// does not wait for it: it takes the terminal back after shellGrace,
// gives up what does not return after closeAfter, returns the error of
// its context, and the terminal is as it was.
//
// When the session comes loose, after Run has returned, nothing more is
// written to the terminal. That is what this test holds of that moment:
// the bytes. The mode of the terminal is set once more then, to the
// usual one, by term.Attach as it returns: see shellGrace.
func TestShellThatDoesNotEndWithTheContext(t *testing.T) {
	grace, closing := shellGrace, closeAfter
	shellGrace, closeAfter = 100*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { shellGrace, closeAfter = grace, closing })
	op := newFakeOperator()
	stuck := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(stuck) }) }
	sh := newFakeShell()
	sh.stuck = stuck
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	// Registered behind the terminal's own, and so done before it looks
	// for goroutines that are left.
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	stopped := time.Now()
	term.stop()
	// The program has ended by itself, with the terminal its own again,
	// before it would have been ended by force: what Run returns is the
	// error of its context and nothing around it.
	if err := term.end(); err != context.Canceled {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	if took := time.Since(stopped); took >= killAfter {
		t.Errorf("Run returned %v after its context was done; want it after shellGrace and closeAfter, and not ended by force", took)
	}
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	release()
	sh.isClosed(t)
	term.quietBehindTheMark()
	term.nextReader()
}

// The context ends while draal has not answered the request for a
// shell, and the request does not honour its context. The terminal was
// never given away: Run ends as it does without a shell, gives up what
// does not return after closeAfter, and a session that comes after that
// is closed, with nothing written to the terminal.
func TestContextEndsWhileDraalIsAskedForAShell(t *testing.T) {
	closing := closeAfter
	closeAfter = 300 * time.Millisecond
	t.Cleanup(func() { closeAfter = closing })
	op := newFakeOperator()
	answer := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(answer) }) }
	sh := newFakeShell()
	op.shell = func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
		<-answer
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.stop()
	if err := term.end(); err != context.Canceled {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	release()
	sh.isClosed(t)
	term.quietBehindTheMark()
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") {
		t.Errorf("a shell that nobody waited for was announced on the terminal: %q", out)
	}
	if n := len(sh.sizes); n != 0 {
		t.Errorf("the terminal was joined to a session that came after the end")
	}
	term.nextReader()
}

// A shell is left behind well before the program would be ended by
// force, whatever the two times are set to: a kill must not come while
// a shell has the terminal.
func TestShellIsLeftBeforeTheKill(t *testing.T) {
	if shellGrace >= killAfter {
		t.Errorf("shellGrace is %v and killAfter %v: a shell has to be left before the kill", shellGrace, killAfter)
	}
	grace, kill := shellGrace, killAfter
	t.Cleanup(func() { shellGrace, killAfter = grace, kill })
	for _, tt := range []struct{ grace, kill, want time.Duration }{
		{2 * time.Second, 5 * time.Second, 2 * time.Second},
		{10 * time.Second, 5 * time.Second, 2500 * time.Millisecond},
		{time.Hour, time.Hour, 30 * time.Minute},
	} {
		shellGrace, killAfter = tt.grace, tt.kill
		if got := graceOfShell(); got != tt.want || got >= killAfter {
			t.Errorf("with shellGrace %v and killAfter %v a shell is left after %v, want %v", tt.grace, tt.kill, got, tt.want)
		}
	}
}

// Review Focus 1: a panic while a shell has the terminal. term.Attach
// restores the terminal and panics on; the terminal UI takes the
// terminal back, ends as after any panic, and Run returns it as an
// error with the terminal as it was. The session is closed.
func TestShellPanics(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	sh.panics = true
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	sh.attached(t)
	sh.finish()
	err := term.end()
	if err == nil || !strings.Contains(err.Error(), "the session went wrong") || !strings.Contains(err.Error(), "the terminal was restored") {
		t.Errorf("Run = %v, want the panic as an error", err)
	}
	sh.isClosed(t)
	term.nextReader()
}

// Review Focus 5: the terminal is resized while a shell is open. Back in
// the terminal UI, the view is drawn for the size the terminal has now.
//
// The size is changed without the signal that a terminal sends with it:
// that the session is told of a resize is the test of term, and a
// SIGWINCH in the middle of a shell makes Bubble Tea write the size from
// two of its goroutines, which the race detector reports.
func TestTerminalResizedWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	if rows := strings.Count(term.shown(), "\n") + 1; rows != 24 {
		t.Fatalf("before the shell the view has %d rows, want 24", rows)
	}
	term.typeKeys("s")
	sh.attached(t)
	if err := pty.Setsize(term.master, &pty.Winsize{Cols: 120, Rows: 40}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	sh.finish()
	term.wait("The shell ended.")
	await(t, "the view to be drawn for 120 columns and 40 rows", func() bool {
		rows := strings.Split(term.shown(), "\n")
		return len(rows) == 40 && width(rows[0]) == 120
	})
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// A zathras that is not connected has no shell: the key says so, and
// draal is not asked.
func TestNoShellOnAZathrasThatIsGone(t *testing.T) {
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	gone := fixtureFleet()
	gone.Services[2].Zathrases = gone.Services[2].Zathrases[1:]
	op.fleets <- gone
	term.wait("This zathras is not connected to draal any more")
	term.typeKeys("s")
	term.wait("This zathras is not connected: there is no container to open a shell in.")
	if _, asked := op.was("shell 20"); asked {
		t.Error("draal was asked for a shell on a zathras that is gone")
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// keysSeen has the program count the keys it takes, and returns how
// many it has taken: a test waits on it for keys that the model drops,
// which show in nothing else.
func keysSeen(t *testing.T) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, key := msg.(tea.KeyPressMsg); key {
			mu.Lock()
			n++
			mu.Unlock()
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

// shellsAsked returns how many shells draal was asked for.
func (f *fakeOperator) shellsAsked() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Count(strings.Join(f.asked, ","), "shell ")
}

// Review Focus 1, while draal has not answered the request for a shell:
// the terminal is the terminal UI's, on its own screen and in its own
// mode, and the view says that a shell is being opened. What is typed
// then is nobody's: it is not echoed, the terminal UI does not act on it
// (q does not quit, s asks for no second shell), and it does not reach
// the shell when the shell is there.
func TestKeysTypedWhileAShellIsOpened(t *testing.T) {
	seen := keysSeen(t)
	op := newFakeOperator()
	sh := newFakeShell()
	answer := make(chan struct{})
	op.shell = func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
		<-answer
		return sh, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	before := seen()
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.typeKeys("sq?x\x1d")
	await(t, "the keys to be taken by the program", func() bool { return seen() == before+6 })
	if screen := term.shown(); !strings.Contains(screen, "Opening a shell on") || !strings.Contains(screen, "20ab12cd34ab12cd") {
		t.Errorf("a key typed while the shell was opened was acted on:\n%s", screen)
	}
	if out := term.written()[mark:]; strings.Contains(out, "\x1b[?1049l") || strings.Contains(out, "great-machine: a shell on") || reflect.DeepEqual(term.mode(), term.before) {
		t.Errorf("the terminal was given away before there was a session: %q", out)
	}
	close(answer)
	sh.attached(t)
	term.waitWritten(mark, shellLine)
	term.typeKeys("in")
	await(t, "what was typed to reach the session", func() bool { return sh.got() != "" })
	sh.finish()
	term.wait("The shell ended.")
	if got := sh.got(); got != "in" {
		t.Errorf("the session got %q, want what was typed when it had the terminal alone", got)
	}
	// The keys are the terminal UI's again, and one shell was asked for.
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	if n := op.shellsAsked(); n != 1 {
		t.Errorf("draal was asked for %d shells, want 1", n)
	}
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	term.nextReader()
}

// s typed twice, and s with q behind it, in one go: one shell is asked
// for, and the key behind s is dropped, whether the program takes it
// while draal is asked or after the terminal was given away.
func TestKeyTypedRightBehindS(t *testing.T) {
	for _, keys := range []string{"ss", "sq"} {
		t.Run(keys, func(t *testing.T) {
			seen := keysSeen(t)
			op := newFakeOperator()
			sh := newFakeShell()
			shellsOf(op, sh)
			term := startRun(t, oneOperator(op), nil)
			onZathrasOfRun(t, term, op)
			before := seen()
			term.typeKeys(keys)
			sh.attached(t)
			sh.finish()
			term.wait("The shell ended.")
			// The second key was read by Bubble Tea before the shell had
			// the terminal, or it would be the shell's. The program has
			// taken it by now, and dropped it.
			await(t, "both keys to be taken by the program", func() bool { return seen() == before+2 })
			if got := sh.got(); got != "" {
				t.Errorf("the session got %q, which was typed before it had the terminal", got)
			}
			term.typeKeys("?")
			term.wait("on a zathras: open a shell")
			if n := op.shellsAsked(); n != 1 {
				t.Errorf("draal was asked for %d shells, want 1", n)
			}
			term.typeKeys("?q")
			if err := term.end(); err != nil {
				t.Errorf("Run = %v, want nil", err)
			}
		})
	}
}

// A key that Bubble Tea's reader holds when the terminal is given away:
// read, and not yet handed to the program, which is busy. It is handed
// over when the program takes messages again, behind the shell, and
// before the message that the shell has ended: the model drops it.
//
// The order stands on this: the reader has waited to send the key since
// before the shell, the message of the shell's end is sent after it,
// and a Go channel serves the senders that wait in the order they came.
func TestKeyHeldWhenTheTerminalIsGivenAway(t *testing.T) {
	held := make(chan struct{})  // closed when the program is at the answer of draal
	letGo := make(chan struct{}) // closed to let it go on
	var keys []string
	var mu sync.Mutex
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg := msg.(type) {
		case shellOpenedMsg:
			close(held)
			<-letGo
		case tea.KeyPressMsg:
			mu.Lock()
			keys = append(keys, msg.String())
			mu.Unlock()
		case shellEndedMsg:
			mu.Lock()
			keys = append(keys, "(the shell has ended)")
			mu.Unlock()
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	var once sync.Once
	release := func() { once.Do(func() { close(letGo) }) }
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	waitFor(t, "the program to be at draal's answer", held)
	// The program takes nothing now. q is read by Bubble Tea's reader,
	// which then waits to hand it over.
	term.typeKeys("q")
	await(t, "Bubble Tea's reader to hold the key", func() bool {
		buf := make([]byte, 4<<20)
		for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, "[chan send") && strings.Contains(g, "TerminalReader).sendEvents") {
				return true
			}
		}
		return false
	})
	release()
	sh.attached(t)
	sh.finish()
	term.wait("The shell ended.")
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	mu.Lock()
	order := strings.Join(keys, " ")
	mu.Unlock()
	if !strings.HasSuffix(order, "s q (the shell has ended) ?") {
		t.Errorf("the program took %q; want the key that was held before the end of the shell", order)
	}
	if got := sh.got(); got != "" {
		t.Errorf("the session got %q", got)
	}
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil: the q that was held is not acted on", err)
	}
}

// Esc gives up a shell that draal has not opened: the request is ended,
// the keys are the terminal UI's again, and a session that draal opens
// all the same is closed and never has the terminal.
func TestEscGivesUpAShellThatIsOpened(t *testing.T) {
	op := newFakeOperator()
	late, second := newFakeShell(), newFakeShell()
	answer := make(chan struct{})
	ended := make(chan struct{})
	n := 0
	var mu sync.Mutex
	op.shell = func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if !first {
			return second, nil
		}
		// The request is ended by its context, and draal answers it all
		// the same, later.
		<-ctx.Done()
		close(ended)
		<-answer
		return late, nil
	}
	term := startRun(t, oneOperator(op), nil)
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up")
	term.typeKeys("\x1b")
	term.wait("The shell was given up before draal had opened it.", "20ab12cd34ab12cd")
	waitFor(t, "the request for the shell to be ended", ended)
	close(answer)
	late.isClosed(t)
	// Another shell, which has the terminal as any other.
	term.typeKeys("s")
	second.attached(t)
	second.finish()
	term.wait("The shell ended.")
	if n := len(late.sizes); n != 0 {
		t.Error("the terminal was joined to a session that was given up")
	}
	if n := strings.Count(term.written()[mark:], "great-machine: a shell on"); n != 1 {
		t.Errorf("%d shells were announced on the terminal, want the one that was opened", n)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, while draal is asked for a shell: the connection is
// lost. The shell is given up with it, the view says so, and the
// terminal was never given away.
func TestConnectionLostWhileAShellIsOpened(t *testing.T) {
	op := newFakeOperator()
	op.shell = func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		<-ctx.Done()
		return nil, fmt.Errorf("operator: shell: %w", ctx.Err())
	}
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("Opening a shell on")
	close(op.fleets)
	term.wait("No shell was opened: the connection to draal was lost.", "✖ lost for")
	if out := term.written()[mark:]; strings.Contains(out, "\x1b[?1049l") {
		t.Errorf("the terminal UI left its screen for a shell that was never opened: %q", out)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// The watch and the log are read whatever the program does: 2,000
// fleets and 20,000 lines come while a shell has the terminal and the
// program takes no message, and every one of them is taken off its
// stream. A shell needs that: it is a stream of the same connection, and
// two streams of a go-iroh connection that nobody reads stall every
// other stream of it, the shell's among them.
func TestWatchAndLogAreReadWhileAShellIsOpen(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	lines := onZathrasOfRun(t, term, op)
	lines <- wire.LogLine{At: t0, Stream: "stdout", Text: "the first line"}
	term.wait("the first line")
	term.typeKeys("s")
	sh.attached(t)
	done := make(chan string, 2)
	go func() {
		for i := range 2000 {
			f := fixtureFleet()
			f.Services[0].Stable = uint64(i + 1)
			select {
			case op.fleets <- f:
			case <-time.After(deadline):
				done <- fmt.Sprintf("fleet %d was not read while the shell was open", i)
				return
			}
		}
		done <- ""
	}()
	go func() {
		for i := range 20000 {
			select {
			case lines <- wire.LogLine{At: t0.Add(time.Duration(i+1) * time.Millisecond), Stream: "stdout", Text: fmt.Sprintf("flood %d", i)}:
			case <-time.After(deadline):
				done <- fmt.Sprintf("line %d was not read while the shell was open", i)
				return
			}
		}
		done <- ""
	}()
	for range 2 {
		if msg := <-done; msg != "" {
			t.Error(msg)
		}
	}
	await(t, "the last fleet to be taken", func() bool { return len(op.fleets) == 0 })
	select {
	case <-sh.closing:
		t.Fatal("the shell ended during the flood")
	default:
	}
	sh.finish()
	term.wait("The shell ended.")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// answerNotTaken makes the program lose draal's answer to the request
// for a shell, as when the program ends, or the shell is given up,
// before it takes the answer. The session is then held by the runner
// and nobody has the terminal joined to it. The channel is closed when
// the answer has come.
func answerNotTaken(t *testing.T) <-chan struct{} {
	t.Helper()
	came := make(chan struct{})
	var once sync.Once
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(shellOpenedMsg); ok {
			once.Do(func() { close(came) })
			return nil
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	return came
}

// Run ends while the runner holds a session that draal has opened and
// the program has not taken: the session is closed. And when its Close
// does not return, Run does not wait for it longer than closeAfter.
func TestSessionThatIsHeldWhenRunEnds(t *testing.T) {
	closing := closeAfter
	closeAfter = 300 * time.Millisecond
	t.Cleanup(func() { closeAfter = closing })
	for _, how := range []string{"its Close returns", "its Close does not return"} {
		t.Run(how, func(t *testing.T) {
			came := answerNotTaken(t)
			op := newFakeOperator()
			sh := newFakeShell()
			var once sync.Once
			release := func() {}
			if how == "its Close does not return" {
				sh.hold = make(chan struct{})
				release = func() { once.Do(func() { close(sh.hold) }) }
			}
			shellsOf(op, sh)
			term := startRun(t, oneOperator(op), nil)
			t.Cleanup(release)
			onZathrasOfRun(t, term, op)
			term.typeKeys("s")
			waitFor(t, "draal's answer", came)
			term.stop()
			if err := term.end(); err != context.Canceled {
				t.Errorf("Run = %v, want the error of its context", err)
			}
			waitFor(t, "the session that was held to be closed", sh.closing)
			release()
			sh.isClosed(t)
		})
	}
}

// Esc gives up a shell whose session the runner holds already, and the
// Close of that session does not return. The terminal UI is not held up
// by it: it takes the next key.
func TestEscWithASessionWhoseCloseDoesNotReturn(t *testing.T) {
	came := answerNotTaken(t)
	op := newFakeOperator()
	sh := newFakeShell()
	sh.hold = make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(sh.hold) }) }
	shellsOf(op, sh)
	term := startRun(t, oneOperator(op), nil)
	t.Cleanup(release)
	onZathrasOfRun(t, term, op)
	term.typeKeys("s")
	term.wait("Opening a shell on")
	waitFor(t, "draal's answer", came)
	term.typeKeys("\x1b")
	term.wait("The shell was given up before draal had opened it.")
	waitFor(t, "the session that was held to be closed", sh.closing)
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	release()
	sh.isClosed(t)
	term.typeKeys("?q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}

// Review Focus 3, between draal's answer and the terminal changing
// hands: the connection is lost, and the program is told before it
// gives the terminal away. The session of a connection that is gone is
// not given the terminal: nothing is written for it, it is closed, and
// the line says that the connection was lost.
func TestConnectionLostBetweenTheAnswerAndTheShell(t *testing.T) {
	op := newFakeOperator()
	sh := newFakeShell()
	shellsOf(op, sh)
	var once sync.Once
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(shellOpenedMsg); ok {
			once.Do(func() {
				// The watch ends, and the program is at draal's answer
				// until the message of that waits to be sent: it is then
				// taken before the one that gives the terminal away, which
				// is sent later, as a channel serves those who wait in
				// their order.
				close(op.fleets)
				await(t, "the end of the watch to wait for the program", func() bool {
					buf := make([]byte, 4<<20)
					for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
						if strings.Contains(g, "(*Program).Send") && strings.Contains(g, "tui.(*runner).dial.func") && !strings.Contains(g, "dial.func1.1") {
							return true
						}
					}
					return false
				})
			})
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, time.Hour })
	onZathrasOfRun(t, term, op)
	mark := len(term.written())
	term.typeKeys("s")
	term.wait("✖ lost for", "No shell was opened: the connection to draal was lost.")
	sh.isClosed(t)
	if out := term.written()[mark:]; strings.Contains(out, "great-machine: a shell on") || len(sh.sizes) != 0 {
		t.Errorf("the session of a connection that was lost was given the terminal: %q", out)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
}
```

Six changes of tests that are there.

1. In `tui/run_test.go`, the fake `Operator` can be given shells. Its struct gets a field behind `watch`:

```go
	watch   func() error                                                                     // what Watch answers; nil for the fleets
	shell   func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) // what Shell answers; nil for a refusal
```

(`gofmt` aligns the comments of the fields above it likewise), and its `Shell` becomes:

```go
func (f *fakeOperator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
	f.note(ctx, "shell "+string(zathras[:2]))
	if f.shell != nil {
		return f.shell(ctx, zathras, req)
	}
	return nil, wire.Errorf(wire.CodeUnavailable, "the fake has no shell")
}
```

2. In `tui/run_test.go`, in the cleanup of `startRun`, the two lines `slave.Close()` and `master.Close()` become (trap 3 above):

```go
		// The terminal's own file is given up without a Close: see
		// tuitest.Release.
		tuitest.Release(t, slave)
		master.Close()
```

and the imports of the file get `"github.com/fables-for-robots/great-machine/tui/tuitest"` before `wire`.

3. In `tui/model_test.go`, at the end of `TestKeys`, the block that begins `// s on a zathras: the place of the shell.` and goes to the end of the function becomes:

```go
	// s on a zathras that is connected asks draal for a shell, and says
	// so. On one that is gone it says so, and asks for nothing; the
	// next key takes the line away.
	m = zathras()
	wantEffects(t, "s on a zathras", press(m, "s"), effShell{epoch: 1, zathras: m.zathras, n: 1})
	if !strings.Contains(m.notice, "Opening a shell on "+m.seen.Name) {
		t.Errorf("s on a zathras says %q", m.notice)
	}
	m = zathras()
	m.present = false
	wantEffects(t, "s on a zathras that is gone", press(m, "s"))
	if !strings.Contains(m.notice, "not connected") {
		t.Errorf("s on a zathras that is gone says %q", m.notice)
	}
	press(m, "j")
	if m.notice != "" {
		t.Errorf("after the next key the notice is still %q", m.notice)
	}
}
```

and in `TestAgainAsksForWhatTheViewHolds`, `m.update(shellEndedMsg{})` becomes `m.update(shellEndedMsg{opened: true})`.

4. In `tui/scenes_test.go`, the scene `zathras-notice` becomes three scenes (and the imports get `"fmt"`):

```go
		{"zathras-notice", func(w, h int) *model {
			m, _ := onZathras(w, h, "web", 2)
			press(m, "s")
			m.update(shellOpenedMsg{n: 1})
			m.update(shellEndedMsg{opened: true, exit: 3})
			return m
		}},
		{"zathras-opening-shell", func(w, h int) *model { m, _ := onZathras(w, h, "web", 2); press(m, "s"); return m }},
		{"zathras-no-shell", func(w, h int) *model {
			m, _ := onZathras(w, h, "web", 2)
			press(m, "s")
			m.update(shellOpenedMsg{n: 1, err: fmt.Errorf("operator: shell: %w", wire.Errorf(wire.CodeUnavailable, "zathras 22ab12cd is not connected"))})
			return m
		}},
```

5. In `tui/view_test.go`, in `TestOddStatesAreSentences`, the line of `zathras-notice` becomes three:

```go
		{"zathras-notice", []string{"The shell ended with status 3."}},
		{"zathras-no-shell", []string{"No shell was opened: zathras 22ab12cd is not connected"}},
		{"zathras-opening-shell", []string{"Opening a shell on web-7d9f8b6c4-8hjwz … esc gives it up"}},
```

6. In `tui/model_test.go`, in `check` of `TestEverySizeOfTerminal`, the last line of a view is the keys, which while a shell is being opened name one key and no way to quit. The condition becomes:

```go
		} else if len(lines) != h || !strings.Contains(lines[0], "great-machine") || !strings.Contains(lines[h-1], "quit") && !strings.Contains(lines[h-1], "give the shell up") {
```

- [ ] **Step 6: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/model_test.go:191:89: unknown field n in struct literal of type effShell
tui/model_test.go:761:56: unknown field opened in struct literal of type shellEndedMsg
tui/scenes_test.go:110:13: undefined: shellOpenedMsg
tui/scenes_test.go:111:27: unknown field opened in struct literal of type shellEndedMsg
tui/scenes_test.go:118:13: undefined: shellOpenedMsg
tui/shell_test.go:30:35: unknown field opened in struct literal of type shellEndedMsg
tui/shell_test.go:31:44: unknown field opened in struct literal of type shellEndedMsg
tui/shell_test.go:32:39: unknown field opened in struct literal of type shellEndedMsg
tui/shell_test.go:33:33: unknown field opened in struct literal of type shellEndedMsg
tui/shell_test.go:36:38: unknown field opened in struct literal of type shellEndedMsg
tui/shell_test.go:36:38: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 7: Write the shell**

Replace `tui/shell.go` with:

```go
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	tea "charm.land/bubbletea/v2"
	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
)

// The shell of a zathras, which is opened in two steps, so that the
// terminal is given away only for a session that exists.
//
// First the model asks for a session (effShell), and the program asks
// draal for one while the terminal UI keeps the terminal and its screen:
// the view says that a shell is being opened, and Esc gives it up. When
// draal has opened the session the program holds it and tells the model
// (shellOpenedMsg), and only then the model asks for the terminal to be
// given to it (effAttach): Bubble Tea gives the terminal away, the
// session has it until the shell has ended or was left, and a message
// tells the model how it ended (shellEndedMsg). A session that draal
// refuses, or that comes when nobody waits for it any more, never has
// the terminal.
//
// From the key s until the shell has ended, the model acts on no key
// but Esc, and on that only while the shell is being opened. What is
// typed while the terminal UI still has the terminal is dropped: it is
// not the terminal UI's, since a shell was asked for, and not the
// shell's, which was not there. What is typed while the shell has the
// terminal is the shell's, but for Ctrl-], and what the shell writes is
// on the terminal's own screen, where it stays when the terminal UI is
// back. While the shell has the terminal the program does nothing else:
// it reads no key, draws nothing and takes no message.

// shellStage is how far a shell is that was asked for.
type shellStage int

const (
	// shellNone: no shell was asked for, or the last one has ended.
	shellNone shellStage = iota
	// shellOpening: draal was asked for a session and has not answered.
	shellOpening
	// shellOpen: the session is there, and the terminal is given to it,
	// or about to be, or was and the model has not been told of its end.
	shellOpen
)

// shellState is what the model holds of a shell: no session, but the
// number of the request for one.
type shellState struct {
	stage shellStage
	n     int    // the number of the shell that was asked for last
	name  string // how its zathras is named; clean
}

// effShell asks the program to ask draal for a shell session on the
// zathras, with the terminal's size. It answers with shellOpenedMsg of
// the same number, and holds the session until effAttach or effGiveUp.
type effShell struct {
	epoch   int
	zathras wire.ID
	n       int
}

// effGiveUp tells the program that nobody waits for the shell of this
// number any more: the request is ended, and a session that is there or
// comes yet is closed.
type effGiveUp struct{ n int }

// effAttach asks the program to give the terminal to the session of
// this number, and to take it back when the shell has ended or was
// left. It answers with shellEndedMsg.
type effAttach struct {
	n    int
	name string // how the line above the shell names the zathras; clean
}

// shellOpenedMsg says that draal has opened the session of this number,
// which the program holds, or with err why there is none.
type shellOpenedMsg struct {
	n   int
	err error
}

// shellEndedMsg says how a shell ended: with an exit status, left with
// Ctrl-], or with an error. A shell that was never opened has ended with
// the error that kept it from opening.
type shellEndedMsg struct {
	opened   bool // draal has opened the session
	exit     int
	detached bool
	err      error
}

// shellCrashedMsg says that the terminal could not be given away or
// taken back, or that something panicked while the shell had it. It is
// the program's to take, and ends it.
type shellCrashedMsg struct {
	what  any
	stack []byte
}

// openShell is the key s on a zathras that is connected, with a
// connection to draal that is live: draal is asked for the session, and
// the view says so.
func (m *model) openShell() []effect {
	m.sh.n++
	m.sh.stage, m.sh.name = shellOpening, named(m.seen.Name)
	m.notice = "Opening a shell on " + m.sh.name + " … esc gives it up"
	return []effect{effShell{epoch: m.epoch, zathras: m.zathras, n: m.sh.n}}
}

// shellTakesKey says whether a key is not the model's to act on because
// a shell was asked for and has not ended, and acts on the one that is
// then: Esc, which gives up a shell that draal has not opened yet.
//
// What this holds, and what it does not. A key typed while the shell
// has the terminal never reaches the terminal UI, and a key typed
// before s never reaches the shell. Within a few milliseconds of the
// terminal changing hands, in either direction, a key may be dropped:
// the model is told of the shell's end by a message, and a key that
// Bubble Tea hands over before that message is one of those it drops.
// A key typed in the instant before the shell takes the terminal has
// one of three ends: Bubble Tea has read it and the model drops it; or
// Bubble Tea has not read it and it is the shell's; or, rarely, Bubble
// Tea has read it behind another key that it could not hand over
// before the terminal went, and it is acted on by the terminal UI when
// the shell has ended. Nothing in Bubble Tea tells such a key from one
// that is typed after the shell.
func (m *model) shellTakesKey(name string) (effs []effect, taken bool) {
	switch {
	case m.sh.stage == shellNone:
		return nil, false
	case m.sh.stage == shellOpening && name == "esc":
		return m.giveUpShell("The shell was given up before draal had opened it."), true
	}
	return nil, true
}

// giveUpShell ends the waiting for a shell that is being opened, and
// says why.
func (m *model) giveUpShell(why string) []effect {
	if m.sh.stage != shellOpening {
		return nil
	}
	m.sh.stage, m.notice = shellNone, why
	return []effect{effGiveUp{n: m.sh.n}}
}

// shellOpened takes draal's answer to the request for a session. The
// answer to a request that was given up is not read: the program has
// closed that session, or closes it when it comes.
func (m *model) shellOpened(msg shellOpenedMsg) []effect {
	if m.sh.stage != shellOpening || msg.n != m.sh.n {
		return nil
	}
	if msg.err != nil {
		m.sh.stage, m.notice = shellNone, shellSentence(shellEndedMsg{err: msg.err})
		return nil
	}
	m.sh.stage, m.notice = shellOpen, ""
	return []effect{effAttach{n: msg.n, name: m.sh.name}}
}

// shellEnded takes the message that a shell has ended, and says in one
// line how. While the shell had the terminal the model was told nothing,
// for as long as that was: what the view holds is asked for anew.
func (m *model) shellEnded(msg shellEndedMsg) []effect {
	m.sh.stage, m.notice = shellNone, shellSentence(msg)
	return m.again()
}

// shellSentence is the one line that says how a shell ended.
func shellSentence(msg shellEndedMsg) string {
	why := ""
	if msg.err != nil {
		// What draal or the zathras refused is said as its sentence; of
		// anything else, the whole error.
		why = clean(msg.err.Error())
		if e, ok := refusal(msg.err); ok {
			why = clean(e.Message)
		}
	}
	switch {
	case !msg.opened:
		return "No shell was opened: " + why
	case msg.detached:
		return "The shell was left with Ctrl-], which hung it up."
	case msg.err != nil:
		return "The shell ended: " + why
	case msg.exit < 0:
		return "The shell was ended by a signal."
	case msg.exit > 0:
		return fmt.Sprintf("The shell ended with status %d.", msg.exit)
	}
	return "The shell ended."
}

// shellGrace is how long a shell has to give the terminal back once the
// context of Run is done. term.Attach returns at once then; what does
// not return is a session that does not return from being told the size
// at the start of term.Attach, with the terminal already in the mode of
// a shell. Behind the grace the terminal is taken back without waiting,
// so that the program ends as it does without a shell.
//
// A shell that is left behind so sets the mode of the terminal once
// more when it comes loose, which may be after Run has returned: that is
// term.Attach putting back the mode it found, the terminal's usual one.
// A Shell of the client cannot wait there, since nothing was sent on its
// stream before; it takes an Operator that misbehaves.
//
// The grace has to be over well before killAfter, which is when a
// program that has not ended is ended by force: a kill that came while a
// shell had the terminal would draw over the shell and change the
// terminal's mode under term.Attach. graceOfShell sees to it whatever
// the two are set to.
var shellGrace = 2 * time.Second

// graceOfShell is shellGrace, and at most half of killAfter.
func graceOfShell() time.Duration {
	return min(shellGrace, killAfter/2)
}

// pendingShell is the shell that draal was asked for last and that has
// not been given the terminal: the request, and the session once it is
// there.
type pendingShell struct {
	n      int
	ctx    context.Context // done when the connection it was asked over is hung up
	cancel context.CancelFunc
	sess   Shell // nil until draal has opened it
}

// drop ends a shell that nobody waits for: the request is ended at
// once, and the session, if it is there, is closed in a goroutine of
// the runner. A Close that does not return so holds up neither Update,
// which gives a shell up, nor the end of Run, which waits closeAfter for
// what the runner started and no more.
func (r *runner) drop(p *pendingShell) {
	if p == nil {
		return
	}
	p.cancel()
	if sess := p.sess; sess != nil {
		r.spawn(func() { sess.Close() })
	}
}

// openShell carries out effShell: draal is asked for the session in a
// goroutine, and the program is told what came of it. The terminal is
// the terminal UI's meanwhile.
func (r *runner) openShell(e effShell) {
	c, op := r.current(e.epoch)
	if c == nil {
		r.spawn(func() { r.send(shellOpenedMsg{n: e.n, err: errors.New("the connection to draal is gone")}) })
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	p := &pendingShell{n: e.n, ctx: ctx, cancel: cancel}
	r.mu.Lock()
	old := r.pending
	r.pending = p
	r.mu.Unlock()
	r.drop(old)
	r.spawn(func() {
		cols, rows := term.Size(r.cfg.In)
		sh, err := op.Shell(ctx, e.zathras, wire.ShellRequest{Zathras: e.zathras, Cols: cols, Rows: rows, Term: os.Getenv("TERM")})
		// Whoever gave the shell up meanwhile has taken it out of the
		// runner: the session is then nobody's, and closed here.
		r.mu.Lock()
		waited := r.pending == p
		if waited && err == nil {
			p.sess = sh
		}
		r.mu.Unlock()
		switch {
		case !waited:
			cancel()
			if err == nil {
				sh.Close()
			}
			return
		case err != nil:
			r.giveUp(e.n)
		}
		r.send(shellOpenedMsg{n: e.n, err: err})
	})
}

// giveUp carries out effGiveUp: the shell of that number, if it is the
// one that waits, is ended.
func (r *runner) giveUp(n int) {
	r.mu.Lock()
	p := r.pending
	if p == nil || p.n != n {
		r.mu.Unlock()
		return
	}
	r.pending = nil
	r.mu.Unlock()
	r.drop(p)
}

// take returns the shell of that number for the terminal to be given
// to, if it waits with its session, and nil otherwise.
func (r *runner) take(n int) *pendingShell {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending
	if p == nil || p.n != n || p.sess == nil {
		return nil
	}
	r.pending = nil
	return p
}

// shell carries out effAttach. It is called from Update, and what it
// returns is run by Bubble Tea: tea.Exec gives the terminal away for as
// long as its command runs, and takes it back. The terminal is then in
// its usual mode and on its own screen, as term.Attach expects it and
// leaves it.
//
// Until Bubble Tea has given the terminal away it reads it. A key that
// its reader holds at that moment, read and not yet handed to the
// program, is handed over when the program takes messages again, behind
// the shell: before the message that the shell has ended, since it has
// waited longer to be sent and a channel serves those who wait in their
// order. The model still has the shell as open then, and drops it. (A
// second key of the same read would come behind that message, and be
// the terminal UI's: it takes two keys within the instant in which the
// terminal changes hands.)
func (r *runner) shell(e effAttach) tea.Cmd {
	c := &shellExec{r: r, e: e}
	return tea.Exec(c, func(err error) tea.Msg {
		switch {
		case c.done.panicked != nil:
			return shellCrashedMsg{what: c.done.panicked, stack: c.done.stack}
		case err != nil:
			// Run returns no error: this one is Bubble Tea's, which could
			// not give the terminal away or take it back.
			return shellCrashedMsg{what: fmt.Errorf("handing the terminal over: %w", err), stack: debug.Stack()}
		}
		return c.done.ended
	})
}

// shellExec is one shell, as Bubble Tea runs it between giving the
// terminal away and taking it back.
type shellExec struct {
	r    *runner
	e    effAttach
	done shellDone
}

// shellDone is what came of a shell.
type shellDone struct {
	ended    shellEndedMsg // how it ended
	panicked any           // or what it panicked with
	stack    []byte        // and where
}

// SetStdin is of tea.ExecCommand. The shell is joined to the terminal of
// the Config, which is what Bubble Tea would hand over here.
func (c *shellExec) SetStdin(io.Reader) {}

// SetStdout is of tea.ExecCommand.
func (c *shellExec) SetStdout(io.Writer) {}

// SetStderr is of tea.ExecCommand.
func (c *shellExec) SetStderr(io.Writer) {}

// Run joins the terminal to the session until the shell has ended or
// was left, and closes the session, which hangs the shell up. It always
// returns nil: how the shell ended is the message.
//
// The shell is a goroutine of its own, which Run waits for, so that Run
// can return without it when the context of Run is done and the shell
// does not end with it: the terminal is put back in the mode it was
// given away in, Bubble Tea takes it back, and the program ends in its
// usual way, with the terminal as it was. What is left behind then is
// the goroutine of the shell, which the end of Run waits closeAfter for.
// A panic, of the session or of term.Attach behind its own restoring of
// the terminal, is kept and not passed on, so that the terminal is
// taken back first and the program ends as it does after any other
// panic.
func (c *shellExec) Run() error {
	r := c.r
	p := r.take(c.e.n)
	if p == nil {
		c.done = shellDone{ended: shellEndedMsg{err: errors.New("its session is gone")}}
		return nil
	}
	// The connection may have been lost, or Run told to end, between
	// draal's answer and this moment: the session is then not given the
	// terminal, and nothing is written. (The terminal has left the
	// terminal UI's screen for it all the same, and comes back at once.)
	switch {
	case p.ctx.Err() != nil:
		r.drop(p)
		c.done = shellDone{ended: shellEndedMsg{err: errors.New("the connection to draal was lost.")}}
		return nil
	case r.user.Err() != nil:
		r.drop(p)
		c.done = shellDone{ended: shellEndedMsg{err: context.Cause(r.user)}}
		return nil
	}
	// The shell ends with the connection it was asked over, and with the
	// context of Run: the program takes no message while the shell has
	// the terminal, so nothing else of the program could end it.
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	stop := context.AfterFunc(r.user, cancel)
	defer stop()

	// The mode the terminal is given away in, which is its usual one.
	fd := int(r.cfg.In.Fd())
	usual, _ := xterm.GetState(fd)

	done := make(chan shellDone, 1)
	r.spawn(func() {
		defer func() {
			if v := recover(); v != nil {
				done <- shellDone{panicked: v, stack: debug.Stack()}
			}
		}()
		done <- shellDone{ended: r.attach(ctx, p, c.e.name)}
	})
	select {
	case c.done = <-done:
		return nil
	case <-r.user.Done():
	}
	grace := time.NewTimer(graceOfShell())
	defer grace.Stop()
	select {
	case c.done = <-done:
	case <-grace.C:
		// The shell is left behind. If it has the terminal in the mode of
		// a shell, the terminal is put back: Bubble Tea takes the mode it
		// finds now for the one to restore at the end.
		if usual != nil {
			xterm.Restore(fd, usual)
		}
		c.done = shellDone{ended: shellEndedMsg{opened: true, err: fmt.Errorf("the shell did not end with the terminal UI: %w", context.Cause(r.user))}}
	}
	return nil
}

// attach joins the terminal to a session that is open until the shell
// has ended or was left, or ctx is done, and closes the session.
func (r *runner) attach(ctx context.Context, p *pendingShell, name string) shellEndedMsg {
	// However this is left, the session is closed: a shell that nobody
	// is joined to is hung up. It is closed here, in the goroutine of the
	// shell, before the terminal is taken back.
	defer func() {
		p.cancel()
		p.sess.Close()
	}()

	// This line is on the terminal's own screen, above what the shell
	// writes: it says where the prompt below is from, and the way out.
	fmt.Fprintf(r.cfg.Out, "great-machine: a shell on %s. Ctrl-] leaves it.\r\n", name)
	exit, detached, err := term.Attach(ctx, r.cfg.In, r.cfg.Out, p.sess)
	if detached {
		// The prompt of the shell is left as it is, on a line of its own.
		fmt.Fprint(r.cfg.Out, "\r\n")
	}
	return shellEndedMsg{opened: true, exit: exit, detached: detached, err: err}
}
```

In `tui/model.go`, four changes.

1. The struct `model` gets a field behind `notice`:

```go
	notice string     // one line that is said once: gone with the next key
	sh     shellState // the shell that was asked for, if one was
```

(`gofmt` aligns the comments of the fields beside it.)

2. In `take`, the case of `shellEndedMsg` gets one before it:

```go
	case shellOpenedMsg:
		return m.shellOpened(msg)
	case shellEndedMsg:
		return m.shellEnded(msg)
```

3. `key` begins with the shell, before `q` and everything else:

```go
func (m *model) key(name string) []effect {
	// From the key that asks for a shell until the shell has ended, no
	// key is the model's but the one that gives the shell up.
	if effs, taken := m.shellTakesKey(name); taken {
		return effs
	}
	if name == "q" || name == "ctrl+c" {
```

4. At the end of `lost`, the last three lines (`m.epoch++` to the `return`) become:

```go
	m.epoch++ // nothing of the old connection is read from here on
	m.pause = m.cfg.RetryFirst
	// A shell that draal was asked for over it is not waited for.
	effs := m.giveUpShell("No shell was opened: the connection to draal was lost.")
	return append(effs, effHangUp{}, m.wait())
}
```

In `tui/run.go`, six changes.

1. The runner knows the context of `Run`. In `run`, the line that makes the program becomes:

```go
	prog := &program{m: newModel(cfg), r: &runner{cfg: cfg, ctx: all, user: ctx}, drawn: drawn}
```

2. The struct `runner` gets two fields, `user` behind `ctx` and `pending` at its end:

```go
// runner carries out what the model wants against the Operator, and
// tells the program what came of it. Every goroutine it starts ends
// when its connection is hung up or the runner is closed, and close
// waits for them.
//
// It tells the program by send, which waits until the program takes the
// message. The program may not take one for a long time: while a shell
// has the terminal, it takes none at all. Nothing here grows meanwhile,
// and nothing gives up: the fleets and the lines of a log are read as
// they come and the newest are kept (one fleet, logKept lines), and
// every other goroutine is one that waits with its one message.
type runner struct {
	cfg   Config
	ctx   context.Context // done when the program has ended
	user  context.Context // the context of Run: done when the program is to end
	send  func(tea.Msg)
	kill  *time.Timer
	crash func(v any) // takes a panic of a goroutine of the runner

	wg      sync.WaitGroup
	mu      sync.Mutex
	conn    *conn         // the connection that is dialed or there
	tick    bool          // the ticker runs
	pending *pendingShell // the shell that was asked for and has not the terminal
}
```

3. `Update` takes `shellCrashedMsg` itself:

```go
// Update hands a message to the model and carries out what it wants.
func (p *program) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	if p.crashed() {
		return p, tea.Quit
	}
	if crash, ok := msg.(shellCrashedMsg); ok {
		// Something failed while a shell had the terminal. Bubble Tea has
		// taken the terminal back, and the program ends as after a panic
		// of its own.
		p.mu.Lock()
		p.panicked, p.stack = crash.what, crash.stack
		p.mu.Unlock()
		return p, tea.Quit
	}
	defer func() {
		if p.recovered(recover()) {
			next, cmd = p, tea.Quit
		}
	}()
	return p, p.do(p.m.update(msg))
}
```

4. In `do` of the program, the case `effShell` becomes the case of the effect that gives the terminal away:

```go
		case effAttach:
			cmds = append(cmds, p.r.shell(e))
```

5. In `do` of the runner, two cases more, before `effUnfollow`:

```go
	case effShell:
		r.openShell(e)
	case effGiveUp:
		r.giveUp(e.n)
```

6. `close` of the runner ends a shell that waits:

```go
// close ends everything the runner started and waits for it, for as
// long as wait at most. It says whether everything has ended.
func (r *runner) close(wait time.Duration) bool {
	r.mu.Lock()
	if r.kill != nil {
		r.kill.Stop()
	}
	c := r.conn
	r.conn = nil
	waiting := r.pending
	r.pending = nil
	r.mu.Unlock()
	r.drop(waiting)
	if c != nil {
		r.spawn(c.hangUp)
	}
	ended := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(ended)
	}()
	select {
	case <-ended:
		return true
	case <-time.After(wait):
		return false
	}
}
```

In `tui/view.go`, what is said once may take three lines, since the error of a shell may be long. `noticeLines` becomes:

```go
// noticeLines is what is said once, when there is something: a line,
// or as many as three when it is long, as the error of a shell may be.
func (m *model) noticeLines() []string {
	if m.notice == "" || m.tooSmall() {
		return nil
	}
	var out []string
	for _, l := range wrap(m.notice, m.width-2, 3) {
		out = append(out, line(m.width, seg{" " + l, stWarn}))
	}
	return out
}
```

(`bodyHeight` and `view` count its lines already.) And the line of the keys says what is true while a shell is asked for: in `keys`, the `switch` gets two cases before `m.help`:

```go
	switch {
	case m.sh.stage == shellOpening:
		// No other key does anything while a shell is being opened.
		keys = []key{{"esc", "give the shell up", 0}}
	case m.sh.stage == shellOpen:
		keys = nil
	case m.help:
```

- [ ] **Step 8: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -run 'Shell|Hands|Key|Esc|Opened|WatchAndLog|Context|Session|LostBetween|TestOddStates|TestAgain' -v`
Expected: PASS: `TestShellSentence`, `TestShellKeyAndItsEnd`, `TestShellHasTheTerminalAndGivesItBack`, `TestKeysTypedWhileTheTerminalChangesHands`, `TestShellLeftWithCtrlRightBracket`, `TestShellRefusedAndShellOfAZathrasThatGoes`, `TestConnectionLostWhileAShellIsOpen`, `TestShellEndsWithTheContext`, `TestShellThatDoesNotEndWithTheContext`, `TestContextEndsWhileDraalIsAskedForAShell`, `TestShellIsLeftBeforeTheKill`, `TestShellPanics`, `TestTerminalResizedWhileAShellIsOpen`, `TestNoShellOnAZathrasThatIsGone`, `TestKeysTypedWhileAShellIsOpened`, `TestKeyTypedRightBehindS` (`ss`, `sq`), `TestKeyHeldWhenTheTerminalIsGivenAway`, `TestEscGivesUpAShellThatIsOpened`, `TestConnectionLostWhileAShellIsOpened`, `TestWatchAndLogAreReadWhileAShellIsOpen`, `TestSessionThatIsHeldWhenRunEnds` (two), `TestEscWithASessionWhoseCloseDoesNotReturn`, `TestConnectionLostBetweenTheAnswerAndTheShell`, and those that were changed. Each takes well under a second, but those that wait `closeAfter`.

Run: `nix develop .#bare --command go test ./tui/...`
Expected: `ok` for `tui`, `tui/demo` and `tui/tuitest`

Run: `nix develop .#bare --command go test -race -count=50 -skip TestEverySizeOfTerminal ./tui/`
Expected: `ok` (about five minutes)

Run: `nix develop .#bare --command gofmt -l tui`
Expected: no output

- [ ] **Step 9: See that the tests hold what they say**

Seven changes, one at a time, each undone before the next (`git checkout tui/`):

1. In `tui/shell.go`, delete the line `p.sess.Close()` in the deferred function of `attach`. `nix develop .#bare --command go test ./tui/ -run 'TestShellHasTheTerminal|TestShellLeft'` fails with `timed out waiting for the session to be closed`, twice (after 30 seconds each).
2. In `shellEnded`, return `nil` in place of `m.again()`. `nix develop .#bare --command go test ./tui/ -run 'TestShellKeyAndItsEnd|TestShellHasTheTerminal'` fails with `the shell has ended: the model wants [], want [{epoch:1 n:0 zathras:20ab…} {epoch:1 zathras:20ab… id:2}]` and `timed out waiting for the log to be asked for again`.
3. In `Run` of `shellExec`, delete the two lines `stop := context.AfterFunc(r.user, cancel)` and `defer stop()`. `nix develop .#bare --command go test ./tui/ -run 'TestShellEndsWithTheContext'` fails after 90 seconds with `timed out waiting for the terminal UI to end` and the goroutines that are left: nothing but the context may end that shell, and the test has put the grace and the kill out of reach.
4. In `tui/model.go`, delete the three lines of `key` that ask `shellTakesKey`. `nix develop .#bare --command go test ./tui/ -run 'TestShellKeyAndItsEnd|TestKeyTypedRightBehindS|TestKeysTypedWhileAShellIsOpened|TestKeyHeldWhenTheTerminalIsGivenAway'` fails in all four: keys are acted on while a shell is opened, `q` ends the terminal UI, and a second shell is asked for.
5. In `close` of the runner (`tui/run.go`), replace `r.drop(waiting)` with `_ = waiting`. `nix develop .#bare --command go test ./tui/ -run TestSessionThatIsHeldWhenRunEnds` fails with `timed out waiting for the session that was held to be closed`.
6. In `drop` (`tui/shell.go`), replace `r.spawn(func() { sess.Close() })` with `sess.Close()`. `nix develop .#bare --command go test ./tui/ -run 'TestSessionThatIsHeld|TestEscWith'` fails: `timed out waiting for the terminal UI to end`, and the terminal UI takes no key after `Esc`.
7. In `Run` of `shellExec`, replace `case p.ctx.Err() != nil:` and the three lines under it with `case false:`. `nix develop .#bare --command go test ./tui/ -run TestConnectionLostBetween` fails: the line never says that the connection was lost.

- [ ] **Step 10: Look at it, in a real terminal**

Run: `nix develop .#bare --command go run ./tui/demo`

The shell of the demo is a real shell on this machine. In a terminal of 80×24 or more:

1. `j`, `j`, `Enter`, `Enter`: a zathras of `web`. `s`: for an instant a yellow line above the keys says `Opening a shell on web-7d9f8b6c4-2xkqp … esc gives it up` (the demo's shell is there at once; against a real draal the line stays for the round trip). Then the alternate screen is gone, the terminal shows what it showed before the demo (its two lines `demo: …`), and under it

   ```
   great-machine: a shell on web-7d9f8b6c4-2xkqp. Ctrl-] leaves it.
   ```

   and the prompt of a shell. Leave it with `exit`, and type `ss` quickly: one shell opens, and after `exit` no second one does.
2. `echo hello-$((6*7))`: `hello-42`. `q` and `Ctrl-C` are the shell's: nothing quits. Scroll the terminal back: nothing of the terminal UI's is in between.
3. `exit 3`: the view of the zathras is back, with one yellow line above the keys: `The shell ended with status 3.` The figures and the log have moved on to now; no line of the log is there twice. The next key takes the line away.
4. `s`, `sleep 1000`, `Ctrl-]`: back at once, `The shell was left with Ctrl-], which hung it up.` In another terminal, `pgrep -f 'sleep 1000'` prints nothing.
5. `s`, then make the window larger, then `stty size`: the rows and columns the window has now. `exit`: the view fills the larger window.
6. `s`, and from another terminal `pkill -TERM -f 'tui/demo|/demo$'` (the demo itself, not `go run`): the prompt of the first terminal is back, in its usual mode (what is typed is echoed, `Enter` runs it), the cursor visible, no alternate screen. The shell that was open is gone: `pgrep -lf 'sh -i'` (or whatever the demo's shell is) does not show it.
7. `q` after a shell: scroll the terminal back, and what the shells wrote is there, under their lines.

The screens of such a session, as a terminal emulator shows them, were captured when this plan was written: before the shell, the line while a shell is being opened (with a shell that answers late), the shell with its line and prompt, the view afterwards with its line, after `Ctrl-]`, and after a resize during a shell.

- [ ] **Step 11: Commit**

```bash
git add tui/
git commit -m "TUI: a shell from the view of a zathras: draal is asked first, the terminal is the session's until the shell ends or Ctrl-] is typed, and one line says how it ended"
```
