### Task 4: `node`: an endpoint for two protocols

draal has one key, and two endpoints with one key evict each other at the relay. So the store cannot have an endpoint of its own under draal's ID: draal's endpoint accepts a second ALPN, and gives the connections of it to whoever was named for it. This task is that, in `node`, and knows nothing of the store: a handler is a function that is given a connection.

go-iroh lets the ALPNs of an endpoint be set after the bind and before the first `Accept` (`SetALPNs`; it fails once an accept loop runs), and a connection says which it was accepted for (`Conn.ALPN`). Both were tried before this plan was written (the spec's §13).

**Files:**
- Modify: `node/listen.go`, `node/node.go`
- Test: `node/handlers_test.go`

**Interfaces:**
- Consumes: jaccard-store's `node.Bind`, as `node.Listen` uses it already; in the tests, jaccard-store's `node.Dial` to dial for another ALPN than great-machine's.
- Produces:
  - `node.ListenConfig.Handlers map[string]func(node.Conn)`: by ALPN, what is given every connection the endpoint accepts for it, in a goroutine of its own. When a handler returns, its connection is closed. `Close` of the listener closes the connections of handlers too.
  - `Listen` refuses a handler for `wire.ALPN`, one for `""`, and one that is nil.
  - `Accept` returns the connections of `wire.ALPN` alone. **A connection of another protocol is handed over by `Accept`**, which goes on to the next: it is taken only while `Accept` is called. Whoever listens with handlers has to keep accepting.
  - Without handlers nothing changes: the endpoint accepts `wire.ALPN` alone.

- [ ] **Step 1: Write the failing test: an endpoint for two protocols: a connection of another protocol goes to its handler**

Create `node/handlers_test.go`:

```go
package node_test

import (
	"context"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// listenWith binds an endpoint on loopback that accepts the protocols of
// handlers besides great-machine's own, and accepts its connections until
// the test ends: a connection of another protocol is handed over by
// Accept. The connections of great-machine's own protocol are sent on the
// channel.
func listenWith(t *testing.T, k node.Key, handlers map[string]func(node.Conn)) (node.Listener, <-chan node.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: k, Local: true, Handlers: handlers, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	accepting, stop := context.WithCancel(context.Background())
	own := make(chan node.Conn, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept(accepting)
			if err != nil {
				return
			}
			own <- c
		}
	}()
	t.Cleanup(func() {
		stop()
		l.Close()
		<-done
	})
	return l, own
}

// dialFor connects to l for another protocol than great-machine's, as a
// client of jaccard-store does. The connection is closed when the test
// ends.
func dialFor(t *testing.T, alpn string, k node.Key, l node.Listener) (*jsnode.Conn, error) {
	t.Helper()
	server, err := irohkey.ParseEndpointID(string(l.ID()))
	if err != nil {
		t.Fatalf("the ID of the listener: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	c, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    k.Iroh(),
		ALPN:   alpn,
		Server: server,
		Addrs:  []netip.AddrPort{l.LocalAddr()},
		Log:    quiet,
	})
	if err == nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, err
}

// A connection that was dialed for the protocol of a handler reaches the
// handler, with the peer it is of, and its streams carry what is written
// in both directions. One for great-machine's own protocol reaches Accept
// as before.
func TestAConnectionOfAnotherProtocolGoesToItsHandler(t *testing.T) {
	serverKey, storeClient, ownClient := newKey(t), newKey(t), newKey(t)
	handed := make(chan node.Conn, 1)
	l, own := listenWith(t, serverKey, map[string]func(node.Conn){
		jswire.ALPN: func(c node.Conn) {
			handed <- c
			// The handler has the connection for as long as it does not
			// return.
			<-c.Done()
		},
	})

	c, err := dialFor(t, jswire.ALPN, storeClient, l)
	if err != nil {
		t.Fatalf("dialing for %s: %v", jswire.ALPN, err)
	}
	var server node.Conn
	select {
	case server = <-handed:
	case got := <-own:
		t.Fatalf("Accept returned the connection of %s, which is the handler's", got.RemoteID())
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler to be given the connection")
	}
	if got := server.RemoteID(); got != storeClient.ID() {
		t.Errorf("the handler's connection is of %s, want the client, %s", got, storeClient.ID())
	}

	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	out, err := c.OpenStreamConn(ctx)
	if err != nil {
		t.Fatalf("OpenStreamConn: %v", err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("ping")); err != nil {
		t.Fatalf("writing to the stream: %v", err)
	}
	in, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream of the handler's connection: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(in, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("the handler read %q, %v; want ping", buf, err)
	}
	if _, err := in.Write([]byte("pong")); err != nil {
		t.Fatalf("the handler writing: %v", err)
	}
	in.Close()
	answer, err := io.ReadAll(out)
	if err != nil || string(answer) != "pong" {
		t.Fatalf("the client read %q, %v; want pong and the end of the stream", answer, err)
	}

	// great-machine's own protocol is Accept's.
	dial(t, ownClient, l)
	select {
	case got := <-own:
		if got.RemoteID() != ownClient.ID() {
			t.Errorf("Accept returned a connection of %s, want %s", got.RemoteID(), ownClient.ID())
		}
	case got := <-handed:
		t.Fatalf("the handler was given the connection of %s, which is Accept's", got.RemoteID())
	case <-time.After(patience):
		t.Fatal("timed out waiting for Accept to return the connection")
	}
}

// Without a handler for it, a protocol is not accepted: the dial fails in
// the handshake.
func TestWithoutAHandlerAnotherProtocolIsRefused(t *testing.T) {
	l, _ := listenWith(t, newKey(t), nil)
	if c, err := dialFor(t, jswire.ALPN, newKey(t), l); err == nil {
		t.Fatalf("a dial for %s completed with %s, which has no handler for it", jswire.ALPN, c.RemoteID())
	}
	// Nor is a protocol accepted that another has a handler for.
	l, _ = listenWith(t, newKey(t), map[string]func(node.Conn){jswire.ALPN: func(node.Conn) {}})
	if c, err := dialFor(t, "nobody/1", newKey(t), l); err == nil {
		t.Fatalf("a dial for nobody/1 completed with %s, which has no handler for it", c.RemoteID())
	}
}

// ended fails the test unless the connection of a client has ended: no
// stream can be opened on it any more.
func ended(t *testing.T, what string, c *jsnode.Conn) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s, err := c.OpenStreamConn(ctx)
		cancel()
		if err != nil {
			return
		}
		s.Close()
		if time.Now().After(deadline) {
			t.Fatalf("%s: the connection of the client is still open", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A handler that returns is done with its connection, which is closed for
// it.
func TestAHandlerThatReturnsLeavesItsConnectionClosed(t *testing.T) {
	returned := make(chan struct{})
	l, _ := listenWith(t, newKey(t), map[string]func(node.Conn){
		jswire.ALPN: func(node.Conn) { close(returned) },
	})
	c, err := dialFor(t, jswire.ALPN, newKey(t), l)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	select {
	case <-returned:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler")
	}
	ended(t, "the handler has returned", c)
}

// Close of the listener closes what a handler still has.
func TestCloseOfTheListenerClosesTheConnectionOfAHandler(t *testing.T) {
	handed := make(chan node.Conn, 1)
	left := make(chan struct{})
	l, _ := listenWith(t, newKey(t), map[string]func(node.Conn){
		jswire.ALPN: func(c node.Conn) {
			handed <- c
			<-c.Done()
			close(left)
		},
	})
	c, err := dialFor(t, jswire.ALPN, newKey(t), l)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	var server node.Conn
	select {
	case server = <-handed:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the handler")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-server.Done():
	case <-time.After(patience):
		t.Fatal("the connection of the handler is not done after Close of the listener")
	}
	select {
	case <-left:
	case <-time.After(patience):
		t.Fatal("the handler did not return after Close of the listener")
	}
	ended(t, "the listener is closed", c)
}

// What cannot be a handler is refused before anything is bound.
func TestListenRefusesHandlersThatCannotBe(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[string]func(node.Conn)
		want     string
	}{
		{"great-machine's own protocol", map[string]func(node.Conn){wire.ALPN: func(node.Conn) {}}, wire.ALPN},
		{"no protocol", map[string]func(node.Conn){"": func(node.Conn) {}}, "no ALPN"},
		{"no handler", map[string]func(node.Conn){jswire.ALPN: nil}, jswire.ALPN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := node.Listen(context.Background(), node.ListenConfig{Key: newKey(t), Local: true, Handlers: tt.handlers, Log: quiet})
			if err == nil {
				l.Close()
				t.Fatal("Listen bound an endpoint")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Listen: %v, want an error that names %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./node/`

Expected: it fails, with

```text
node/handlers_test.go:28:68: unknown field Handlers in struct literal of type node.ListenConfig
```

- [ ] **Step 3: Write the code: node.ListenConfig.Handlers**

Change `node/listen.go`:

```diff
--- a/node/listen.go
+++ b/node/listen.go
@@ -27,9 +27,20 @@ type ListenConfig struct {
 	// Log receives what binding and announcing have to say. Nil means
 	// slog.Default().
 	Log *slog.Logger
+	// Handlers are for the protocols the endpoint accepts besides
+	// great-machine's own: by its ALPN, what is given every connection
+	// that is accepted for it. A handler is called in a goroutine of its
+	// own and returns when it is done with the connection, which is then
+	// closed: it need not close it. Close of the listener closes the
+	// connections of handlers too.
+	//
+	// Such a connection is handed over by Accept, which goes on to the
+	// next: it is taken only while Accept is called.
+	Handlers map[string]func(Conn)
 }
 
-// Listen binds an endpoint that accepts connections for wire.ALPN.
+// Listen binds an endpoint that accepts connections for wire.ALPN, and
+// for the ALPN of every handler of cfg.
 //
 // Unless cfg.Local, the endpoint uses the default relays and is announced
 // through pkarr and mDNS, as jaccard-store announces its server, so that
@@ -40,6 +51,18 @@ func Listen(ctx context.Context, cfg ListenConfig) (Listener, error) {
 	if cfg.Key.secret.IsZero() {
 		return nil, errors.New("listen: no key")
 	}
+	alpns := []string{wire.ALPN}
+	for alpn, handler := range cfg.Handlers {
+		switch {
+		case alpn == "":
+			return nil, errors.New("listen: a handler for no ALPN")
+		case alpn == wire.ALPN:
+			return nil, fmt.Errorf("listen: a handler for %s, whose connections are Accept's", alpn)
+		case handler == nil:
+			return nil, fmt.Errorf("listen: no handler for %s", alpn)
+		}
+		alpns = append(alpns, alpn)
+	}
 	// The announcing of jaccard-store runs until its context is done. It
 	// gets one that ends with the listener, and with ctx only while
 	// Listen runs.
@@ -56,6 +79,15 @@ func Listen(ctx context.Context, cfg ListenConfig) (Listener, error) {
 		stop()
 		return nil, fmt.Errorf("listen: %w", err)
 	}
+	// jaccard-store binds for one ALPN. The others are added here, which
+	// go-iroh lets be done until the first Accept.
+	if len(alpns) > 1 {
+		if err := ep.SetALPNs(alpns); err != nil {
+			stop()
+			ep.Shutdown(context.Background())
+			return nil, fmt.Errorf("listen: accepting %v: %w", alpns, err)
+		}
+	}
 	if !binding() {
 		// ctx was done while the endpoint was bound, and took the
 		// announcing with it.
@@ -63,7 +95,7 @@ func Listen(ctx context.Context, cfg ListenConfig) (Listener, error) {
 		ep.Shutdown(context.Background())
 		return nil, fmt.Errorf("listen: %w", context.Cause(ctx))
 	}
-	return &listener{ep: ep, stop: stop, log: logger(cfg.Log), turn: make(chan struct{}, 1), conns: make(map[*conn]struct{})}, nil
+	return &listener{ep: ep, stop: stop, log: logger(cfg.Log), handlers: cfg.Handlers, turn: make(chan struct{}, 1), conns: make(map[*conn]struct{})}, nil
 }
 
 // listener is a Listener over an endpoint of go-iroh.
@@ -71,6 +103,8 @@ type listener struct {
 	ep   *iroh.Endpoint
 	stop context.CancelFunc
 	log  *slog.Logger
+	// handlers are given the connections of other protocols, by ALPN.
+	handlers map[string]func(Conn)
 	// turn is a lock with a slot for one: go-iroh lets one call of Accept
 	// wait at a time and fails the others at once.
 	turn chan struct{}
@@ -94,9 +128,11 @@ func (l *listener) LocalAddr() netip.AddrPort {
 	return l.ep.LocalAddr()
 }
 
-// Accept returns the next connection whose handshake has completed. A
-// handshake that fails is not reported: Accept goes on to the next, and
-// returns an error only when ctx is done or the listener is closed.
+// Accept returns the next connection for wire.ALPN whose handshake has
+// completed. A handshake that fails is not reported: Accept goes on to the
+// next, and returns an error only when ctx is done or the listener is
+// closed. A connection for the ALPN of a handler is given to the handler,
+// and Accept goes on to the next.
 //
 // Calls may be concurrent: they take turns, and one that waits for its
 // turn gives up with its ctx or when the listener is closed.
@@ -117,7 +153,23 @@ func (l *listener) Accept(ctx context.Context) (Conn, error) {
 				c.Close()
 				return nil, errors.New("accept: the listener is closed")
 			}
-			return c, nil
+			alpn := ic.ALPN()
+			if alpn == wire.ALPN {
+				return c, nil
+			}
+			handler, ok := l.handlers[alpn]
+			if !ok {
+				// The endpoint accepts no ALPN but these, so this is
+				// not reached; a connection nobody answers is closed.
+				l.log.Warn("a connection for a protocol nobody answers: closing it", "alpn", alpn, "peer", c.RemoteID())
+				c.Close()
+				continue
+			}
+			go func() {
+				defer c.Close()
+				handler(c)
+			}()
+			continue
 		}
 		if ctx.Err() != nil {
 			return nil, ctx.Err()
```

Change `node/node.go`:

```diff
--- a/node/node.go
+++ b/node/node.go
@@ -2,9 +2,16 @@
 // draal listens on, and the dial of a zathras or an operator.
 //
 // A peer is known by its endpoint ID, the public half of its key, which
-// the handshake proves. Every connection is for wire.ALPN. Both sides of a
-// connection open streams and accept them: a zathras dials draal, and
-// draal opens the streams on which it sends its commands.
+// the handshake proves. Every connection that is dialed here is for
+// wire.ALPN. Both sides of a connection open streams and accept them: a
+// zathras dials draal, and draal opens the streams on which it sends its
+// commands.
+//
+// The endpoint draal listens on can accept other protocols as well, each
+// given to a handler (ListenConfig.Handlers): that is how draal answers
+// jaccard-store's protocol under its own endpoint ID. Two endpoints with
+// one key evict each other at the relay, so a second protocol under the
+// same ID is the same endpoint or none.
 //
 // Listen is over the node package of jaccard-store, which binds the
 // endpoint and announces it. The dial is this package's own, modelled on
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./node/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add node/handlers_test.go node/listen.go node/node.go
git commit -m 'Node: an endpoint for two protocols: a connection of another protocol goes to its handler'
```

- [ ] **Step 6: The whole**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 30 packages that have tests; and nothing from `git status`: every step of this task was committed, and nothing was built into the tree.
