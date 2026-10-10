package draal

import (
	"bytes"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// floodSize is how much input a test sends to a shell that takes none. A
// stream holds 512 KiB unread, so most of it can never be written.
const floodSize = 16 << 20

// deafShell returns what a fake zathras answers shell with: a shell that
// runs and reads no input, as one whose program reads none comes to do,
// until draal closes the stream.
func deafShell(ended chan<- struct{}) func(wire.ShellRequest, node.Stream) {
	return func(req wire.ShellRequest, s node.Stream) {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
		<-s.Done()
		ended <- struct{}{}
	}
}

// flood calls write with 16 KiB of input until floodSize bytes are
// written or write fails, and returns the count of the bytes written and
// what the writing ended with.
func flood(write func(p []byte) error) (written *atomic.Int64, ended <-chan error) {
	written = new(atomic.Int64)
	done := make(chan error, 1)
	chunk := bytes.Repeat([]byte("0123456789abcde\n"), 1<<10)
	go func() {
		for written.Load() < floodSize {
			if err := write(chunk); err != nil {
				done <- err
				return
			}
			written.Add(int64(len(chunk)))
		}
		done <- nil
	}()
	return written, done
}

// stuck waits until the writing of a flood has come to a halt: nothing
// more was written for a second, though not all is written. It fails the
// test when all of it was taken, which means that nothing backed up.
func stuck(t *testing.T, written *atomic.Int64, ended <-chan error) {
	t.Helper()
	end := time.Now().Add(6 * deadline)
	last, since := int64(-1), time.Now()
	for {
		select {
		case err := <-ended:
			t.Fatalf("the flood ended with %v after %d bytes; want it to wait for a shell that reads nothing", err, written.Load())
		default:
		}
		if now := written.Load(); now != last {
			last, since = now, time.Now()
		} else if last > 0 && time.Since(since) > time.Second {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("the flood did not come to a halt in %v; %d bytes are written", 6*deadline, written.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// noSession fails the test unless draal comes to have no goroutine that
// serves a shell: none in serveShell or in what it started, and none in a
// method of relayShell.
func noSession(t *testing.T) {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		buf := make([]byte, 4<<20)
		buf = buf[:runtime.Stack(buf, true)]
		if !bytes.Contains(buf, []byte("(*Draal).serveShell")) && !bytes.Contains(buf, []byte("(*relayShell).")) {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("goroutines of a shell session are left after %v:\n%s", deadline, buf)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// What the web API stands on: Close of a Shell ends a Write of it that
// waits for a zathras that reads nothing, and the stream to the zathras.
func TestShellCloseEndsAWriteThatWaits(t *testing.T) {
	w := startWorld(t, testConfig(t))
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = deafShell(ended)
	z.connect()
	sh, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	written, failed := flood(func(p []byte) error { _, err := sh.Write(p); return err })
	stuck(t, written, failed)

	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wait(t, "the Write that waited to end", failed); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("the Write that waited ended with %v, want unavailable", err)
	}
	wait(t, "the stream to the zathras to end", ended)
}

// An operator's shell over the protocol, the same way: the operator has
// sent more input than the zathras takes, and closes its stream. draal
// reads that stream no more, because it waits in the Write to the
// zathras, and reads nothing of the zathras that it could write to the
// operator: Done of the operator's stream is all that tells it. The
// session has to end on both sides, and nothing of it may be left.
func TestProtocolShellEndsWithTheOperatorWhileInputWaits(t *testing.T) {
	w := startWorld(t, testConfig(t))
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = deafShell(ended)
	z.connect()
	op := w.dial(operatorID)

	s, resp, err := ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: z.id, Cols: 80, Rows: 24}})
	if err != nil || resp.Shell == nil {
		t.Fatalf("shell = %+v, %v; want the empty output that says it runs", resp, err)
	}
	written, failed := flood(func(p []byte) error { return wire.Write(s, &wire.ShellInput{Data: p}) })
	stuck(t, written, failed)

	s.Close()
	if err := wait(t, "the operator's Write that waited to end", failed); err == nil {
		t.Error("the operator's Write that waited ended without an error")
	}
	wait(t, "the stream to the zathras to end with the operator's", ended)
	noSession(t)
}

// The session fails on the side of the zathras while input waits to be
// written to it: the zathras ends what it sends without an exit status,
// and still reads nothing. The operator is told, and closes its stream.
// Nothing of the session may be left: the Write that waits is ended when
// the session has failed, and not held for the zathras.
func TestProtocolShellThatFailsEndsWhileInputWaits(t *testing.T) {
	w := startWorld(t, testConfig(t))
	fail, hold := make(chan struct{}), make(chan struct{})
	defer close(hold)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = func(req wire.ShellRequest, s node.Stream) {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
		<-fail
		s.CloseWrite()
		<-hold
	}
	z.connect()
	op := w.dial(operatorID)

	s, resp, err := ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: z.id, Cols: 80, Rows: 24}})
	if err != nil || resp.Shell == nil {
		t.Fatalf("shell = %+v, %v; want the empty output that says it runs", resp, err)
	}
	written, failed := flood(func(p []byte) error { return wire.Write(s, &wire.ShellInput{Data: p}) })
	stuck(t, written, failed)

	close(fail)
	answer := make(chan error, 1)
	go func() {
		_, err := wire.ReadResponse(s)
		answer <- err
	}()
	if err := wait(t, "the answer that the session failed", answer); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Fatalf("the session ended with %v, want unavailable", err)
	}
	s.Close()
	if err := wait(t, "the operator's Write that waited to end", failed); err == nil {
		t.Error("the operator's Write that waited ended without an error")
	}
	// The zathras has not closed its stream: it is draal that ends the
	// Write.
	noSession(t)
}
