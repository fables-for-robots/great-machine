package zathras

import (
	"bytes"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// floodSize is how much input a test sends to a program that reads none.
// A terminal holds a few kilobytes of it and a stream 512 KiB, so most of
// it can never be written.
const floodSize = 16 << 20

// flood writes floodSize bytes of input to the shell, in lines, as draal
// does it, and returns the count of the bytes that were written and what
// the writing ended with. In lines, because a terminal that waits for the
// end of a line drops what does not fit, and one that holds whole lines
// nobody reads takes no more.
func (r *remoteShell) flood() (written *atomic.Int64, ended <-chan error) {
	written = new(atomic.Int64)
	done := make(chan error, 1)
	chunk := bytes.Repeat([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n"), 256) // 16 KiB
	go func() {
		for written.Load() < floodSize {
			if err := wire.Write(r.s, &wire.ShellInput{Data: chunk}); err != nil {
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
func stuck(t *testing.T, written *atomic.Int64, ended <-chan error) int64 {
	t.Helper()
	deadline := time.Now().Add(6 * patience)
	last, since := int64(-1), time.Now()
	for {
		select {
		case err := <-ended:
			t.Fatalf("the flood ended with %v after %d bytes; want it to wait for a program that reads nothing", err, written.Load())
		default:
		}
		if now := written.Load(); now != last {
			last, since = now, time.Now()
		} else if last > 0 && time.Since(since) > time.Second {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("the flood did not come to a halt in %v; %d bytes are written", 6*patience, written.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// discard reads what the shell prints, as an operator's terminal does,
// until the stream ends.
func (r *remoteShell) discard() {
	go func() {
		for {
			if _, err := wire.ReadResponse(r.s); err != nil {
				return
			}
		}
	}()
}

// A program that reads no input, and more input than the terminal and the
// stream hold: the agent waits in its write to the terminal, and reads
// the stream no more. draal closes the stream. Done of the stream is all
// that tells the agent, and it has to end the shell and what runs in it:
// a program that is quiet, and one that prints, whose output the stream
// refuses from then on.
func TestShellEndsWithItsStreamWhileInputWaits(t *testing.T) {
	for name, program := range map[string]string{
		"a quiet program":       "exec sleep 1000",
		"a program that prints": "exec yes",
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, setup{})
			l := w.accept()
			sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
			pid := sh.pid()
			// The program says its process ID and reads nothing from then on.
			sh.typeIn("sh -c 'echo child=$$.; " + program + "'")
			child, err := strconv.Atoi(sh.expect(`child=(\d+)\.`))
			if err != nil {
				t.Fatal(err)
			}
			sh.discard()

			written, ended := sh.flood()
			n := stuck(t, written, ended)
			t.Logf("the input came to a halt after %d bytes", n)

			sh.s.Close()
			if err := within(t, "the write of the flood to end", func() error { return <-ended }); err == nil {
				t.Error("the flood ended without an error after the stream was closed")
			}
			gone(t, child)
			gone(t, pid)
			w.idle()
		})
	}
}
