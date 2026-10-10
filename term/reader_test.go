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

// On macOS poll answers POLLNVAL for /dev/tty, a device it does not know,
// whether or not the terminal is alive, and a terminal that is alive must
// not be taken for gone.
func TestADeviceThatPollDoesNotKnowIsTakenForAlive(t *testing.T) {
	cases := []struct {
		name    string
		revents int16
		want    bool
	}{
		{"nothing", 0, false},
		{"readable", unix.POLLIN, false},
		{"not known", unix.POLLNVAL, false},
		{"not known and readable", unix.POLLNVAL | unix.POLLIN, false},
		{"hangup", unix.POLLHUP, true},
		{"error", unix.POLLERR, true},
		{"hangup and error", unix.POLLHUP | unix.POLLERR, true},
	}
	for _, c := range cases {
		if got := hungUp(c.revents); got != c.want {
			t.Errorf("hungUp(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
