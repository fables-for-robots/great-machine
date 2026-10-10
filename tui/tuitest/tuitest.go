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
