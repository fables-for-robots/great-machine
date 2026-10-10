### Task 0: A stream that is closed ends a Write that waits

**Files:**
- Modify: `node/node.go` (the comment of `Stream.Close`; the new method `Stream.Done`)
- Modify: `node/conn.go` (the stream over go-iroh counts its Writes; `Close` resets when one has not returned; `Done`)
- Modify: `node/nodetest/nodetest.go` (the same `Close` and `Done` for the network in memory)
- Modify: `zathras/shell.go` (`answerShell` ends the session when `Done` of the stream is closed)
- Modify: `draal/handler.go` (`serveShell` ends the session when `Done` of the operator's stream is closed, and ends the Write of input when the session has failed)
- Modify: `draal/draal.go` (the comment of `Shell.Close`), `draal/relay.go` (the comment of `relayShell.Close`)
- Test: `node/conn_test.go` (five cases more in `testConn`, which runs over go-iroh on loopback and over `nodetest`)
- Test: `zathras/shell_blocked_test.go`, `draal/shell_blocked_test.go`, `e2e/shell_blocked_test.go` (all new)

**Interfaces:**
- Consumes (code that exists):
  - go-iroh v0.3.0, `*iroh.Stream`: `Write`, `Close() error` (ends the sending direction with a FIN behind all that was written; does not wake a `Write` that waits), `CloseWrite() error` (the same), `CancelRead(code uint64)` (STOP_SENDING: fails the peer's `Write`, cancels the peer's stream context), `CancelWrite(code uint64)` (RESET_STREAM: a `Write` that waits returns at once with an error; the peer's `Read` fails, and what the peer has not read is dropped, even what has arrived), `Context() context.Context` (of the sending direction: cancelled by this side's `Close` and `CancelWrite`, by the peer's `CancelRead`, and with the connection).
  - `wire.Write`, `wire.Read`, `wire.ReadResponse`, `wire.ShellInput`, `wire.ShellOutput`, `wire.Response`, `wire.CodeOf`, `wire.CodeUnavailable`.
  - `ptyshell.Session.Close() error`: ends the shell (SIGHUP to its process group and to the one in the foreground of the terminal, SIGKILL two seconds later) and closes the terminal, which ends a `Write` to the terminal that waits. Unchanged.
  - In the tests: `testConn`, `open`, `wait`, `waitFor`, `readAll`, `result`, `patience`, `soon`, `notYet` of `node/conn_test.go`; `newWorld`, `(*world).accept`, `(*world).idle`, `(*link).openShell`, `(*remoteShell).typeIn/expect/pid`, `gone`, `within`, `patience` of the tests of `zathras`; `startWorld`, `testConfig`, `(*world).newFake`, `(*fake).connect`, `(*fake).onShell`, `(*world).dial`, `ask`, `wait`, `idle`, `operatorID`, `deadline` of the tests of `draal`; `start`, `options`, `eventually`, `alive`, `deadline` of `e2e/harness_test.go`.
- Produces:
  - The promise of `node.Stream.Close`, which both implementations keep. The comment, exactly:

    ```go
    	// Close ends the stream in both directions, and returns at once.
    	//
    	// When no Write of this side is under way, the end is orderly: what
    	// this side wrote before is still delivered, and the peer reads
    	// io.EOF after it.
    	//
    	// When a Write of this side has not returned, because the peer does
    	// not read, the stream is broken off instead: that Write fails, what
    	// this side wrote and the peer has not read may be lost, and the peer
    	// reads an error and not io.EOF. A side that wants its last words
    	// read lets its Write return before it calls Close.
    	//
    	// Either way a Write of the peer fails, at once or when it next
    	// waits, a Read of this side fails, every later Write of this side
    	// fails, and Done is closed on both sides. Close may be called after
    	// CloseWrite and more than once.
    	Close() error
    ```

  - A new method of `node.Stream`, which whoever implements the interface must have (there are two implementations, `node` and `node/nodetest`; no test of the repository has a third):

    ```go
    	// Done is closed when this side can send no more on the stream: the
    	// peer has closed it with Close, this side has ended what it sends
    	// with CloseWrite or Close, or the connection has ended. It is how a
    	// side that neither reads nor writes at the moment learns that the
    	// peer has closed the stream: nothing else tells it.
    	Done() <-chan struct{}
    ```

  - `draal.Shell.Close`, as its comment now says: "Close ends the session, and the shell if it runs. It ends a Read and a Write that wait, each with an error, and returns at once. It may be called from another goroutine, and more than once." Task 2's WebSocket handler stands on this (it closes the `Shell` when the browser goes, whatever its own goroutines wait in), and so do Task 6's tests through HTTP. `TestShellCloseEndsAWriteThatWaits` in `draal/shell_blocked_test.go` pins it.
  - A shell session ends on both sides when either side closes its stream, whatever waits: the operator's `Write`, draal's relay in both directions, the two goroutines of `answerShell`, and the shell with the program in the foreground of its terminal. `TestShellEndsWithTheOperatorWhileInputWaits` in `e2e/shell_blocked_test.go` pins the whole of it over go-iroh.
  - For later tests of `draal`: `deafShell`, `flood`, `stuck` and `noSession` in `draal/shell_blocked_test.go`.

What was found, and what the changes rest on (all of it measured over go-iroh v0.3.0 on loopback):

- A stream takes 512 KiB (524288 bytes) that the peer has not read; a `Write` of at most 16 KiB is buffered beyond that until 64 KiB wait, so writing in pieces of 16 KiB halts after 573440 bytes. Then `Write` waits.
- `Close` of go-iroh's stream, called while a `Write` waits, returns at once and leaves the `Write` waiting until the peer reads (it then completes, and the FIN follows it) or the connection ends. `CancelRead` of this side does nothing to it. So the `Close` of `node` before this task (`CancelRead` and `Close`) never ended such a `Write`.
- `CancelWrite` ends it within microseconds with an error. It is not orderly: the peer's `Read` fails with an error, and what the peer has not read is dropped, even bytes of Writes that had returned long before and had arrived at the peer. So `Close` cannot always reset; it resets only when a `Write` of this side has not returned, and whoever closes a stream in the middle of its own `Write` has no claim that its last words are read.
- A side that neither reads nor writes is told of the peer's `Close` by nothing but the context of go-iroh's stream, which the peer's `CancelRead` cancels. That is the state of a zathras whose shell runs a program that reads no input and prints nothing, once the terminal is full: one goroutine waits in the write to the terminal, the other in the read of it. And it is the state of draal between an operator and that zathras. Hence `Done`.

---

**Cycle 1: `Close` of a stream ends a Write of this side that waits**

- [ ] **Step 1: Write the failing tests**

In `node/conn_test.go`, in `testConn`, put these two cases before the loop that begins with `for _, who := range []string{"the side that dialed", "the side that accepted"} {` (the cases of `Close of the connection by ...`). `testConn` runs once over `nodetest` (`TestNodetestConn`) and once over go-iroh on loopback (`TestIrohConn`).

The first is the defect. The `Write` is under way for certain when `Close` is called: the peer has read a megabyte of it, and it cannot return, because it is four megabytes and nobody reads the rest. The second is the orderly end, which must stay as it is; it passes before the change too.

```go
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
```

- [ ] **Step 2: Run the tests to see the first fail**

Run: `nix develop .#bare --command go test ./node/ -run 'TestNodetestConn|TestIrohConn' -count=1`
Expected: FAIL, after some 10 seconds, in the first new case of both and in no other:

```
--- FAIL: TestNodetestConn
    --- FAIL: TestNodetestConn/Close_of_a_stream_ends_a_Write_of_this_side_that_waits
        conn_test.go:449: the peer read 524288 bytes more and then the end; want an error, because not all that was written came
--- FAIL: TestIrohConn
    --- FAIL: TestIrohConn/Close_of_a_stream_ends_a_Write_of_this_side_that_waits
        conn_test.go:441: the Write that waited: nothing after 3s
```

Over go-iroh the `Write` stays where it is. Over `nodetest` it ends, and the peer is told a clean end behind a part of what was written, which is not what go-iroh does and not true.

- [ ] **Step 3: Write the implementation**

In `node/node.go`, the interface `Stream` becomes (the comment of `Close` is what changes):

```go
// Stream is one stream of a connection, in both directions.
//
// One goroutine may read while another writes. Two that write at once
// have to take turns themselves.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends what this side sends. The peer reads io.EOF after
	// what was written. This side can still read.
	CloseWrite() error
	// Close ends the stream in both directions, and returns at once.
	//
	// When no Write of this side is under way, the end is orderly: what
	// this side wrote before is still delivered, and the peer reads
	// io.EOF after it.
	//
	// When a Write of this side has not returned, because the peer does
	// not read, the stream is broken off instead: that Write fails, what
	// this side wrote and the peer has not read may be lost, and the peer
	// reads an error and not io.EOF. A side that wants its last words
	// read lets its Write return before it calls Close.
	//
	// Either way a Write of the peer fails, at once or when it next
	// waits, a Read of this side fails, and every later Write of this
	// side fails. Close may be called after CloseWrite and more than once.
	Close() error
}
```

In `node/conn.go`, the head of the file down to the comment `// conn is a Conn over a connection of go-iroh.` becomes this. The stream is a pointer now, because it has a lock:

```go
package node

import (
	"context"
	"errors"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// errClosed is what a Write returns after Close.
var errClosed = errors.New("node: the stream was closed on this side")

// stream is a Stream over a stream of go-iroh.
type stream struct {
	s *iroh.Stream

	mu      sync.Mutex
	writing int  // the Writes that have not returned
	closed  bool // Close was called
}

func (s *stream) Read(p []byte) (int, error) { return s.s.Read(p) }

// Write counts itself, so that Close knows of a Write that may wait.
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errClosed
	}
	s.writing++
	s.mu.Unlock()
	n, err := s.s.Write(p)
	s.mu.Lock()
	s.writing--
	s.mu.Unlock()
	return n, err
}

// CloseWrite sends the end of the stream after what was written.
func (s *stream) CloseWrite() error {
	return s.s.CloseWrite()
}

// Close tells the peer to stop sending, which fails its Write, and ends
// what this side sends: with the end of the stream after what was
// written, which the peer reads as io.EOF, or, when a Write has not
// returned, with a reset.
//
// Close of go-iroh's stream ends one direction only, sends the end behind
// all that was written, and does not wake a Write that waits for the peer
// to read: such a Write would wait until the connection ends. A reset
// (CancelWrite) wakes it at once, and drops what the peer has not read,
// even what has arrived there. Hence the one where no Write is under way
// and the other where one is. No Write begins between the look at the
// count and the end of the direction: both are under the lock that a
// Write takes to begin.
//
// The error of ending a direction that has ended before is of no use to
// anybody, so Close returns nil.
func (s *stream) Close() error {
	s.s.CancelRead(0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.writing > 0 {
		s.s.CancelWrite(0)
	} else {
		_ = s.s.Close()
	}
	return nil
}
```

and, in the same file, both `OpenStream` and `AcceptStream` of `conn` return the pointer: `return stream{s}, nil` becomes, in both,

```go
	return &stream{s: s}, nil
```

In `node/nodetest/nodetest.go`, the comment of the package becomes:

```go
// Package nodetest is a network in memory with the Conn, Stream and
// Listener of package node, for the tests of what speaks the protocol. It
// needs no iroh, no socket and no time.
//
// It behaves as the real thing does wherever a protocol could tell the
// difference:
//
//   - Both sides of a connection open streams and accept them.
//   - A stream is not seen by the peer's AcceptStream until its opener has
//     written to it or ended it.
//   - A Write returns before the peer has read, until 512 KiB wait to be
//     read; then it waits, as a stream of QUIC does.
//   - CloseWrite ends one direction: the peer reads io.EOF after what was
//     written, and can still write.
//   - Close of a stream delivers what was written and then io.EOF, fails
//     the peer's Write, and fails this side's Read. When a Write of this
//     side has not returned, Close breaks the stream off instead: that
//     Write fails, what the peer has not read is dropped, and the peer
//     reads an error.
//   - Close of a connection closes Done on both sides and fails every
//     call that waits on it or on one of its streams.
package nodetest
```

the errors get one more:

```go
var (
	errConnEnded     = errors.New("nodetest: the connection has ended")
	errStreamClosed  = errors.New("nodetest: the stream was closed on this side")
	errPeerClosed    = errors.New("nodetest: the peer closed the stream")
	errPeerReset     = errors.New("nodetest: the peer broke the stream off")
	errListenerEnded = errors.New("nodetest: the listener is closed")
)
```

`Close` of a stream calls `closeOrReset` where it called `closeWrite`:

```go
func (s *stream) Close() error {
	s.show()
	s.out.closeOrReset()
	s.in.closeRead()
	return nil
}
```

the pipe counts its writes, and knows that it was broken off: the type `pipe`, `newPipe`, `write` and `read` become

```go
// pipe is one direction of a stream: bytes that were written and wait to
// be read.
type pipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	writing int  // the writes that have not returned
	eof     bool // the writer ended the direction
	reset   bool // the writer broke the direction off
	gone    bool // the reader closed the stream
	failed  bool // the connection has ended
}

func newPipe() *pipe {
	p := &pipe{}
	p.changed = sync.NewCond(&p.mu)
	return p
}

func (p *pipe) write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writing++
	defer func() { p.writing-- }()
	written := 0
	for {
		switch {
		case p.failed:
			return written, errConnEnded
		case p.eof, p.reset:
			return written, errStreamClosed
		case p.gone:
			return written, errPeerClosed
		}
		if len(b) == 0 {
			return written, nil
		}
		if room := window - len(p.buf); room > 0 {
			n := min(room, len(b))
			p.buf = append(p.buf, b[:n]...)
			b = b[n:]
			written += n
			p.changed.Broadcast()
			continue
		}
		p.changed.Wait()
	}
}

func (p *pipe) read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		switch {
		case p.failed:
			return 0, errConnEnded
		case p.gone:
			return 0, errStreamClosed
		case p.reset:
			return 0, errPeerReset
		case len(p.buf) > 0:
			n := copy(b, p.buf)
			p.buf = p.buf[n:]
			p.changed.Broadcast()
			return n, nil
		case p.eof:
			return 0, io.EOF
		}
		if len(b) == 0 {
			return 0, nil
		}
		p.changed.Wait()
	}
}
```

and behind `closeWrite` comes the new function:

```go
// closeOrReset ends the direction from the side that writes, for Close of
// a stream: as closeWrite does when no write is under way, and otherwise
// by breaking it off, which fails the write that waits, drops what was
// not read and fails the reader. A direction that has ended stays as it
// ended.
func (p *pipe) closeOrReset() {
	p.mu.Lock()
	switch {
	case p.eof || p.reset:
	case p.writing > 0:
		p.reset = true
		p.buf = nil
	default:
		p.eof = true
	}
	p.mu.Unlock()
	p.changed.Broadcast()
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./node/... -race -count=1`
Expected: PASS (`ok` for `node` after some 12 seconds, and for `node/nodetest`)

- [ ] **Step 5: Commit**

```bash
git add node/
git commit -m "Node: Close of a stream ends a Write of this side that waits"
```

---

**Cycle 2: `Done` of a stream**

- [ ] **Step 6: Write the failing tests**

In `node/conn_test.go`, in `testConn`, behind the two cases of cycle 1 and before the same loop:

```go
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
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./node/ -run 'TestNodetestConn|TestIrohConn' -count=1`
Expected: FAIL, the package does not build: six lines like `node/conn_test.go:496:27: opened.Done undefined (type "github.com/fables-for-robots/great-machine/node".Stream has no field or method Done)`, for `opened.Done` and `accepted.Done`

- [ ] **Step 8: Write the implementation**

In `node/node.go`, the interface `Stream` becomes (`Done` is new, and the last paragraph of the comment of `Close` names it):

```go
// Stream is one stream of a connection, in both directions.
//
// One goroutine may read while another writes. Two that write at once
// have to take turns themselves.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends what this side sends. The peer reads io.EOF after
	// what was written. This side can still read.
	CloseWrite() error
	// Close ends the stream in both directions, and returns at once.
	//
	// When no Write of this side is under way, the end is orderly: what
	// this side wrote before is still delivered, and the peer reads
	// io.EOF after it.
	//
	// When a Write of this side has not returned, because the peer does
	// not read, the stream is broken off instead: that Write fails, what
	// this side wrote and the peer has not read may be lost, and the peer
	// reads an error and not io.EOF. A side that wants its last words
	// read lets its Write return before it calls Close.
	//
	// Either way a Write of the peer fails, at once or when it next
	// waits, a Read of this side fails, every later Write of this side
	// fails, and Done is closed on both sides. Close may be called after
	// CloseWrite and more than once.
	Close() error
	// Done is closed when this side can send no more on the stream: the
	// peer has closed it with Close, this side has ended what it sends
	// with CloseWrite or Close, or the connection has ended. It is how a
	// side that neither reads nor writes at the moment learns that the
	// peer has closed the stream: nothing else tells it.
	Done() <-chan struct{}
}
```

In `node/conn.go`, `Close` of the stream (its comment names `Done` now; the function is as it was) and the new `Done` behind it:

```go
// Close tells the peer to stop sending, which fails its Write and closes
// its Done, and ends what this side sends: with the end of the stream
// after what was written, which the peer reads as io.EOF, or, when a
// Write has not returned, with a reset.
//
// Close of go-iroh's stream ends one direction only, sends the end behind
// all that was written, and does not wake a Write that waits for the peer
// to read: such a Write would wait until the connection ends. A reset
// (CancelWrite) wakes it at once, and drops what the peer has not read,
// even what has arrived there. Hence the one where no Write is under way
// and the other where one is. No Write begins between the look at the
// count and the end of the direction: both are under the lock that a
// Write takes to begin.
//
// The error of ending a direction that has ended before is of no use to
// anybody, so Close returns nil.
func (s *stream) Close() error {
	s.s.CancelRead(0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.writing > 0 {
		s.s.CancelWrite(0)
	} else {
		_ = s.s.Close()
	}
	return nil
}

// Done is the end of the context of go-iroh's stream, which is that of
// the direction this side sends in: go-iroh cancels it when the peer
// tells this side to stop sending, which its Close does, when this side
// ends or resets what it sends, and when the connection ends.
func (s *stream) Done() <-chan struct{} {
	return s.s.Context().Done()
}
```

In `node/nodetest/nodetest.go`, the comment of the package becomes:

```go
// Package nodetest is a network in memory with the Conn, Stream and
// Listener of package node, for the tests of what speaks the protocol. It
// needs no iroh, no socket and no time.
//
// It behaves as the real thing does wherever a protocol could tell the
// difference:
//
//   - Both sides of a connection open streams and accept them.
//   - A stream is not seen by the peer's AcceptStream until its opener has
//     written to it or ended it.
//   - A Write returns before the peer has read, until 512 KiB wait to be
//     read; then it waits, as a stream of QUIC does.
//   - CloseWrite ends one direction: the peer reads io.EOF after what was
//     written, and can still write.
//   - Close of a stream delivers what was written and then io.EOF, fails
//     the peer's Write, and fails this side's Read. When a Write of this
//     side has not returned, Close breaks the stream off instead: that
//     Write fails, what the peer has not read is dropped, and the peer
//     reads an error.
//   - Done of a stream is closed when the peer has closed the stream,
//     when this side has ended what it sends, and with the connection.
//   - Close of a connection closes Done on both sides and fails every
//     call that waits on it or on one of its streams.
package nodetest
```

a stream gets `Done` behind its `Close`:

```go
func (s *stream) Close() error {
	s.show()
	s.out.closeOrReset()
	s.in.closeRead()
	return nil
}

func (s *stream) Done() <-chan struct{} { return s.out.ended }
```

the pipe gets the channel, and `end` closes it (the type `pipe` and `newPipe` change, `end` is new, and `write` and `read` behind them stay as they are):

```go
// pipe is one direction of a stream: bytes that were written and wait to
// be read.
type pipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	writing int  // the writes that have not returned
	eof     bool // the writer ended the direction
	reset   bool // the writer broke the direction off
	gone    bool // the reader closed the stream
	failed  bool // the connection has ended
	// ended is closed when nothing more can be written: with the first of
	// eof, reset, gone and failed.
	ended chan struct{}
}

func newPipe() *pipe {
	p := &pipe{ended: make(chan struct{})}
	p.changed = sync.NewCond(&p.mu)
	return p
}

// end closes ended, the first time it is called. The caller holds mu.
func (p *pipe) end() {
	select {
	case <-p.ended:
	default:
		close(p.ended)
	}
}
```

and each of the four functions that end a direction calls `end` under the lock. From `closeWrite` to the end of the file:

```go
// closeWrite ends the direction from the side that writes: the reader
// gets what was written and then io.EOF.
func (p *pipe) closeWrite() {
	p.mu.Lock()
	p.eof = true
	p.end()
	p.mu.Unlock()
	p.changed.Broadcast()
}

// closeOrReset ends the direction from the side that writes, for Close of
// a stream: as closeWrite does when no write is under way, and otherwise
// by breaking it off, which fails the write that waits, drops what was
// not read and fails the reader. A direction that has ended stays as it
// ended.
func (p *pipe) closeOrReset() {
	p.mu.Lock()
	switch {
	case p.eof || p.reset:
	case p.writing > 0:
		p.reset = true
		p.buf = nil
	default:
		p.eof = true
	}
	p.end()
	p.mu.Unlock()
	p.changed.Broadcast()
}

// closeRead ends the direction from the side that reads: what waits is
// dropped, and a Write fails.
func (p *pipe) closeRead() {
	p.mu.Lock()
	p.gone = true
	p.end()
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}

// fail ends the direction because the connection has ended.
func (p *pipe) fail() {
	p.mu.Lock()
	p.failed = true
	p.end()
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./node/... -race -count=1`
Expected: PASS

Run: `nix develop .#bare --command go build ./... && nix develop .#bare --command go vet ./...`
Expected: no output but the warnings of Nix about a dirty tree (nothing else implements `node.Stream`)

- [ ] **Step 10: Commit**

```bash
git add node/
git commit -m "Node: Done of a stream is closed when the peer has closed the stream"
```

---

**Cycle 3: a zathras ends a shell whose stream draal has closed while input waits**

- [ ] **Step 11: Write the failing test**

`zathras/shell_blocked_test.go`, new. The input is in lines, because a terminal that waits for the end of a line drops what does not fit, while one that holds whole lines nobody reads takes no more (macOS and Linux both). `stuck` makes sure that the input did back up before the stream is closed: the flood has halted for a second with most of it unwritten, and it fails the test if all of it was taken.

```go
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
```

- [ ] **Step 12: Run the test to see it fail**

Run: `nix develop .#bare --command go test ./zathras/ -run TestShellEndsWithItsStreamWhileInputWaits -count=1 -v`
Expected: FAIL after some 22 seconds, in both cases alike (the process ID differs):

```
    shell_blocked_test.go:103: the input came to a halt after 524288 bytes
    shell_blocked_test.go:109: waited 10s for process 94555 to end
```

The agent's two goroutines wait in the write to the terminal and in the read of it, and nothing tells them that draal has closed the stream. (The processes the failed test leaves are ended when the test's agent stops.)

- [ ] **Step 13: Write the implementation**

`zathras/shell.go`, whole. Three things change in `answerShell`: `Done` of the stream ends the session; the goroutine of the output says whether it came to the exit status (`exited`), because when it gave up for a stream that takes no more output the session is to end at once and not to wait for a reading of the input that may be waiting itself; and `CloseWrite` behind the exit status has moved out of that goroutine to where the exit status is waited for, so that `Done`, which this side's own `CloseWrite` closes too, is closed by it only where `Done` is no longer looked at.

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/wire"
)

// answerShell runs a shell session on s: it starts the shell, says so
// with an empty output, and then copies what is typed to the shell and
// what the shell prints to the stream, until the shell has ended, which
// its exit status says, or the stream has, which ends the shell. ctx is
// done when the connection has ended.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	cfg := a.cfg.Shell
	cfg.Term, cfg.Cols, cfg.Rows = req.Term, req.Cols, req.Rows
	session, err := ptyshell.Start(cfg)
	if err != nil {
		a.log.Warn("a shell could not be started", "error", err)
		wire.WriteError(s, wire.Errorf(wire.CodeInternal, "%s", reason(err)))
		s.CloseWrite()
		io.Copy(io.Discard, s) // until draal has read the answer and closed
		return
	}
	a.log.Info("a shell has started")
	wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})

	// What is typed, and the changes of size, until the stream ends.
	streamEnded := make(chan struct{})
	go func() {
		defer close(streamEnded)
		for {
			var in wire.ShellInput
			if wire.Read(s, &in) != nil {
				return
			}
			if in.Resize != nil {
				session.Resize(in.Resize.Cols, in.Resize.Rows)
			}
			if len(in.Data) > 0 {
				// A Write that fails is one to a shell that has ended,
				// which the output says.
				session.Write(in.Data)
			}
		}
	}()

	// What the shell prints, and at its end the exit status. exited says,
	// once shellEnded is closed, whether it came that far: it did not if
	// the stream took no more output.
	shellEnded := make(chan struct{})
	exited := false
	go func() {
		defer close(shellEnded)
		buf := make([]byte, 32<<10)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}) != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
		exit := session.Wait()
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &exit}})
		exited = true
	}()

	select {
	case <-shellEnded:
		if !exited {
			// draal has closed the stream. The reading of the input may
			// not see it: see Done below.
			break
		}
		// The exit status is on its way. draal closes the stream when it
		// has read it. The end of what is sent closes Done, so it comes
		// here, where Done is waited for no more.
		s.CloseWrite()
		select {
		case <-streamEnded:
		case <-ctx.Done():
		}
	case <-streamEnded:
	case <-s.Done():
		// draal has closed the stream, and the reading of the input has
		// not seen it: it waits in a write to a terminal whose program
		// reads nothing.
	case <-ctx.Done():
	}
	session.Close() // ends the shell if it runs, and with it the output and an input that waits to be written
	s.Close()       // ends the input, and an output that waits to be written
	<-shellEnded
	<-streamEnded
	a.log.Info("a shell has ended", "status", session.Wait())
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./zathras/ -race -count=1`
Expected: PASS

Run: `pgrep -fl 'sleep 1000'; pgrep -x yes`
Expected: no output: no program of a test is left

- [ ] **Step 15: Commit**

```bash
git add zathras/
git commit -m "Zathras: a shell ends when draal closes its stream while input waits for the program"
```

---

**Cycle 4: draal ends an operator's shell whose stream the operator has closed while input waits**

- [ ] **Step 16: Write the failing tests**

`draal/shell_blocked_test.go`, new. Three tests. The first is what the web API stands on (`Close` of a `draal.Shell` ends a `Write` of it that waits); it passes since cycle 1, and fails on the code before this task. The other two are an operator's shell over the protocol. `noSession` looks at the stacks of the goroutines and not at their number: the count that `goroutines` of `streams_test.go` takes can be two too high (the request it asks to take it may still be served), which is as many as the session leaves.

```go
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
```

- [ ] **Step 17: Run the tests to see two fail**

Run: `nix develop .#bare --command go test ./draal/ -run 'TestShellCloseEndsAWriteThatWaits|TestProtocolShellEndsWithTheOperatorWhileInputWaits|TestProtocolShellThatFailsEndsWhileInputWaits' -count=1 -v`
Expected: FAIL after some 25 seconds:

```
--- PASS: TestShellCloseEndsAWriteThatWaits
    shell_blocked_test.go:142: timed out waiting for the stream to the zathras to end with the operator's
--- FAIL: TestProtocolShellEndsWithTheOperatorWhileInputWaits
    shell_blocked_test.go:187: goroutines of a shell session are left after 10s:
--- FAIL: TestProtocolShellThatFailsEndsWhileInputWaits
```

and behind the last message the stacks, with `(*Draal).serveShell` waiting for its context and `(*relayShell).Write` waiting in the stream to the zathras.

- [ ] **Step 18: Write the implementation**

In `draal/handler.go`, `serveShell` (the last function of the file) becomes:

```go
// serveShell relays a shell session between the stream of an operator and
// the Shell of the Controller. The session ends when the shell does, and
// when the operator closes the stream, whatever waits then.
func (d *Draal) serveShell(ctx context.Context, cancel context.CancelFunc, s node.Stream, req wire.ShellRequest) {
	sh, err := d.Shell(ctx, req.Zathras, req)
	if err != nil {
		d.refuse(s, err)
		return
	}
	defer sh.Close()
	if err := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}}); err != nil {
		return
	}
	// An operator that closes the stream is not always seen by the
	// reading of the input: that waits in a Write to a zathras whose
	// shell takes no input, while the shell prints nothing that could be
	// written to the operator. Done of the stream says it then. Done is
	// closed as well when draal ends what it sends, behind the exit
	// status or an error: answered is closed before that, and from then
	// on the session is over and the input is read to the operator's end
	// of the stream.
	answered := make(chan struct{})
	go func() {
		select {
		case <-s.Done():
			select {
			case <-answered:
			default:
				sh.Close()
				cancel()
			}
		case <-ctx.Done():
		}
	}()
	// Input, until the operator ends the stream, which ends the session.
	go func() {
		defer cancel()
		defer sh.Close()
		for {
			var in wire.ShellInput
			if err := wire.Read(s, &in); err != nil {
				return
			}
			if in.Resize != nil {
				if err := sh.Resize(in.Resize.Cols, in.Resize.Rows); err != nil {
					return
				}
			}
			if len(in.Data) > 0 {
				if _, err := sh.Write(in.Data); err != nil {
					return
				}
			}
		}
	}()
	// Output, and behind it the exit status.
	buf := make([]byte, 16<<10)
	for {
		n, err := sh.Read(buf)
		if n > 0 {
			if werr := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}); werr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			sh.Close() // ends a Write of input that waits for the zathras
			wire.WriteError(s, err)
			close(answered)
			s.CloseWrite()
			<-ctx.Done()
			return
		}
	}
	status, err := sh.Wait(ctx)
	sh.Close() // the zathras need not wait for the operator
	if err != nil {
		wire.WriteError(s, err)
	} else {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &status}})
	}
	close(answered)
	s.CloseWrite()
	<-ctx.Done()
}
```

In `draal/draal.go`, the interface `Shell` becomes (the comment of `Close` is what changes):

```go
// Shell is a shell session as an operator has it.
type Shell interface {
	io.Reader // output; io.EOF when the shell has ended
	io.Writer // input
	// Resize tells the shell the new size of the terminal.
	Resize(cols, rows uint16) error
	// Wait returns the exit status when the shell has ended. The status
	// comes behind the output, so Wait returns only once Read has reached
	// the end.
	Wait(ctx context.Context) (int, error)
	// Close ends the session, and the shell if it runs. It ends a Read
	// and a Write that wait, each with an error, and returns at once. It
	// may be called from another goroutine, and more than once.
	Close() error
}
```

In `draal/relay.go`, the comment of `Close` of `relayShell` becomes (the function is as it was):

```go
// Close closes the stream, which ends the shell if it runs, and a Read
// and a Write that wait: the Write even when the zathras reads nothing.
func (r *relayShell) Close() error {
	r.close.Do(func() { r.s.Close() })
	return nil
}
```

- [ ] **Step 19: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./draal/ ./operator/ ./cli/ ./web/... -race -count=1`
Expected: PASS for each

- [ ] **Step 20: Commit**

```bash
git add draal/
git commit -m "Draal: an operator's shell ends with its stream while input waits for the zathras"
```

---

**Cycle 5: the whole of it, over go-iroh with a real shell**

- [ ] **Step 21: Write the test**

`e2e/shell_blocked_test.go`, new. With the four commits before it, it passes. It is the test that would have caught the defect: on the tree before this task it fails after 31 seconds with `the operator's Write still waits 30s after Close` (to see that, make a worktree of the commit before cycle 1 with `git worktree add`, copy the file into its `e2e/`, `git add` it there, run the command of step 22 in it, and remove the worktree).

```go
package e2e

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// shellGoroutines are the functions a goroutine of a shell session is in,
// in draal and in a zathras, as a stack names them.
var shellGoroutines = []string{
	"draal.(*Draal).serveShell",
	"draal.(*relayShell).",
	"zathras.(*Agent).answerShell",
}

// shellStacks returns the stacks of all goroutines if one of them is of a
// shell session, and nil otherwise.
func shellStacks() []byte {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, name := range shellGoroutines {
		if bytes.Contains(buf, []byte(name)) {
			return buf
		}
	}
	return nil
}

// An operator's shell runs a program that reads no input, and the
// operator sends far more input than the terminal of the shell and the
// two streams on the way hold. Then the operator goes. Within the
// deadline the program and the shell are gone, and neither draal nor the
// zathras has a goroutine of the session left.
//
// Before a stream that is closed ended a Write that waits, all of it
// stayed until the connection of the zathras ended: the operator's Write,
// draal's two goroutines, the two of the zathras, and the program.
//
// This test is not parallel: it looks at the goroutines of the whole
// process, where the other tests of the package have shells of their own.
// Those wait until the tests that are not parallel have ended.
func TestShellEndsWithTheOperatorWhileInputWaits(t *testing.T) {
	h := start(t, options{zathrases: 1})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	sh, err := h.op.Shell(ctx, h.pods[0].id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()

	// What the shell prints is read to the end of the session, as a
	// terminal does it.
	var (
		mu     sync.Mutex
		output []byte
	)
	go func() {
		buf := make([]byte, 16<<10)
		for {
			n, err := sh.Read(buf)
			mu.Lock()
			// Only the beginning is looked at: the rest is the echo of
			// the flood.
			if len(output) < 64<<10 {
				output = append(output, buf[:n]...)
			}
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	// printed waits for a number that the shell prints behind name. The
	// terminal echoes what is typed, and that has "$$" where the output
	// has the number.
	printed := func(name string) int {
		t.Helper()
		re := regexp.MustCompile(name + `=(\d+)\.`)
		var number int
		eventually(t, "the shell to print "+name, func() bool {
			mu.Lock()
			defer mu.Unlock()
			m := re.FindSubmatch(output)
			if m == nil {
				return false
			}
			number, _ = strconv.Atoi(string(m[1]))
			return true
		})
		return number
	}

	// The shell, and in it a program that says its process ID and reads
	// nothing from then on.
	if _, err := sh.Write([]byte("echo shell=$$.\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	shell := printed("shell")
	if _, err := sh.Write([]byte("sh -c 'echo program=$$.; exec sleep 1000'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	program := printed("program")
	if !alive(shell) || !alive(program) {
		t.Fatalf("the shell %d or its program %d does not run", shell, program)
	}

	// 16 MiB of input, in lines. A terminal takes a few kilobytes of
	// whole lines that nobody reads, a stream of go-iroh 512 KiB and some
	// 64 KiB that it buffers before that, and there are two streams on
	// the way: all but some 1.2 MiB can never be written.
	const size = 16 << 20
	chunk := bytes.Repeat([]byte("0123456789abcde\n"), 1<<10)
	var written atomic.Int64
	flooded := make(chan error, 1)
	go func() {
		for written.Load() < size {
			if _, err := sh.Write(chunk); err != nil {
				flooded <- err
				return
			}
			written.Add(int64(len(chunk)))
		}
		flooded <- nil
	}()
	// The Write waits: nothing more was written for a second, though not
	// all is written.
	last, since := int64(-1), time.Now()
	eventually(t, "the input to come to a halt", func() bool {
		select {
		case err := <-flooded:
			t.Fatalf("the flood ended with %v after %d bytes; want it to wait for a program that reads nothing", err, written.Load())
		default:
		}
		if now := written.Load(); now != last {
			last, since = now, time.Now()
		}
		return last > 0 && time.Since(since) > time.Second
	})
	t.Logf("the input came to a halt after %d bytes", last)
	if !alive(program) {
		t.Fatalf("the program %d has ended before the operator went", program)
	}

	// The operator goes.
	closed := time.Now()
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-flooded:
		if err == nil {
			t.Error("the Write that waited ended without an error")
		}
	case <-time.After(deadline):
		t.Fatalf("the operator's Write still waits %v after Close", deadline)
	}
	eventually(t, "the program and the shell to end", func() bool { return !alive(program) && !alive(shell) })
	end := time.Now().Add(deadline)
	for stacks := shellStacks(); stacks != nil; stacks = shellStacks() {
		if time.Now().After(end) {
			t.Fatalf("goroutines of the session are left %v after the operator went:\n%s", deadline, stacks)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("all of the session was gone %v after the operator", time.Since(closed).Round(time.Millisecond))
}
```

- [ ] **Step 22: Run the test to see it pass**

Run: `nix develop .#bare --command go test ./e2e/ -run TestShellEndsWithTheOperatorWhileInputWaits -race -count=1 -v`
Expected: PASS within some 3 seconds, with two lines like

```
    shell_blocked_test.go:149: the input came to a halt after 1048576 bytes
    shell_blocked_test.go:175: all of the session was gone 12ms after the operator
```

(the count is between one and 1.2 MiB: two streams and a terminal). Seen on macOS and, in a container, on Linux.

- [ ] **Step 23: Run everything**

Run: `nix develop .#bare --command go test ./node/... ./zathras/ ./draal/ ./e2e/ ./operator/ -race -count=5`
Expected: PASS for each (`node` and `e2e` take about a minute each)

Run: `nix develop .#bare --command go test ./...`
Expected: PASS for every package

Run: `nix develop .#bare --command gofmt -l . ; nix develop .#bare --command go vet ./...`
Expected: no output but the warnings of Nix about a dirty tree

Run: `pgrep -fl 'sleep 1000'; pgrep -x yes`
Expected: no output

- [ ] **Step 24: Commit**

```bash
git add e2e/shell_blocked_test.go
git commit -m "E2E: an operator's shell ends with the operator while input waits for its program"
```
