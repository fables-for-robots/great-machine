package draal

import (
	"context"
	"errors"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// goroutines returns how many goroutines there are, for settled, once
// draal serves conn: a request was asked on it and its stream is closed.
func goroutines(t *testing.T, conn node.Conn) int {
	t.Helper()
	s, _, _ := ask(t, conn, &wire.Request{Store: &wire.StoreRequest{}})
	s.Close()
	return runtime.NumGoroutine()
}

// settled waits until there are no more goroutines than before: nothing
// of what the test began since is left.
func settled(t *testing.T, before int) {
	t.Helper()
	end := time.Now().Add(deadline)
	for runtime.NumGoroutine() > before {
		if time.Now().After(end) {
			buf := make([]byte, 1<<20)
			t.Fatalf("%d goroutines, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(time.Millisecond)
	}
}

// ended returns a channel that is closed when draal has ended the stream:
// what it sent is read to the end.
func ended(s node.Stream) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(io.Discard, s)
	}()
	return done
}

// closedByDraal says whether draal has closed a stream: a write to it
// fails.
func closedByDraal(s node.Stream) bool {
	_, err := s.Write([]byte{0})
	return err != nil
}

func TestAStreamWithoutARequestIsClosed(t *testing.T) {
	cfg := testConfig(t)
	cfg.RequestWait = 50 * time.Millisecond
	w := startWorld(t, cfg)
	conn := w.dial(strangerID)
	before := goroutines(t, conn)

	s, err := conn.OpenStream(context.Background())
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer s.Close()
	// Half of the length of a frame: draal sees the stream, and no request.
	start := time.Now()
	if _, err := s.Write([]byte{0, 0}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wait(t, "draal to close the stream", ended(s))
	if waited := time.Since(start); waited < cfg.RequestWait {
		t.Errorf("the stream was closed after %v, before RequestWait (%v)", waited, cfg.RequestWait)
	}
	s.Close()
	settled(t, before)
	if closed(conn) {
		t.Error("the connection was closed with the stream")
	}
}

func TestTheDefaultsOfStreams(t *testing.T) {
	cfg := Config{}.withDefaults()
	if cfg.RequestWait != 5*time.Second || cfg.MaxStreams != 64 || cfg.RefuseWait != 2*time.Second {
		t.Errorf("RequestWait %v, MaxStreams %d, RefuseWait %v; want 5s, 64, 2s", cfg.RequestWait, cfg.MaxStreams, cfg.RefuseWait)
	}
	if cfg.ActivateWait != 30*time.Second {
		t.Errorf("ActivateWait %v, want 30s", cfg.ActivateWait)
	}
}

func TestAConnectionHasAtMostMaxStreams(t *testing.T) {
	cfg := testConfig(t) // MaxStreams is the default, 64
	w := startWorld(t, cfg)
	// Counted on a connection of its own: the stream it asks on would hold
	// a place of op for a moment after it was closed.
	before := goroutines(t, w.dial(operatorID))
	op := w.dial(operatorID)
	watch := &wire.Request{Watch: &wire.WatchRequest{}}

	var held []node.Stream
	for range 64 {
		s, resp, err := ask(t, op, watch)
		if err != nil || resp.Fleet == nil {
			t.Fatalf("watch %d = %+v, %v", len(held)+1, resp, err)
		}
		held = append(held, s)
	}
	// One more is closed at once, without an answer.
	if _, resp, err := ask(t, op, watch); !errors.Is(err, io.EOF) {
		t.Fatalf("the 65th watch = %+v, %v; want the stream closed without an answer", resp, err)
	}
	if closed(op) {
		t.Fatal("the connection was closed with the stream")
	}
	// When one ends, another is served.
	held[0].Close()
	eventually(t, "a watch to be served again", func() bool {
		s, resp, err := ask(t, op, watch)
		if err == nil && resp.Fleet != nil {
			held = append(held, s)
			return true
		}
		return false
	})

	for _, s := range held {
		s.Close()
	}
	eventually(t, "the watchers to end", func() bool { return w.watchers() == 1 })
	op.Close()
	settled(t, before)
}

func TestARefusedStreamIsClosedWithinRefuseWait(t *testing.T) {
	cfg := testConfig(t)
	cfg.RefuseWait = 50 * time.Millisecond
	w := startWorld(t, cfg)
	stranger := w.dial(strangerID)
	before := goroutines(t, stranger)

	cases := map[string]*wire.Request{
		"store of a stranger":        {Store: &wire.StoreRequest{}},
		"shell of a stranger":        {Shell: &wire.ShellRequest{Zathras: operatorID}},
		"a register with a bad name": {Register: &wire.Register{Service: "web", Name: "a b", State: idle()}},
	}
	for name, req := range cases {
		s, _, err := ask(t, stranger, req)
		if wire.CodeOf(err) != wire.CodeUnauthorized && wire.CodeOf(err) != wire.CodeBadRequest {
			t.Fatalf("%s = %v, want a refusal", name, err)
		}
		// The peer does not close the stream: draal does.
		eventually(t, "draal to close the stream of "+name, func() bool { return closedByDraal(s) })
		s.Close()
	}
	settled(t, before)
}
