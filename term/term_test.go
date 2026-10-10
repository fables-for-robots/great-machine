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
