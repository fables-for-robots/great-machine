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

	"github.com/fables-for-robots/great-machine/tui/tuitest"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// fakeOperator is an Operator whose channels the test feeds.
type fakeOperator struct {
	mu      sync.Mutex
	asked   []string                                                                         // what was asked of it, in order
	fleets  chan wire.Fleet                                                                  // what Watch returns
	lines   map[wire.ID]chan wire.LogLine                                                    // what Logs returns; made when asked for
	history func() ([]wire.Sample, error)                                                    // what History answers; nil for no samples
	logs    func(id wire.ID) error                                                           // what Logs answers; nil for a log
	watch   func() error                                                                     // what Watch answers; nil for the fleets
	shell   func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) // what Shell answers; nil for a refusal
	done    map[string]context.Context                                                       // the contexts of what was asked
	closed  chan struct{}                                                                    // closed by Close
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
	f.note(ctx, "shell "+string(zathras[:2]))
	if f.shell != nil {
		return f.shell(ctx, zathras, req)
	}
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
		// The terminal's own file is given up without a Close: see
		// tuitest.Release.
		tuitest.Release(t, slave)
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
