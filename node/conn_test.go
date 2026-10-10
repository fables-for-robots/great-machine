package node_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
)

// patience is how long a test waits for what has to happen. Nothing waits
// that long unless it fails.
const patience = 10 * time.Second

// soon is how long a test waits for what a Close must bring about at the
// peer. It has to be well under the idle timeout of the dial, 6 seconds: a
// peer that was not told would notice by itself after that, and a wait
// that long would pass without the Close having done anything.
const soon = 3 * time.Second

// notYet is how long a test waits for what must not happen.
const notYet = 250 * time.Millisecond

// connPair makes a connection and returns its two sides: client dialed,
// server accepted. Both are closed when the test ends.
type connPair func(t *testing.T) (client, server node.Conn)

// The same expectations are checked of the network in memory and, in
// iroh_test.go, of go-iroh, so that what is tested over the one holds over
// the other.
func TestNodetestConn(t *testing.T) {
	testConn(t, func(t *testing.T) (client, server node.Conn) {
		t.Helper()
		n := nodetest.New()
		l := n.Listen(nodetest.ID(1))
		t.Cleanup(func() { l.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		client, err := n.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		t.Cleanup(func() { client.Close() })
		server, err = l.Accept(ctx)
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		t.Cleanup(func() { server.Close() })
		if got := client.RemoteID(); got != nodetest.ID(1) {
			t.Errorf("the client's RemoteID is %s, want %s", got, nodetest.ID(1))
		}
		if got := server.RemoteID(); got != nodetest.ID(2) {
			t.Errorf("the server's RemoteID is %s, want %s", got, nodetest.ID(2))
		}
		return client, server
	})
}

// result is what a call that may wait returned.
type result struct {
	n    int
	data []byte
	err  error
}

// wait returns what arrives on ch, and fails the test when nothing does.
func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	return waitFor(t, patience, ch, what)
}

// waitFor is wait with a bound of its own.
func waitFor[T any](t *testing.T, bound time.Duration, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(bound):
		t.Fatalf("%s: nothing after %v", what, bound)
		panic("not reached")
	}
}

// open opens a stream on from, writes first to it, and accepts it on to.
func open(t *testing.T, from, to node.Conn, first string) (opened, accepted node.Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	opened, err := from.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := opened.Write([]byte(first)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	accepted, err = to.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	expect(t, accepted, first)
	return opened, accepted
}

// expect reads exactly want from s.
func expect(t *testing.T, s io.Reader, want string) {
	t.Helper()
	got := make(chan result, 1)
	go func() {
		buf := make([]byte, len(want))
		_, err := io.ReadFull(s, buf)
		got <- result{data: buf, err: err}
	}()
	r := wait(t, got, "reading "+want)
	if r.err != nil {
		t.Fatalf("reading %q: %v", want, r.err)
	}
	if string(r.data) != want {
		t.Fatalf("read %q, want %q", r.data, want)
	}
}

// readAll reads s to its end in the background.
func readAll(s io.Reader) <-chan result {
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(s)
		ch <- result{data: data, err: err}
	}()
	return ch
}

func testConn(t *testing.T, pair connPair) {
	t.Run("streams in both directions on one connection", func(t *testing.T) {
		client, server := pair(t)
		for _, dir := range []struct {
			name     string
			from, to node.Conn
		}{
			{"opened by the side that dialed", client, server},
			{"opened by the side that accepted", server, client},
		} {
			opened, accepted := open(t, dir.from, dir.to, "request")
			if _, err := accepted.Write([]byte("answer")); err != nil {
				t.Fatalf("%s: Write of the answer: %v", dir.name, err)
			}
			expect(t, opened, "answer")
			if _, err := opened.Write([]byte("more")); err != nil {
				t.Fatalf("%s: second Write: %v", dir.name, err)
			}
			expect(t, accepted, "more")
		}
	})

	t.Run("several streams at once", func(t *testing.T) {
		client, server := pair(t)
		const each = 8
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()

		// Each side answers every stream it accepts with what it read,
		// behind "echo ".
		echo := func(c node.Conn) {
			for {
				s, err := c.AcceptStream(ctx)
				if err != nil {
					return
				}
				go func() {
					data, err := io.ReadAll(s)
					if err != nil {
						return
					}
					s.Write(append([]byte("echo "), data...))
					s.CloseWrite()
				}()
			}
		}
		go echo(client)
		go echo(server)

		var wg sync.WaitGroup
		errs := make(chan error, 2*each)
		for i := 0; i < each; i++ {
			for name, c := range map[string]node.Conn{"client": client, "server": server} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					msg := bytes.Repeat([]byte(fmt.Sprintf("%s %d;", name, i)), 1000)
					s, err := c.OpenStream(ctx)
					if err != nil {
						errs <- fmt.Errorf("%s %d: OpenStream: %w", name, i, err)
						return
					}
					defer s.Close()
					if _, err := s.Write(msg); err != nil {
						errs <- fmt.Errorf("%s %d: Write: %w", name, i, err)
						return
					}
					if err := s.CloseWrite(); err != nil {
						errs <- fmt.Errorf("%s %d: CloseWrite: %w", name, i, err)
						return
					}
					got, err := io.ReadAll(s)
					if err != nil {
						errs <- fmt.Errorf("%s %d: reading the echo: %w", name, i, err)
						return
					}
					if want := append([]byte("echo "), msg...); !bytes.Equal(got, want) {
						errs <- fmt.Errorf("%s %d: the echo has %d bytes and is not what was sent (%d bytes)", name, i, len(got), len(msg))
					}
				}()
			}
		}
		finished := make(chan struct{})
		go func() { wg.Wait(); close(finished) }()
		wait(t, finished, "the streams")
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})

	// Nobody may rely on the opposite: whoever opens a stream writes first.
	t.Run("a stream is not seen before its opener has written", func(t *testing.T) {
		client, server := pair(t)
		for _, dir := range []struct {
			name     string
			from, to node.Conn
		}{
			{"opened by the side that dialed", client, server},
			{"opened by the side that accepted", server, client},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			opened, err := dir.from.OpenStream(ctx)
			if err != nil {
				t.Fatalf("%s: OpenStream: %v", dir.name, err)
			}
			short, stop := context.WithTimeout(ctx, notYet)
			if s, err := dir.to.AcceptStream(short); err == nil {
				t.Errorf("%s: AcceptStream returned a stream nothing was written to: %v", dir.name, s)
			}
			stop()
			if _, err := opened.Write([]byte("now")); err != nil {
				t.Fatalf("%s: Write: %v", dir.name, err)
			}
			accepted, err := dir.to.AcceptStream(ctx)
			if err != nil {
				t.Fatalf("%s: AcceptStream after the Write: %v", dir.name, err)
			}
			expect(t, accepted, "now")
			cancel()
		}
	})

	t.Run("a stream that is ended without a Write is seen", func(t *testing.T) {
		client, server := pair(t)
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		opened, err := client.OpenStream(ctx)
		if err != nil {
			t.Fatalf("OpenStream: %v", err)
		}
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		accepted, err := server.AcceptStream(ctx)
		if err != nil {
			t.Fatalf("AcceptStream: %v", err)
		}
		if r := wait(t, readAll(accepted), "the end of the stream"); r.err != nil || len(r.data) != 0 {
			t.Errorf("read %q, %v; want the end and nothing before it", r.data, r.err)
		}
	})

	t.Run("AcceptStream gives up with its context", func(t *testing.T) {
		client, server := pair(t)
		short, stop := context.WithTimeout(context.Background(), notYet)
		defer stop()
		if _, err := server.AcceptStream(short); err == nil {
			t.Fatal("AcceptStream returned a stream nobody opened")
		}
		// The connection is as good as before.
		open(t, client, server, "still here")
	})

	t.Run("CloseWrite ends one direction", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		rest := readAll(accepted)
		if _, err := opened.Write([]byte(" and the rest")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		// The peer reads what was written and then the end.
		if r := wait(t, rest, "the end of what was sent"); r.err != nil || string(r.data) != " and the rest" {
			t.Fatalf("the peer read %q, %v; want %q and the end", r.data, r.err, " and the rest")
		}
		// The other direction is as it was.
		if _, err := accepted.Write([]byte("answer")); err != nil {
			t.Fatalf("the peer's Write after CloseWrite: %v", err)
		}
		expect(t, opened, "answer")
		if _, err := opened.Write([]byte("x")); err == nil {
			t.Error("a Write after CloseWrite did not fail")
		}
		if err := accepted.CloseWrite(); err != nil {
			t.Fatalf("the peer's CloseWrite: %v", err)
		}
		if r := wait(t, readAll(opened), "the end of the answer"); r.err != nil || len(r.data) != 0 {
			t.Errorf("read %q, %v; want the end", r.data, r.err)
		}
	})

	t.Run("Close of a stream delivers what was written and then the end", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		// The peer waits in Read.
		rest := readAll(accepted)
		if _, err := opened.Write([]byte("last words")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if r := wait(t, rest, "the peer's Read"); r.err != nil || string(r.data) != "last words" {
			t.Errorf("the peer read %q, %v; want %q and the end", r.data, r.err, "last words")
		}
	})

	t.Run("Close of a stream fails the peer's Write", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		// The peer writes until it may not: it comes to wait in Write when
		// nobody reads, and Close has to end that wait too.
		failed := make(chan error, 1)
		go func() {
			chunk := make([]byte, 64<<10)
			for {
				if _, err := accepted.Write(chunk); err != nil {
					failed <- err
					return
				}
			}
		}()
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := wait(t, failed, "the failure of the peer's Write"); err == nil {
			t.Error("the peer's Write ended without an error")
		}
	})

	t.Run("Close of a stream fails this side's Read and Write", func(t *testing.T) {
		client, server := pair(t)
		opened, _ := open(t, client, server, "request")
		// This side waits in Read.
		blocked := make(chan result, 1)
		go func() {
			n, err := opened.Read(make([]byte, 1))
			blocked <- result{n: n, err: err}
		}()
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if r := wait(t, blocked, "the Read that waited"); r.err == nil || r.err == io.EOF {
			t.Errorf("the Read that waited returned %d, %v; want an error that is not io.EOF", r.n, r.err)
		}
		if _, err := opened.Read(make([]byte, 1)); err == nil || err == io.EOF {
			t.Errorf("a Read after Close returned %v; want an error that is not io.EOF", err)
		}
		if _, err := opened.Write([]byte("x")); err == nil {
			t.Error("a Write after Close did not fail")
		}
	})

	t.Run("Close of a stream may come after CloseWrite and twice", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Errorf("Close after CloseWrite: %v", err)
		}
		if err := opened.Close(); err != nil {
			t.Errorf("a second Close: %v", err)
		}
		if r := wait(t, readAll(accepted), "the end"); r.err != nil {
			t.Errorf("the peer read %v, want the end", r.err)
		}
		if err := accepted.Close(); err != nil {
			t.Errorf("the peer's Close: %v", err)
		}
		// The connection is as good as before.
		open(t, server, client, "another")
	})

	// The peer reads nothing more, and this side has a Write that cannot
	// end: it is more than a stream holds unread (512 KiB of window, and
	// 64 KiB that go-iroh buffers before that). Close has to end it.
	t.Run("Close of a stream ends a Write of this side that waits", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		const (
			size = 4 << 20 // what is written at once
			read = 1 << 20 // what the peer reads of it before it stops
		)
		written := make(chan result, 1)
		go func() {
			n, err := opened.Write(make([]byte, size))
			written <- result{n: n, err: err}
		}()
		// The Write is under way for certain once the peer has read a part
		// of it, and it cannot return, because nobody reads the rest.
		part := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(accepted, make([]byte, read))
			part <- err
		}()
		if err := wait(t, part, "the peer's Read of the first part"); err != nil {
			t.Fatalf("the peer's Read of the first part: %v", err)
		}
		select {
		case r := <-written:
			t.Fatalf("a Write of %d bytes returned %d, %v, and the peer has read %d of them", size, r.n, r.err, read)
		case <-time.After(notYet):
		}

		closed := make(chan error, 1)
		go func() { closed <- opened.Close() }()
		if err := waitFor(t, soon, closed, "Close while a Write waits"); err != nil {
			t.Fatalf("Close: %v", err)
		}
		r := waitFor(t, soon, written, "the Write that waited")
		if r.err == nil || r.n >= size {
			t.Errorf("the Write that waited returned %d, %v; want an error and less than %d", r.n, r.err, size)
		}
		// The peer is not told that this was all: it reads an error, and
		// not the end.
		got := waitFor(t, soon, readAll(accepted), "the peer's Read after the Close")
		if got.err == nil {
			t.Errorf("the peer read %d bytes more and then the end; want an error, because not all that was written came", len(got.data))
		}
		if _, err := opened.Write([]byte("x")); err == nil {
			t.Error("a Write after Close did not fail")
		}
		// The connection is as good as before.
		open(t, client, server, "another")
	})

	// The orderly end, with a peer that reads only afterwards: every Write
	// has returned when Close is called, so nothing is taken away.
	t.Run("Close of a stream delivers what was written to a peer that reads later", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		want := bytes.Repeat([]byte("0123456789abcdef"), 16<<10) // 256 KiB, in the window
		for rest := want; len(rest) > 0; {
			n := min(len(rest), 10_000)
			if _, err := opened.Write(rest[:n]); err != nil {
				t.Fatalf("Write: %v", err)
			}
			rest = rest[n:]
		}
		if err := opened.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		time.Sleep(notYet) // the peer is late
		r := wait(t, readAll(accepted), "the peer's Read")
		if r.err != nil || !bytes.Equal(r.data, want) {
			t.Errorf("the peer read %d bytes, %v; want the %d that were written and the end", len(r.data), r.err, len(want))
		}
	})

	t.Run("Done of a stream is closed when the peer closes the stream", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		// This side neither reads nor writes: Done alone can tell it.
		select {
		case <-opened.Done():
			t.Fatal("Done is closed and nobody closed the stream")
		case <-accepted.Done():
			t.Fatal("Done of the peer is closed and nobody closed the stream")
		case <-time.After(notYet):
		}
		if err := accepted.Close(); err != nil {
			t.Fatalf("the peer's Close: %v", err)
		}
		wait(t, accepted.Done(), "Done of the side that closed")
		waitFor(t, soon, opened.Done(), "Done of the other side")
	})

	t.Run("Done of a stream is closed when the peer closes it under a Write that waits", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		go accepted.Write(make([]byte, 4<<20)) // nobody reads it
		go opened.Write(make([]byte, 4<<20))   // nor this
		select {
		case <-opened.Done():
			t.Fatal("Done is closed and nobody closed the stream")
		case <-time.After(notYet):
		}
		if err := accepted.Close(); err != nil {
			t.Fatalf("the peer's Close: %v", err)
		}
		waitFor(t, soon, opened.Done(), "Done of the other side")
	})

	t.Run("Done of a stream is closed by CloseWrite and by the end of the connection", func(t *testing.T) {
		client, server := pair(t)
		opened, accepted := open(t, client, server, "request")
		if err := opened.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
		wait(t, opened.Done(), "Done after CloseWrite")
		// The peer can still send, so its Done is open.
		select {
		case <-accepted.Done():
			t.Fatal("Done of the peer is closed by a CloseWrite of this side")
		case <-time.After(notYet):
		}
		if err := client.Close(); err != nil {
			t.Fatalf("Close of the connection: %v", err)
		}
		waitFor(t, soon, accepted.Done(), "Done of the peer after the end of the connection")
	})

	for _, who := range []string{"the side that dialed", "the side that accepted"} {
		t.Run("Close of the connection by "+who, func(t *testing.T) {
			client, server := pair(t)
			closer, other := client, server
			if who == "the side that accepted" {
				closer, other = server, client
			}
			opened, accepted := open(t, client, server, "request")
			ctx, cancel := context.WithTimeout(context.Background(), patience)
			defer cancel()

			// Calls that wait, on both sides.
			accepts := make(chan error, 2)
			for _, c := range []node.Conn{client, server} {
				go func() {
					_, err := c.AcceptStream(ctx)
					accepts <- err
				}()
			}
			reads := make(chan error, 2)
			for _, s := range []node.Stream{opened, accepted} {
				go func() {
					_, err := s.Read(make([]byte, 1))
					reads <- err
				}()
			}
			select {
			case <-client.Done():
				t.Fatal("Done of the client is closed before anybody closed")
			case <-server.Done():
				t.Fatal("Done of the server is closed before anybody closed")
			case <-time.After(notYet):
			}

			if err := closer.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			wait(t, closer.Done(), "Done of the side that closed")
			waitFor(t, soon, other.Done(), "Done of the other side")
			for range 2 {
				if err := wait(t, accepts, "an AcceptStream that waited"); err == nil {
					t.Error("an AcceptStream that waited returned a stream")
				}
			}
			for range 2 {
				if err := waitFor(t, soon, reads, "a Read that waited"); err == nil || err == io.EOF {
					t.Errorf("a Read that waited returned %v; want an error that is not io.EOF", err)
				}
			}
			for name, c := range map[string]node.Conn{"client": client, "server": server} {
				if _, err := c.AcceptStream(ctx); err == nil {
					t.Errorf("%s: AcceptStream after the end returned a stream", name)
				}
				if s, err := c.OpenStream(ctx); err == nil {
					// Opening may still seem to work; writing may not.
					if _, err := s.Write([]byte("x")); err == nil {
						t.Errorf("%s: a Write to a stream opened after the end did not fail", name)
					}
				}
			}
			if _, err := opened.Write([]byte("x")); err == nil {
				t.Error("a Write to a stream after the end of the connection did not fail")
			}
			if err := closer.Close(); err != nil {
				t.Errorf("a second Close: %v", err)
			}
			if err := other.Close(); err != nil {
				t.Errorf("Close of the other side after the end: %v", err)
			}
		})
	}
}
