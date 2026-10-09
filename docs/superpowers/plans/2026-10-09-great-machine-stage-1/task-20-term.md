### Task 20: term

A remote shell session on the local terminal: `term.Attach`, which
`great-machine shell` (Task 21) is and which the terminal UI of stage 3
gives the terminal to, and `term.Size`. Spec sections 7.6 and 10.

The package is for Linux and macOS, as the program is. The code that
listens for SIGWINCH is in a file with a `//go:build unix` tag, and there
is no file for any other system.

**Files:**
- Create: `term/term.go`
- Create: `term/winch_unix.go` (build tag `unix`)
- Test: `term/term_test.go`
- Modify: `go.mod`, `go.sum` (`golang.org/x/term` and `github.com/creack/pty` as direct requirements)

**Interfaces:**
- Consumes: nothing of the other tasks. `golang.org/x/term` (`IsTerminal`,
  `MakeRaw`, `Restore`, `GetSize`, `GetState`) and, in the test alone,
  `github.com/creack/pty` (`Open`, `Start`, `Setsize`).
- Produces, exactly as the contract has it:

  ```go
  package term

  type Session interface {
  	io.Reader
  	io.Writer
  	Resize(cols, rows uint16) error
  	Wait(ctx context.Context) (int, error)
  }

  const Detach = 0x1d

  func Attach(ctx context.Context, in *os.File, out io.Writer, s Session) (exit int, detached bool, err error)
  func Size(f *os.File) (cols, rows uint16)
  ```

  What the contract leaves open, and this task decides:
  - **No size is sent when `in` is no terminal.** The caller asked for the
    session with `term.Size(in)`, which is 80 and 24 then, and the session
    keeps that.
  - When `in` is no terminal, **input whose last byte is no line feed gets
    a line feed before the 0x04**; an empty input is the 0x04 alone. A
    shell takes 0x04 for the end of its input at the start of a line only.
  - `Attach` does not close the session. After it has returned, **nothing
    is written to the session or to `out`**, though two goroutines may
    still be reading: the one on `in` until the next key or the end of the
    file (it cannot be interrupted), the one on the session until the
    caller closes the session. `Attach` calls `Write` and `Resize` of the
    session one at a time, never both at once.
  - A panic in a goroutine of `Attach` is passed on as a panic of `Attach`
    itself, after the terminal was restored.
  - A context that is done returns its error at once, without waiting for
    a `Write` of the session that is under way.

**Review Focus 5 is pinned here**, by `TestAttachInputIsNoTerminal`,
`TestAttachEndOfInput` and `TestAttachPipeToRealShell`.

- [ ] **Step 1: Add the dependencies**

Run, in the bare shell (`nix develop .#bare`):

```bash
go get golang.org/x/term@v0.45.0 github.com/creack/pty@v1.1.24
```

Expected: both are in the `require` block of `go.mod`. (`go mod tidy`
would take them out again until the code of the next steps is there; it is
run in Step 5.)

- [ ] **Step 2: Write the failing test**

`term/term_test.go`:

```go
package term

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	xterm "golang.org/x/term"
)

// waitFor polls cond until it holds, and fails the test when it does not
// within five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// fakeSession is a Session of the test: it keeps what it is sent, and
// writes and ends when the test says so.
type fakeSession struct {
	out  chan []byte // what Read returns next; closed: the shell has ended
	exit int

	mu      sync.Mutex
	input   []byte
	resizes [][2]uint16
	panics  bool // Read panics
}

func newFakeSession(exit int) *fakeSession {
	return &fakeSession{out: make(chan []byte, 16), exit: exit}
}

func (f *fakeSession) Read(p []byte) (int, error) {
	f.mu.Lock()
	panics := f.panics
	f.mu.Unlock()
	if panics {
		panic("the session went wrong")
	}
	b, ok := <-f.out
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (f *fakeSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, p...)
	return len(p), nil
}

func (f *fakeSession) Resize(cols, rows uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]uint16{cols, rows})
	return nil
}

func (f *fakeSession) Wait(ctx context.Context) (int, error) { return f.exit, nil }

func (f *fakeSession) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

func (f *fakeSession) sizes() [][2]uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]uint16(nil), f.resizes...)
}

// attached is what an Attach that runs in a goroutine returned.
type attached struct {
	exit     int
	detached bool
	err      error
}

// start runs an attachment in a goroutine.
func start(ctx context.Context, a *attachment) <-chan attached {
	done := make(chan attached, 1)
	go func() {
		exit, detached, err := a.run(ctx)
		done <- attached{exit, detached, err}
	}()
	return done
}

// await waits for what start began.
func await(t *testing.T, done <-chan attached) attached {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not return")
		return attached{}
	}
}

// pipe returns a pipe that is closed with the test.
func pipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

// Review Focus 5: `great-machine shell` with a standard input that is no
// terminal. The input is passed on unchanged, at its end the shell is told
// so, and its exit status is what Attach returns.
func TestAttachInputIsNoTerminal(t *testing.T) {
	r, w := pipe(t)
	s := newFakeSession(3)
	var out bytes.Buffer
	a := newAttachment(r, &out, s)
	done := start(context.Background(), a)

	// The byte of Detach is data here: nobody types into a pipe.
	if _, err := w.Write([]byte("up\x1dtime\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the input", func() bool { return s.got() == "up\x1dtime\n" })
	s.out <- []byte("12:00 up 3 days\r\n")

	w.Close()
	waitFor(t, "the end of the input", func() bool { return s.got() == "up\x1dtime\n\x04" })
	select {
	case r := <-done:
		t.Fatalf("Attach returned %+v at the end of the input, before the shell had ended", r)
	default:
	}

	s.out <- []byte("bye\r\n")
	close(s.out)
	got := await(t, done)
	if got.err != nil || got.detached || got.exit != 3 {
		t.Errorf("Attach = %d, %v, %v; want 3, false, nil", got.exit, got.detached, got.err)
	}
	if out.String() != "12:00 up 3 days\r\nbye\r\n" {
		t.Errorf("out = %q", out.String())
	}
	// No size is sent for an input that is no terminal: the session keeps
	// the size it was asked for with.
	if sizes := s.sizes(); len(sizes) != 0 {
		t.Errorf("Resize was called: %v", sizes)
	}
	if s.got() != "up\x1dtime\n\x04" {
		t.Errorf("the session was sent %q", s.got())
	}
}

// Review Focus 5: input whose last line has no line feed gets one, so
// that the 0x04 is at the start of a line, where a shell takes it for the
// end; and an input that is empty is the 0x04 alone.
func TestAttachEndOfInput(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"line feed at the end", "uptime\n", "uptime\n\x04"},
		{"no line feed at the end", "uptime", "uptime\n\x04"},
		{"empty", "", "\x04"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, w := pipe(t)
			s := newFakeSession(0)
			a := newAttachment(r, io.Discard, s)
			done := start(context.Background(), a)
			if _, err := w.Write([]byte(c.input)); err != nil {
				t.Fatal(err)
			}
			w.Close()
			waitFor(t, "the end of the input", func() bool { return s.got() == c.want })
			close(s.out)
			if got := await(t, done); got.err != nil || got.exit != 0 {
				t.Errorf("Attach = %d, %v", got.exit, got.err)
			}
		})
	}
}

// shellSession is a real shell on a pseudo-terminal of this machine, as a
// zathras runs one.
type shellSession struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (s *shellSession) Read(p []byte) (int, error) {
	n, err := s.ptmx.Read(p)
	if err != nil {
		// Linux says EIO and macOS EOF when the shell has ended.
		return n, io.EOF
	}
	return n, nil
}
func (s *shellSession) Write(p []byte) (int, error) { return s.ptmx.Write(p) }
func (s *shellSession) Resize(cols, rows uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}
func (s *shellSession) Wait(ctx context.Context) (int, error) {
	s.cmd.Wait()
	return s.cmd.ProcessState.ExitCode(), nil
}

// Review Focus 5, with a shell that is one: `echo 'exit 7' | great-machine
// shell web` ends with status 7.
func TestAttachPipeToRealShell(t *testing.T) {
	for _, input := range []string{"echo reached; exit 7\n", "echo reached; exit 7"} {
		t.Run(strings.ReplaceAll(input, "\n", "<LF>"), func(t *testing.T) {
			cmd := exec.Command("/bin/sh")
			cmd.Env = []string{"PATH=/usr/bin:/bin", "PS1=$ ", "TERM=dumb"}
			ptmx, err := pty.Start(cmd)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill(); ptmx.Close() })

			r, w := pipe(t)
			var out bytes.Buffer
			done := start(context.Background(), newAttachment(r, &out, &shellSession{ptmx, cmd}))
			if _, err := w.Write([]byte(input)); err != nil {
				t.Fatal(err)
			}
			w.Close()
			got := await(t, done)
			if got.err != nil || got.detached || got.exit != 7 {
				t.Errorf("Attach = %d, %v, %v; want 7, false, nil\noutput: %q", got.exit, got.detached, got.err, out.String())
			}
			if !strings.Contains(out.String(), "reached\r\n") {
				t.Errorf("the output of the command is missing: %q", out.String())
			}
		})
	}
}

func TestAttachContextDone(t *testing.T) {
	r, w := pipe(t)
	s := newFakeSession(0)
	var out bytes.Buffer
	a := newAttachment(r, &out, s)
	ctx, cancel := context.WithCancel(context.Background())
	done := start(ctx, a)
	if _, err := w.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the input", func() bool { return s.got() == "before" })

	cancel()
	got := await(t, done)
	if !errors.Is(got.err, context.Canceled) || got.detached {
		t.Errorf("Attach = %d, %v, %v; want the error of the context", got.exit, got.detached, got.err)
	}

	// The goroutine that reads the input is still there. What it reads
	// now reaches nobody, and neither does what the session writes.
	if _, err := w.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	select {
	case <-a.inputEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the reading of the input did not end with the input")
	}
	if s.got() != "before" {
		t.Errorf("the session was sent %q after Attach had returned", s.got())
	}
	s.out <- []byte("late")
	close(s.out)
	waitFor(t, "the session to be read to its end", func() bool { return len(s.out) == 0 })
	if out.Len() != 0 {
		t.Errorf("%q was written after Attach had returned", out.String())
	}
}

// terminal returns a pseudo-terminal: what the test types into master is
// read from tty, which is the terminal Attach is given.
func terminal(t *testing.T) (master, tty *os.File) {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tty.Close(); master.Close() })
	return master, tty
}

// mode returns the mode of a terminal, for comparing.
func mode(t *testing.T, tty *os.File) xterm.State {
	t.Helper()
	state, err := xterm.GetState(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return *state
}

func TestAttachTerminal(t *testing.T) {
	master, tty := terminal(t)
	if err := pty.Setsize(master, &pty.Winsize{Cols: 100, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	cooked := mode(t, tty)
	s := newFakeSession(0)
	var out bytes.Buffer
	a := newAttachment(tty, &out, s)
	done := start(context.Background(), a)

	// The size goes out at once, and by then the terminal is raw.
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	if got := s.sizes()[0]; got != [2]uint16{100, 40} {
		t.Errorf("the first size is %v, want [100 40]", got)
	}
	if reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal is not raw while attached")
	}

	// A change of size: the system tells the process with SIGWINCH.
	if err := pty.Setsize(master, &pty.Winsize{Cols: 120, Rows: 50}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new size", func() bool {
		sizes := s.sizes()
		return sizes[len(sizes)-1] == [2]uint16{120, 50}
	})

	// What is typed reaches the session byte for byte: a raw terminal
	// makes nothing of Ctrl-C or a carriage return.
	if _, err := master.Write([]byte("ls\r\x03")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "what was typed", func() bool { return s.got() == "ls\r\x03" })
	s.out <- []byte("file\r\n")

	// Ctrl-] ends Attach and is not sent, nor is what follows it.
	if _, err := master.Write([]byte("x\x1dy")); err != nil {
		t.Fatal(err)
	}
	got := await(t, done)
	if got.err != nil || !got.detached {
		t.Errorf("Attach = %d, %v, %v; want detached", got.exit, got.detached, got.err)
	}
	if s.got() != "ls\r\x03x" {
		t.Errorf("the session was sent %q, want %q", s.got(), "ls\r\x03x")
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal was not restored")
	}
	if out.String() != "file\r\n" {
		t.Errorf("out = %q", out.String())
	}

	// A change of size is nothing to an Attach that has returned.
	before := len(s.sizes())
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if _, err := master.Write([]byte("z")); err != nil {
		t.Fatal(err)
	}
	// The key above wakes the goroutine that was left reading, which ends.
	select {
	case <-a.inputEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the reading of the terminal did not end with the next key")
	}
	if s.got() != "ls\r\x03x" || len(s.sizes()) != before {
		t.Errorf("the session was sent %q and %d sizes after Attach had returned", s.got(), len(s.sizes())-before)
	}
}

func TestAttachTerminalShellEnds(t *testing.T) {
	_, tty := terminal(t)
	cooked := mode(t, tty)
	s := newFakeSession(42)
	var out bytes.Buffer
	done := start(context.Background(), newAttachment(tty, &out, s))
	s.out <- []byte("logout\r\n")
	close(s.out)
	got := await(t, done)
	if got.err != nil || got.detached || got.exit != 42 {
		t.Errorf("Attach = %d, %v, %v; want 42, false, nil", got.exit, got.detached, got.err)
	}
	if out.String() != "logout\r\n" {
		t.Errorf("out = %q", out.String())
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal was not restored")
	}
}

func TestAttachTerminalContextDone(t *testing.T) {
	_, tty := terminal(t)
	cooked := mode(t, tty)
	s := newFakeSession(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := start(ctx, newAttachment(tty, io.Discard, s))
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	cancel()
	if got := await(t, done); !errors.Is(got.err, context.Canceled) {
		t.Errorf("Attach = %d, %v, %v; want the error of the context", got.exit, got.detached, got.err)
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal was not restored")
	}
}

// A panic in a goroutine of Attach would end the program with the terminal
// left raw. It is passed on to the caller of Attach, behind the restoring.
func TestAttachPanicRestoresTheTerminal(t *testing.T) {
	_, tty := terminal(t)
	cooked := mode(t, tty)
	s := newFakeSession(0)
	s.panics = true

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		Attach(context.Background(), tty, io.Discard, s)
	}()
	text, _ := panicked.(string)
	if !strings.Contains(text, "the session went wrong") {
		t.Errorf("Attach did not pass the panic on: %v", panicked)
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal was not restored")
	}
}

func TestSize(t *testing.T) {
	master, tty := terminal(t)
	if err := pty.Setsize(master, &pty.Winsize{Cols: 132, Rows: 43}); err != nil {
		t.Fatal(err)
	}
	if cols, rows := Size(tty); cols != 132 || rows != 43 {
		t.Errorf("Size of a terminal = %d, %d; want 132, 43", cols, rows)
	}
	r, _ := pipe(t)
	if cols, rows := Size(r); cols != 80 || rows != 24 {
		t.Errorf("Size of a pipe = %d, %d; want 80, 24", cols, rows)
	}
}
```

- [ ] **Step 3: Run the test to see it fail**

Run: `go test ./term/`
Expected: FAIL, the test does not build:

```
term/term_test.go:100:36: undefined: attachment
term/term_test.go:139:7: undefined: newAttachment
term/term_test.go:445:3: undefined: Attach
term/term_test.go:461:19: undefined: Size
```

(among others of the same kind).

- [ ] **Step 4: Write the implementation**

`term/term.go`:

```go
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
```

`term/winch_unix.go`:

```go
//go:build unix

package term

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize returns a channel that gets a value whenever the size of
// the terminal of this process has changed (SIGWINCH), and a function that
// ends that and closes the channel.
func notifyResize() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run:

```bash
go mod tidy
go vet ./term/
go test ./term/ -race -count=5 -v
```

Expected: PASS, five times over, for `TestAttachInputIsNoTerminal`,
`TestAttachEndOfInput` (three cases), `TestAttachPipeToRealShell` (two
cases), `TestAttachContextDone`, `TestAttachTerminal`,
`TestAttachTerminalShellEnds`, `TestAttachTerminalContextDone`,
`TestAttachPanicRestoresTheTerminal` and `TestSize`. After `go mod tidy`,
`golang.org/x/term` and `github.com/creack/pty` are in `go.mod` without
`// indirect`.

The tests need no terminal of their own: they open pseudo-terminals, and
`TestAttachPipeToRealShell` runs `/bin/sh` on one.

- [ ] **Step 6: Commit**

```bash
git add term/ go.mod go.sum
git commit -m "Term: a remote shell session on the local terminal"
```
