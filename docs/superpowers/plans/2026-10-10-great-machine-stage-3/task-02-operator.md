### Task 2: `operator`: a client that is closed ends what it started

`Watch` and `Logs` each start a goroutine that reads its stream and sends on a channel. As stage 1 leaves them, `Client.Close` closes the connection and nothing else. That fails every read of a stream, on `node/nodetest` and on go-iroh alike, so a goroutine that waits in a read ends. But a goroutine that waits to send on its channel, with nobody receiving and a context that is not done, stays for good, and its channel is never closed; and `Wait` of a `Shell` that nobody reads never returns. The terminal UI dials again after a lost connection and closes the old client, whose channels it no longer receives from: nothing of the old one may live on.

**Files:**
- Modify: `operator/client.go` (`ErrClosed`; `Client` knows that it is closed and counts its goroutines; `Close` ends and waits for them; `Watch` and `Logs` end with the client; `failed` is a method and says `ErrClosed`; a hook for a test)
- Modify: `operator/shell.go` (a `Shell` knows the client's end: `Read`, `Write`, `Resize` and `Wait` return `ErrClosed`)
- Test: `operator/close_test.go` (new)

**Interfaces:**
- Consumes: `node.Conn` and `node.Stream` as `node/node.go` documents them, in particular: `Conn.Close` "ends the connection, and every stream of it on both sides", so that a `Read` of a stream fails and a `Write` that waits fails. The tests use `nodetest.New`, `(*nodetest.Net).Listen`, `(*nodetest.Net).Dial`, `nodetest.ID`, and for go-iroh `node.NewKey`, `node.Listen(ctx, node.ListenConfig{Key, Local: true, Log})` and `node.Dial(ctx, node.DialConfig{Key, Server, Addrs, Log})`, and the helpers of `operator/client_test.go` (`connect`, `wait`, `waitEnd`, `answer`, `fleetOf`, `someLines`, `echoShell`, `deadline`).
- Produces:
  - `var ErrClosed = errors.New("the client is closed")` in package `operator`. `errors.Is(err, operator.ErrClosed)` holds for: every request made on a closed client (`Store`, `Rollout`, `Rollback`, `Rollouts`, `History`, `Watch`, `Logs`, `Shell`); a request that was under way when `Close` came, unless draal had answered it with a refusal; `Read`, `Write`, `Resize` and `Wait` of a `Shell` that `Close` of its client ended. (This is an addition to the plan's contract, which asks for "an error" alone.)
  - `func (c *Client) Close() error`: when it returns, the channel of every `Watch` and of every `Logs` of the client is closed and their goroutines have ended, whatever their contexts say and whether or not anybody receives. It may be called more than once and from several goroutines while requests are under way; each call returns when all that is done; the first returns what closing the connection returned, the others nil. It waits for nothing but the client's own goroutines, which end as soon as the connection is closed: milliseconds.
  - `Wait` of a `Shell` whose exit status had come before `Close` still returns that status.
  - `Watch` and `Logs` keep their signatures. A channel that is closed now says one of three things: the context is done, the client was closed, or draal (or the zathras) has gone.
  - **Receive from the channel of a `Watch` or a `Logs`, or end its context.** What draal sends and nobody receives stays in the connection, and a connection of go-iroh takes about 768 KiB of it over all its streams together (512 KiB in one stream); after that no request of the same client is answered, a shell's output included, until somebody receives or the stream is ended. Measured: with one watch that is fed without pause and not received from, other requests are still answered; with two, none is. `node/nodetest` has no such bound over a connection. The comments of `Watch` and `Logs` say so.

- [ ] **Step 1: Write the failing tests**

Create `operator/close_test.go`. `TestCloseWaitsForItsGoroutines` is the one that holds `Close` to its promise: it holds each goroutine for a fifth of a second at its end, through a hook that `Client` gets in Step 4, and looks right after a `Close` that it calls itself. (The other tests could not tell a `Close` that waits from one that does not: by the time they look, the goroutines are gone either way.)

```go
package operator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// transports are what the tests of Close run over: what Close does to a
// stream is the transport's, so they run over the real one too.
var transports = []struct {
	name    string
	connect func(t *testing.T, handle func(req *wire.Request, s node.Stream)) *Client
}{
	{"nodetest", func(t *testing.T, handle func(req *wire.Request, s node.Stream)) *Client {
		_, c := connect(t, handle)
		return c
	}},
	{"go-iroh", connectIroh},
}

// connectIroh is connect over go-iroh on loopback, without relays.
func connectIroh(t *testing.T, handle func(req *wire.Request, s node.Stream)) *Client {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	key := func() node.Key {
		k, err := node.NewKey()
		if err != nil {
			t.Fatalf("NewKey: %v", err)
		}
		return k
	}
	ctx, cancel := context.WithCancel(context.Background())
	starting, done := context.WithTimeout(ctx, deadline)
	defer done()
	l, err := node.Listen(starting, node.ListenConfig{Key: key(), Local: true, Log: quiet})
	if err != nil {
		cancel()
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		l.Close()
	})
	go func() {
		for {
			conn, err := l.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				for {
					s, err := conn.AcceptStream(ctx)
					if err != nil {
						return
					}
					go func() {
						defer s.Close()
						var req wire.Request
						if err := wire.Read(s, &req); err != nil {
							return
						}
						handle(&req, s)
					}()
				}
			}()
		}
	}()
	conn, err := node.Dial(starting, node.DialConfig{
		Key:    key(),
		Server: l.ID(),
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c := New(conn)
	t.Cleanup(func() { c.Close() })
	return c
}

// followers returns the stacks of the goroutines of Watch and of Logs
// that there are, of whatever client: "" when there is none. It looks for
// them by name, which the count of all goroutines could not do: closing a
// connection ends goroutines of the transport too, and fewer than before
// would say nothing of these.
func followers() string {
	buf := make([]byte, 4<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var found []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "operator.(*Client).Watch.func") || strings.Contains(g, "operator.(*Client).Logs.func") {
			found = append(found, g)
		}
	}
	return strings.Join(found, "\n\n")
}

// closeClient closes c and fails the test when Close does not return.
func closeClient(t *testing.T, c *Client) {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	if err := wait(t, "Close to return", closed); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// endless answers a watch with fleets and a log with lines for as long as
// the client takes them, and anything else with one answer.
func endless(req *wire.Request, s node.Stream) {
	line := someLines()[0]
	for {
		var err error
		switch {
		case req.Watch != nil:
			err = wire.Write(s, &wire.Response{Fleet: fleetOf(1)})
		case req.Logs != nil:
			err = wire.Write(s, &wire.Response{Line: &line})
		default:
			answer(s, &wire.Response{Store: &wire.StoreInfo{Prefix: "nix/"}})
			return
		}
		if err != nil {
			return
		}
	}
}

// The terminal UI dials again after a lost connection and closes the old
// client, whose watch and log nobody receives from any more. Their
// goroutines wait to send on their channels. Close ends them, though
// their contexts are not done, and closes the channels.
func TestCloseEndsAWatchAndALogThatNobodyReceivesFrom(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.connect(t, func(req *wire.Request, s node.Stream) {
				switch {
				case req.Watch != nil:
					wire.Write(s, &wire.Response{Fleet: fleetOf(1)})
				case req.Logs != nil:
					wire.Write(s, &wire.Response{Line: &someLines()[0]})
				}
				io.Copy(io.Discard, s)
			})
			// Each returns when its first answer has come, which its
			// goroutine then waits to send.
			fleets, err := c.Watch(context.Background())
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}
			lines, err := c.Logs(context.Background(), nodetest.ID(10), true)
			if err != nil {
				t.Fatalf("Logs: %v", err)
			}

			closeClient(t, c)
			if left := followers(); left != "" {
				t.Errorf("goroutines of the client are left when Close has returned:\n%s", left)
			}
			waitEnd(t, "the channel of the watch to be closed", fleets)
			waitEnd(t, "the channel of the log to be closed", lines)
		})
	}
}

// Close returns only when the goroutines of Watch and Logs have ended:
// that is its promise, and the terminal UI dials again on it. Each
// goroutine is held here for a fifth of a second at its very end. A Close
// that did not wait would have returned long before.
func TestCloseWaitsForItsGoroutines(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		switch {
		case req.Watch != nil:
			wire.Write(s, &wire.Response{Fleet: fleetOf(1)})
		case req.Logs != nil:
			wire.Write(s, &wire.Response{Line: &someLines()[0]})
		}
		io.Copy(io.Discard, s)
	})
	var ended atomic.Int64
	c.ending = func() {
		time.Sleep(200 * time.Millisecond)
		ended.Add(1)
	}
	if _, err := c.Watch(context.Background()); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if _, err := c.Logs(context.Background(), nodetest.ID(10), true); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if n := ended.Load(); n != 2 {
		t.Errorf("when Close returned, %d of the 2 goroutines of the client had ended", n)
	}
}

// The same while somebody receives, and draal sends as fast as it can:
// the goroutines are in a read of their streams, or in a send that is
// taken, when Close comes.
func TestCloseEndsAWatchAndALogThatAreReceivedFrom(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.connect(t, endless)
			fleets, err := c.Watch(context.Background())
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}
			lines, err := c.Logs(context.Background(), nodetest.ID(10), true)
			if err != nil {
				t.Fatalf("Logs: %v", err)
			}
			var gotFleets, gotLines atomic.Int64
			ended := make(chan string, 2)
			go func() {
				for range fleets {
					gotFleets.Add(1)
				}
				ended <- "the watch"
			}()
			go func() {
				for range lines {
					gotLines.Add(1)
				}
				ended <- "the log"
			}()
			end := time.Now().Add(deadline)
			for gotFleets.Load() < 10 || gotLines.Load() < 10 {
				if time.Now().After(end) {
					t.Fatalf("timed out receiving: %d fleets and %d lines", gotFleets.Load(), gotLines.Load())
				}
				time.Sleep(time.Millisecond)
			}

			closeClient(t, c)
			if left := followers(); left != "" {
				t.Errorf("goroutines of the client are left when Close has returned:\n%s", left)
			}
			wait(t, "a channel to be closed", ended)
			wait(t, "the other channel to be closed", ended)
		})
	}
}

// A shell whose Read waits for output, whose Write waits for a shell that
// takes no input, and whose Wait waits with a context that never ends:
// Close of the client ends all three with an error.
func TestCloseEndsAShellThatWaits(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.connect(t, func(req *wire.Request, s node.Stream) {
				wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
				// Neither reads nor writes until the stream has ended.
				<-s.Done()
			})
			sh, err := c.Shell(context.Background(), nodetest.ID(10), wire.ShellRequest{})
			if err != nil {
				t.Fatalf("Shell: %v", err)
			}
			defer sh.Close()

			read := make(chan error, 1)
			go func() {
				_, err := sh.Read(make([]byte, 1))
				read <- err
			}()
			var written atomic.Int64
			wrote := make(chan error, 1)
			go func() {
				chunk := make([]byte, 64<<10)
				for {
					n, err := sh.Write(chunk)
					written.Add(int64(n))
					if err != nil {
						wrote <- err
						return
					}
				}
			}()
			waited := make(chan error, 1)
			go func() {
				_, err := sh.Wait(context.Background())
				waited <- err
			}()
			// Until the Write waits: nothing more is written for a while.
			// This is no assertion. A Write that had not come to wait when
			// Close came has to fail as well.
			stalled(&written)

			closeClient(t, c)
			if err := wait(t, "Read to return", read); !errors.Is(err, ErrClosed) {
				t.Errorf("Read = %v, want ErrClosed", err)
			}
			if err := wait(t, "Write to return", wrote); !errors.Is(err, ErrClosed) {
				t.Errorf("Write = %v, want ErrClosed", err)
			}
			if err := wait(t, "Wait to return", waited); !errors.Is(err, ErrClosed) {
				t.Errorf("Wait = %v, want ErrClosed", err)
			}
			if err := sh.Resize(80, 24); !errors.Is(err, ErrClosed) {
				t.Errorf("Resize after Close = %v, want ErrClosed", err)
			}
		})
	}
}

// stalled returns when n is more than 0 and has not changed for a tenth
// of a second, or after five seconds.
func stalled(n *atomic.Int64) {
	end := time.Now().Add(5 * time.Second)
	last, since := n.Load(), time.Now()
	for time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
		if now := n.Load(); now != last {
			last, since = now, time.Now()
		} else if last > 0 && time.Since(since) > 100*time.Millisecond {
			return
		}
	}
}

// Wait of a shell that nobody reads returns when the client is closed:
// nothing else would end it. And the exit status of a shell that had
// ended before is still to be had.
func TestCloseEndsWaitOfAShellThatNobodyReads(t *testing.T) {
	c := transports[0].connect(t, echoShell(make(chan wire.ShellRequest, 2), make(chan wire.WinSize, 1), make(chan struct{}, 2)))
	running, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	ended, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if _, err := ended.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rest, err := io.ReadAll(ended); err != nil || string(rest) != "bye\n" {
		t.Fatalf("the output = %q, %v; want bye and the end", rest, err)
	}

	waited := make(chan error, 1)
	go func() {
		_, err := running.Wait(context.Background())
		waited <- err
	}()
	closeClient(t, c)
	if err := wait(t, "Wait to return", waited); !errors.Is(err, ErrClosed) {
		t.Errorf("Wait of the shell that ran = %v, want ErrClosed", err)
	}
	if status, err := ended.Wait(context.Background()); err != nil || status != 3 {
		t.Errorf("Wait of the shell that had ended = %d, %v; want 3", status, err)
	}
}

// Close from several goroutines at once, and again, while requests are
// under way: each returns, every request ends with an error, nothing is
// left, and what is asked afterwards is refused with ErrClosed.
//
// Nothing here waits for an answer behind streams that nobody reads. A
// connection of go-iroh takes about 768 KiB that are not read, over all
// its streams together, and then no answer of any stream arrives: with a
// draal that feeds watches and logs without a pause, as endless does, two
// of them that nobody receives from stop every other request. So each
// watch and each log here gets one answer, which is a few bytes, and no
// asker begins more than a few of them.
func TestCloseTwiceAndFromSeveralGoroutines(t *testing.T) {
	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.connect(t, func(req *wire.Request, s node.Stream) {
				switch {
				case req.Watch != nil:
					wire.Write(s, &wire.Response{Fleet: fleetOf(1)})
					io.Copy(io.Discard, s)
				case req.Logs != nil:
					wire.Write(s, &wire.Response{Line: &someLines()[0]})
					io.Copy(io.Discard, s)
				default:
					answer(s, &wire.Response{Store: &wire.StoreInfo{Prefix: "nix/"}})
				}
			})
			if _, err := c.Watch(context.Background()); err != nil {
				t.Fatalf("Watch: %v", err)
			}

			// Each asker begins three watches and three logs, which nobody
			// receives from, and then asks for the store for as long as
			// it is answered.
			const askers, rounds = 4, 3
			var asking sync.WaitGroup
			var under atomic.Int64 // the askers that have begun all their watches and logs
			for range askers {
				asking.Go(func() {
					for range rounds {
						if _, err := c.Watch(context.Background()); err != nil {
							return
						}
						if _, err := c.Logs(context.Background(), nodetest.ID(10), true); err != nil {
							return
						}
					}
					under.Add(1)
					for {
						if _, err := c.Store(context.Background()); err != nil {
							return
						}
					}
				})
			}
			end := time.Now().Add(deadline)
			for under.Load() < askers {
				if time.Now().After(end) {
					t.Fatalf("timed out asking: %d of %d askers have begun their watches and logs", under.Load(), askers)
				}
				time.Sleep(time.Millisecond)
			}

			var closing sync.WaitGroup
			begin := make(chan struct{})
			for range 3 {
				closing.Go(func() {
					<-begin
					c.Close()
				})
			}
			close(begin)
			closed := make(chan struct{})
			go func() {
				closing.Wait()
				asking.Wait()
				close(closed)
			}()
			wait(t, "every Close and every request to return", closed)
			if left := followers(); left != "" {
				t.Errorf("goroutines of the client are left when Close has returned:\n%s", left)
			}

			if err := c.Close(); err != nil {
				t.Errorf("one more Close = %v, want nil", err)
			}
			if _, err := c.Store(context.Background()); !errors.Is(err, ErrClosed) {
				t.Errorf("Store on a closed client = %v, want ErrClosed", err)
			}
			if _, err := c.Watch(context.Background()); !errors.Is(err, ErrClosed) {
				t.Errorf("Watch on a closed client = %v, want ErrClosed", err)
			}
			if _, err := c.Logs(context.Background(), nodetest.ID(10), true); !errors.Is(err, ErrClosed) {
				t.Errorf("Logs on a closed client = %v, want ErrClosed", err)
			}
			if _, err := c.Shell(context.Background(), nodetest.ID(10), wire.ShellRequest{}); !errors.Is(err, ErrClosed) {
				t.Errorf("Shell on a closed client = %v, want ErrClosed", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./operator/ -run TestClose`

Expected: the build fails, because neither the error that tells a closed client nor the hook exists:

```
# github.com/fables-for-robots/great-machine/operator [github.com/fables-for-robots/great-machine/operator.test]
operator/close_test.go:195:4: c.ending undefined (type *Client has no field or method ending)
operator/close_test.go:306:63: undefined: ErrClosed
operator/close_test.go:309:65: undefined: ErrClosed
operator/close_test.go:312:65: undefined: ErrClosed
operator/close_test.go:315:49: undefined: ErrClosed
operator/close_test.go:363:63: undefined: ErrClosed
operator/close_test.go:456:64: undefined: ErrClosed
operator/close_test.go:459:64: undefined: ErrClosed
operator/close_test.go:462:86: undefined: ErrClosed
operator/close_test.go:465:102: undefined: ErrClosed
operator/close_test.go:465:102: too many errors
FAIL	github.com/fables-for-robots/great-machine/operator [build failed]
FAIL
```

- [ ] **Step 3: Name the error and the hook, and see what `Close` leaves**

In `operator/client.go`, put this above the comment `// Client talks to draal on one connection.`:

```go
// ErrClosed is the error of what is asked of a client after its Close, and
// of what its Close ended. errors.Is finds it.
var ErrClosed = errors.New("the client is closed")
```

and this field into `Client`, under `close sync.Once`:

```go
	// ending, when a test has set it, is called by a goroutine of Watch
	// or Logs when it has ended all but itself.
	ending func()
```

Run: `nix develop .#bare --command go test ./operator/ -run TestClose`

Expected: FAIL after about ten seconds. This is the output without the stacks (each "goroutines of the client are left" is followed by their stacks, in `operator.(*Client).Watch.func1` or `operator.(*Client).Logs.func1` and waiting in `select`):

```
--- FAIL: TestCloseEndsAWatchAndALogThatNobodyReceivesFrom (0.01s)
    --- FAIL: TestCloseEndsAWatchAndALogThatNobodyReceivesFrom/nodetest (0.00s)
        close_test.go:172: goroutines of the client are left when Close has returned:
        close_test.go:174: the channel of the watch to be closed: got {Services:[{Name:a Stable:0 Rollout:<nil> Zathrases:[]}]}, want the end of the channel
    --- FAIL: TestCloseEndsAWatchAndALogThatNobodyReceivesFrom/go-iroh (0.01s)
        close_test.go:172: goroutines of the client are left when Close has returned:
        close_test.go:174: the channel of the watch to be closed: got {Services:[{Name:a Stable:0 Rollout:<nil> Zathrases:[]}]}, want the end of the channel
--- FAIL: TestCloseWaitsForItsGoroutines (0.00s)
    close_test.go:209: when Close returned, 0 of the 2 goroutines of the client had ended
--- FAIL: TestCloseEndsAWatchAndALogThatAreReceivedFrom (0.01s)
    --- FAIL: TestCloseEndsAWatchAndALogThatAreReceivedFrom/nodetest (0.00s)
        close_test.go:252: goroutines of the client are left when Close has returned:
    --- FAIL: TestCloseEndsAWatchAndALogThatAreReceivedFrom/go-iroh (0.00s)
        close_test.go:252: goroutines of the client are left when Close has returned:
--- FAIL: TestCloseEndsAShellThatWaits (0.22s)
    --- FAIL: TestCloseEndsAShellThatWaits/nodetest (0.11s)
        close_test.go:307: Read = operator: the shell session failed: read frame: nodetest: the connection has ended, want ErrClosed
        close_test.go:310: Write = operator: writing to the shell: write frame: nodetest: the connection has ended, want ErrClosed
        close_test.go:313: Wait = operator: the shell session failed: read frame: nodetest: the connection has ended, want ErrClosed
        close_test.go:316: Resize after Close = operator: resizing the shell: write frame: nodetest: the connection has ended, want ErrClosed
    --- FAIL: TestCloseEndsAShellThatWaits/go-iroh (0.11s)
        close_test.go:307: Read = operator: the shell session failed: read frame: Application error 0x0 (local), want ErrClosed
        close_test.go:310: Write = operator: writing to the shell: write frame: Application error 0x0 (local), want ErrClosed
        close_test.go:313: Wait = operator: the shell session failed: read frame: Application error 0x0 (local), want ErrClosed
        close_test.go:316: Resize after Close = operator: resizing the shell: write frame: Application error 0x0 (local), want ErrClosed
--- FAIL: TestCloseEndsWaitOfAShellThatNobodyReads (10.00s)
    close_test.go:363: timed out waiting for Wait to return
--- FAIL: TestCloseTwiceAndFromSeveralGoroutines (0.01s)
    --- FAIL: TestCloseTwiceAndFromSeveralGoroutines/nodetest (0.00s)
        close_test.go:450: goroutines of the client are left when Close has returned:
        close_test.go:457: Store on a closed client = operator: store: nodetest: the connection has ended, want ErrClosed
        close_test.go:460: Watch on a closed client = operator: watch: nodetest: the connection has ended, want ErrClosed
        close_test.go:463: Logs on a closed client = operator: logs: nodetest: the connection has ended, want ErrClosed
        close_test.go:466: Shell on a closed client = operator: shell: nodetest: the connection has ended, want ErrClosed
    --- FAIL: TestCloseTwiceAndFromSeveralGoroutines/go-iroh (0.01s)
        close_test.go:450: goroutines of the client are left when Close has returned:
        close_test.go:457: Store on a closed client = operator: store: Application error 0x0 (local), want ErrClosed
        close_test.go:460: Watch on a closed client = operator: watch: Application error 0x0 (local), want ErrClosed
        close_test.go:463: Logs on a closed client = operator: logs: Application error 0x0 (local), want ErrClosed
        close_test.go:466: Shell on a closed client = operator: shell: Application error 0x0 (local), want ErrClosed
FAIL
FAIL	github.com/fables-for-robots/great-machine/operator	10.582s
FAIL
```

This is what stage 1's `Close` does: the goroutines that wait to send stay, the value they hold is still handed to whoever receives next, and a `Wait` that nobody reads for waits for ever. (`TestCloseEndsAWatchAndALogThatAreReceivedFrom` and `TestCloseTwiceAndFromSeveralGoroutines` fail too: goroutines of the tests before them are still there.)

- [ ] **Step 4: Write the implementation: the client**

Replace `operator/client.go` with the following. Against the file as it is: `ErrClosed`; the fields `done`, `mu`, `closed`, `following` and `ending` of `Client`, and `New` makes `done`; `Close`; `follow` is new; `failed` is a method of the client and says `ErrClosed` (its callers in `open` are changed with it); the goroutines of `Watch` and `Logs` are begun with `follow` and end with `c.done`, and their comments say why to receive from the channels. `open`, `call`, `unexpected`, `Store`, `Rollout`, `Rollback`, `Rollouts` and `History` are as they were, but for `c.failed` in `open`.

```go
// Package operator is the client of draal: what the commands and the
// terminal UI use to ask it. Every method of Client is one request of the
// protocol on a stream of its own. What draal refuses comes back as a
// *wire.Error, which errors.As finds.
package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// ErrClosed is the error of what is asked of a client after its Close, and
// of what its Close ended. errors.Is finds it.
var ErrClosed = errors.New("the client is closed")

// Client talks to draal on one connection. It is safe for concurrent use.
type Client struct {
	conn node.Conn

	// done is closed by Close.
	done  chan struct{}
	close sync.Once

	mu     sync.Mutex
	closed bool // Close was called: no goroutine is begun any more
	// following counts the goroutines of Watch and Logs. It is added to
	// under mu while closed is not set, so that Close waits for all.
	following sync.WaitGroup

	// ending, when a test has set it, is called by a goroutine of Watch
	// or Logs when it has ended all but itself.
	ending func()
}

// New returns a client on a connection to draal. The client owns the
// connection from then on.
func New(conn node.Conn) *Client {
	return &Client{conn: conn, done: make(chan struct{})}
}

// Close ends everything the client started, and closes the connection.
// Every request under way fails. The channel of every Watch and of every
// Logs is closed and their goroutines have ended when Close returns,
// whatever their contexts say and whether or not anybody receives from
// the channels. A Shell's Read, Write, Resize and Wait return with
// ErrClosed.
//
// Close may be called more than once and from several goroutines, while
// requests are under way: each call returns when all that is done. The
// first returns what closing the connection returned, the others nil.
func (c *Client) Close() error {
	var err error
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		// This ends a goroutine that waits for somebody to receive from
		// its channel, and closing the connection one that reads its
		// stream: Close of a node.Conn ends every stream of it.
		close(c.done)
		err = c.conn.Close()
		c.following.Wait()
	})
	return err
}

// follow runs f in a goroutine that Close waits for. It returns false,
// and runs nothing, when the client is closed.
func (c *Client) follow(f func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.following.Add(1)
	go func() {
		defer c.following.Done()
		f()
		if c.ending != nil {
			c.ending()
		}
	}()
	return true
}

// failed makes the error of a request: what draal answered as it is,
// ErrClosed when the client is closed, the error of ctx when that ended
// the request, and any other with what was asked in front.
func (c *Client) failed(ctx context.Context, what string, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	select {
	case <-c.done:
		return fmt.Errorf("operator: %s: %w", what, ErrClosed)
	default:
	}
	if ctx.Err() != nil {
		return fmt.Errorf("operator: %s: %w", what, context.Cause(ctx))
	}
	return fmt.Errorf("operator: %s: %w", what, err)
}

// open opens a stream, writes req and reads the first answer. The stream
// is closed when ctx is done; stop takes that back.
func (c *Client) open(ctx context.Context, what string, req *wire.Request) (s node.Stream, resp *wire.Response, stop func() bool, err error) {
	s, err = c.conn.OpenStream(ctx)
	if err != nil {
		return nil, nil, nil, c.failed(ctx, what, err)
	}
	stop = context.AfterFunc(ctx, func() { s.Close() })
	resp, err = wire.Call(s, req)
	if err != nil {
		stop()
		s.Close()
		return nil, nil, nil, c.failed(ctx, what, err)
	}
	return s, resp, stop, nil
}

// call does a request that has one answer.
func (c *Client) call(ctx context.Context, what string, req *wire.Request) (*wire.Response, error) {
	s, resp, stop, err := c.open(ctx, what, req)
	if err != nil {
		return nil, err
	}
	stop()
	s.Close()
	return resp, nil
}

// unexpected is the error for an answer of another kind than was asked for.
func unexpected(what string) error {
	return fmt.Errorf("operator: %s: draal answered with something else", what)
}

// Store asks for the jaccard-store server and the prefix to push to.
func (c *Client) Store(ctx context.Context) (wire.StoreInfo, error) {
	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{}})
	if err != nil {
		return wire.StoreInfo{}, err
	}
	if resp.Store == nil {
		return wire.StoreInfo{}, unexpected("store")
	}
	return *resp.Store, nil
}

// Rollout asks for a rollout, and returns it as draal recorded it.
func (c *Client) Rollout(ctx context.Context, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollout", &wire.Request{Rollout: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollout")
	}
	return *resp.Rollout, nil
}

// Rollback asks for a rollout to the stable generation of a service.
func (c *Client) Rollback(ctx context.Context, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollback", &wire.Request{Rollback: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollback")
	}
	return *resp.Rollout, nil
}

// Rollouts returns the last rollouts of a service, newest first.
func (c *Client) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollouts", &wire.Request{Rollouts: &wire.RolloutsRequest{Service: service}})
	if err != nil {
		return nil, err
	}
	if resp.Rollouts == nil {
		return nil, unexpected("rollouts")
	}
	return resp.Rollouts.Rollouts, nil
}

// History returns the samples draal has of the zathras, oldest first.
func (c *Client) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	resp, err := c.call(ctx, "history", &wire.Request{History: &wire.HistoryRequest{Zathras: zathras}})
	if err != nil {
		return nil, err
	}
	if resp.History == nil {
		return nil, unexpected("history")
	}
	return resp.History.Samples, nil
}

// Watch returns a channel of the fleet: as it is now, and again whenever
// it changed. The channel is closed when ctx is done, which ends the
// request, when the connection has ended, or when the client is closed.
// Watch returns when the first fleet has come, or with what draal
// refused. A channel that is closed while ctx is not done and the client
// is not closed says that draal has gone.
//
// Nobody has to receive from the channel for it to be closed: the
// goroutine that sends on it ends with ctx and with the client.
//
// Receive from the channel, or end ctx: what draal sends and nobody
// receives stays in the connection, which takes about 768 KiB of it over
// all its streams together, and then no request of this client is
// answered any more, until somebody receives or the stream is ended.
func (c *Client) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	s, resp, stop, err := c.open(ctx, "watch", &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil {
		return nil, err
	}
	if resp.Fleet == nil {
		stop()
		s.Close()
		return nil, unexpected("watch")
	}
	out := make(chan wire.Fleet)
	started := c.follow(func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			select {
			case out <- *resp.Fleet:
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
			for {
				var err error
				if resp, err = wire.ReadResponse(s); err != nil {
					return
				}
				if resp.Fleet != nil {
					break
				}
			}
		}
	})
	if !started {
		stop()
		s.Close()
		return nil, c.failed(ctx, "watch", ErrClosed)
	}
	return out, nil
}

// Logs returns a channel of the lines of the ring of a zathras, and with
// follow of those that follow. The channel is closed at the end of the
// stream, when ctx is done, which ends the request, or when the client is
// closed; nobody has to receive from it for that.
//
// Receive from the channel, or end ctx: as with Watch, lines that nobody
// receives stay in the connection until it takes no more, and then no
// request of this client is answered any more. A followed log of a
// service that writes much comes there soon.
//
// The protocol answers logs with lines alone, so Logs returns when the
// first line has come, when the stream has ended without one, or with
// what draal refused. With follow and an empty ring that is when the
// first line is written, or when ctx is done. A channel of a followed log
// that is closed while ctx is not done and the client is not closed says
// that draal or the zathras has gone.
func (c *Client) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	s, resp, stop, err := c.open(ctx, "logs", &wire.Request{Logs: &wire.LogsRequest{Zathras: zathras, Follow: follow}})
	if errors.Is(err, io.EOF) && ctx.Err() == nil {
		// The ring is empty.
		out := make(chan wire.LogLine)
		close(out)
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(chan wire.LogLine)
	started := c.follow(func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			if resp.Line != nil {
				select {
				case out <- *resp.Line:
				case <-ctx.Done():
					return
				case <-c.done:
					return
				}
			}
			var err error
			if resp, err = wire.ReadResponse(s); err != nil {
				return
			}
		}
	})
	if !started {
		stop()
		s.Close()
		return nil, c.failed(ctx, "logs", ErrClosed)
	}
	return out, nil
}
```

- [ ] **Step 5: Write the implementation: the shell**

Replace `operator/shell.go` with the following. Against the file as it is: `Client.Shell` hands the session the client's `done`; the field `clientClosed` and the comment of `Shell`; `Read` says `ErrClosed` for an error that came after the client was closed, and `Write` and `Resize` do through `failed`; `clientIsClosed`, `failed` and `result` are new; `Wait` ends with the client. `Close` is as it was.

```go
package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// shellChunk is the most input one message carries.
const shellChunk = 16 << 10

// Shell asks for a shell in the container of a zathras, and returns the
// session once the shell runs, or what draal or the zathras refused. The
// zathras is the one given, whatever req.Zathras says. ctx bounds the
// start alone: the session lasts until its Close.
func (c *Client) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (*Shell, error) {
	req.Zathras = zathras
	s, resp, stop, err := c.open(ctx, "shell", &wire.Request{Shell: &req})
	if err != nil {
		return nil, err
	}
	if !stop() {
		s.Close()
		return nil, fmt.Errorf("operator: shell: %w", context.Cause(ctx))
	}
	if resp.Shell == nil {
		s.Close()
		return nil, unexpected("shell")
	}
	return &Shell{s: s, ended: make(chan struct{}), clientClosed: c.done}, nil
}

// Shell is a session. It has the methods of draal.Shell. Read is for one
// goroutine; Write, Resize and Close may be called from others.
//
// Close of the client the session is of ends it: Read, Write, Resize and
// Wait fail with ErrClosed, but a Wait whose exit status had come before.
type Shell struct {
	s node.Stream

	clientClosed <-chan struct{} // closed by Close of the client

	rest []byte // output that was received and not read yet; Read's alone

	ended chan struct{} // closed when exit or err is set
	exit  int
	err   error

	wmu   sync.Mutex // one message at a time
	close sync.Once
}

// Read returns output of the shell, and io.EOF once its exit status has
// come. A session that ends without one ends with an error: ErrClosed
// when Close of the client ended it.
func (s *Shell) Read(p []byte) (int, error) {
	for len(s.rest) == 0 {
		select {
		case <-s.ended:
			if s.err != nil {
				return 0, s.err
			}
			return 0, io.EOF
		default:
		}
		resp, err := wire.ReadResponse(s.s)
		var refusal *wire.Error
		switch {
		case err != nil && s.clientIsClosed():
			s.err = fmt.Errorf("operator: the shell session ended: %w", ErrClosed)
			close(s.ended)
		case err == io.EOF:
			s.err = errors.New("operator: the shell session ended without an exit status")
			close(s.ended)
		case errors.As(err, &refusal):
			s.err = refusal
			close(s.ended)
		case err != nil:
			s.err = fmt.Errorf("operator: the shell session failed: %w", err)
			close(s.ended)
		case resp.Shell == nil:
		case resp.Shell.Exit != nil:
			s.exit = *resp.Shell.Exit
			s.rest = resp.Shell.Data
			close(s.ended)
			if len(s.rest) == 0 {
				return 0, io.EOF
			}
		default:
			s.rest = resp.Shell.Data
		}
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}

// clientIsClosed says whether Close of the client was called.
func (s *Shell) clientIsClosed() bool {
	select {
	case <-s.clientClosed:
		return true
	default:
		return false
	}
}

// failed makes the error of a Write or a Resize: ErrClosed when Close of
// the client has ended the session, and the error of the stream otherwise.
func (s *Shell) failed(what string, err error) error {
	if s.clientIsClosed() {
		return fmt.Errorf("operator: %s: %w", what, ErrClosed)
	}
	return fmt.Errorf("operator: %s: %w", what, err)
}

// Write sends p as input of the shell.
func (s *Shell) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), shellChunk)]
		if err := wire.Write(s.s, &wire.ShellInput{Data: chunk}); err != nil {
			return written, s.failed("writing to the shell", err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Resize tells the shell the new size of the terminal.
func (s *Shell) Resize(cols, rows uint16) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := wire.Write(s.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: cols, Rows: rows}}); err != nil {
		return s.failed("resizing the shell", err)
	}
	return nil
}

// Wait returns the exit status when the shell has ended, or the error the
// session ended with instead. The status comes behind the output, so Wait
// returns only once Read has reached the end. When the client is closed
// before that, Wait returns ErrClosed, also when nobody reads.
func (s *Shell) Wait(ctx context.Context) (int, error) {
	// An end that has come counts, whatever else has happened since.
	select {
	case <-s.ended:
		return s.result()
	default:
	}
	select {
	case <-s.ended:
		return s.result()
	case <-s.clientClosed:
		return 0, fmt.Errorf("operator: the shell session ended: %w", ErrClosed)
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// result is what Wait returns once ended is closed.
func (s *Shell) result() (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.exit, nil
}

// Close ends the session, and the shell if it runs. It may be called
// more than once.
func (s *Shell) Close() error {
	s.close.Do(func() { s.s.Close() })
	return nil
}
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./operator/ ./cli/ ./e2e/`

Expected (the times differ):

```
ok  	github.com/fables-for-robots/great-machine/operator	1.938s
ok  	github.com/fables-for-robots/great-machine/cli	3.171s
ok  	github.com/fables-for-robots/great-machine/e2e	18.318s
```

- [ ] **Step 7: See that the test of the wait holds it**

Take the line `c.following.Wait()` out of `Close` in `operator/client.go`, and run: `nix develop .#bare --command go test -race -count=5 ./operator/ -run TestCloseWaitsForItsGoroutines`

Expected: FAIL, five times of five:

```
--- FAIL: TestCloseWaitsForItsGoroutines (0.00s)
    close_test.go:209: when Close returned, 0 of the 2 goroutines of the client had ended
```

Put the line back, and run the same command again. Expected: `ok`.

- [ ] **Step 8: Run them fifty times, here and on Linux**

```bash
nix develop .#bare --command go test -race -count=50 ./operator/
docker run --init --rm -v "$PWD":/src:ro -w /src -e GOFLAGS=-buildvcs=false -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod golang:1.26 go test -race -count=50 ./operator/
```

Expected: `ok` from each, after about half a minute. Nothing may fail once. (Writing this plan, both ran fifty of fifty.)

Then the whole repository once, and its checks:

```bash
nix develop .#bare --command gofmt -l .
nix develop .#bare --command go vet ./...
nix develop .#bare --command go test ./...
```

Expected: `gofmt` and `go vet` print nothing; every package says `ok` or `[no test files]`.

- [ ] **Step 9: Commit**

```bash
git add operator/
git commit -m "Operator: a client that is closed ends its watches, its logs and its shells"
```
