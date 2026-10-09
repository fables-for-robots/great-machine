// Package term joins a remote shell session to the local terminal: what is
// typed goes to the session, what the session writes goes to the terminal,
// and the size of the terminal follows the window. It is what
// `great-machine shell` is, and what the terminal UI gives the terminal to.
package term

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	xterm "golang.org/x/term"
)

// Session is what Attach joins the terminal to. *operator.Shell is one.
type Session interface {
	io.Reader
	io.Writer
	Resize(cols, rows uint16) error
	Wait(ctx context.Context) (int, error)
}

// Detach is the byte that ends Attach without ending the shell: Ctrl-].
const Detach = 0x1d

// endOfInput is what a terminal in its usual mode takes for the end of
// what is typed: Ctrl-D.
const endOfInput = 0x04

// Attach copies in to the session and the session to out until the shell
// has ended or, on a terminal, Detach was typed. When in is a terminal it
// is made raw, its size is sent at once and after every change, and it is
// restored before Attach returns. When in is no terminal it is copied as
// it is, and at its end the byte 0x04 is sent so that the shell ends.
//
// exit is the exit status of the shell, and detached says that Detach
// ended Attach and the shell still runs. A ctx that is done ends Attach
// with the error of ctx. The session is not closed: that is the caller's
// to do, and it ends what Attach has left reading.
//
// Nothing is written to the session or to out once Attach has returned.
// Two more things where the documentation of the session leaves room:
//
//   - In a file that is no terminal the byte of Detach is data like any
//     other, and no size is sent: the session has the size it was asked
//     for with.
//   - Input that does not end with a line feed gets one before the 0x04.
//     A shell takes 0x04 for the end of its input at the start of a line
//     alone, and the last line is to be run as `sh` runs the last line of
//     a file that lacks its line feed.
func Attach(ctx context.Context, in *os.File, out io.Writer, s Session) (exit int, detached bool, err error) {
	return newAttachment(in, out, s).run(ctx)
}

// Size returns the size of the terminal f is, or 80 and 24.
func Size(f *os.File) (cols, rows uint16) {
	w, h, err := xterm.GetSize(int(f.Fd()))
	if err != nil || w <= 0 || h <= 0 || w > 0xffff || h > 0xffff {
		return 80, 24
	}
	return uint16(w), uint16(h)
}

// attachment is one Attach.
type attachment struct {
	in  *os.File
	out io.Writer
	s   Session
	tty bool // in is a terminal

	// toSession lets input and sizes through to the session, one at a
	// time, and toOut output to out, each until Attach returns.
	toSession, toOut gate

	// inputEnded is closed when the goroutine that reads in has ended,
	// which may be long after Attach returned.
	inputEnded chan struct{}
}

func newAttachment(in *os.File, out io.Writer, s Session) *attachment {
	return &attachment{in: in, out: out, s: s, inputEnded: make(chan struct{})}
}

// result is how one of the goroutines of an attachment ended it.
type result struct {
	exit     int
	detached bool
	err      error
	panicked string // what a goroutine panicked with, and where
}

func (a *attachment) run(ctx context.Context) (exit int, detached bool, err error) {
	fd := int(a.in.Fd())
	a.tty = xterm.IsTerminal(fd)

	// Shutting the gates is deferred first and so happens last, when the
	// terminal is restored. A Write that is under way is waited for, but
	// not when ctx is done: a session that takes no more input would keep
	// Attach from returning. That Write may then end after Attach.
	wait := true
	defer func() {
		a.toSession.shut(wait)
		a.toOut.shut(wait)
	}()

	// A goroutine tells how it ended here. There is room for every one of
	// them, so that none waits for an Attach that has returned.
	results := make(chan result, 3)

	if a.tty {
		state, err := xterm.MakeRaw(fd)
		if err != nil {
			return 0, false, fmt.Errorf("term: making the terminal raw: %w", err)
		}
		// Deferred, so that it happens however this function is left: by
		// a return, or by the panic below.
		defer xterm.Restore(fd, state)

		a.sendSize()
		changed, stop := notifyResize()
		defer stop()
		a.spawn(results, func() (result, bool) {
			for range changed {
				a.sendSize()
			}
			return result{}, false
		})
	}

	// Neither goroutine is waited for when Attach returns. The one that
	// reads in cannot be interrupted while it waits in Read on a terminal:
	// it ends with the next key, or never. The one that reads the session
	// ends when the caller closes the session. What either of them reads
	// after Attach has returned is stopped by its gate and dropped.
	a.spawn(results, a.input)
	a.spawn(results, func() (result, bool) { return a.output(ctx), true })

	select {
	case <-ctx.Done():
		wait = false
		return 0, false, ctx.Err()
	case r := <-results:
		if r.panicked != "" {
			// A panic in a goroutine would end the program with the
			// terminal left raw. It goes on from here instead, behind the
			// deferred Restore.
			panic(r.panicked)
		}
		return r.exit, r.detached, r.err
	}
}

// spawn runs f in a goroutine. What f returns ends Attach when its second
// value is true, and so does a panic of f.
func (a *attachment) spawn(results chan<- result, f func() (result, bool)) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				results <- result{panicked: fmt.Sprintf("term: panic while attached: %v\n\n%s", v, debug.Stack())}
			}
		}()
		if r, ok := f(); ok {
			results <- r
		}
	}()
}

// sendSize tells the session the size of the terminal. An error is not
// looked at: a session that cannot take a size has ended or is about to,
// and the reading of its output says so.
func (a *attachment) sendSize() {
	cols, rows := Size(a.in)
	a.toSession.do(func() error { return a.s.Resize(cols, rows) })
}

// input copies in to the session. It ends Attach when Detach was typed or
// the terminal cannot be read. The end of an input that is no terminal
// does not end Attach: the shell is told, and ends when it will.
func (a *attachment) input() (result, bool) {
	defer close(a.inputEnded)
	send := func(p []byte) bool {
		return a.toSession.do(func() error {
			_, err := a.s.Write(p)
			return err
		})
	}
	buf := make([]byte, 4096)
	last := byte('\n')
	for {
		n, err := a.in.Read(buf)
		data := buf[:n]
		if a.tty {
			if i := bytes.IndexByte(data, Detach); i >= 0 {
				if i > 0 {
					send(data[:i])
				}
				return result{detached: true}, true
			}
		}
		if n > 0 {
			last = data[n-1]
			if !send(data) {
				// The session takes no more, or Attach has returned.
				return result{}, false
			}
		}
		if err == nil {
			continue
		}
		if a.tty {
			return result{err: fmt.Errorf("term: reading the terminal: %w", err)}, true
		}
		end := []byte{endOfInput}
		if last != '\n' {
			end = []byte{'\n', endOfInput}
		}
		send(end)
		return result{}, false
	}
}

// output copies the session to out until the shell has ended, and returns
// its exit status.
func (a *attachment) output(ctx context.Context) result {
	buf := make([]byte, 32<<10)
	for {
		n, err := a.s.Read(buf)
		if n > 0 {
			var werr error
			open := a.toOut.do(func() error {
				_, werr = a.out.Write(buf[:n])
				return werr
			})
			if werr != nil {
				return result{err: fmt.Errorf("term: writing what the shell wrote: %w", werr)}
			}
			if !open {
				return result{}
			}
		}
		if errors.Is(err, io.EOF) {
			exit, err := a.s.Wait(ctx)
			if err != nil {
				return result{err: fmt.Errorf("term: the exit status of the shell: %w", err)}
			}
			return result{exit: exit}
		}
		if err != nil {
			return result{err: fmt.Errorf("term: reading from the shell: %w", err)}
		}
	}
}

// gate lets calls through one at a time until it is shut.
type gate struct {
	mu     sync.Mutex
	closed atomic.Bool
}

// do calls f unless the gate is shut, and reports whether it called f and
// f returned nil.
func (g *gate) do(f func() error) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed.Load() {
		return false
	}
	return f() == nil
}

// shut shuts the gate: no call begins from here on. With wait it returns
// when the call that is under way, if one is, has ended.
func (g *gate) shut(wait bool) {
	g.closed.Store(true)
	if wait {
		g.mu.Lock()
		// Taken only to wait for the call that is under way.
		g.mu.Unlock()
	}
}
