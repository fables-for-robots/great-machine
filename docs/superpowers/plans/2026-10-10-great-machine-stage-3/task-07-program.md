### Task 7: The program around the model, and the demo

`tui.Run`, as the contract has it: the Bubble Tea program on `In` and `Out`, on the alternate screen, and a runner that carries out what the model wants against the `Operator` and tells the program what came of it. Then `tui/demo`: the terminal UI over the made-up fleet of Task 3.

**Review Focus pinned here:** 1, for the ways out that are the terminal UI's own, by `TestRunShowsTheFleetAndWhatAViewHolds` (quit), `TestRunEndsWithItsContext`, `TestRunEndsAProgramThatIsStuck` (the context is done while an `Update` does not return), `TestRunTheKillThatComesLateDoesNothing` and `TestRunTheKillWinsOverAQuitThatIsOnItsWay` (the program is ended once, by itself or by the kill, and nothing is written to the terminal once `Run` has returned), `TestRunRestoresTheTerminalAfterAPanic` (a panic in `Update`, and one in a view, each where it says) and `TestRunRestoresTheTerminalWhenTheOperatorPanics`: each compares the mode of the pseudo-terminal before and after, and that what was written ends by leaving the alternate screen and showing the cursor; 2, by `TestRunStaysAwakeInAFlood` (no title of a log line is ever written to the terminal) and `TestRunFirstDial` (the error `Run` returns, which the command prints on the terminal, is safe to print); 3, by `TestRunConnectionLostAndBack`; 5, by `TestRunStaysAwakeInAFlood` and `TestRunnerStandsAProgramThatTakesNothing`. And on every way out, no goroutine of the terminal UI is left: every test that starts `run` checks it when it ends.

**Files:**
- Create: `tui/run.go`
- Modify: `tui/shell.go` (one function more: how the program carries out `effShell`; until the shell is built it does nothing)
- Test: `tui/run_test.go`
- Create: `tui/demo/main.go`
- Test: `tui/demo/main_test.go`

**Interfaces:**
- Consumes: the model, its messages and its effects (Task 5); `view` (Task 6); `pending`, `logKept`, `logEvery` (Task 4); `demo.New`, `(*demo.Demo).Run` (Task 3); `draal.Controller`, `draal.Shell`; Bubble Tea v2.0.10 (`tea.NewProgram`, `tea.WithInput`, `tea.WithOutput`, `tea.WithoutSignalHandler`, `tea.WithEnvironment`, `(*tea.Program).Run`, `Send`, `Quit`, `Kill`, `tea.View` with `AltScreen`, `tea.Quit`, `tea.Batch`, `tea.ErrProgramPanic`); `github.com/creack/pty` and `golang.org/x/term` in the tests (the module has both).
- Produces:
  - `func Run(ctx context.Context, cfg Config) error`, as the contract has it. It needs `Dial`, `In` and `Out`, and returns:
    - nil when the user quits;
    - the error of `ctx` when `ctx` is done;
    - when draal refuses the operator at the first dial: the refusal. **Its `Error()` is safe to print on a terminal** (it went through `clean`); `errors.Is` finds the error as `Dial` or the watch returned it, and `errors.As` gives a `*wire.Error` with the refusal's code and its message, both made safe. A command prints the error, or what `errors.As` gave it, and never what `errors.Unwrap` returns;
    - an error that begins `the terminal UI failed, and the terminal was restored: panic:` with the stack when the model, or a goroutine of the runner (a method of the `Operator`), panicked;
    - **within a bounded time of `ctx` being done, whatever the program and the `Operator` do.** A program that has not ended 5 seconds (`killAfter`) after `ctx` was done is ended as it is, the terminal restored, and when it has still not returned a second later (`cutAfter`) `Run` returns `the terminal UI did not end 5s after it was told to and was cut off, with the terminal restored: context canceled` (`errors.Is` finds the error of `ctx`). Left behind then: the goroutine of the program, stuck in its `Update`, and what it holds. And when something asked of the `Operator` has not returned 5 seconds (`closeAfter`) after it was told to end (a `Dial`, a `Watch` or a `Close` that does not honour its context), `Run` returns what it would have returned and says so in `Config.Log`; left behind then: the goroutines that wait for the `Operator`, and its connection. Both are for a process that is ending.
  - `func run(ctx context.Context, cfg Config, drawn func(view string), opts ...tea.ProgramOption) error`: `Run` for tests, with `drawn`, if set, called with every view as it is handed to the renderer, and with options of Bubble Tea (the environment: `TERM`, `NO_COLOR`).
  - `type runner struct` with `spawn(f func())` (a goroutine that the end of `Run` waits for), `current(epoch int) (*conn, Operator)` (the connection of that epoch and its `Operator`, if it is the one there is), `send func(tea.Msg)`, `ctx` (done when the program has ended), `cfg`; and `func (r *runner) shell(e effShell) tea.Cmd` in `tui/shell.go`, **the hook of the shell**: it is called from `Update` when the model wants `effShell`, and what it returns is run by Bubble Tea.
  - In the tests, for the tests of the shell and of the whole: `fakeOperator` (`newFakeOperator`, its `fleets` channel, `linesOf`, `was`; its `Logs` returns with the first line, as the client's does), `shortly(t)` (the times `Run` waits for what does not end, short, for one test), `killStages(t, hold...)` (the stages of the timer that kills, `fired`, `killing`, `done`, reported and held, through the package's `killStage`), `(*terminal).quiet()` (called when `Run` has returned: marks the place in what the pseudo-terminal carries and fails when anything is written behind it; a count of the bytes read so far would not do, since what was written before may be read later), `waitFor`; and the package variables for tests `killStage` and `filterForTests` (a test's own filter goes there, not into `tea.WithFilter`), `startRun(t, dial, change, env...) *terminal` with `wait(words...)` (until the screen says them), `shown() string` (the screen now), `typeKeys`, `end() error` (waits for `run`, and checks the terminal), `written() string` (every byte that was written to the pseudo-terminal), `stop`; `await`; `oneOperator`; `goroutinesLeft`.
  - `go run ./tui/demo [--empty]`.

**How the program is made, and why:**
- **The context is not given to Bubble Tea.** With `tea.WithContext`, a context that is done makes Bubble Tea close its reader of the terminal without waiting for the read: a race, and an error that is none of the context's. `Run` asks the program to quit instead, which is the graceful way out, and returns `ctx.Err()`. **The kill is armed before the quit is asked for**: `p.Quit` is a send to the event loop, and waits for as long as an `Update` does not return, or a shell has the terminal; so the timer for `p.Kill` (5 seconds, `killAfter`) is set first and `p.Quit` runs in a goroutine of its own. When the timer fires, the terminal is first given back with `p.ReleaseTerminal`, as Bubble Tea does for a shell: that waits for the reader of the terminal to have ended, where `p.Kill` alone closes the terminal under its read (a race the detector reports on Linux). Then `p.Kill`. Both work while the event loop is stuck, but `p.Run` does not return then: the program runs in a goroutine, and `Run` stops waiting for it a second after the kill.
- **How the program ends is decided once, under one mutex.** The kill and the program's own end are not exclusive by themselves: an `Update` that returns at about `killAfter` lets the program end by itself while the timer fires, and a `ReleaseTerminal` on a program that has given the terminal back paints its last frame again, on the main screen, after `Run` has returned. So there is one mutex and one flag, "the end is decided". A filter (`tea.WithFilter`, the last of the options, so that no other takes its place; every other message passes as it came) sets the flag when the program takes `tea.QuitMsg` or `tea.InterruptMsg`: it is ending by itself. The timer takes the mutex, does nothing when the flag is set, and otherwise sets it and gives the terminal back and kills, holding the mutex: a message to quit that comes meanwhile waits in the filter, and when it is let through the program's context is done, so it ends as one that was killed and draws no last frame. And `run` sets the flag, under the mutex, as soon as `p.Run` has returned: a timer that fires late does nothing, and one at work is waited for. **The window that is left:** a program that ends through an error of Bubble Tea's own, not by a message to quit, at the very moment the timer fires, gives the terminal back while the timer does.
- **A kill gives the terminal back twice**: `ReleaseTerminal` and `Kill` each close the renderer, so the resets are written twice (the alternate screen left twice, the keyboard modes popped twice). The second set changes nothing but for a terminal with a stack of keyboard modes pushed by the program that started this one, which loses one level. Without `ReleaseTerminal` the race is back, and Bubble Tea has no way to end a stuck program without closing; this is the path of a failure, and `TestRunEndsAProgramThatIsStuck` asserts the two, so that nobody takes them for an accident.
- **A program that took the message to quit and then does not end** (its last view does not return) is not killed, since it is giving the terminal back itself; `Run` waits a second longer than `killAfter` for it and returns `… while it was ending by itself, and was left to it: the terminal may not be as it was`.
- **Bubble Tea handles no signals** (`tea.WithoutSignalHandler`): they are the caller's, who ends `ctx`. Ctrl-C in the terminal UI is a key, since the terminal is raw.
- **A panic** in `Update`, in a view, at the start, or in a goroutine of the runner (`spawn` recovers; a method of the `Operator` may panic there) is recovered by the program itself, which then quits in the usual way, so that the terminal is restored by the same code as on every other way out; `Run` returns the panic and its stack as an error. Bubble Tea's own recover, which prints to the standard error of the process, is left in place behind it for a panic in its own code.
- **The runner.** `effDial` is `Dial` and then `Watch`, as one step: a refusal of either is the refusal of the first dial. Every goroutine the runner starts ends when its connection is hung up or the program has ended, and `Run` waits for all of them before it returns, for 5 seconds at most, and closes what `Dial` returned.
- **A request that fails** with a `*wire.Error` is draal's answer, and the model is told it. One that fails with anything else is the connection: `lostMsg`. Of a connection that was hung up, nothing is told: an error after one's own `Close` is no connection that was lost.
- **`Send` waits until the program takes the message**, and the program may not take one for a long time: while a shell has the terminal (`tea.Exec`), it takes none at all. Nothing here grows meanwhile and nothing gives up. The watch is read as it comes and the newest fleet is kept; a followed log is read as it comes and its newest 400 lines are kept (`pending`); the ticker drops the seconds nobody takes; every other goroutine is one that waits with its one message. No goroutine is started for a message. `TestRunnerStandsAProgramThatTakesNothing` pins it.
- **A log is followed from its first line.** `Operator.Logs` returns when the first line has come, when the log has ended without one, or with a refusal (so does `operator.Client`, and the fake of the tests): until then the program tells the model nothing, and the view says that no line has come yet.
- **A flood** is handed to the model every 50 ms at most (`logEvery`), so it is drawn twenty times a second at most, however fast the lines come.
- **The tests read the screen from the program, and the bytes from the pseudo-terminal.** What Bubble Tea writes to a terminal is what has changed since the last frame, in pieces, with the cursor moved between them: only a terminal emulator could say what it adds up to, and a test that looked for words in it passed on macOS and failed one run in five on Linux. So `run` tells a test every view it hands to the renderer (`drawn`), and `wait` reads that. What must be true of the bytes is read from the pseudo-terminal: the alternate screen entered and left, the cursor shown again, no mouse asked for, no title, colours or none, and the mode of the terminal before and after.
- **`tick`**: one goroutine tells the program every second that time has passed, so that "3s ago" moves on.

- [ ] **Step 1: Write the failing tests of the program**

Create `tui/run_test.go`:

```go
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// fakeOperator is an Operator whose channels the test feeds.
type fakeOperator struct {
	mu      sync.Mutex
	asked   []string                      // what was asked of it, in order
	fleets  chan wire.Fleet               // what Watch returns
	lines   map[wire.ID]chan wire.LogLine // what Logs returns; made when asked for
	history func() ([]wire.Sample, error) // what History answers; nil for no samples
	logs    func(id wire.ID) error        // what Logs answers; nil for a log
	watch   func() error                  // what Watch answers; nil for the fleets
	done    map[string]context.Context    // the contexts of what was asked
	closed  chan struct{}                 // closed by Close
	once    sync.Once
}

func newFakeOperator() *fakeOperator {
	return &fakeOperator{fleets: make(chan wire.Fleet, 4), lines: map[wire.ID]chan wire.LogLine{}, done: map[string]context.Context{}, closed: make(chan struct{})}
}

// note records that something was asked, and with which context.
func (f *fakeOperator) note(ctx context.Context, what string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, what)
	f.done[what] = ctx
}

// was says whether something was asked, and returns its context.
func (f *fakeOperator) was(what string) (context.Context, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ctx, ok := f.done[what]
	return ctx, ok
}

func (f *fakeOperator) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	f.note(ctx, "watch")
	if f.watch != nil {
		if err := f.watch(); err != nil {
			return nil, err
		}
	}
	// As a client does: the channel is closed when ctx is done, or when
	// the test closes fleets, which is the connection lost.
	out := make(chan wire.Fleet)
	go func() {
		defer close(out)
		for {
			select {
			case fl, ok := <-f.fleets:
				if !ok {
					return
				}
				select {
				case out <- fl:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (f *fakeOperator) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	f.note(ctx, "rollouts "+service)
	return fixtureRollouts(service), nil
}

func (f *fakeOperator) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	f.note(ctx, "history "+string(zathras[:2]))
	if f.history != nil {
		return f.history()
	}
	return fixtureSamples(), nil
}

func (f *fakeOperator) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	f.note(ctx, "logs "+string(zathras[:2]))
	if f.logs != nil {
		if err := f.logs(zathras); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	in := make(chan wire.LogLine)
	f.lines[zathras] = in
	f.mu.Unlock()
	out := make(chan wire.LogLine)
	// As a client does: the protocol answers a log with its lines
	// alone, so Logs returns with the first line, with the end of the
	// log, or when ctx is done.
	var first wire.LogLine
	select {
	case l, ok := <-in:
		if !ok {
			close(out)
			return out, nil
		}
		first = l
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	go func() {
		defer close(out)
		select {
		case out <- first:
		case <-ctx.Done():
			return
		}
		for {
			select {
			case l, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- l:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (f *fakeOperator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
	return nil, wire.Errorf(wire.CodeUnavailable, "the fake has no shell")
}

func (f *fakeOperator) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// linesOf returns the channel the test feeds the log of a zathras with,
// once the log was asked for.
func (f *fakeOperator) linesOf(t *testing.T, id wire.ID) chan wire.LogLine {
	t.Helper()
	var ch chan wire.LogLine
	await(t, "the log to be asked for", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		ch = f.lines[id]
		return ch != nil
	})
	return ch
}

// await waits until cond holds, and fails the test when it does not
// within the deadline.
func await(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(deadline); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// terminal is a pseudo-terminal that a terminal UI under test runs in,
// and everything the terminal UI wrote to it.
type terminal struct {
	t      *testing.T
	master *os.File
	slave  *os.File
	before *xterm.State

	mu     sync.Mutex
	out    bytes.Buffer
	screen string // the view that was drawn last

	ended chan error // what run returned
	stop  context.CancelFunc
}

// startRun runs the terminal UI in a pseudo-terminal of 80 columns and
// 24 rows, with dial as its Dial, and ends it with the test.
func startRun(t *testing.T, dial func(ctx context.Context) (Operator, error), change func(*Config), env ...string) *terminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("opening a pseudo-terminal: %v", err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("sizing the pseudo-terminal: %v", err)
	}
	before, err := xterm.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatalf("reading the mode of the pseudo-terminal: %v", err)
	}
	term := &terminal{t: t, master: master, slave: slave, before: before, ended: make(chan error, 1)}
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
	ctx, stop := context.WithCancel(context.Background())
	term.stop = stop
	cfg := Config{Dial: dial, In: slave, Out: slave, Draal: "3f9a2c1b", RetryFirst: 10 * time.Millisecond, RetryMax: 40 * time.Millisecond}
	if change != nil {
		change(&cfg)
	}
	go func() {
		drawn := func(view string) {
			term.mu.Lock()
			term.screen = view
			term.mu.Unlock()
		}
		term.ended <- run(ctx, cfg, drawn, tea.WithEnvironment(append([]string{"TERM=xterm-256color"}, env...)))
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-term.ended:
		case <-time.After(deadline):
			t.Error("timed out waiting for the terminal UI to end")
		}
		// Review Focus 1 and the contract: no goroutine is left. (One is
		// the test's own: it reads the pseudo-terminal.) The terminal is
		// closed only then: a goroutine of Bubble Tea may ask it for its
		// size for a moment after Run has returned.
		var left []string
		for end := time.Now().Add(deadline); ; time.Sleep(5 * time.Millisecond) {
			if left = goroutinesLeft(); len(left) == 0 {
				break
			}
			if time.Now().After(end) {
				t.Errorf("%d goroutines of the terminal UI are left:\n%s", len(left), strings.Join(left, "\n\n"))
				break
			}
		}
		slave.Close()
		master.Close()
		select {
		case <-read:
		case <-time.After(deadline):
			t.Error("timed out waiting for the pseudo-terminal to be read to its end")
		}
	})
	return term
}

// goroutinesLeft returns the stacks of the goroutines that are of the
// terminal UI: of this package, but for the tests themselves, of Bubble
// Tea and of what reads the terminal for it.
func goroutinesLeft() []string {
	buf := make([]byte, 4<<20)
	var left []string
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		ours := false
		for _, of := range []string{"tui.(*runner)", "tui.(*conn)", "tui.(*program)", "tui.(*fakeOperator)", "tui.run", "charm.land/bubbletea", "charmbracelet/ultraviolet", "muesli/cancelreader"} {
			ours = ours || strings.Contains(g, of)
		}
		if ours {
			left = append(left, g)
		}
	}
	return left
}

// written returns everything the terminal UI wrote so far.
func (term *terminal) written() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.out.String()
}

// shown returns what a person reads on the screen now: the view that
// was drawn last, without its colours. What was written to the
// pseudo-terminal is no screen: the renderer writes what has changed,
// in pieces, and only a terminal could say what that adds up to.
func (term *terminal) shown() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return plain(term.screen)
}

// wait waits until the screen says all of the words. A sentence may be
// broken over lines.
func (term *terminal) wait(words ...string) {
	term.t.Helper()
	said := ""
	for end := time.Now().Add(deadline); ; time.Sleep(2 * time.Millisecond) {
		said = strings.Join(strings.Fields(term.shown()), " ")
		missing := ""
		for _, w := range words {
			if !strings.Contains(said, w) {
				missing = w
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(end) {
			term.t.Fatalf("timed out waiting for the screen to say %q. It says:\n%s", missing, term.shown())
		}
	}
}

// typeKeys types into the terminal.
func (term *terminal) typeKeys(keys string) {
	term.t.Helper()
	if _, err := term.master.WriteString(keys); err != nil {
		term.t.Fatalf("typing: %v", err)
	}
}

// end waits for run to return, and checks that the terminal is as it
// was: in the mode it was in, the alternate screen left and the cursor
// shown, as the last things of their kind that were written. This is
// Review Focus 1, for the ways out that are the terminal UI's own.
func (term *terminal) end() error {
	term.t.Helper()
	var err error
	select {
	case err = <-term.ended:
		term.ended <- err
	case <-time.After(deadline):
		term.t.Fatal("timed out waiting for the terminal UI to end")
	}
	after, gerr := xterm.GetState(int(term.slave.Fd()))
	if gerr != nil {
		term.t.Fatalf("reading the mode of the pseudo-terminal: %v", gerr)
	}
	if !reflect.DeepEqual(after, term.before) {
		term.t.Errorf("the terminal is in another mode than before:\n got %+v\nwant %+v", after, term.before)
	}
	// What was written is read to its end before it is judged.
	var out string
	await(term.t, "the terminal to be restored", func() bool {
		out = term.written()
		return strings.LastIndex(out, "\x1b[?1049l") > strings.LastIndex(out, "\x1b[?1049h") &&
			strings.LastIndex(out, "\x1b[?25h") > strings.LastIndex(out, "\x1b[?25l")
	})
	if !strings.Contains(out, "\x1b[?1049h") {
		term.t.Error("the alternate screen was never entered")
	}
	for _, mode := range []string{"\x1b[?1000h", "\x1b[?1002h", "\x1b[?1003h", "\x1b[?1006h"} {
		if strings.Contains(out, mode) {
			term.t.Errorf("the mouse was asked for: %q", mode)
		}
	}
	return err
}

// oneOperator returns a Dial that connects once, to op.
func oneOperator(op *fakeOperator) func(ctx context.Context) (Operator, error) {
	var once sync.Once
	return func(ctx context.Context) (Operator, error) {
		var out Operator
		once.Do(func() { out = op })
		if out == nil {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return out, nil
	}
}

// The fleet arrives and is drawn; a view is opened and what it holds is
// asked for, and ended when it is left; q quits, and Run returns nil
// with the terminal as it was and the connection closed.
func TestRunShowsTheFleetAndWhatAViewHolds(t *testing.T) {
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	term.wait("Connecting to draal 3f9a2c1b")
	op.fleets <- fixtureFleet()
	term.wait("4 services, 12 zathrases connected", "● live", "worker")

	// A service: its rollouts are asked for.
	term.typeKeys("jj\r")
	term.wait("Rollout 31 to generation 21", "web-7d…-2xkqp", "#30 ✔ completed")
	if _, ok := op.was("rollouts web"); !ok {
		t.Errorf("the rollouts of web were not asked for: %v", op.asked)
	}
	// A zathras: its samples and its log.
	term.typeKeys("j\r")
	term.wait("21ab12cd34ab12cd", "Figures of the container", "Log · no line has come yet")
	lines := op.linesOf(t, idOf(0x21))
	lines <- wire.LogLine{At: t0, Stream: "stderr", Text: "\x1b[1;31mERROR\x1b[0m the disk is full"}
	term.wait("Log · followed live", "ERROR the disk is full")
	history, _ := op.was("history 21")
	logs, _ := op.was("logs 21")
	if history == nil || logs == nil {
		t.Fatalf("the samples and the log of the zathras were not asked for: %v", op.asked)
	}
	// Left: the log is ended.
	term.typeKeys("\x1b")
	select {
	case <-logs.Done():
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the log to be ended when its view was left")
	}
	term.wait("Last rollouts")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil when the user quits", err)
	}
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Error("the connection was not closed")
	}
}

// Review Focus 1: the context is done. Run returns its error, with the
// terminal as it was.
func TestRunEndsWithItsContext(t *testing.T) {
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jj\r\r") // on a zathras, with a log that is followed
	op.linesOf(t, idOf(0x20))
	term.stop()
	if err := term.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	select {
	case <-op.closed:
	case <-time.After(deadline):
		t.Error("the connection was not closed")
	}
}

// Review Focus 1: a panic, in Update and in a view. Run returns it as an
// error, with the terminal as it was. The clock of each case panics
// where the case says, and nowhere else: under Update, or under View.
func TestRunRestoresTheTerminalAfterAPanic(t *testing.T) {
	for _, where := range []string{"Update", "View"} {
		t.Run(where, func(t *testing.T) {
			var armed sync.Mutex
			panics := false
			now := func() time.Time {
				armed.Lock()
				defer armed.Unlock()
				if panics && strings.Contains(string(debug.Stack()), "tui.(*program)."+where+"(") {
					panic("the clock broke\x1b]0;pwned\x07")
				}
				return t0
			}
			op := newFakeOperator()
			term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.Now = now })
			op.fleets <- fixtureFleet()
			term.wait("4 services")
			armed.Lock()
			panics = true
			armed.Unlock()
			op.fleets <- fixtureFleet() // a fleet reads the clock in Update, and is drawn
			err := term.end()
			if err == nil || !strings.Contains(err.Error(), "panic: the clock broke") || !strings.Contains(err.Error(), "the terminal was restored") {
				t.Fatalf("Run = %v, want the panic as an error", err)
			}
			if !strings.Contains(err.Error(), "tui.(*program)."+where+"(") {
				t.Errorf("the panic was not under %s:\n%v", where, err)
			}
			if first, _, _ := strings.Cut(err.Error(), "\n"); unsafe(first) != "" {
				t.Errorf("the error has %s", unsafe(first))
			}
		})
	}
}

// A panic in a goroutine of the runner, here in a method of the
// Operator, ends the program as a panic of the model does: the terminal
// is restored, and Run returns it.
func TestRunRestoresTheTerminalWhenTheOperatorPanics(t *testing.T) {
	op := newFakeOperator()
	op.history = func() ([]wire.Sample, error) { panic("the operator broke") }
	term := startRun(t, oneOperator(op), nil)
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jj\r")
	term.wait("Last rollouts")
	term.typeKeys("\r")
	err := term.end()
	if err == nil || !strings.Contains(err.Error(), "panic: the operator broke") || !strings.Contains(err.Error(), "the terminal was restored") {
		t.Errorf("Run = %v, want the panic as an error", err)
	}
}

// shortly sets the times Run waits for a program and an Operator that
// do not end, for one test.
func shortly(t *testing.T) {
	kill, cut, closing := killAfter, cutAfter, closeAfter
	killAfter, cutAfter, closeAfter = 300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { killAfter, cutAfter, closeAfter = kill, cut, closing })
}

// Review Focus 1: the context is done while the program is stuck: an
// Update that does not return, as while a shell has the terminal. The
// program is ended as it is, the terminal is restored, and Run returns
// the error of its context in a sentence that says so, without waiting
// for the Update.
func TestRunEndsAProgramThatIsStuck(t *testing.T) {
	shortly(t)
	var mu sync.Mutex
	stuck := false
	entered, release := make(chan struct{}, 1), make(chan struct{})
	now := func() time.Time {
		mu.Lock()
		s := stuck
		mu.Unlock()
		if s && strings.Contains(string(debug.Stack()), "tui.(*program).Update(") {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
		return t0
	}
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.Now = now })
	var released sync.Once
	t.Cleanup(func() { released.Do(func() { close(release) }) }) // before the cleanup of startRun, which counts the goroutines
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	mu.Lock()
	stuck = true
	mu.Unlock()
	op.fleets <- fixtureFleet() // Update reads the clock: stuck
	select {
	case <-entered:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for Update to be stuck")
	}
	began := time.Now()
	term.stop()
	err := term.end() // Run has returned, and the terminal is as it was
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "did not end") || !strings.Contains(err.Error(), "cut off") {
		t.Errorf("Run = %v, want the error of its context and that the terminal UI was cut off", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("Run returned %v after its context was done", took)
	}
	// On this path, and on no other, the terminal is given back twice:
	// once as for a shell, which waits for the reader of the terminal,
	// and once by the kill (see the comment at the kill in run). It is
	// known, and no accident.
	if n := strings.Count(term.written(), "\x1b[?1049l"); n != 2 {
		t.Errorf("the alternate screen is left %d times by a program that was killed, want 2", n)
	}
	// And nothing is written once Run has returned, also when the
	// Update that was stuck returns.
	released.Do(func() { close(release) })
	term.quiet()
}

// killStages has killStage report the stages of the timer that ends a
// program, and hold it at the ones that are named until they are let
// go, for one test.
func killStages(t *testing.T, hold ...string) (reached map[string]chan struct{}, letGo func(stage string)) {
	reached = map[string]chan struct{}{"fired": make(chan struct{}), "killing": make(chan struct{}), "done": make(chan struct{})}
	held := map[string]chan struct{}{}
	for _, stage := range hold {
		held[stage] = make(chan struct{})
	}
	was := killStage
	killStage = func(stage string) {
		close(reached[stage])
		if wait, ok := held[stage]; ok {
			<-wait
		}
	}
	var once sync.Map
	letGo = func(stage string) {
		if _, done := once.LoadOrStore(stage, true); !done {
			close(held[stage])
		}
	}
	t.Cleanup(func() {
		for stage := range held {
			letGo(stage)
		}
		killStage = was
	})
	return reached, letGo
}

// returnedMark is what a test writes to the terminal when Run has
// returned: what the terminal UI wrote before is before it in what the
// test reads of the pseudo-terminal, however late it reads it.
const returnedMark = "<<Run has returned>>"

// quiet is called when Run has returned. It fails the test when anything
// is written to the terminal from then on: it marks the place, and reads
// until nothing has come for a while.
func (term *terminal) quiet() {
	term.t.Helper()
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		term.t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	term.quietBehindTheMark()
}

// quietBehindTheMark is quiet for a test that has set the mark itself.
func (term *terminal) quietBehindTheMark() {
	term.t.Helper()
	await(term.t, "the mark to be read", func() bool { return strings.Contains(term.written(), returnedMark) })
	for last, since := 0, time.Now(); time.Since(since) < 300*time.Millisecond; time.Sleep(10 * time.Millisecond) {
		if n := len(term.written()); n != last {
			last, since = n, time.Now()
		}
	}
	if _, after, _ := strings.Cut(term.written(), returnedMark); after != "" {
		term.t.Errorf("after Run had returned, %d bytes were written to the terminal: %q", len(after), after)
	}
}

// waitFor waits for a channel of a test to be closed.
func waitFor(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Review Focus 1: the timer that ends a program fires when the program
// has ended by itself. It leaves the terminal alone: the program gave it
// back, once, and nothing is written to it after Run has returned.
func TestRunTheKillThatComesLateDoesNothing(t *testing.T) {
	shortly(t)
	reached, letGo := killStages(t, "fired")
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	// The timer fires and is held before it looks; the program is asked
	// to quit, quits, and Run returns.
	killAfter = 0
	term.stop()
	waitFor(t, "the timer to fire", reached["fired"])
	if err := term.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	// From here on nothing may be written: the mark is set before the
	// timer is let go.
	if _, err := term.slave.WriteString(returnedMark); err != nil {
		t.Fatalf("writing to the pseudo-terminal: %v", err)
	}
	letGo("fired")
	waitFor(t, "the timer to return", reached["done"])
	term.quietBehindTheMark()
	select {
	case <-reached["killing"]:
		t.Error("the timer ended a program that had ended")
	default:
	}
	if out := term.written(); strings.Count(out, "\x1b[?1049l") != 1 || strings.Count(out, "\x1b[?1049h") != 1 {
		t.Errorf("the alternate screen was entered %d times and left %d times, want once each", strings.Count(out, "\x1b[?1049h"), strings.Count(out, "\x1b[?1049l"))
	}
}

// Review Focus 1: the timer ends a program while the message that tells
// it to quit is on its way to it. The kill wins: the terminal gets its
// resets, the message is let through only then, and the program draws
// no frame behind them, on the main screen.
func TestRunTheKillWinsOverAQuitThatIsOnItsWay(t *testing.T) {
	shortly(t)
	reached, letGo := killStages(t, "killing")
	var mu sync.Mutex
	stuck := false
	entered, release := make(chan struct{}, 1), make(chan struct{})
	now := func() time.Time {
		mu.Lock()
		s := stuck
		mu.Unlock()
		if s && strings.Contains(string(debug.Stack()), "tui.(*program).Update(") {
			select {
			case entered <- struct{}{}:
				<-release
			default:
			}
		}
		return t0
	}
	// The filter says when the message to quit has come to the program:
	// it is then before the mutex that the timer holds.
	quitting := make(chan struct{})
	filterForTests = func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, quit := msg.(tea.QuitMsg); quit {
			close(quitting)
		}
		return msg
	}
	t.Cleanup(func() { filterForTests = nil })
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), func(cfg *Config) { cfg.Now = now })
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	mu.Lock()
	stuck = true
	mu.Unlock()
	op.fleets <- fixtureFleet() // Update reads the clock: stuck
	waitFor(t, "Update to be stuck", entered)
	// The context ends: the message to quit waits for the Update, and
	// the timer decides to kill, and is held there.
	term.stop()
	waitFor(t, "the timer to decide", reached["killing"])
	// The Update returns, and the program takes the message to quit.
	close(release)
	waitFor(t, "the message to quit to come to the program", quitting)
	letGo("killing")
	waitFor(t, "the timer to return", reached["done"])
	if err := term.end(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want the error of its context", err)
	}
	term.quiet()
	out, _, _ := strings.Cut(term.written(), returnedMark)
	left := strings.Index(out, "\x1b[?1049l")
	if left < 0 {
		t.Fatal("the alternate screen was not left")
	}
	// Behind the first leaving of the alternate screen there is nothing
	// a person reads: resets, and no frame.
	if rest := strings.TrimSpace(plain(out[left:])); rest != "" {
		t.Errorf("behind the resets, on the main screen, the terminal has %q", rest)
	}
}

// An Operator that does not honour its context, here one whose Close
// does not return, does not keep Run: the terminal is restored at once,
// and Run returns what it would have returned, and says in its log what
// it left behind.
func TestRunDoesNotWaitForAnOperatorThatDoesNotReturn(t *testing.T) {
	shortly(t)
	var mu sync.Mutex
	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logged.Write(p)
	}), nil))
	release := make(chan struct{})
	op := newFakeOperator()
	term := startRun(t, func(ctx context.Context) (Operator, error) { return stubborn{op, release}, nil }, func(cfg *Config) { cfg.Log = log })
	t.Cleanup(func() { close(release) })
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil: the user quit", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logged.String(), "left behind") {
		t.Errorf("the log has %q, want what was left behind", logged.String())
	}
}

// stubborn is an Operator whose Close does not return until it is
// released.
type stubborn struct {
	*fakeOperator
	release chan struct{}
}

func (s stubborn) Close() error {
	<-s.release
	return s.fakeOperator.Close()
}

// writerFunc is a function as an io.Writer.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// What draal refuses at the first dial ends Run with the refusal, and
// is not tried again; a draal that does not answer is tried again.
func TestRunFirstDial(t *testing.T) {
	refused := wire.Errorf(wire.CodeUnauthorized, "this operator is not on the allowlist")
	// Review Focus 2: the error of Run is printed on the operator's
	// terminal. Whatever draal put into its refusal, the text of the
	// error is safe to print, and the refusal can still be told.
	t.Run("refused in words that are sequences", func(t *testing.T) {
		evil := wire.Errorf(wire.CodeUnauthorized, "not on the allowlist\x1b]0;pwned\x07\x1b[2J")
		for _, dial := range []func(ctx context.Context) (Operator, error){
			func(ctx context.Context) (Operator, error) { return nil, fmt.Errorf("connecting\x1b[5;5H: %w", evil) },
			func(ctx context.Context) (Operator, error) {
				op := newFakeOperator()
				op.watch = func() error { return evil }
				return op, nil
			},
		} {
			err := startRun(t, dial, nil).end()
			if err == nil || unsafe(err.Error()) != "" || !strings.Contains(err.Error(), "unauthorized: not on the allowlist") {
				t.Errorf("Run = %q: %s", err, unsafe(fmt.Sprint(err)))
			}
			var refusal *wire.Error
			if !errors.Is(err, evil) || !errors.As(err, &refusal) || refusal.Code != wire.CodeUnauthorized || refusal.Message != "not on the allowlist" || unsafe(refusal.Error()) != "" {
				t.Errorf("the refusal in Run's error is %+v, want its code and its message, made safe", refusal)
			}
		}
	})
	t.Run("refused by Dial", func(t *testing.T) {
		dials := 0
		term := startRun(t, func(ctx context.Context) (Operator, error) {
			dials++
			return nil, fmt.Errorf("connecting to draal: %w", refused)
		}, nil)
		if err := term.end(); !errors.Is(err, refused) || dials != 1 {
			t.Errorf("Run = %v after %d dials, want the refusal after one", err, dials)
		}
	})
	t.Run("refused by the watch", func(t *testing.T) {
		op := newFakeOperator()
		op.watch = func() error { return refused }
		term := startRun(t, oneOperator(op), nil)
		if err := term.end(); !errors.Is(err, refused) {
			t.Errorf("Run = %v, want the refusal", err)
		}
		select {
		case <-op.closed:
		case <-time.After(deadline):
			t.Error("the connection that was refused was not closed")
		}
	})
	t.Run("no answer", func(t *testing.T) {
		op := newFakeOperator()
		var mu sync.Mutex
		dials := 0
		term := startRun(t, func(ctx context.Context) (Operator, error) {
			mu.Lock()
			defer mu.Unlock()
			if dials++; dials < 4 {
				return nil, errors.New("draal is not there yet")
			}
			return op, nil
		}, nil)
		term.wait("draal does not answer: draal is not there yet")
		op.fleets <- fixtureFleet()
		term.wait("4 services", "● live")
		term.typeKeys("q")
		if err := term.end(); err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	})
}

// Review Focus 3: the connection is lost while the terminal UI is open,
// and found again: it says so, keeps what it showed, dials again, and is
// back on the view it was on, with what the view holds asked for again
// of the new connection and the old one closed. A key ends a pause. (That
// the pauses grow by themselves is the model's test,
// TestConnectionLostAndBack, and TestRunFirstDial has them run out.)
func TestRunConnectionLostAndBack(t *testing.T) {
	first, second := newFakeOperator(), newFakeOperator()
	gate := make(chan struct{}) // the second dial waits for it
	var mu sync.Mutex
	dials := 0
	term := startRun(t, func(ctx context.Context) (Operator, error) {
		mu.Lock()
		dials++
		n := dials
		mu.Unlock()
		switch n {
		case 1:
			return first, nil
		case 2:
			return nil, errors.New("draal is restarting")
		}
		select {
		case <-gate:
			return second, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, func(cfg *Config) { cfg.RetryFirst, cfg.RetryMax = time.Hour, 2*time.Hour })
	first.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jj\rj\r")
	first.linesOf(t, idOf(0x21)) <- wire.LogLine{At: t0, Stream: "stdout", Text: "the line before"}
	term.wait("Log · followed live", "the line before")

	// draal goes: the watch ends. The screen says so, keeps what it
	// showed, and the old connection is closed.
	close(first.fleets)
	term.wait("✖ lost for", "The connection to draal is lost since", "any key asks now.", "the line before", "The connection to draal was lost.")
	select {
	case <-first.closed:
	case <-time.After(deadline):
		t.Fatal("the connection that was lost was not closed")
	}
	// The pause is an hour: a key ends it. That dial fails, which is
	// said, and the next pause is two hours: a key ends it, and the dial
	// that follows waits at the gate.
	term.typeKeys("k")
	term.wait("(draal is restarting)", "any key asks now.", "the line before")
	term.typeKeys("k")
	term.wait("Asking draal now.", "the line before", "web-7d9f8b6c4-8hjwz")
	mu.Lock()
	if dials != 3 {
		t.Errorf("%d dials, want 3: the first, and one for each key", dials)
	}
	mu.Unlock()
	close(gate)
	second.fleets <- fixtureFleet()
	await(t, "the new connection to be asked for what the view holds", func() bool {
		_, history := second.was("history 21")
		_, logs := second.was("logs 21")
		return history && logs
	})
	second.linesOf(t, idOf(0x21)) <- wire.LogLine{At: t0.Add(time.Second), Stream: "stdout", Text: "the line after"}
	term.wait("● live", "the line after")
	if _, asked := second.was("rollouts web"); asked {
		t.Errorf("the rollouts were asked for on the view of a zathras: %v", second.asked)
	}
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	select {
	case <-second.closed:
	case <-time.After(deadline):
		t.Error("the second connection was not closed")
	}
}

// Review Focus 5: a flood. Ten thousand lines as fast as they are taken
// leave the terminal UI answering keys, and the end of the flood is
// what it shows. And Review Focus 2: none of the sequences of the lines
// is written to the terminal.
func TestRunStaysAwakeInAFlood(t *testing.T) {
	op := newFakeOperator()
	term := startRun(t, oneOperator(op), nil)
	op.fleets <- fixtureFleet()
	term.wait("4 services")
	term.typeKeys("jj\r\r")
	lines := op.linesOf(t, idOf(0x20))
	flooded := make(chan struct{})
	go func() {
		defer close(flooded)
		for i := range 10000 {
			lines <- wire.LogLine{At: t0.Add(time.Duration(i) * time.Microsecond), Stream: "stdout",
				Text: fmt.Sprintf("\x1b]0;pwned %d\x07\x1b[2J\x1b[32mINFO\x1b[0m reindex: document %05d of 10000", i, i+1)}
		}
	}()
	// In the middle of it, a key is answered.
	asked := time.Now()
	term.typeKeys("?")
	term.wait("on a zathras: open a shell")
	if took := time.Since(asked); took > 10*time.Second {
		t.Errorf("a key was answered after %v in a flood", took)
	}
	term.typeKeys("?")
	select {
	case <-flooded:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the flood to be read")
	}
	term.wait("document 10000 of 10000")
	term.typeKeys("q")
	if err := term.end(); err != nil {
		t.Errorf("Run = %v, want nil", err)
	}
	out := term.written()
	if strings.Contains(out, "\x1b]0;") || strings.Contains(out, "pwned") {
		t.Error("the title of a log line was written to the terminal")
	}
	// A flood is drawn some tens of times a second, not once a line: of
	// ten thousand lines, far fewer than a tenth are ever written.
	if n := strings.Count(plain(out), "reindex:"); n > 4000 {
		t.Errorf("%d of ten thousand lines were written to the terminal", n)
	}
}

// With NO_COLOR the terminal UI draws without colour, and with its
// signs and its words.
func TestRunWithoutColour(t *testing.T) {
	colour := regexp.MustCompile("\x1b\\[[0-9;]*\\b(3[0-7]|4[0-7]|9[0-7]|10[0-7]|38|48)[;m]")
	for _, env := range []string{"", "NO_COLOR=1"} {
		op := newFakeOperator()
		term := startRun(t, oneOperator(op), nil, env)
		op.fleets <- fixtureFleet()
		term.wait("4 services", "● 3 running", "✖ 1 backoff", "halted")
		term.typeKeys("q")
		if err := term.end(); err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
		if got := colour.MatchString(term.written()); got != (env == "") {
			t.Errorf("with %q in the environment, colours are written: %v", env, got)
		}
	}
}

// While a shell has the terminal the program takes no message, for as
// long as the shell is open. What carries the fleets and the lines into
// the program stands that: it reads on, keeps the newest, starts no
// goroutine and gives nothing up.
func TestRunnerStandsAProgramThatTakesNothing(t *testing.T) {
	op := newFakeOperator()
	taken := make(chan tea.Msg)
	ctx, end := context.WithCancel(context.Background())
	r := &runner{cfg: Config{Dial: oneOperator(op)}.withDefaults(), ctx: ctx}
	r.send = func(msg tea.Msg) {
		select {
		case taken <- msg:
		case <-ctx.Done():
		}
	}
	defer func() {
		end()
		r.close(deadline)
		if left := goroutinesLeft(); len(left) != 0 {
			t.Errorf("%d goroutines are left:\n%s", len(left), strings.Join(left, "\n\n"))
		}
	}()
	take := func() tea.Msg {
		t.Helper()
		for {
			select {
			case msg := <-taken:
				if _, tick := msg.(tickMsg); !tick {
					return msg
				}
			case <-time.After(deadline):
				t.Fatal("timed out waiting for a message of the runner")
			}
		}
	}
	r.do(effDial{epoch: 1})
	if msg := take(); msg != (dialedMsg{epoch: 1}) {
		t.Fatalf("the first message is %+v", msg)
	}
	r.do(effFollow{epoch: 1, zathras: idOf(0x20), id: 1})
	lines := op.linesOf(t, idOf(0x20))
	lines <- lineAt(-1)
	if msg, ok := take().(logMsg); !ok || msg.id != 1 || len(msg.lines) != 1 {
		t.Fatalf("the first line of the log is not handed on: %+v", msg)
	}

	// From here on nothing is taken. A thousand fleets and ten thousand
	// lines come, and all of them are read.
	before := len(goroutinesLeft())
	for i := range 1000 {
		f := fixtureFleet()
		f.Services[0].Stable = uint64(i + 1)
		select {
		case op.fleets <- f:
		case <-time.After(deadline):
			t.Fatalf("fleet %d was not read while the program takes nothing", i)
		}
	}
	for i := range 10000 {
		select {
		case lines <- lineAt(i):
		case <-time.After(deadline):
			t.Fatalf("line %d was not read while the program takes nothing", i)
		}
	}
	await(t, "the last fleet to be read", func() bool { return len(op.fleets) == 0 })
	if after := len(goroutinesLeft()); after > before {
		t.Errorf("there are %d goroutines of the runner, and there were %d before the flood", after, before)
	}

	// The program takes again: the newest fleet but for the one or two
	// that were on their way, and the end of the log, in order.
	var fleets, got int
	var newest uint64
	lastLine := ""
	for lastLine != "line 9999" || newest != 1000 {
		switch msg := take().(type) {
		case fleetMsg:
			fleets++
			newest = msg.fleet.Services[0].Stable
		case logMsg:
			if len(msg.lines) > logKept {
				t.Fatalf("a batch of %d lines", len(msg.lines))
			}
			got += len(msg.lines)
			if n := len(msg.lines); n > 0 {
				lastLine = msg.lines[n-1].Text
			}
		default:
			t.Fatalf("an unexpected message: %+v", msg)
		}
	}
	if fleets > 4 || got > 3*logKept {
		t.Errorf("after the program took nothing, %d fleets and %d lines were handed to it; want the newest", fleets, got)
	}
}

// Run needs a Dial and a terminal.
func TestRunNeedsItsConfig(t *testing.T) {
	if err := Run(t.Context(), Config{}); err == nil {
		t.Error("Run without a Dial and a terminal returned nil")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./tui/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui [github.com/fables-for-robots/great-machine/tui.test]
tui/run_test.go:251:17: undefined: run
tui/run_test.go:528:24: undefined: killAfter
tui/run_test.go:528:35: undefined: cutAfter
tui/run_test.go:528:45: undefined: closeAfter
tui/run_test.go:529:2: undefined: killAfter
tui/run_test.go:529:13: undefined: cutAfter
tui/run_test.go:529:23: undefined: closeAfter
tui/run_test.go:530:21: undefined: killAfter
tui/run_test.go:530:32: undefined: cutAfter
tui/run_test.go:530:42: undefined: closeAfter
tui/run_test.go:530:42: too many errors
FAIL	github.com/fables-for-robots/great-machine/tui [build failed]
FAIL
```

- [ ] **Step 3: Write the program**

Create `tui/run.go`:

```go
package tui

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// killAfter is how long the program has to end by itself once its
// context is done, before it is ended whatever it is doing. (It and the
// two times below are variables for the tests alone.)
var killAfter = 5 * time.Second

// cutAfter is how long Run waits for the program once it was ended
// whatever it was doing, and closeAfter how long it waits for what the
// program started: for an Operator whose Dial, Watch or Close does not
// return. Behind them Run returns without what it waited for.
var (
	cutAfter   = time.Second
	closeAfter = 5 * time.Second
)

// killStage is called by the timer that ends a program whatever it is
// doing: with "fired" when the time has come, with "killing" when it has
// decided to end the program and is about to, and with "done" when it
// returns. filterForTests, if set, sees every message of the program
// before it is handled, as a filter of Bubble Tea does. Both are for the
// tests alone.
var (
	killStage      = func(stage string) {}
	filterForTests func(tea.Model, tea.Msg) tea.Msg
)

// tickEvery is how often the times that are drawn move on.
const tickEvery = time.Second

// Run runs the terminal UI until the user quits, which is nil, or ctx is
// done, which is the error of ctx. The terminal is as it was when Run
// returns.
//
// What draal refuses at the first dial, by Dial or by the watch that
// follows it (the code unauthorized), ends Run with that error. Any
// other failure of a dial is tried again.
//
// Run returns within a bounded time of ctx being done, whatever the
// program and the Operator do. A program that has not ended killAfter
// after ctx was done (an Update that does not return) is ended as it
// is: the terminal is restored, and Run returns the error of ctx in a
// sentence that says so, leaving behind the goroutine that is stuck and
// what it holds. (A program that had begun to end by itself by then is
// not ended a second time: should it stay in its last view, Run returns
// all the same, and says that the terminal may not be as it was.) And when something asked of the Operator does not
// return closeAfter after it was told to end (a Dial, a Watch or a
// Close that does not honour its context), Run returns what it would
// have returned, says so in the log of its Config, and leaves behind
// the goroutines that wait for the Operator, and its connection.
//
// The text of every error Run returns that has words of draal in it is
// safe to print on a terminal: it went through the function everything
// that is drawn goes through. errors.Is and errors.As see through it;
// the *wire.Error that errors.As finds has its code and its message
// made safe likewise.
func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, nil)
}

// run is Run for the tests: with drawn, if set, called with every view
// as it is handed to the renderer, and with options of Bubble Tea (the
// environment).
func run(ctx context.Context, cfg Config, drawn func(view string), opts ...tea.ProgramOption) error {
	if cfg.Dial == nil || cfg.In == nil || cfg.Out == nil {
		return errors.New("tui: Dial, In and Out are needed")
	}
	cfg = cfg.withDefaults()
	all, end := context.WithCancel(context.WithoutCancel(ctx))
	prog := &program{m: newModel(cfg), r: &runner{cfg: cfg, ctx: all}, drawn: drawn}
	prog.r.crash = func(v any) { prog.recovered(v) }
	// The context is not given to Bubble Tea: it would end by closing
	// its reader of the terminal under the read. It is asked to quit.
	// It handles no signals: they are the caller's, who ends ctx.
	// How the program ends is decided once, under one mutex: by itself,
	// when it takes the message that tells it to quit, or by the timer
	// below, whatever it is doing. Whoever comes second finds it decided
	// and leaves the terminal alone: the terminal is given back by one
	// of them, and nothing is written to it once Run has returned.
	var ending sync.Mutex
	decided := false
	// The filter is the place where the program is seen to take that
	// message; every other message passes as it came. It is the last of
	// the options, so that no other filter takes its place.
	ends := tea.WithFilter(func(m tea.Model, msg tea.Msg) tea.Msg {
		if filterForTests != nil {
			if msg = filterForTests(m, msg); msg == nil {
				return nil
			}
		}
		switch msg.(type) {
		case tea.QuitMsg, tea.InterruptMsg:
			// A kill that is under way is waited for here. When this
			// message is let through after it, the program's context is
			// done: it ends as one that was killed, and draws nothing.
			ending.Lock()
			decided = true
			ending.Unlock()
		}
		return msg
	})
	options := append([]tea.ProgramOption{tea.WithInput(cfg.In), tea.WithOutput(cfg.Out), tea.WithoutSignalHandler()}, opts...)
	p := tea.NewProgram(prog, append(options, ends)...)
	prog.r.send = p.Send
	killed, late := make(chan struct{}), make(chan struct{})
	stopQuit := context.AfterFunc(ctx, func() {
		// The end whatever it is doing is set first: asking the program
		// to quit waits until it takes the message, and a program whose
		// Update does not return, or that has given the terminal to a
		// shell, takes none.
		kill := time.AfterFunc(killAfter, func() {
			killStage("fired")
			defer killStage("done")
			ending.Lock()
			defer ending.Unlock()
			if decided {
				// The program is ending by itself, or has ended: it
				// gives the terminal back, and this must not.
				close(late)
				return
			}
			decided = true
			killStage("killing")
			// The terminal is given back the way it is for a shell,
			// which waits for the reader of the terminal to have ended;
			// the kill alone closes the terminal under its read. Each of
			// the two closes the renderer, so the resets are written
			// twice; the second set changes nothing but for a terminal
			// with a stack of keyboard modes pushed by the program that
			// started this one, which loses one level. Bubble Tea has no
			// way to end a program that is stuck without closing, and
			// this is the path of a failure.
			//
			// One window is left: a program that ends through an error of
			// Bubble Tea's own (not by a message to quit) at the very
			// moment this fires gives the terminal back while this does.
			_ = p.ReleaseTerminal()
			p.Kill()
			close(killed)
		})
		prog.r.mu.Lock()
		prog.r.kill = kill
		prog.r.mu.Unlock()
		go p.Quit()
	})
	ran := make(chan error, 1)
	go func() {
		_, err := p.Run()
		ran <- err
	}()
	var err error
	cut, itself := false, false
	select {
	case err = <-ran:
		// The program has ended and given the terminal back. A timer
		// that fires from here on does nothing, and one that is at work
		// is waited for.
		ending.Lock()
		decided = true
		ending.Unlock()
	case <-killed:
		// The terminal is restored. The program returns when its Update
		// does; Run does not wait for that.
		select {
		case err = <-ran:
		case <-time.After(cutAfter):
			cut = true
		}
	case <-late:
		// The program took the message to quit and has not ended by the
		// time it would have been killed: it is in its last view, or
		// giving the terminal back. That is its own to finish, and is
		// waited for a moment longer, and no more.
		select {
		case err = <-ran:
		case <-time.After(cutAfter):
			cut, itself = true, true
		}
	}
	stopQuit()
	// Everything the program started is ended, and waited for.
	end()
	if !prog.r.close(closeAfter) {
		cfg.Log.Warn("the terminal UI has ended, and something it asked of draal has not: it is left behind", "after", closeAfter)
	}
	if itself {
		return fmt.Errorf("the terminal UI did not end %v after it was told to, while it was ending by itself, and was left to it: the terminal may not be as it was: %w", killAfter, context.Cause(ctx))
	}
	if cut {
		return fmt.Errorf("the terminal UI did not end %v after it was told to and was cut off, with the terminal restored: %w", killAfter, context.Cause(ctx))
	}

	prog.mu.Lock()
	defer prog.mu.Unlock()
	switch {
	case prog.panicked != nil:
		return fmt.Errorf("the terminal UI failed, and the terminal was restored: panic: %s\n%s", clean(capped(fmt.Sprint(prog.panicked))), prog.stack)
	case errors.Is(err, tea.ErrProgramPanic):
		return fmt.Errorf("the terminal UI failed, and the terminal was restored: %w", err)
	case prog.failed != nil:
		return said{prog.failed}
	case prog.quit:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return fmt.Errorf("the terminal UI: %w", err)
	}
	return nil
}

// said is an error of draal's as Run returns it: with a text that is
// safe to print on a terminal.
type said struct{ err error }

// Error returns the text of the error as it may be drawn.
func (e said) Error() string { return clean(capped(e.err.Error())) }

// Unwrap returns the error as it came, for errors.Is.
func (e said) Unwrap() error { return e.err }

// As gives errors.As a *wire.Error whose code and message are as safe
// as the text: the refusal in the chain, made clean.
func (e said) As(target any) bool {
	t, ok := target.(**wire.Error)
	if !ok {
		return false
	}
	w, ok := refusal(e.err)
	if !ok {
		return false
	}
	*t = &wire.Error{Code: wire.Code(clean(capped(string(w.Code)))), Message: clean(capped(w.Message))}
	return true
}

// program is the model as Bubble Tea runs it: it hands the messages to
// the model, has the runner carry out what the model wants, and draws
// the model on the alternate screen. A panic of the model ends the
// program in the usual way, so that the terminal is restored, and Run
// returns it as an error.
type program struct {
	m     *model
	r     *runner
	drawn func(view string) // for the tests: told every view

	mu       sync.Mutex
	panicked any    // what the model panicked with
	stack    []byte // and where
	quit     bool   // the user has quit
	failed   error  // why the model ended the program
}

// crashedMsg says that a view panicked: Update ends the program.
type crashedMsg struct{}

// recovered takes a panic of the model. It says whether there was one.
func (p *program) recovered(r any) bool {
	if r == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panicked == nil {
		p.panicked, p.stack = r, debug.Stack()
	}
	return true
}

// crashed says whether the model has panicked.
func (p *program) crashed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.panicked != nil
}

// Init begins the model.
func (p *program) Init() (cmd tea.Cmd) {
	defer func() {
		if p.recovered(recover()) {
			cmd = tea.Quit
		}
	}()
	return p.do(p.m.start())
}

// Update hands a message to the model and carries out what it wants.
func (p *program) Update(msg tea.Msg) (next tea.Model, cmd tea.Cmd) {
	if p.crashed() {
		return p, tea.Quit
	}
	defer func() {
		if p.recovered(recover()) {
			next, cmd = p, tea.Quit
		}
	}()
	return p, p.do(p.m.update(msg))
}

// View draws the model on the alternate screen, without a cursor and
// without the mouse.
func (p *program) View() (v tea.View) {
	v.AltScreen = true
	if p.crashed() {
		return v
	}
	defer func() {
		if p.recovered(recover()) {
			v.SetContent("")
			p.r.spawn(func() { p.r.send(crashedMsg{}) })
		}
	}()
	content := p.m.view()
	if p.drawn != nil {
		p.drawn(content)
	}
	v.SetContent(content)
	return v
}

// do carries out effects, and returns what Bubble Tea is to do for
// them.
func (p *program) do(effs []effect) tea.Cmd {
	var cmds []tea.Cmd
	for _, e := range effs {
		switch e := e.(type) {
		case effQuit:
			p.mu.Lock()
			p.quit = true
			p.mu.Unlock()
			cmds = append(cmds, tea.Quit)
		case effFail:
			p.mu.Lock()
			p.failed = e.err
			p.mu.Unlock()
			cmds = append(cmds, tea.Quit)
		case effShell:
			if cmd := p.r.shell(e); cmd != nil {
				cmds = append(cmds, cmd)
			}
		default:
			p.r.do(e)
		}
	}
	return tea.Batch(cmds...)
}

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
	send  func(tea.Msg)
	kill  *time.Timer
	crash func(v any) // takes a panic of a goroutine of the runner

	wg   sync.WaitGroup
	mu   sync.Mutex
	conn *conn // the connection that is dialed or there
	tick bool  // the ticker runs
}

// conn is one connection to draal: what Dial returned, and the context
// of everything that is asked of it.
type conn struct {
	epoch  int
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	op       Operator           // nil until Dial has returned
	hungUp   bool               // it is to be closed as soon as it is there
	unfollow context.CancelFunc // ends the log that is followed
}

// spawn runs f as a goroutine that close waits for. A panic in it, which
// may be one of a method of the Operator, ends the program as a panic of
// the model does: the terminal is restored, and Run returns it.
func (r *runner) spawn(f func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			v := recover()
			switch {
			case v == nil:
			case r.crash == nil:
				panic(v)
			default:
				r.crash(v)
				r.send(crashedMsg{})
			}
		}()
		f()
	}()
}

// close ends everything the runner started and waits for it, for as
// long as wait at most. It says whether everything has ended.
func (r *runner) close(wait time.Duration) bool {
	r.mu.Lock()
	if r.kill != nil {
		r.kill.Stop()
	}
	c := r.conn
	r.conn = nil
	r.mu.Unlock()
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

// hangUp ends everything that was asked of the connection and closes
// it, now or as soon as Dial has returned it.
func (c *conn) hangUp() {
	c.cancel()
	c.mu.Lock()
	op := c.op
	c.op, c.hungUp = nil, true
	c.mu.Unlock()
	if op != nil {
		op.Close()
	}
}

// current returns the connection of that epoch and its Operator, if it
// is the one there is and it is dialed.
func (r *runner) current(epoch int) (*conn, Operator) {
	r.mu.Lock()
	c := r.conn
	r.mu.Unlock()
	if c == nil || c.epoch != epoch {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.op == nil {
		return nil, nil
	}
	return c, c.op
}

// do carries out one effect.
func (r *runner) do(e effect) {
	switch e := e.(type) {
	case effDial:
		r.dial(e.epoch)
	case effHangUp:
		r.mu.Lock()
		c := r.conn
		r.conn = nil
		r.mu.Unlock()
		if c != nil {
			r.spawn(c.hangUp)
		}
	case effWait:
		r.spawn(func() {
			timer := time.NewTimer(e.pause)
			defer timer.Stop()
			select {
			case <-timer.C:
				r.send(retryMsg{n: e.n})
			case <-r.ctx.Done():
			}
		})
	case effRollouts:
		if c, op := r.current(e.epoch); c != nil {
			r.spawn(func() {
				list, err := op.Rollouts(c.ctx, e.service)
				r.answer(c, err, rolloutsMsg{epoch: e.epoch, n: e.n, service: e.service, rollouts: list, err: err})
			})
		}
	case effHistory:
		if c, op := r.current(e.epoch); c != nil {
			r.spawn(func() {
				samples, err := op.History(c.ctx, e.zathras)
				r.answer(c, err, historyMsg{epoch: e.epoch, n: e.n, zathras: e.zathras, samples: samples, err: err})
			})
		}
	case effFollow:
		if c, op := r.current(e.epoch); c != nil {
			ctx, cancel := context.WithCancel(c.ctx)
			c.mu.Lock()
			old := c.unfollow
			c.unfollow = cancel
			c.mu.Unlock()
			if old != nil {
				old()
			}
			r.spawn(func() { r.follow(ctx, c, op, e) })
		}
	case effUnfollow:
		r.mu.Lock()
		c := r.conn
		r.mu.Unlock()
		if c != nil {
			c.mu.Lock()
			old := c.unfollow
			c.unfollow = nil
			c.mu.Unlock()
			if old != nil {
				old()
			}
		}
	}
}

// answer tells the program what draal answered a request: the answer,
// or draal's refusal, as msg has it; or, when the request failed with
// anything else, that the connection is lost. Of a connection that was
// hung up meanwhile nothing is told.
func (r *runner) answer(c *conn, err error, msg tea.Msg) {
	if c.ctx.Err() != nil {
		return
	}
	if _, refused := refusal(err); err != nil && !refused {
		r.send(lostMsg{epoch: c.epoch, err: err})
		return
	}
	r.send(msg)
}

// dial begins the connection of an epoch in place of the one there is:
// Dial, then the watch, whose fleets it hands on until it ends.
func (r *runner) dial(epoch int) {
	ctx, cancel := context.WithCancel(r.ctx)
	c := &conn{epoch: epoch, ctx: ctx, cancel: cancel}
	r.mu.Lock()
	old := r.conn
	r.conn = c
	ticking := r.tick
	r.tick = true
	r.mu.Unlock()
	if old != nil {
		r.spawn(old.hangUp)
	}
	if !ticking {
		r.spawn(r.ticker)
	}
	r.spawn(func() {
		defer cancel()
		op, err := r.cfg.Dial(ctx)
		if err == nil && op == nil {
			err = errors.New("Dial returned no connection")
		}
		if err != nil {
			if ctx.Err() == nil {
				r.send(dialFailedMsg{epoch: epoch, err: err})
			}
			return
		}
		c.mu.Lock()
		if c.hungUp {
			c.mu.Unlock()
			op.Close()
			return
		}
		c.op = op
		c.mu.Unlock()

		fleets, err := op.Watch(ctx)
		if err != nil {
			gone := ctx.Err() != nil
			c.hangUp()
			if !gone {
				r.send(dialFailedMsg{epoch: epoch, err: fmt.Errorf("asking draal for the fleet: %w", err)})
			}
			return
		}
		r.send(dialedMsg{epoch: epoch})

		// The watch is read as it comes, and the newest fleet is kept
		// for the program, which takes it when it does.
		newest := make(chan wire.Fleet, 1)
		r.spawn(func() {
			defer close(newest)
			for f := range fleets {
				select {
				case newest <- f:
				default:
					select {
					case <-newest:
					default:
					}
					newest <- f
				}
			}
		})
		for f := range newest {
			if ctx.Err() != nil {
				continue
			}
			r.send(fleetMsg{epoch: epoch, fleet: f})
		}
		if ctx.Err() == nil {
			r.send(lostMsg{epoch: epoch})
		}
	})
}

// ticker tells the program every second that time has passed.
func (r *runner) ticker() {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.send(tickMsg{})
		case <-r.ctx.Done():
			return
		}
	}
}

// follow follows the log of a zathras until ctx is done or the log
// ends. The lines are read as fast as they come; the program is handed
// what has come every logEvery at most, and of a flood the end of it.
//
// draal answers a log with its lines alone, so Logs returns with the
// first line, with the end of the log, or with a refusal: of a log that
// is followed and has no line yet, nothing is known until one comes,
// and the program is told nothing.
func (r *runner) follow(ctx context.Context, c *conn, op Operator, e effFollow) {
	lines, err := op.Logs(ctx, e.zathras, true)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if _, refused := refusal(err); !refused {
			r.send(lostMsg{epoch: e.epoch, err: err})
			return
		}
		r.send(logEndedMsg{epoch: e.epoch, id: e.id, err: err})
		return
	}
	var mu sync.Mutex
	waiting := pending{keep: logKept}
	ended := false
	wake := make(chan struct{}, 1)
	r.spawn(func() {
		for l := range lines {
			mu.Lock()
			waiting.add(l)
			mu.Unlock()
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		mu.Lock()
		ended = true
		mu.Unlock()
		close(wake)
	})
	pause := time.NewTimer(0)
	defer pause.Stop()
	for range wake {
		// The reader is waited for in any case: it ends when the
		// channel of the lines is closed, which ctx sees to.
		if ctx.Err() != nil {
			continue
		}
		select {
		case <-pause.C:
		case <-ctx.Done():
			continue
		}
		mu.Lock()
		batch, done := waiting.take(), ended
		mu.Unlock()
		if len(batch) > 0 {
			r.send(logMsg{epoch: e.epoch, id: e.id, lines: batch})
		}
		if done {
			break
		}
		pause.Reset(logEvery)
	}
	mu.Lock()
	batch := waiting.take()
	mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	if len(batch) > 0 {
		r.send(logMsg{epoch: e.epoch, id: e.id, lines: batch})
	}
	r.send(logEndedMsg{epoch: e.epoch, id: e.id})
}
```

**Replace** `tui/shell.go` with the following. It is the file of Task 5 with one function more at its end, `(*runner).shell`, by which the program carries out `effShell`:

```go
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/fables-for-robots/great-machine/wire"
)

// The shell of a zathras comes with a later task. This file is its
// place: the effect by which the model asks for one, the message by
// which the program says how it ended, and what the key does until then.

// effShell asks the program for a shell on the zathras: to give the
// terminal to the session, and to take it back when the shell has ended
// or was left.
type effShell struct {
	epoch   int
	zathras wire.ID
}

// shellEndedMsg says how a shell ended: with an exit status, left with
// Ctrl-], or with an error.
type shellEndedMsg struct {
	exit     int
	detached bool
	err      error
}

// openShell is the key s on a zathras that is connected, with a
// connection to draal that is live.
func (m *model) openShell() []effect {
	m.notice = "The shell is not part of this terminal UI yet: `great-machine shell` opens one."
	return nil
}

// shellEnded takes the message that a shell has ended. While the shell
// had the terminal the model was told nothing, for as long as that was:
// what the view holds is asked for anew.
func (m *model) shellEnded(msg shellEndedMsg) []effect {
	return m.again()
}

// shell carries out effShell. Until the shell is built it does nothing.
// It is called from Update, and what it returns is run by Bubble Tea:
// tea.Exec gives the terminal away for as long as its command runs, and
// takes it back.
func (r *runner) shell(e effShell) tea.Cmd {
	return nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./tui/ -run 'TestRun' -v`
Expected: PASS: `TestRunShowsTheFleetAndWhatAViewHolds`, `TestRunEndsWithItsContext`, `TestRunRestoresTheTerminalAfterAPanic` (`Update`, `View`), `TestRunRestoresTheTerminalWhenTheOperatorPanics`, `TestRunEndsAProgramThatIsStuck`, `TestRunTheKillThatComesLateDoesNothing`, `TestRunTheKillWinsOverAQuitThatIsOnItsWay`, `TestRunDoesNotWaitForAnOperatorThatDoesNotReturn`, `TestRunFirstDial` (four), `TestRunConnectionLostAndBack`, `TestRunStaysAwakeInAFlood`, `TestRunWithoutColour`, `TestRunnerStandsAProgramThatTakesNothing`, `TestRunNeedsItsConfig`; each under a second.

Run: `nix develop .#bare --command go test -race -count=50 -skip TestEverySizeOfTerminal ./tui/`
Expected: `ok` (about seven minutes)

- [ ] **Step 5: Commit**

```bash
git add tui/run.go tui/run_test.go tui/shell.go
git commit -m "TUI: the program around the model: Run, with the dialing again, the end on quit and on a context that is done, and the terminal as it was"
```

- [ ] **Step 6: Write the failing test of the demo**

Create `tui/demo/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// sequences is what a terminal does not show of what is written to it.
var sequences = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]|\x1b[=>]|\\s")

// The smoke test: the demo is started in a pseudo-terminal, shows the
// made-up fleet, or with --empty that there is no service, and ends
// when q is typed.
func TestDemoRuns(t *testing.T) {
	for _, tt := range []struct {
		empty       bool
		says, shows string
	}{
		{false, "demo: a made-up fleet behind the real terminal UI of great-machine\n", "4services"},
		{true, "demo: a fleet without a service behind the real terminal UI of great-machine\n", "draalknowsnoserviceyet."},
	} {
		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("opening a pseudo-terminal: %v", err)
		}
		if err := pty.Setsize(master, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
			t.Fatalf("sizing the pseudo-terminal: %v", err)
		}
		var mu sync.Mutex
		var out bytes.Buffer
		read := make(chan struct{})
		go func() {
			defer close(read)
			buf := make([]byte, 32<<10)
			for {
				n, err := master.Read(buf)
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		var said bytes.Buffer
		ended := make(chan error, 1)
		go func() { ended <- run(t.Context(), slave, slave, tt.empty, &said) }()
		for end := time.Now().Add(deadline); ; time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			shown := sequences.ReplaceAllString(out.String(), "")
			mu.Unlock()
			if strings.Contains(shown, tt.shows) && strings.Contains(shown, "live") {
				break
			}
			if time.Now().After(end) {
				t.Fatalf("timed out waiting for the demo to show %q; it shows %q", tt.shows, shown)
			}
		}
		if _, err := master.WriteString("q"); err != nil {
			t.Fatalf("typing q: %v", err)
		}
		select {
		case err := <-ended:
			if err != nil {
				t.Errorf("run = %v, want nil", err)
			}
		case <-time.After(deadline):
			t.Fatal("timed out waiting for the demo to end")
		}
		if got := said.String(); !strings.HasPrefix(got, "demo: the shell of the terminal UI is a REAL SHELL ON THIS MACHINE") || !strings.HasSuffix(got, tt.says) {
			t.Errorf("the demo says %q", got)
		}
		// Bubble Tea may ask the terminal for its size for a moment
		// after it has ended: it is closed when nothing of it is left.
		for end := time.Now().Add(deadline); ; time.Sleep(5 * time.Millisecond) {
			buf := make([]byte, 4<<20)
			if stacks := string(buf[:runtime.Stack(buf, true)]); !strings.Contains(stacks, "charm.land/bubbletea") {
				break
			}
			if time.Now().After(end) {
				t.Fatal("timed out waiting for the goroutines of Bubble Tea to end")
			}
		}
		slave.Close()
		master.Close()
		select {
		case <-read:
		case <-time.After(deadline):
			t.Error("timed out waiting for the pseudo-terminal to be read to its end")
		}
	}
}

// The connection of the demo is closed as a client's is: what was asked
// of it ends, and the shell it has ready is a session of the terminal
// UI.
func TestOperatorOfTheDemoEndsWhatWasAskedOfIt(t *testing.T) {
	fleet := demo.New(false)
	op := newOperator(fleet)
	fleets, err := op.Watch(context.Background())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	f := <-fleets
	if len(f.Services) != 4 {
		t.Fatalf("the fleet has %d services", len(f.Services))
	}
	var z wire.Zathras
	for _, svc := range f.Services {
		if svc.Name == "web" {
			z = svc.Zathrases[0]
		}
	}
	if list, err := op.Rollouts(context.Background(), "web"); err != nil || len(list) != 20 {
		t.Errorf("Rollouts = %d of them, %v", len(list), err)
	}
	if samples, err := op.History(context.Background(), z.ID); err != nil || len(samples) < 400 {
		t.Errorf("History = %d samples, %v", len(samples), err)
	}
	lines, err := op.Logs(context.Background(), z.ID, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	<-lines
	sh, err := op.Shell(context.Background(), z.ID, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Errorf("closing the shell: %v", err)
	}

	op.Close()
	timeout := time.After(deadline)
	for fleets != nil || lines != nil {
		select {
		case _, open := <-fleets:
			if !open {
				fleets = nil
			}
		case _, open := <-lines:
			if !open {
				lines = nil
			}
		case <-timeout:
			t.Fatal("timed out waiting for the watch and the log to end when the connection was closed")
		}
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./tui/demo/`
Expected: FAIL, the package does not build:

```
# github.com/fables-for-robots/great-machine/tui/demo [github.com/fables-for-robots/great-machine/tui/demo.test]
tui/demo/main_test.go:61:24: undefined: run
tui/demo/main_test.go:113:8: undefined: newOperator
FAIL	github.com/fables-for-robots/great-machine/tui/demo [build failed]
FAIL
```

- [ ] **Step 8: Write the demo**

Create `tui/demo/main.go`:

```go
// Command demo runs the terminal UI of great-machine over a made-up
// fleet, for developing the terminal UI: the real program of the package
// tui, and behind it the controller of the package demo, which invents
// four services, their zathrases, their figures and their logs, and
// moves them along.
//
//	go run ./tui/demo [--empty]
//
// With --empty there is no service at all, as draal has none when it was
// started for the first time.
//
// Everything it shows is made up but the shell: THE SHELL OF THE
// TERMINAL UI IS A REAL SHELL ON THE MACHINE THAT RUNS THE DEMO, as the
// user who runs it.
//
// It is not part of the program, and nothing of great-machine uses it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	xterm "golang.org/x/term"

	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/tui"
	"github.com/fables-for-robots/great-machine/wire"
)

func main() {
	empty := flag.Bool("empty", false, "show a fleet without a service, as a draal that was just started")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "demo: %q is no argument of the demo\n", flag.Arg(0))
		os.Exit(2)
	}
	if !xterm.IsTerminal(int(os.Stdin.Fd())) || !xterm.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "demo: the terminal UI needs a terminal as its standard input and its standard output")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := run(ctx, os.Stdin, os.Stdout, *empty, os.Stderr); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// run runs the terminal UI on the terminal in and out, over the made-up
// fleet or, with empty, over a fleet without a service, until the user
// quits or ctx is done. It writes to errs what it is.
func run(ctx context.Context, in, out *os.File, empty bool, errs io.Writer) error {
	what := "a made-up fleet"
	if empty {
		what = "a fleet without a service"
	}
	fmt.Fprintf(errs, "demo: the shell of the terminal UI is a REAL SHELL ON THIS MACHINE, as the user who runs the demo\n")
	fmt.Fprintf(errs, "demo: %s behind the real terminal UI of great-machine\n", what)

	fleet := demo.New(empty)
	moving, stopMoving := context.WithCancel(context.Background())
	defer stopMoving()
	go fleet.Run(moving)

	return tui.Run(ctx, tui.Config{
		Dial:  func(context.Context) (tui.Operator, error) { return newOperator(fleet), nil },
		In:    in,
		Out:   out,
		Draal: "the demo",
	})
}

// operator is a draal.Controller as the tui.Operator of one connection:
// what an *operator.Client is over a real draal. Close ends what was
// asked of it, as closing a client does.
type operator struct {
	c    draal.Controller
	ctx  context.Context
	stop context.CancelFunc
}

// An operator is a tui.Operator.
var _ tui.Operator = (*operator)(nil)

// newOperator returns a connection to the controller.
func newOperator(c draal.Controller) *operator {
	ctx, stop := context.WithCancel(context.Background())
	return &operator{c: c, ctx: ctx, stop: stop}
}

// both returns a context that is done when ctx is, or when the
// connection is closed, and what frees it.
func (o *operator) both(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

// Watch sends the fleet, now and whenever it changes, until ctx is done
// or the connection is closed.
func (o *operator) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	ctx, free := o.both(ctx)
	fleets, err := o.c.Watch(ctx)
	if err != nil {
		free()
		return nil, err
	}
	context.AfterFunc(ctx, free)
	return fleets, nil
}

// Rollouts returns the last rollouts of a service.
func (o *operator) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	ctx, free := o.both(ctx)
	defer free()
	return o.c.Rollouts(ctx, service)
}

// History returns the samples of a zathras.
func (o *operator) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	ctx, free := o.both(ctx)
	defer free()
	return o.c.History(ctx, zathras)
}

// Logs sends the log of a zathras, until ctx is done or the connection
// is closed.
func (o *operator) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	ctx, free := o.both(ctx)
	lines, err := o.c.Logs(ctx, zathras, follow)
	if err != nil {
		free()
		return nil, err
	}
	context.AfterFunc(ctx, free)
	return lines, nil
}

// Shell starts a shell: of the demo, a real one on this machine.
func (o *operator) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (tui.Shell, error) {
	return o.c.Shell(ctx, zathras, req)
}

// Close ends the connection.
func (o *operator) Close() error {
	o.stop()
	return nil
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race -count=20 ./tui/demo/`
Expected: `ok`

- [ ] **Step 10: Look at it, in a real terminal**

Run: `nix develop .#bare --command go run ./tui/demo`

What must be seen and done, in a terminal of 80×24 or more:

1. Before anything else, on the line the prompt was on: `demo: the shell of the terminal UI is a REAL SHELL ON THIS MACHINE, as the user who runs the demo`. Then the alternate screen: four services, `● live` at the right of the header.
2. `j`, `j`, `Enter`: the service `web`, with a rollout that moves: its zathrases wander from `preparing` to `prepared` to `on it`, the counts of draal follow, and the table changes with them. Two zathrases have one name, and are told apart by `·` and four characters of their IDs.
3. `Enter`: a zathras. Its sparklines grow at the right every two seconds; its log runs. `k` stops it following (the title says which rows are in sight), `G` follows again.
4. `Esc`, `j`, `Enter` on `web-…8hjwz`: about 14 seconds after a rollout began it floods its log for ten seconds, a thousand lines a second. The log runs and the terminal UI answers `?` at once; where lines were lost on the way, a yellow line in the log says how many.
5. `Esc`, `G`, `Enter`: `web-…q5rvd`, which goes about 43 seconds into every minute for six seconds. A red banner says since when it is not connected; what it reported last stays; and when it is back, the banner goes and the log carries on without a line twice.
6. `s`: one yellow line above the keys says that the shell is not part of the terminal UI yet. The next key takes it away.
7. Make the terminal smaller than 50×12: one line says so. Larger again: the view is back.
8. `q`: the prompt is back on the line under the two lines of the demo, the screen as it was before, the cursor visible, and what is typed next is echoed. The same after Ctrl-C, and after `kill -TERM` of the demo from another terminal.
9. `NO_COLOR=1 nix develop .#bare --command go run ./tui/demo`: no colour, and every state still has its sign and its word.
10. `nix develop .#bare --command go run ./tui/demo --empty`: `draal knows no service yet.`, and the command that starts a zathras.
11. `nix develop .#bare --command go run ./tui/demo < /dev/null`: one line, `demo: the terminal UI needs a terminal as its standard input and its standard output`, and `exit status 2` from `go run`.

- [ ] **Step 11: Commit**

```bash
git add tui/demo
git commit -m "TUI: the demo: the terminal UI over the made-up fleet"
```
