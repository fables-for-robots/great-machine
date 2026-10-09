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

// The path that leaves a reader on a terminal: Attach ends with nothing
// typed. The terminal is restored, and the key typed next is read by the
// goroutine that was left, and dropped: the session never gets it.
func TestAttachTerminalLeftoverReader(t *testing.T) {
	cases := []struct {
		name  string
		end   func(s *fakeSession, cancel func())
		check func(t *testing.T, got attached)
	}{
		{"the shell ends",
			func(s *fakeSession, cancel func()) { close(s.out) },
			func(t *testing.T, got attached) {
				if got.err != nil || got.detached || got.exit != 42 {
					t.Errorf("Attach = %d, %v, %v; want 42, false, nil", got.exit, got.detached, got.err)
				}
			}},
		{"the context is done",
			func(s *fakeSession, cancel func()) { cancel() },
			func(t *testing.T, got attached) {
				if !errors.Is(got.err, context.Canceled) || got.detached {
					t.Errorf("Attach = %d, %v, %v; want the error of the context", got.exit, got.detached, got.err)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			master, tty := terminal(t)
			cooked := mode(t, tty)
			s := newFakeSession(42)
			a := newAttachment(tty, io.Discard, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := start(ctx, a)
			waitFor(t, "the size", func() bool { return len(s.sizes()) == 1 })

			c.end(s, cancel)
			c.check(t, await(t, done))
			if !reflect.DeepEqual(mode(t, tty), cooked) {
				t.Error("the terminal was not restored")
			}
			// The goroutine is left reading: it has not ended on its own.
			select {
			case <-a.inputEnded:
				t.Fatal("the reading of the terminal ended with nothing typed")
			default:
			}

			// The next input of the user is read by it and dropped. A line
			// feed completes the line now that the terminal is cooked again.
			if _, err := master.Write([]byte("z\n")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-a.inputEnded:
			case <-time.After(5 * time.Second):
				t.Fatal("the reading of the terminal did not end with the next input")
			}
			if s.got() != "" {
				t.Errorf("the session was sent %q after Attach had returned", s.got())
			}
			noSizeAfterReturn(t, master, s)
		})
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
