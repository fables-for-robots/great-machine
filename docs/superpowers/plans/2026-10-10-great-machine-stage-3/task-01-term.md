### Task 1: `term`: when `Attach` returns, nothing of it reads the terminal any more

As stage 1 leaves it, `term.Attach` on a terminal that returns for any reason but a detach leaves a goroutine in a read of the terminal that cannot be interrupted: the next key or line typed is read by it and dropped. `great-machine shell` ends its process after `Attach`, so nothing was lost there. The terminal UI lives on and reads the terminal again at once. This task makes the read endable and makes `Attach` return only when nothing of it reads `in` any more.

The read is made endable in `term` itself, with `select(2)` on the file and on a pipe of `Attach`'s own (`golang.org/x/sys/unix`, which the module requires already; no new dependency). Why this and not the alternatives:

- `github.com/muesli/cancelreader` (which Bubble Tea uses for its own reader) has no way to wait until its read has ended: its `Cancel` returns at once, and a read that was about to begin may still take a key. On Linux it uses `epoll`, which refuses a regular file, so that by its source `NewReader` returns an error for the input of `great-machine shell web < commands`; on macOS it uses `kqueue`, which does not work for `/dev/tty`, where it falls back to `select`.
- A terminal opened or switched to non-blocking is switched for every process that shares the open file, the shell that started `great-machine` among them. `os.File.SetReadDeadline` works only for files the runtime's poller has, which `os.Stdin` is not when it is an ordinary, blocking terminal.
- `select` works for a terminal, a pipe, a file and `/dev/null`, on Linux and on macOS, and leaves the file as it is. Its cost: it knows file descriptors below 1024 alone (`FD_SETSIZE`), so `Attach` returns an error in a process that has more than about a thousand files open. An operator's command has a handful.

`Attach` waits for the read that is under way before it returns, so that read must never wait. After `select` it does not, unless something else reads the same terminal and takes what `select` found: Bubble Tea's own reader, which Bubble Tea gives up waiting for after half a second and leaves running, or another program of the foreground. So, while it is attached, `Attach` sets the terminal's mode so that a read returns at once with nothing when there is nothing (`VMIN` and `VTIME` 0); it owns the mode then, and restores the one it found. What that mode does at the end of a terminal, found out in a pseudo-terminal on macOS (Darwin 25.6) and on Linux (in the container) alike: when the other end is closed, `select` says for ever that the terminal can be read and `read` returns 0 and no error (on Linux too: the `EIO` of a read that waits does not come), which is what a read returns when another reader was faster. The two are told apart with `poll`, asked without waiting: it reports `POLLHUP` for a terminal whose other end is closed (with `POLLERR` on Linux), and nothing for one that is merely empty. (On macOS nothing else tells: the requests for the mode and the size of such a terminal still succeed. On Linux they fail with `EIO`.) Should `poll` not know some device, the terminal is taken for alive, and the reader pauses 10 ms between reads that got nothing after the third in a row, so that it cannot spin.

For a file that is no terminal there is no mode to set. There `Attach` has to be the only reader, and its comment says what happens otherwise.

**Files:**
- Create: `term/read_unix.go` (the reader that can be stopped)
- Create: `term/termios_bsd.go`, `term/termios_other.go` (the two requests that read and set a terminal's mode, which differ between the systems; split as `golang.org/x/term` splits them)
- Modify: `term/term.go` (`Attach`'s comment; `attachment.run` makes reads of the terminal not wait, stops the reading before it restores the terminal and returns, and does not wait when it passes a panic on; `attachment.input` reads through the reader; two hooks for tests)
- Test: `term/term_test.go` (one test is rewritten, the one that pinned the reader that was left is gone, six are new)
- Test: `term/reader_test.go` (new: another reader of the terminal, the mode while attached and after, the order on the way out, the end of a terminal, a read into no room)

**Interfaces:**
- Consumes: `golang.org/x/sys/unix` (`Pipe`, `Select`, `FdSet`, `Poll`, `Read`, `Write`, `Close`, `CloseOnExec`, `IoctlGetTermios`, `IoctlSetTermios`, `FD_SETSIZE`), `syscall.ForkLock`, `golang.org/x/term`, and in the tests `github.com/creack/pty`; all are in `go.mod`.
- Produces: `term.Attach(ctx context.Context, in *os.File, out io.Writer, s term.Session) (exit int, detached bool, err error)`, with its signature and everything else unchanged, and this promise, which Task 2 does not need and the tasks of the terminal UI build on:
  - When `Attach` returns, for whatever reason (the shell ended, `ctx` is done, the session failed, a detach, the terminal failed, a panic of one of its goroutines passed on), nothing of it reads `in` any more, and it has returned only after that is so. This holds for a terminal and for a file that is none.
  - What is typed after `Attach` returned, and what was typed before and `Attach` had not read, is read by whoever reads `in` next. What `Attach` had read and the session had not taken is lost.
  - The terminal is in the mode it was in before `Attach`, `VMIN` and `VTIME` included, restored after the reading has stopped.
  - **`Attach` is to be the only reader of `in` while it runs.** On a terminal another reader does no more harm than to take keys away: each key goes to one of the two, and `Attach` still returns when it is to. While `Attach` is attached, a plain read of the terminal by anything else returns at once with nothing when nothing was typed (to an `os.File` that is `io.EOF`). In a file that is no terminal (a pipe, a regular file), another reader can take what `Attach` was about to read: `Attach` then waits in a read that nothing ends but the next input or the end of `in`, returns only then, and has read that input.
  - Nothing is written to the session or to `out` once `Attach` has returned, with one exception, when `ctx` ended it: a `Write` that was under way then is not waited for (a session that takes no input, or a terminal that is held with Ctrl-S, would keep `Attach` from returning). So one piece of the shell's output, 32 KiB at most, may still be arriving on `out` when `Attach` has returned for its context, and nothing follows it; and a `Write` to the session that had not returned, with the goroutine that is in it, is left until the caller closes the session. The goroutine that reads the session is left until then on every way out, as before. Neither reads `in`.
  - A panic of one of its goroutines is passed on to the caller of `Attach`, behind the restoring of the terminal, also while a `Write` to the session waits.
  - A terminal whose other end is closed ends `Attach` with an error that says "the terminal has gone".
  - `Attach` returns an error at once when `in` or the pipe it opens has a file descriptor of 1024 or more.

- [ ] **Step 1: Write the failing tests of what `Attach` leaves**

Replace `term/term_test.go` with the following. Against the file as it is: `fakeSession` can fail, panic later and take no input, and is ended with `hangUp`; `attached` and `start` carry a panic; `TestAttachContextDone` now reads the pipe after `Attach`; `TestAttachTerminalLeftoverReader`, which pinned the defect, is gone; `baseline`, `settled`, `readNext`, `waysOut`, `TestAttachInputIsAFile`, `TestAttachLeavesTheTerminalToTheNextReader` (Review Focus 1), `TestAttachLeavesWhatItHasNotRead` (Review Focus 1), `TestAttachPanicWhileAWriteWaits`, `TestAttachTerminalFails` and `TestAttachSizeChangesWhileTyping` (Review Focus 5) are new. Every other test is as it was.

```go
package term

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
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

	// stuck, when it is set, gets a value from every Write, which then
	// waits until release is closed: a session that takes no input.
	stuck   chan struct{}
	release chan struct{}

	mu      sync.Mutex
	input   []byte
	resizes [][2]uint16
	panics  bool  // Read panics
	failure error // what Read returns at the end, in place of io.EOF
	hungUp  bool  // out is closed
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
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failure != nil {
			return 0, f.failure
		}
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (f *fakeSession) Write(p []byte) (int, error) {
	if f.stuck != nil {
		f.stuck <- struct{}{}
		<-f.release
	}
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

// hangUp ends the session, as the Close of a real one does: Read returns
// the end. It may be called more than once.
func (f *fakeSession) hangUp() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hungUp {
		f.hungUp = true
		close(f.out)
	}
}

// fail ends the session with an error.
func (f *fakeSession) fail(err error) {
	f.mu.Lock()
	f.failure = err
	f.mu.Unlock()
	f.hangUp()
}

// panicNext makes the Read after the one that waits panic.
func (f *fakeSession) panicNext() {
	f.mu.Lock()
	f.panics = true
	f.mu.Unlock()
	f.out <- []byte("the last words")
}

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

// attached is what an Attach that runs in a goroutine returned, or what
// it panicked with.
type attached struct {
	exit     int
	detached bool
	err      error
	panicked string
}

// start runs an attachment in a goroutine.
func start(ctx context.Context, a *attachment) <-chan attached {
	done := make(chan attached, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- attached{panicked: fmt.Sprint(v)}
			}
		}()
		exit, detached, err := a.run(ctx)
		done <- attached{exit: exit, detached: detached, err: err}
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

// `great-machine shell web < commands` and `< /dev/null`: a file and a
// device that are neither a terminal nor a pipe are waited for and read as
// a pipe is.
func TestAttachInputIsAFile(t *testing.T) {
	commands := t.TempDir() + "/commands"
	if err := os.WriteFile(commands, []byte("uptime\nexit"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, path, want string }{
		{"a file", commands, "uptime\nexit\n\x04"},
		{"/dev/null", os.DevNull, "\x04"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := baseline()
			in, err := os.Open(c.path)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			s := newFakeSession(0)
			done := start(context.Background(), newAttachment(in, io.Discard, s))
			waitFor(t, "the end of the input", func() bool { return s.got() == c.want })
			s.hangUp()
			if got := await(t, done); got.err != nil || got.exit != 0 {
				t.Errorf("Attach = %+v", got)
			}
			settled(t, before)
		})
	}
}

// A pipe is left to the next reader as a terminal is: what is written to
// it after Attach has returned is not read by Attach.
func TestAttachContextDone(t *testing.T) {
	before := baseline()
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
	select {
	case <-a.inputEnded:
	default:
		t.Error("the reading of the input is still going after Attach has returned")
	}

	if _, err := w.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if next := readNext(t, r, len("after")); next != "after" {
		t.Errorf("the next reader of the pipe read %q, want %q", next, "after")
	}
	if s.got() != "before" {
		t.Errorf("the session was sent %q after Attach had returned", s.got())
	}
	// Nor does what the session writes now reach anybody.
	s.out <- []byte("late")
	s.hangUp()
	settled(t, before)
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

	// After a detach nothing is left reading: the goroutine returned by
	// itself, before Attach did.
	select {
	case <-a.inputEnded:
	default:
		t.Error("the reading of the terminal is still going after a detach")
	}

	// A change of size is nothing to an Attach that has returned.
	noSizeAfterReturn(t, master, s)
}

// noSizeAfterReturn changes the size of the terminal, raises SIGWINCH, and
// fails when the session is sent a size within a third of a second. It
// waits that long because it shows that something does NOT happen: a handler
// that was still armed would have acted by then.
func noSizeAfterReturn(t *testing.T, master *os.File, s *fakeSession) {
	t.Helper()
	before := len(s.sizes())
	if err := pty.Setsize(master, &pty.Winsize{Cols: 140, Rows: 60}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n := len(s.sizes()); n != before {
			t.Fatalf("%d sizes were sent after Attach had returned: %v", n-before, s.sizes()[before:])
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// baseline returns how many goroutines there are before a test begins
// anything. The first signal.Notify of a process starts a goroutine that
// stays for good: it is started here, so that it is not taken for one of
// Attach.
func baseline() int {
	_, stop := notifyResize()
	stop()
	return runtime.NumGoroutine()
}

// settled waits until there are no more goroutines than before: nothing
// of what the test began since is left.
func settled(t *testing.T, before int) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(end) {
			buf := make([]byte, 1<<20)
			t.Fatalf("%d goroutines, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(time.Millisecond)
	}
}

// readNext reads n bytes of f with plain Reads, as whoever reads the
// terminal after Attach does. It fails the test when they do not come:
// somebody else has read them.
func readNext(t *testing.T, f *os.File, n int) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, n)
		m, _ := io.ReadFull(f, buf)
		got <- string(buf[:m])
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out reading %d bytes that were typed after Attach had returned: something else read them", n)
		return ""
	}
}

// waysOut are the ways Attach returns on a terminal that still works.
var waysOut = []struct {
	name  string
	end   func(t *testing.T, master *os.File, s *fakeSession, cancel func())
	check func(t *testing.T, got attached)
}{
	{"the shell ends",
		func(t *testing.T, master *os.File, s *fakeSession, cancel func()) { s.hangUp() },
		func(t *testing.T, got attached) {
			if got.err != nil || got.detached || got.exit != 42 || got.panicked != "" {
				t.Errorf("Attach = %+v; want 42, false, nil", got)
			}
		}},
	{"the context is done",
		func(t *testing.T, master *os.File, s *fakeSession, cancel func()) { cancel() },
		func(t *testing.T, got attached) {
			if !errors.Is(got.err, context.Canceled) || got.detached {
				t.Errorf("Attach = %+v; want the error of the context", got)
			}
		}},
	{"the session fails",
		func(t *testing.T, master *os.File, s *fakeSession, cancel func()) {
			s.fail(errors.New("the connection was lost"))
		},
		func(t *testing.T, got attached) {
			if got.err == nil || !strings.Contains(got.err.Error(), "the connection was lost") || got.detached {
				t.Errorf("Attach = %+v; want the error of the session", got)
			}
		}},
	{"a detach",
		func(t *testing.T, master *os.File, s *fakeSession, cancel func()) {
			if _, err := master.Write([]byte{Detach}); err != nil {
				t.Fatal(err)
			}
		},
		func(t *testing.T, got attached) {
			if got.err != nil || !got.detached {
				t.Errorf("Attach = %+v; want detached", got)
			}
		}},
	{"a panic",
		func(t *testing.T, master *os.File, s *fakeSession, cancel func()) { s.panicNext() },
		func(t *testing.T, got attached) {
			if !strings.Contains(got.panicked, "the session went wrong") {
				t.Errorf("Attach = %+v; want the panic of the session passed on", got)
			}
		}},
}

// Review Focus 1, the terminal after a shell: whichever way Attach
// returns, the terminal is in the mode it was in, nothing of Attach reads
// it any more, and what is typed from then on is read, whole and in
// order, by whoever reads the terminal next. The terminal UI is such a
// reader: it reads the terminal again as soon as Attach has returned.
//
// Each way out is tried on a terminal in its usual mode, as `great-machine
// shell` finds it, where the next reader gets lines, and on one that was
// raw before, where it gets every byte as it is typed.
func TestAttachLeavesTheTerminalToTheNextReader(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, c := range waysOut {
			name := c.name + ", the terminal in its usual mode"
			typed := []string{"one\n", "two\n", "three\n"}
			if raw {
				name = c.name + ", the terminal raw before"
				typed = []string{"o", "ne", "\x1b[A", "\x03", "two"}
			}
			t.Run(name, func(t *testing.T) {
				before := baseline()
				master, tty := terminal(t)
				if raw {
					if _, err := xterm.MakeRaw(int(tty.Fd())); err != nil {
						t.Fatal(err)
					}
				}
				was := mode(t, tty)
				s := newFakeSession(42)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := start(ctx, newAttachment(tty, io.Discard, s))

				// The size is sent when the terminal is raw. From then on
				// what is typed reaches the session as it is typed.
				waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
				if _, err := master.Write([]byte("ls\r")); err != nil {
					t.Fatal(err)
				}
				waitFor(t, "what was typed", func() bool { return s.got() == "ls\r" })

				c.end(t, master, s, cancel)
				c.check(t, await(t, done))
				if !reflect.DeepEqual(mode(t, tty), was) {
					t.Error("the terminal is not in the mode it was in")
				}

				// Typed after Attach has returned, in pieces.
				want := ""
				for _, piece := range typed {
					if _, err := master.Write([]byte(piece)); err != nil {
						t.Fatal(err)
					}
					want += piece
				}
				if got := readNext(t, tty, len(want)); got != want {
					t.Errorf("the next reader of the terminal read %q, want %q", got, want)
				}
				if s.got() != "ls\r" {
					t.Errorf("the session was sent %q, want what was typed while attached alone", s.got())
				}

				// The caller closes the session, and then nothing is left.
				s.hangUp()
				settled(t, before)
				if !raw {
					noSizeAfterReturn(t, master, s)
				}
			})
		}
	}
}

// Review Focus 1: what was typed and not read when Attach returned stays
// in the terminal. The session takes no input here, so that Attach has
// read the first key and not the second when its context ends it; the
// Write that waits ends when the caller closes the session, and reads
// nothing after that.
func TestAttachLeavesWhatItHasNotRead(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	if _, err := xterm.MakeRaw(int(tty.Fd())); err != nil {
		t.Fatal(err)
	}
	was := mode(t, tty)
	s := newFakeSession(0)
	s.stuck, s.release = make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	a := newAttachment(tty, io.Discard, s)
	done := start(ctx, a)
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })

	if _, err := master.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("the first key was not written to the session")
	}
	// Attach is in the Write of the first key, and reads nothing.
	if _, err := master.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	cancel()
	if got := await(t, done); !errors.Is(got.err, context.Canceled) {
		t.Errorf("Attach = %+v; want the error of the context", got)
	}
	if !reflect.DeepEqual(mode(t, tty), was) {
		t.Error("the terminal is not in the mode it was in")
	}
	if _, err := master.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	if got := readNext(t, tty, 2); got != "bc" {
		t.Errorf("the next reader of the terminal read %q, want %q", got, "bc")
	}

	// The caller closes the session: the Write returns, and the goroutine
	// that was in it ends without reading the terminal again.
	close(s.release)
	s.hangUp()
	select {
	case <-a.inputEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine that reads the terminal did not end with the Write it was in")
	}
	if _, err := master.Write([]byte("d")); err != nil {
		t.Fatal(err)
	}
	if got := readNext(t, tty, 1); got != "d" {
		t.Errorf("the next reader of the terminal read %q, want %q", got, "d")
	}
	settled(t, before)
}

// A panic of the goroutine that reads the session is passed on also while
// the session takes no input: the Write that waits is not waited for,
// which would swallow the panic and hold Attach for good.
func TestAttachPanicWhileAWriteWaits(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	cooked := mode(t, tty)
	s := newFakeSession(0)
	s.stuck, s.release = make(chan struct{}), make(chan struct{})
	done := start(context.Background(), newAttachment(tty, io.Discard, s))
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	if _, err := master.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("the key was not written to the session")
	}

	s.panicNext()
	if got := await(t, done); !strings.Contains(got.panicked, "the session went wrong") {
		t.Errorf("Attach = %+v; want the panic of the session passed on", got)
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal was not restored")
	}
	close(s.release)
	settled(t, before)
}

// The terminal fails: its other end is closed, as when the window is.
// Attach returns with an error and leaves nothing.
func TestAttachTerminalFails(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	s := newFakeSession(0)
	done := start(context.Background(), newAttachment(tty, io.Discard, s))
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	master.Close()
	got := await(t, done)
	if got.err == nil || !strings.Contains(got.err.Error(), "reading the terminal") || got.detached {
		t.Errorf("Attach = %+v; want an error of reading the terminal", got)
	}
	s.hangUp()
	settled(t, before)
}

// Review Focus 5, a terminal resized while a shell is open: every change
// of size reaches the session, and the signal that tells of it, which
// interrupts a wait for input, loses nothing of what is typed meanwhile.
func TestAttachSizeChangesWhileTyping(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	s := newFakeSession(0)
	done := start(context.Background(), newAttachment(tty, io.Discard, s))
	waitFor(t, "the first size", func() bool { return len(s.sizes()) == 1 })

	want := ""
	for i := range 50 {
		size := [2]uint16{uint16(81 + i), uint16(25 + i)}
		if err := pty.Setsize(master, &pty.Winsize{Cols: size[0], Rows: size[1]}); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("%d,", i)
		if _, err := master.Write([]byte(key)); err != nil {
			t.Fatal(err)
		}
		want += key
		waitFor(t, "the size", func() bool {
			sizes := s.sizes()
			return sizes[len(sizes)-1] == size
		})
	}
	waitFor(t, "what was typed", func() bool { return s.got() == want })

	s.hangUp()
	if got := await(t, done); got.err != nil || got.detached {
		t.Errorf("Attach = %+v", got)
	}
	settled(t, before)
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

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./term/ -run 'TestAttachContextDone|TestAttachLeavesTheTerminalToTheNextReader/the_shell_ends,_the_terminal_in_its_usual_mode'`

Expected: FAIL after about ten seconds, with these lines:

```
--- FAIL: TestAttachContextDone (5.00s)
    term_test.go:366: the reading of the input is still going after Attach has returned
    term_test.go:372: timed out reading 5 bytes that were typed after Attach had returned: something else read them
--- FAIL: TestAttachLeavesTheTerminalToTheNextReader (5.01s)
    --- FAIL: TestAttachLeavesTheTerminalToTheNextReader/the_shell_ends,_the_terminal_in_its_usual_mode (5.01s)
        term_test.go:648: timed out reading 14 bytes that were typed after Attach had returned: something else read them
FAIL
FAIL	github.com/fables-for-robots/great-machine/term	10.289s
FAIL
```

The goroutine that `Attach` left has read what was typed next. (Do not run the whole package at this point: every way out fails in the same way, five seconds each.)

- [ ] **Step 3: Write the failing tests of the reader**

Create `term/reader_test.go`. Its tests look inside: at the mode of the terminal while it is attached, and, through two hooks that `attachment` gets in Step 6 (`found`, called when the reader has found input and before it reads it, and `stopped`, called when the reading has stopped and before the terminal is restored), at what happens when another reader takes a key at the worst moment (Review Focus 1), and at the order on the way out.

`TestAttachWithAnotherReaderOfTheTerminal` is deterministic: the other reader takes the key inside the hook, between `Attach`'s `select` and its read, every time. Without the mode that makes a read not wait it fails every time ("Attach did not return"), and with it never.

```go
package term

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

// vminVtime returns the two settings of a terminal that say how long a
// read of it waits.
func vminVtime(t *testing.T, fd int) [2]uint8 {
	t.Helper()
	m, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		t.Fatal(err)
	}
	return [2]uint8{m.Cc[unix.VMIN], m.Cc[unix.VTIME]}
}

// Review Focus 1, with something else reading the same terminal: Bubble
// Tea's own reader, which it gives up waiting for after half a second, or
// another program. The other reader here is as fast as one can be: it
// takes the key at the moment Attach has been told that there is one, and
// so every time the test runs. Attach then finds nothing to read. It must
// not wait for the next key: it would return only when that is typed, and
// would have read it.
func TestAttachWithAnotherReaderOfTheTerminal(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	// When this test fails, Attach waits in a read of the terminal, and
	// closing the terminal would wait for that read. A key ends it.
	t.Cleanup(func() { master.Write([]byte("\n")) })
	cooked := mode(t, tty)
	fd := int(tty.Fd())
	s := newFakeSession(0)
	a := newAttachment(tty, io.Discard, s)
	took := make(chan string, 1)
	first := true // the hook is called by one goroutine
	a.found = func() {
		if first {
			first = false
			buf := make([]byte, 8)
			n, _ := unix.Read(fd, buf)
			took <- string(buf[:n])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := start(ctx, a)
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })

	if _, err := master.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-took:
		if key != "a" {
			t.Fatalf("the other reader took %q, want the key", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not find the key")
	}

	// Nothing more is typed. Attach returns all the same.
	cancel()
	if got := await(t, done); !errors.Is(got.err, context.Canceled) {
		t.Errorf("Attach = %+v; want the error of the context", got)
	}
	if !reflect.DeepEqual(mode(t, tty), cooked) {
		t.Error("the terminal is not in the mode it was in")
	}
	if _, err := master.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	if got := readNext(t, tty, len("next\n")); got != "next\n" {
		t.Errorf("the next reader of the terminal read %q, want %q", got, "next\n")
	}
	if s.got() != "" {
		t.Errorf("the session was sent %q", s.got())
	}
	s.hangUp()
	settled(t, before)
}

// The same reader goes on attached after a key was taken from it: the
// next key is its own, and it does not take the terminal for gone.
func TestAttachGoesOnAfterAnotherReaderTookAKey(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	fd := int(tty.Fd())
	s := newFakeSession(0)
	a := newAttachment(tty, io.Discard, s)
	took := make(chan string, 1)
	first := true
	a.found = func() {
		if first {
			first = false
			buf := make([]byte, 8)
			n, _ := unix.Read(fd, buf)
			took <- string(buf[:n])
		}
	}
	done := start(context.Background(), a)
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	if _, err := master.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-took:
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not find the key")
	}
	if _, err := master.Write([]byte("bc")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the keys that follow", func() bool { return s.got() == "bc" })
	s.hangUp()
	if got := await(t, done); got.err != nil || got.detached {
		t.Errorf("Attach = %+v", got)
	}
	settled(t, before)
}

// While attached, a read of the terminal does not wait (VMIN and VTIME are
// 0), and afterwards the terminal has the settings it had, whatever they
// were: here two that nothing else would set.
func TestAttachRestoresHowLongAReadWaits(t *testing.T) {
	_, tty := terminal(t)
	fd := int(tty.Fd())
	if _, err := xterm.MakeRaw(fd); err != nil {
		t.Fatal(err)
	}
	m, err := unix.IoctlGetTermios(fd, getTermios)
	if err != nil {
		t.Fatal(err)
	}
	m.Cc[unix.VMIN], m.Cc[unix.VTIME] = 5, 3
	if err := unix.IoctlSetTermios(fd, setTermios, m); err != nil {
		t.Fatal(err)
	}
	was := mode(t, tty)

	s := newFakeSession(0)
	done := start(context.Background(), newAttachment(tty, io.Discard, s))
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	if got := vminVtime(t, fd); got != [2]uint8{0, 0} {
		t.Errorf("while attached VMIN and VTIME are %v, want 0 and 0", got)
	}
	s.hangUp()
	if got := await(t, done); got.err != nil {
		t.Errorf("Attach = %+v", got)
	}
	if got := vminVtime(t, fd); got != [2]uint8{5, 3} {
		t.Errorf("after Attach VMIN and VTIME are %v, want 5 and 3", got)
	}
	if !reflect.DeepEqual(mode(t, tty), was) {
		t.Error("the terminal is not in the mode it was in")
	}
}

// The reading is stopped before the terminal is restored, on every way
// out: when the reading has stopped the terminal is still in the mode of
// Attach, in which a read does not wait. Restored first, a read could
// wait in the terminal's own mode and hold the stop up.
func TestAttachStopsReadingBeforeItRestoresTheTerminal(t *testing.T) {
	for _, c := range waysOut {
		t.Run(c.name, func(t *testing.T) {
			before := baseline()
			master, tty := terminal(t)
			fd := int(tty.Fd())
			cooked := mode(t, tty)
			s := newFakeSession(42)
			a := newAttachment(tty, io.Discard, s)
			type seen struct {
				mode  xterm.State
				waits [2]uint8
			}
			stopped := make(chan seen, 1)
			// Not in the goroutine of the test: nothing here fails it.
			a.stopped = func() {
				var at seen
				if state, err := xterm.GetState(fd); err == nil {
					at.mode = *state
				}
				if m, err := unix.IoctlGetTermios(fd, getTermios); err == nil {
					at.waits = [2]uint8{m.Cc[unix.VMIN], m.Cc[unix.VTIME]}
				}
				stopped <- at
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := start(ctx, a)
			waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })

			c.end(t, master, s, cancel)
			c.check(t, await(t, done))
			select {
			case at := <-stopped:
				if reflect.DeepEqual(at.mode, cooked) || at.waits != [2]uint8{0, 0} {
					t.Errorf("when the reading stopped the terminal was restored already (VMIN and VTIME %v)", at.waits)
				}
			default:
				t.Error("the reading was not stopped")
			}
			if !reflect.DeepEqual(mode(t, tty), cooked) {
				t.Error("the terminal was not restored")
			}
			s.hangUp()
			settled(t, before)
		})
	}
}

// The end of a terminal is told from a terminal on which nothing was
// typed, though a read returns nothing for both: Attach ends with an
// error when the other end is closed, at once and also when nothing was
// ever typed, and does not end before.
func TestAttachTellsATerminalThatIsGone(t *testing.T) {
	before := baseline()
	master, tty := terminal(t)
	s := newFakeSession(0)
	done := start(context.Background(), newAttachment(tty, io.Discard, s))
	waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })
	if _, err := master.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "what was typed", func() bool { return s.got() == "abc" })
	select {
	case got := <-done:
		t.Fatalf("Attach returned %+v while the terminal is there", got)
	default:
	}
	master.Close()
	got := await(t, done)
	if got.err == nil || !strings.Contains(got.err.Error(), "the terminal has gone") {
		t.Errorf("Attach = %+v; want the error of a terminal that has gone", got)
	}
	s.hangUp()
	settled(t, before)
}

// A Read into no room returns at once, as a Read of a file does, and
// reads nothing.
func TestReaderReadIntoNoRoom(t *testing.T) {
	r, w := pipe(t)
	in, err := newReader(r)
	if err != nil {
		t.Fatal(err)
	}
	defer in.stop()
	if n, err := in.Read(nil); n != 0 || err != nil {
		t.Errorf("Read into no room = %d, %v; want 0, nil", n, err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if n, err := in.Read([]byte{}); n != 0 || err != nil {
		t.Errorf("Read into no room = %d, %v; want 0, nil", n, err)
	}
	buf := make([]byte, 4)
	if n, err := in.Read(buf); n != 1 || err != nil || buf[0] != 'x' {
		t.Errorf("Read = %d, %v, %q; want what was written", n, err, buf[:n])
	}
	in.stop()
	if _, err := in.Read(buf); !errors.Is(err, errStopped) {
		t.Errorf("Read after stop = %v, want errStopped", err)
	}
}
```

- [ ] **Step 4: Run them to see them fail**

Run: `nix develop .#bare --command go test ./term/ -run TestReader`

Expected: the tests do not build, because nothing of this exists yet:

```
# github.com/fables-for-robots/great-machine/term [github.com/fables-for-robots/great-machine/term.test]
term/reader_test.go:20:37: undefined: getTermios
term/reader_test.go:46:4: a.found undefined (type *attachment has no field or method found)
term/reader_test.go:102:4: a.found undefined (type *attachment has no field or method found)
term/reader_test.go:140:37: undefined: getTermios
term/reader_test.go:145:37: undefined: setTermios
term/reader_test.go:187:6: a.stopped undefined (type *attachment has no field or method stopped)
term/reader_test.go:192:43: undefined: getTermios
term/reader_test.go:253:13: undefined: newReader
term/reader_test.go:272:45: undefined: errStopped
FAIL	github.com/fables-for-robots/great-machine/term [build failed]
FAIL
```

- [ ] **Step 5: Write the reader that can be stopped**

Create `term/termios_bsd.go`:

```go
//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package term

import "golang.org/x/sys/unix"

// The requests that read and set the mode of a terminal.
const (
	getTermios = unix.TIOCGETA
	setTermios = unix.TIOCSETA
)
```

Create `term/termios_other.go`:

```go
//go:build aix || linux || solaris || zos

package term

import "golang.org/x/sys/unix"

// The requests that read and set the mode of a terminal.
const (
	getTermios = unix.TCGETS
	setTermios = unix.TCSETS
)
```

Create `term/read_unix.go`:

```go
//go:build unix

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// errStopped is what Read of a reader returns once stop was called.
var errStopped = errors.New("term: the reading was stopped")

// errGone is what Read of a reader returns when the terminal it reads has
// gone: its other end is closed, as when the window is.
var errGone = errors.New("the terminal has gone")

// reader reads a file so that the reading can be ended while it waits for
// input, which a plain Read of a terminal cannot be.
//
// It waits with select(2) for the file or for a pipe of its own, and reads
// the file only when select says that there is something to read; stop
// writes to the pipe. select is used because it is the one call that does
// this for a terminal on Linux and on macOS alike: on macOS kqueue does not
// work for /dev/tty, and poll is documented not to work for every device.
// The file itself is left as it is: it is not made non-blocking, which
// would change it for every process that shares it, the shell that started
// this program among them.
//
// stop waits for the read that is under way, so that read must not wait.
// After select it does not, as long as nobody else reads the file; but
// something else may: a reader that the caller has not quite ended, or
// another program on the same terminal. It may take what select found,
// and a read would then wait for the next key, hold stop up until it is
// typed, and take it. For a terminal that is ruled out with its mode:
// noWait makes a read return at once, with nothing, when there is nothing.
// For a file that is no terminal nothing rules it out: there the reader
// has to be the only one.
//
// What select costs: it knows file descriptors below 1024 alone, so
// newReader fails in a process that has more files open than that.
type reader struct {
	fd         int  // the file
	tty        bool // the file is a terminal whose reads do not wait
	wake, woke int  // the pipe: stop writes to woke, Read waits for wake

	// found, when a test has set it, is called when select has found
	// input and before it is read.
	found func()

	stopped atomic.Bool
	mu      sync.Mutex // held while Read waits for the file or reads it
	once    sync.Once
}

// newReader returns a reader of f. It is to be stopped, which frees the
// pipe.
func newReader(f *os.File) (*reader, error) {
	var p [2]int
	// No child may get the pipe. Without pipe2, which macOS lacks, that
	// takes the lock that keeps a fork away until both ends are marked.
	syscall.ForkLock.RLock()
	err := unix.Pipe(p[:])
	if err == nil {
		unix.CloseOnExec(p[0])
		unix.CloseOnExec(p[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("term: a pipe to end the reading with: %w", err)
	}
	r := &reader{fd: int(f.Fd()), wake: p[0], woke: p[1]}
	if r.fd < 0 || r.fd >= unix.FD_SETSIZE || r.wake >= unix.FD_SETSIZE {
		unix.Close(p[0])
		unix.Close(p[1])
		return nil, fmt.Errorf("term: too many files are open to wait for input: select knows file descriptors below %d alone", unix.FD_SETSIZE)
	}
	return r, nil
}

// noWait is to be called when the file is a terminal that was made raw:
// it sets the mode of the terminal so that a read returns at once, with
// nothing, when there is nothing to read (VMIN and VTIME 0). Whoever
// restores the mode the terminal had takes that back.
func (r *reader) noWait() error {
	mode, err := unix.IoctlGetTermios(r.fd, getTermios)
	if err != nil {
		return err
	}
	mode.Cc[unix.VMIN], mode.Cc[unix.VTIME] = 0, 0
	if err := unix.IoctlSetTermios(r.fd, setTermios, mode); err != nil {
		return err
	}
	r.tty = true
	return nil
}

// Read waits for input and reads it, as a Read of the file does: it
// returns at least one byte, or an error, or nothing at once when p is
// empty. Once stop was called it returns errStopped, without having read
// anything. It is for one goroutine.
func (r *reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	empty := 0 // reads in a row that select promised and that got nothing
	for {
		if r.stopped.Load() {
			return 0, errStopped
		}
		var set unix.FdSet
		set.Set(r.fd)
		set.Set(r.wake)
		// A signal ends select, and SIGWINCH comes whenever the window
		// changes: it is asked again.
		if _, err := unix.Select(max(r.fd, r.wake)+1, &set, nil, nil, nil); err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, fmt.Errorf("select: %w", err)
		}
		if !set.IsSet(r.fd) {
			// The pipe alone: stopped is set, and the loop says so.
			continue
		}
		if r.stopped.Load() {
			// Both at once: what is there to read is left for whoever
			// reads the file next.
			return 0, errStopped
		}
		if r.found != nil {
			r.found()
		}
		n, err := unix.Read(r.fd, p)
		switch {
		case err == unix.EINTR || err == unix.EAGAIN:
			continue
		case err != nil:
			return 0, fmt.Errorf("read: %w", err)
		case n > 0:
			return n, nil
		case !r.tty:
			return 0, io.EOF
		}
		// A terminal whose reads do not wait had nothing. Either it has
		// gone: then select says for ever that it can be read, and a read
		// returns nothing, on Linux and on macOS alike. Or another reader
		// took what select had found: then there is nothing to do but to
		// wait again.
		if r.gone() {
			return 0, errGone
		}
		// Not to spin, should a terminal ever be gone without saying so.
		if empty++; empty > 3 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// gone says whether the other end of the terminal is closed. poll tells:
// it reports a hangup for a pseudo-terminal whose other end is closed, on
// Linux and on macOS, where nothing else does (on macOS even the requests
// for the mode and the size still succeed). It is asked without waiting,
// and only once select and a read have disagreed. Should poll not know a
// device, it reports nothing and the terminal is taken for alive: hence
// the pause in Read.
func (r *reader) gone() bool {
	fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, 0)
		if err == unix.EINTR {
			continue
		}
		return err == nil && fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
	}
}

// stop ends the reading: a Read that waits returns errStopped, and so does
// every later one. When stop returns, no Read is in the file and none will
// be. It may be called more than once.
func (r *reader) stop() {
	r.once.Do(func() {
		r.stopped.Store(true)
		for {
			if _, err := unix.Write(r.woke, []byte{0}); err != unix.EINTR {
				break
			}
		}
		// Waits for the Read that is under way: it is in select, which the
		// pipe has ended, or in a read of what select found, which does
		// not wait: see reader.
		r.mu.Lock()
		defer r.mu.Unlock()
		unix.Close(r.wake)
		unix.Close(r.woke)
	})
}
```

- [ ] **Step 6: Read through it, and stop it before `Attach` returns**

Replace `term/term.go` with the following. Against the file as it is: in `Attach`'s comment the paragraph on the goroutine it leaves is replaced by the one of the plan's contract, a paragraph on being the only reader follows it, the paragraph before says what is still left, and the sentence on what is written after `Attach` has returned says exactly what can be; the comment of `inputEnded` and the two hooks of `attachment`; `run` makes the reader, makes reads of the terminal not wait, stops the reading before the terminal is restored (and on the ways out before that), waits for the goroutine that reads `in` when it waits for a `Write`, and waits for nothing when it passes a panic on; `input` takes the reader, reads through it and ends when it is stopped. `Size`, `spawn`, `sendSize`, `output` and `gate` are as they were.

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
```

- [ ] **Step 7: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./term/ ./cli/`

Expected (the times differ; `term` takes about six seconds on macOS, where closing a terminal that has echoed waits a little, and two on Linux):

```
ok  	github.com/fables-for-robots/great-machine/term	6.991s
ok  	github.com/fables-for-robots/great-machine/cli	3.171s
```

- [ ] **Step 8: Run them fifty times, here and on Linux**

A test of a terminal that passes once has shown little. Run:

```bash
nix develop .#bare --command go test -race -count=50 ./term/
docker run --init --rm -v "$PWD":/src:ro -w /src -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod golang:1.26 go test -race -count=50 ./term/
```

Expected: `ok` from each, after about five minutes on macOS and two in the container. Nothing may fail once. (Writing this plan, both ran fifty of fifty.)

Then `nix develop .#bare --command gofmt -l term` prints nothing and `nix develop .#bare --command go vet ./term/` prints nothing.

What each new test holds was checked by taking the code away, and is not to be repeated: without the mode that makes a read not wait, `TestAttachWithAnotherReaderOfTheTerminal` fails every time and `TestAttachRestoresHowLongAReadWaits` with it; with the terminal restored before the reading is stopped, `TestAttachStopsReadingBeforeItRestoresTheTerminal` fails; with the wait kept when a panic is passed on, `TestAttachPanicWhileAWriteWaits` fails; with a terminal never taken for gone, `TestAttachTellsATerminalThatIsGone` and `TestAttachTerminalFails` fail.

- [ ] **Step 9: Commit**

```bash
git add term/
git commit -m "Term: when Attach returns, nothing of it reads the terminal any more"
```
