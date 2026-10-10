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
