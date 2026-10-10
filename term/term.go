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
// to do, and it ends what Attach has left: the reading of the session,
// and a Write to it that had not returned when ctx was done.
//
// When Attach returns, for whatever reason, nothing of it reads in any
// more: what is typed next is read by whoever reads in next. A caller
// that lives on, as the terminal UI does, may read the terminal again at
// once. What was typed and not read when Attach returned is still in the
// terminal; what Attach had read and the session had not taken by then is
// lost.
//
// Attach is to be the only reader of in while it runs. On a terminal
// another reader does no more harm than to take keys away: each key goes
// to one of the two, and Attach still returns when it is to. While it is
// attached, a read of the terminal returns at once, with nothing, when
// nothing was typed (VMIN and VTIME are 0), which another reader has to
// bear. In a file that is no terminal, a pipe above all, another reader
// can take what Attach was about to read: Attach then waits in a read that
// nothing ends but the next input or the end of in, returns only then,
// and has read that input.
//
// Nothing is written to the session or to out once Attach has returned,
// with one exception, when ctx ended it: a Write that was under way then
// is not waited for, neither the one to the session nor the one to out,
// since a session that takes no input, or a terminal that is held
// (Ctrl-S), would keep Attach from returning. So one piece of what the
// shell wrote, 32 KiB at most, may still be arriving on out when Attach
// has returned for its context. Nothing follows it.
//
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

	// inputEnded is closed when the goroutine that reads in has ended.
	// That is before Attach returns, but when ctx ended Attach while a
	// Write to the session had not returned: then it is when that Write
	// returns. It reads in no more either way.
	inputEnded chan struct{}

	// Two hooks for tests. found is called when the reader has found
	// input and before it reads it; stopped when the reading has stopped
	// and before the terminal is restored.
	found, stopped func()
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

	in, err := newReader(a.in)
	if err != nil {
		return 0, false, err
	}
	in.found = a.found
	// For the ways out before the reading has begun. On the others it is
	// stopped earlier, below.
	defer in.stop()

	// Shutting the gates is deferred first and so happens last, when the
	// reading of in has stopped and the terminal is restored. A Write that
	// is under way is waited for, and with it the goroutine that reads in,
	// but not when ctx is done or a panic is passed on: a session that
	// takes no more input would keep Attach from returning. That Write may
	// then end after Attach.
	wait := true
	started := false
	defer func() {
		a.toSession.shut(wait)
		a.toOut.shut(wait)
		if wait && started {
			<-a.inputEnded
		}
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
		// A read of the terminal is not to wait: see reader.
		if err := in.noWait(); err != nil {
			return 0, false, fmt.Errorf("term: making the terminal raw: %w", err)
		}

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

	// Deferred last and so done first, while the terminal is still raw:
	// from here on nothing of Attach reads in. What is typed from now on,
	// and what was typed and not read, is in the terminal for whoever
	// reads it next. The order matters: the mode that is restored may be
	// one in which a read waits, and a read that waits holds stop up.
	defer func() {
		in.stop()
		if a.stopped != nil {
			a.stopped()
		}
	}()

	// The goroutine that reads the session is not waited for when Attach
	// returns: it ends when the caller closes the session, and what it
	// reads after Attach has returned is dropped.
	started = true
	a.spawn(results, func() (result, bool) { return a.input(in) })
	a.spawn(results, func() (result, bool) { return a.output(ctx), true })

	select {
	case <-ctx.Done():
		wait = false
		return 0, false, ctx.Err()
	case r := <-results:
		if r.panicked != "" {
			// A panic in a goroutine would end the program with the
			// terminal left raw. It goes on from here instead, behind the
			// deferred Restore. Nothing is waited for on the way: a Write
			// to the session that does not return would swallow it.
			wait = false
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
// the terminal cannot be read, and ends by itself when the reading is
// stopped. The end of an input that is no terminal does not end Attach:
// the shell is told, and ends when it will.
func (a *attachment) input(in *reader) (result, bool) {
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
		n, err := in.Read(buf)
		if errors.Is(err, errStopped) {
			return result{}, false
		}
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
