### Task 2: web, the sockets

**Files:**
- Create: `web/socket.go`, `web/shell.go`
- Modify: `web/web.go` (three routes in `New`)
- Modify: `go.mod` (`github.com/coder/websocket` becomes a direct requirement, at the version the module has)
- Test: `web/socket_test.go`, `web/shell_test.go`

**Interfaces:**
- Consumes:
  - Task 1: `handler` with `cfg`, `log`, `mux`, `sockets` and `answerWait`; `h.get`, `h.refuse(w, r, err)`, `h.refusalOf(r, err) *wire.Error`, `refusal`, `zathrasID(r) (wire.ID, error)`; in the tests `fake`, `serve`, `serveUnder`, `serveHandler`, `ask`, `wantRefusal`, `zathrasA`, `zathrasB`, `deadline`.
  - `draal.Controller`: `Watch(ctx) (<-chan wire.Fleet, error)`, `Logs(ctx, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)`, `Shell(ctx, zathras wire.ID, req wire.ShellRequest) (draal.Shell, error)`; `draal.Shell` (`Read`, `Write`, `Resize(cols, rows uint16) error`, `Wait(ctx) (int, error)`, `Close() error`).
  - `github.com/coder/websocket` v1.8.14: `websocket.Accept`, `(*Conn).Read`, `Write`, `Ping`, `Close`, `CloseNow`, `SetReadLimit`. Of this library: every method but `Read` may be called from several goroutines; a `Read` or a `Write` whose context ends closes the connection; `Ping` needs a `Read` that runs; `CloseRead` closes the socket with a policy violation when a data message comes, so it is not used here, where what the page sends is to be ignored; `Close` waits up to 5 seconds to write its frame and 5 more for the page's, and the library hands out no way to cut a connection, so the handler keeps the `net.Conn` the library takes over (the type `taken`).
- Produces:
  - `GET /api/watch`, `GET /api/zathras/{id}/logs[?follow=1]`, `GET /api/zathras/{id}/shell[?cols=&rows=&term=]` as WebSockets, as the plan's Interfaces have them.
  - Before the upgrade, as JSON refusals: 400 for what is no endpoint ID, for `follow` that is not `1`, `true`, `0`, `false` or empty, for `cols` or `rows` outside 1 to 1000, for a `term` that is not up to 64 of `A-Za-z0-9._+-`, and for a request that is no upgrade; 403 with the code `unauthorized` for an `Origin` whose host (with its port) is not the request's `Host`. No `Origin` is accepted. This is all that is asked of a socket's origin, under whatever name the request came (Review Focus 1: a page under any name gets its sockets when it was loaded from the host it asks, and a page of another origin gets none). It does not keep out a site that points a name of its own at draal's address (DNS rebinding): `TestPageOfANameThatWasPointedAtDraalIsAnsweredAndGetsASocket` states that such a page is answered, which is the person's decision, recorded in the spec's section 4.
  - After the upgrade: the text message `{"error": {"code": ..., "message": ...}}` is the last one, and the close after it is normal (1000). A log without `follow` and a shell that has ended (`{"exit": N}`) close with 1000 too. A server whose requests' context is cancelled closes its sockets with 1001 (going away). A socket that did not take a message within `WriteWait`, or answered no ping, is cut without a close frame (the page sees 1006). A message of the page of more than 64 KiB closes the socket with 1009.
  - The close of a socket takes at most 1 second (`closeWait`), or `WriteWait` if that is shorter: the close frame is written, the page has the rest of that time to answer it, and the connection is cut. The handler of every socket has returned within that time of the end of its request's context, whatever the page does.
  - At most `Config.MaxSockets` sockets are open. One more is refused before the upgrade with 503 `unavailable`; the count goes down on every way a socket ends.
  - A shell: what the page types is held for the shell, at most 64 KiB of it, and handed to `Shell.Write` by a goroutine of its own; what is typed while the shell is started is held the same way. When there is no room for the next message, the reader of the socket waits for room, for at most `WriteWait`, and reads nothing meanwhile: the page's sends back up in the browser, which is what lets a paste of megabytes arrive at the pace the shell takes it. Only when a message has found no room for `WriteWait` is the socket closed with 1009 and the reason `the shell does not take its input`. Nothing is ever dropped from the middle. While the reader waits, pongs are not read; the pinger does not hold that against the page. The page sends what is typed in messages of at most 16 KiB.
  - The handler of a shell waits for no `Write` and no `Read` of the `Shell`, only for its `Close`, which it calls once: when the socket ends or the handler returns, whichever is first. It ends the reader's wait for room when it returns.
  - `handler.inputWaits` (how many readers wait for room) is there for the tests.
  - An `internal` refusal on a socket has the fixed message of Task 1.
  - The defaults of a shell: 80 columns, 24 rows, `xterm-256color`. A `resize` outside 1 to 1000 is dropped.
  - **For Task 5 (cli):** `http.Server.Shutdown` does not close a WebSocket. The server needs `BaseContext: func(net.Listener) context.Context { return ctx }`, and that `ctx` cancelled when draal ends: every socket then ends with 1001 and its handler returns.
  - For the tests: `serveSockets`, `settled`, `dial`, `dialWith`, `next`, `nextText`, `wantClose`, `wantError`, `await`, `fakeShell`, `stubbornShell` (a `Shell` whose `Write` is not ended by `Close`).

How a socket is built: its handler is the one goroutine that writes messages. A second goroutine reads what the page sends (which is also how the close and the pongs are seen); it waits for nothing but, on a shell's socket, room for input, for at most `WriteWait`. A third pings when nothing was written for `PingEvery`. Nothing that goes to the page is queued: a fleet, a line or a piece of output is taken from the controller when the one before is written, so a page that reads slowly holds up what is its own and nothing else. A shell has two goroutines more, which the handler does not wait for: one hands the input on, one reads the output, a piece at a time.

- [ ] **Step 1: The library as a direct requirement**

Run: `nix develop .#bare --command sh -c 'go get github.com/coder/websocket@v1.8.14'`

`go.mod` then has `github.com/coder/websocket v1.8.14` in its first `require` block, without `// indirect`. `go.sum` does not change.

- [ ] **Step 2: Write the failing tests**

The helpers of the socket tests, and the tests of `watch`, `logs`, the origin, the slow page, the pings and the end of the server, `web/socket_test.go`:

```go
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)

// serveSockets is serve for a test of sockets: when the test ends, and
// its sockets with it, there may be no more goroutines than now, when
// the server is there and nobody has asked it anything. So every test
// that uses it shows that a socket that has ended leaves nothing behind.
func serveSockets(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	srv := serve(t, cfg)
	before := runtime.NumGoroutine()
	// Cleanups run last first: this one after the sockets of the test
	// are closed, and before the server is.
	t.Cleanup(func() { settled(t, before) })
	return srv
}

// settled waits until there are no more goroutines than before.
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

// dialWith opens a socket as a program that is no browser does, with the
// options given, and returns the socket or the status it was refused with.
// The socket is closed when the test ends.
func dialWith(t *testing.T, srv *httptest.Server, path string, opts *websocket.DialOptions) (*websocket.Conn, int) {
	t.Helper()
	if opts == nil {
		opts = &websocket.DialOptions{}
	}
	// A client of its own, so that no connection it keeps outlives the
	// test.
	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)
	opts.HTTPClient = client
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, opts)
	if err != nil {
		if resp == nil {
			t.Fatalf("dialing %s: %v", path, err)
		}
		return nil, resp.StatusCode
	}
	c.SetReadLimit(-1)
	t.Cleanup(func() { c.CloseNow() })
	return c, http.StatusSwitchingProtocols
}

// dial opens a socket, and fails the test when it is refused.
func dial(t *testing.T, srv *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	c, status := dialWith(t, srv, path, nil)
	if c == nil {
		t.Fatalf("the socket %s was refused with %d", path, status)
	}
	return c
}

// next reads one message.
func next(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("reading a message: %v", err)
	}
	return typ, data
}

// nextText reads one message, which has to be text, into v.
func nextText(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	typ, data := next(t, c)
	if typ != websocket.MessageText {
		t.Fatalf("the message %q is binary, want text", data)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("the message %q: %v", data, err)
	}
}

// wantClose fails the test unless the socket is closed next, with the
// status.
func wantClose(t *testing.T, c *websocket.Conn, status websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("a message came (%v %q), want the socket closed with %v", typ, data, status)
	}
	if got := websocket.CloseStatus(err); got != status {
		t.Fatalf("the socket ended with %v, want it closed with %v", err, status)
	}
}

// wantError fails the test unless the next message is the error message
// with the code and the socket is closed normally after it, and returns
// the message of the error.
func wantError(t *testing.T, c *websocket.Conn, code wire.Code) string {
	t.Helper()
	var got refusal
	nextText(t, c, &got)
	if got.Error == nil || got.Error.Code != code {
		t.Fatalf("the message is %+v, want an error with the code %q", got.Error, code)
	}
	wantClose(t, c, websocket.StatusNormalClosure)
	return got.Error.Message
}

// await returns what comes out of a channel next: what a test asserts of
// a handler has happened only when the handler has got to it.
func await[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %s", what)
	}
	panic("unreachable")
}

// isDone waits until ctx is done: what the controller was given for a
// socket ends with the socket.
func isDone(t *testing.T, what string, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// watcher is a fake's Watch that hands the test the context of every
// call and sends what the test puts into fleets.
type watcher struct {
	fleets chan wire.Fleet
	calls  chan context.Context
}

func newWatcher() *watcher {
	return &watcher{fleets: make(chan wire.Fleet, 1), calls: make(chan context.Context, 16)}
}

func (w *watcher) watch(ctx context.Context) (<-chan wire.Fleet, error) {
	w.calls <- ctx
	return w.fleets, nil
}

// fleetOf returns a fleet of one service of the name.
func fleetOf(name string) wire.Fleet {
	return wire.Fleet{Services: []wire.Service{{Name: name, Zathrases: []wire.Zathras{}}}}
}

// linesOf returns a fake's Logs that sends the lines and then, without
// follow, closes the channel; with follow it sends what the test puts
// into more, and closes when more is closed or ctx is done. The context
// of every call is handed to the test.
func linesOf(lines []wire.LogLine, more <-chan wire.LogLine, calls chan<- context.Context) func(context.Context, wire.ID, bool) (<-chan wire.LogLine, error) {
	return func(ctx context.Context, id wire.ID, follow bool) (<-chan wire.LogLine, error) {
		if calls != nil {
			calls <- ctx
		}
		out := make(chan wire.LogLine)
		go func() {
			defer close(out)
			send := func(l wire.LogLine) bool {
				select {
				case out <- l:
					return true
				case <-ctx.Done():
					return false
				}
			}
			for _, l := range lines {
				if !send(l) {
					return
				}
			}
			for follow {
				select {
				case l, ok := <-more:
					if !ok || !send(l) {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, nil
	}
}

func TestSocketIsOpenedByItsOwnPageAlone(t *testing.T) {
	w := newWatcher()
	w.fleets <- fleetOf("web")
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch}})
	address := strings.TrimPrefix(srv.URL, "http://")
	_, port, _ := strings.Cut(address, ":")

	tests := []struct {
		name   string
		host   string // the Host of the request; "": the server's address
		origin string // "": none, as a program that is no browser
		status int
	}{
		{"no origin", "", "", http.StatusSwitchingProtocols},
		{"no origin, under a name", "draal.example", "", http.StatusSwitchingProtocols},

		// A page under any name gets its sockets when it was loaded from
		// the host it asks: Review Focus 1.
		{"its own page by address", "", "http://" + address, http.StatusSwitchingProtocols},
		{"its own page by localhost", "localhost:" + port, "http://localhost:" + port, http.StatusSwitchingProtocols},
		{"its own page by another address", "192.168.1.5:8080", "http://192.168.1.5:8080", http.StatusSwitchingProtocols},
		{"its own page by IPv6", "[::1]:8080", "http://[::1]:8080", http.StatusSwitchingProtocols},
		{"its own page under a name", "draal.example:8080", "http://draal.example:8080", http.StatusSwitchingProtocols},
		{"its own page under a name, in capitals", "draal.example:8080", "http://DRAAL.example:8080", http.StatusSwitchingProtocols},
		{"its own page through a proxy with TLS", "draal.example", "https://draal.example", http.StatusSwitchingProtocols},
		{"its own page through a tunnel", "tunnel-3f9a.trycloudflare.example", "https://tunnel-3f9a.trycloudflare.example", http.StatusSwitchingProtocols},

		// What the rule is there for: a page of evil.example, open in the
		// operator's browser, opens a WebSocket to 127.0.0.1 or to draal's
		// name. The browser lets it, and sends the page's origin.
		{"a page of another site", "", "http://evil.example", http.StatusForbidden},
		{"a page of another site, draal under a name", "draal.example", "https://evil.example", http.StatusForbidden},
		{"a page of another site, draal by localhost", "localhost:" + port, "http://evil.example:" + port, http.StatusForbidden},
		// And a page of another program on the same machine.
		{"a page of another port", "", "http://127.0.0.1:1", http.StatusForbidden},
		{"the same host on another port", "draal.example:8080", "http://draal.example:9090", http.StatusForbidden},
		{"the same host without the port", "draal.example:8080", "http://draal.example", http.StatusForbidden},
		{"localhost is not 127.0.0.1", "", "http://localhost:" + port, http.StatusForbidden},
		{"a name is not its subdomain", "draal.example", "https://x.draal.example", http.StatusForbidden},
		{"an origin that is none", "", "null", http.StatusForbidden},
		{"an origin without a host", "", "file://", http.StatusForbidden},
	}
	for _, tt := range tests {
		opts := &websocket.DialOptions{Host: tt.host, HTTPHeader: http.Header{}}
		if tt.origin != "" {
			opts.HTTPHeader.Set("Origin", tt.origin)
		}
		for _, path := range []string{"/api/watch", "/api/zathras/" + string(zathrasA) + "/logs", "/api/zathras/" + string(zathrasA) + "/shell"} {
			c, status := dialWith(t, srv, path, opts)
			if status != tt.status {
				t.Errorf("%s: %s was answered %d, want %d", tt.name, path, status, tt.status)
			}
			if c == nil {
				continue
			}
			// The handler of a socket that was opened asks the controller.
			// That is waited for before the socket is closed: the handler
			// runs when it runs.
			if path == "/api/watch" {
				await(t, tt.name+": the controller to be asked to watch", w.calls)
			}
			c.CloseNow()
		}
	}
	// What was refused never reached the controller: a refusal is sent
	// before the handler returns, so nothing can come of it now.
	if got := len(w.calls); got != 0 {
		t.Errorf("the controller was asked to watch %d times more than sockets were opened", got)
	}
}

// THIS IS NOT A PROTECTION, AND IS NOT MEANT TO BE ONE. A page of a name
// that was pointed at draal's address IS answered, and gets its sockets,
// a shell among them.
//
// A web site can point a name of its own at draal's address while its
// page is open in the browser of someone who can reach draal's port (DNS
// rebinding). The browser then asks draal under that name, the page's
// origin is the host it asks, and the rule of the Origin, which is all
// there is, lets it in: draal answers under any host name and cannot
// tell that name from one of its own. The person who ordered
// great-machine was told this and decided that the page answers under
// any host name, on every address, loopback included; the spec's section
// 4 records it. Who changes this test changes that decision.
func TestPageOfANameThatWasPointedAtDraalIsAnsweredAndGetsASocket(t *testing.T) {
	w := newWatcher()
	w.fleets <- fleetOf("web")
	asked := make(chan wire.ShellRequest, 1)
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch, shell: shells(newFakeShell(), asked)}})
	_, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	rebound := "attacker.example:" + port

	if a := ask(t, srv, http.MethodGet, "/", rebound); a.status != http.StatusOK {
		t.Errorf("GET / as %s = %d, want the page", rebound, a.status)
	}
	opts := func() *websocket.DialOptions {
		return &websocket.DialOptions{Host: rebound, HTTPHeader: http.Header{"Origin": {"http://" + rebound}}}
	}
	c := mustDial(t, srv, "/api/watch", opts())
	await(t, "the controller to be asked to watch", w.calls)
	var f wire.Fleet
	nextText(t, c, &f)
	if len(f.Services) != 1 || f.Services[0].Name != "web" {
		t.Errorf("the page of %s read the fleet %+v, want the one of web", rebound, f)
	}
	mustDial(t, srv, shellPath, opts())
	await(t, "the controller to be asked for a shell", asked)
}

// mustDial is dialWith for a socket that is to be opened.
func mustDial(t *testing.T, srv *httptest.Server, path string, opts *websocket.DialOptions) *websocket.Conn {
	t.Helper()
	c, status := dialWith(t, srv, path, opts)
	if c == nil {
		t.Fatalf("the socket %s was refused with %d", path, status)
	}
	return c
}

// At most MaxSockets sockets are open at a time. One more is refused
// before the upgrade, and is accepted when one of them has ended,
// however it ended.
func TestNoMoreSocketsThanMaxSockets(t *testing.T) {
	w := newWatcher()
	more := make(chan wire.LogLine)
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch, logs: linesOf(nil, more, nil)}, MaxSockets: 3})
	logs := "/api/zathras/" + string(zathrasA) + "/logs"
	a, b, c := dial(t, srv, "/api/watch"), dial(t, srv, logs+"?follow=1"), dial(t, srv, "/api/watch")
	refused := func() {
		t.Helper()
		for _, path := range []string{"/api/watch", logs, shellPath} {
			req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			client := &http.Client{Timeout: deadline}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			client.CloseIdleConnections()
			wantRefusal(t, answer{status: resp.StatusCode, header: resp.Header, body: string(body)}, http.StatusServiceUnavailable, wire.CodeUnavailable)
		}
	}
	refused()
	// One ends in each way a socket can end, and one more is accepted
	// each time, and no more than one. accepted asks until it is.
	accepted := func(how string) *websocket.Conn {
		t.Helper()
		end := time.Now().Add(deadline)
		for {
			if c, _ := dialWith(t, srv, "/api/watch", nil); c != nil {
				return c
			}
			if time.Now().After(end) {
				t.Fatalf("no socket was accepted after one had ended (%s)", how)
			}
			time.Sleep(time.Millisecond)
		}
	}
	a.Close(websocket.StatusNormalClosure, "")
	a = accepted("the page closed it")
	refused()
	c.CloseNow()
	c = accepted("the page went")
	refused()
	close(more) // the zathras goes: the server ends the socket
	wantError(t, b, wire.CodeUnavailable)
	b = accepted("the server ended it")
	refused()
	// All of them go, and as many are accepted again.
	a.CloseNow()
	b.CloseNow()
	c.CloseNow()
	for range 3 {
		accepted("all of them went")
	}
	refused()
}

// A socket lives longer than an answer may take to be written: the
// deadline of an answer is taken off a connection that becomes a socket.
func TestSocketOutlivesTheTimeOfAnAnswer(t *testing.T) {
	w := newWatcher()
	inner := New(Config{Controller: &fake{watch: w.watch}, Dist: testDist(), Log: quiet})
	inner.(*handler).answerWait = 50 * time.Millisecond
	srv := serveHandler(t, context.Background(), inner)
	c := dial(t, srv, "/api/watch")
	opened := time.Now()
	for time.Since(opened) < 4*inner.(*handler).answerWait {
		time.Sleep(time.Millisecond)
	}
	for _, name := range []string{"late", "later"} {
		w.fleets <- fleetOf(name)
		var f wire.Fleet
		nextText(t, c, &f)
		if f.Services[0].Name != name {
			t.Fatalf("the fleet is %+v, want the one of %s", f, name)
		}
	}
}

func TestSocketPathAskedWithoutAnUpgradeIsRefusedAsJSON(t *testing.T) {
	srv := serveSockets(t, Config{})
	for _, path := range []string{"/api/watch", "/api/zathras/" + string(zathrasA) + "/logs", "/api/zathras/" + string(zathrasA) + "/shell"} {
		wantRefusal(t, ask(t, srv, http.MethodGet, path, ""), http.StatusBadRequest, wire.CodeBadRequest)
		wantRefusal(t, ask(t, srv, http.MethodPost, path, ""), http.StatusMethodNotAllowed, wire.CodeBadRequest)
	}
}

func TestWatch(t *testing.T) {
	w := newWatcher()
	w.fleets <- fleetOf("first")
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch}})
	c := dial(t, srv, "/api/watch")
	ctx := <-w.calls

	// The first fleet at once, as text, in the JSON of wire.
	typ, data := next(t, c)
	if want := `{"services":[{"name":"first","stable":0,"zathrases":[]}]}`; typ != websocket.MessageText || string(data) != want {
		t.Fatalf("the first message is %v %s, want the text %s", typ, data, want)
	}

	// What the page sends is ignored: the fleets go on.
	bg := context.Background()
	if err := c.Write(bg, websocket.MessageText, []byte(`{"anything":1}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := c.Write(bg, websocket.MessageBinary, []byte{0, 1, 2}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, name := range []string{"second", "third"} {
		w.fleets <- fleetOf(name)
		var f wire.Fleet
		nextText(t, c, &f)
		if len(f.Services) != 1 || f.Services[0].Name != name {
			t.Fatalf("the fleet is %+v, want the one of %s", f, name)
		}
	}

	// The page closing the socket ends the controller's watch.
	if ctx.Err() != nil {
		t.Fatal("the watch has ended while the socket is open")
	}
	c.Close(websocket.StatusNormalClosure, "")
	isDone(t, "the watch to end with the socket", ctx)
}

func TestWatchThatDraalRefusesOrEnds(t *testing.T) {
	// Refused: the page is told after the upgrade, as it cannot read the
	// answer of an upgrade that failed.
	srv := serveSockets(t, Config{Controller: &fake{watch: func(context.Context) (<-chan wire.Fleet, error) {
		return nil, wire.Errorf(wire.CodeInternal, "reading the fleet: the database is closed")
	}}})
	// An internal refusal says nothing of what went wrong, on a socket as
	// anywhere.
	if msg := wantError(t, dial(t, srv, "/api/watch"), wire.CodeInternal); msg != "draal failed to answer; its log says why" {
		t.Errorf("the message is %q, want nothing of the controller's text", msg)
	}

	// Ended: a channel that is closed while the page is there.
	fleets := make(chan wire.Fleet, 1)
	fleets <- fleetOf("web")
	srv = serveSockets(t, Config{Controller: &fake{watch: func(context.Context) (<-chan wire.Fleet, error) { return fleets, nil }}})
	c := dial(t, srv, "/api/watch")
	next(t, c)
	close(fleets)
	wantError(t, c, wire.CodeUnavailable)
}

func TestLogsWithoutFollowAreTheRingAndANormalClose(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 5, time.UTC)
	ring := []wire.LogLine{
		{At: at, Stream: "zathras", Text: "started generation 1: /nix/store/x-web/bin/web (pid 7)"},
		{At: at, Stream: "stdout", Text: "hello\twith a tab and \x1b[32mcolour\x1b[0m"},
		{At: at, Stream: "zathras", Lost: 12},
		{At: at, Stream: "stderr", Text: "<b>not markup</b>"},
	}
	var asked []string
	logs := linesOf(ring, nil, nil)
	srv := serveSockets(t, Config{Controller: &fake{logs: func(ctx context.Context, id wire.ID, follow bool) (<-chan wire.LogLine, error) {
		asked = append(asked, fmt.Sprintf("%s %v", id, follow))
		return logs(ctx, id, follow)
	}}})
	for _, query := range []string{"", "?follow=0", "?follow=false", "?follow="} {
		c := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs"+query)
		typ, data := next(t, c)
		if want := `{"at":"2026-10-09T12:00:00.000000005Z","stream":"zathras","text":"started generation 1: /nix/store/x-web/bin/web (pid 7)"}`; typ != websocket.MessageText || string(data) != want {
			t.Fatalf("%q: the first message is %v %s, want the text %s", query, typ, data, want)
		}
		for i, want := range ring[1:] {
			var got wire.LogLine
			nextText(t, c, &got)
			if got != want {
				t.Errorf("%q: line %d is %+v, want %+v", query, i+1, got, want)
			}
		}
		wantClose(t, c, websocket.StatusNormalClosure)
	}
	for _, a := range asked {
		if a != string(zathrasA)+" false" {
			t.Errorf("the controller was asked for %q, want %s without follow", a, zathrasA)
		}
	}
}

func TestLogsWithFollowGoOnUntilThePageCloses(t *testing.T) {
	more := make(chan wire.LogLine)
	calls := make(chan context.Context, 1)
	ring := []wire.LogLine{{Stream: "stdout", Text: "in the ring"}}
	srv := serveSockets(t, Config{Controller: &fake{logs: linesOf(ring, more, calls)}})
	for _, query := range []string{"?follow=1", "?follow=true"} {
		c := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs"+query)
		ctx := <-calls
		var got wire.LogLine
		nextText(t, c, &got)
		for _, text := range []string{"later", "later still"} {
			more <- wire.LogLine{Stream: "stderr", Text: text}
			nextText(t, c, &got)
			if got.Text != text || got.Stream != "stderr" {
				t.Fatalf("%s: the line is %+v, want %q", query, got, text)
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("%s: the log has ended while the socket is open", query)
		}
		c.Close(websocket.StatusNormalClosure, "")
		isDone(t, "the log to end with the socket", ctx)
	}
}

// Review Focus 2: a zathras that goes while its log is open. The socket
// ends with a reason and not in silence.
func TestLogsOfAZathrasThatGoesEndWithTheReason(t *testing.T) {
	more := make(chan wire.LogLine)
	srv := serveSockets(t, Config{Controller: &fake{logs: linesOf(nil, more, nil)}})
	c := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs?follow=1")
	more <- wire.LogLine{Stream: "stdout", Text: "the last line"}
	var got wire.LogLine
	nextText(t, c, &got)

	// The zathras goes: draal closes the channel of its lines.
	close(more)
	msg := wantError(t, c, wire.CodeUnavailable)
	if !strings.Contains(msg, string(zathrasA)) || !strings.Contains(msg, "gone") {
		t.Errorf("the message is %q, want it to name the zathras and say that it is gone", msg)
	}
}

func TestLogsThatDraalRefuses(t *testing.T) {
	srv := serveSockets(t, Config{Controller: &fake{logs: func(_ context.Context, id wire.ID, _ bool) (<-chan wire.LogLine, error) {
		return nil, wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", id)
	}}})
	// A zathras that is not connected: told after the upgrade.
	for _, query := range []string{"", "?follow=1"} {
		c := dial(t, srv, "/api/zathras/"+string(zathrasB)+"/logs"+query)
		if msg := wantError(t, c, wire.CodeUnavailable); msg != "zathras "+string(zathrasB)+" is not connected" {
			t.Errorf("the message is %q, want the one of the controller", msg)
		}
	}
	// What is wrong with the request itself: refused before the upgrade.
	for _, path := range []string{"/api/zathras/web-0/logs", "/api/zathras/" + strings.ToUpper(string(zathrasB)) + "/logs", "/api/zathras/" + string(zathrasA) + "/logs?follow=yes"} {
		if c, status := dialWith(t, srv, path, nil); status != http.StatusBadRequest {
			t.Errorf("%s was answered %d, want 400", path, status)
			if c != nil {
				c.CloseNow()
			}
		}
	}
}

func TestLogsAnErrorThatIsNoRefusalIsInternalWithoutItsText(t *testing.T) {
	srv := serveSockets(t, Config{Controller: &fake{logs: func(context.Context, wire.ID, bool) (<-chan wire.LogLine, error) {
		return nil, errors.New("open /var/lib/draal/secret: permission denied")
	}}})
	c := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs")
	if msg := wantError(t, c, wire.CodeInternal); strings.Contains(msg, "secret") {
		t.Errorf("the message is %q, want nothing of the error's text in it", msg)
	}
}

// Review Focus 5, the flood: ten thousand lines arrive, every one and in
// order.
func TestLogsFloodArrivesInOrder(t *testing.T) {
	const count = 10000
	ring := make([]wire.LogLine, count)
	for i := range ring {
		ring[i] = wire.LogLine{Stream: "stdout", Text: fmt.Sprintf("line %d %s", i, strings.Repeat("x", i%200))}
	}
	srv := serveSockets(t, Config{Controller: &fake{logs: linesOf(ring, nil, nil)}})
	c := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs")
	for i := range ring {
		var got wire.LogLine
		nextText(t, c, &got)
		if got.Text != ring[i].Text {
			t.Fatalf("line %d is %q, want %q", i, got.Text, ring[i].Text)
		}
	}
	wantClose(t, c, websocket.StatusNormalClosure)
}

// Review Focus 5, the slow browser: a page that does not read loses its
// socket within WriteWait of the message it does not take, and holds up
// nobody else meanwhile.
func TestSocketOfAPageThatDoesNotReadIsClosedAndHoldsUpNoOther(t *testing.T) {
	calls := make(chan context.Context, 2)
	line := wire.LogLine{Stream: "stdout", Text: strings.Repeat("x", 16<<10)}
	// A log without an end: as fast as it is taken.
	endless := func(ctx context.Context, _ wire.ID, _ bool) (<-chan wire.LogLine, error) {
		calls <- ctx
		out := make(chan wire.LogLine)
		go func() {
			defer close(out)
			for {
				select {
				case out <- line:
				case <-ctx.Done():
					return
				}
			}
		}()
		return out, nil
	}
	srv := serveSockets(t, Config{Controller: &fake{logs: endless}, WriteWait: 200 * time.Millisecond})

	// The first page never reads. The lines fill what the connection
	// holds, and then one of them is not taken.
	stuck := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs?follow=1")
	stuckCtx := <-calls

	// The second page reads, and is served while the first is stuck.
	reader := dial(t, srv, "/api/zathras/"+string(zathrasB)+"/logs?follow=1")
	readerCtx := <-calls
	for i := range 500 {
		var got wire.LogLine
		nextText(t, reader, &got)
		if len(got.Text) != len(line.Text) {
			t.Fatalf("line %d of the page that reads has %d bytes, want %d", i, len(got.Text), len(line.Text))
		}
	}

	isDone(t, "the socket of the page that does not read to be closed", stuckCtx)
	if readerCtx.Err() != nil {
		t.Error("the socket of the page that reads was closed with the other")
	}
	// What the stuck page finds when it reads at last: what was on the
	// way, and then a connection that was cut, not a close.
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	for {
		_, _, err := stuck.Read(ctx)
		if err == nil {
			continue
		}
		if status := websocket.CloseStatus(err); status != -1 {
			t.Errorf("the socket of the page that did not read was closed with %v, want it cut", status)
		}
		break
	}
	// And the other is served still.
	var got wire.LogLine
	nextText(t, reader, &got)
}

func TestIdleSocketIsPingedAndOneThatDoesNotAnswerIsClosed(t *testing.T) {
	w := newWatcher()
	// WriteWait is what a pong has: long enough for a machine that is
	// busy, and what the second half of the test takes.
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch}, PingEvery: 20 * time.Millisecond, WriteWait: time.Second})

	// A page that reads answers the pings, and keeps its socket.
	var pings atomic.Int32
	c, _ := dialWith(t, srv, "/api/watch", &websocket.DialOptions{
		OnPingReceived: func(context.Context, []byte) bool { pings.Add(1); return true },
	})
	ctx := <-w.calls
	reading, stop := context.WithCancel(context.Background())
	defer stop()
	go c.Read(reading) // no message comes: this reads the pings alone
	end := time.Now().Add(deadline)
	for pings.Load() < 5 {
		if time.Now().After(end) {
			t.Fatalf("%d pings came, want 5", pings.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("the socket of a page that answers its pings was closed")
	}
	stop()
	isDone(t, "the watch to end with the socket", ctx)

	// A page that reads nothing answers no ping, and loses its socket.
	dial(t, srv, "/api/watch")
	isDone(t, "the socket of a page that answers no ping to be closed", <-w.calls)
}

func TestMessageOfAPageThatIsTooLongEndsTheSocket(t *testing.T) {
	w := newWatcher()
	srv := serveSockets(t, Config{Controller: &fake{watch: w.watch}})
	c := dial(t, srv, "/api/watch")
	ctx := <-w.calls
	// As long as a message may be.
	if err := c.Write(context.Background(), websocket.MessageBinary, make([]byte, readLimit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.fleets <- fleetOf("web")
	next(t, c)
	// One byte more.
	if err := c.Write(context.Background(), websocket.MessageBinary, make([]byte, readLimit+1)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wantClose(t, c, websocket.StatusMessageTooBig)
	isDone(t, "the watch to end with the socket", ctx)
}

func TestServerThatEndsClosesItsSocketsAsGoingAway(t *testing.T) {
	base, shutDown := context.WithCancel(context.Background())
	defer shutDown()
	w := newWatcher()
	w.fleets <- fleetOf("web")
	more := make(chan wire.LogLine)
	sh := newFakeShell()
	srv := serveUnder(t, base, Config{Controller: &fake{
		watch: w.watch,
		logs:  linesOf(nil, more, nil),
		shell: func(context.Context, wire.ID, wire.ShellRequest) (draal.Shell, error) { return sh, nil },
	}})
	before := runtime.NumGoroutine()
	watch := dial(t, srv, "/api/watch")
	next(t, watch)
	logs := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/logs?follow=1")
	shell := dial(t, srv, "/api/zathras/"+string(zathrasA)+"/shell")
	sh.out <- []byte("$ ")
	next(t, shell)

	// What cli does when draal ends: the context of the server's
	// requests is cancelled. http.Server.Shutdown alone leaves a socket
	// open.
	shutDown()
	for _, c := range []*websocket.Conn{watch, logs, shell} {
		wantClose(t, c, websocket.StatusGoingAway)
	}
	isDone(t, "the watch to end with the server", <-w.calls)
	select {
	case <-sh.closed:
	case <-time.After(deadline):
		t.Fatal("the shell was not closed with the server")
	}
	settled(t, before)
}

// The close of a socket is bounded. When the server ends, the handlers of
// sockets whose pages read nothing, and so answer no close, have all
// returned within two seconds: nobody waits for a page.
func TestSocketsOfPagesThatDoNotAnswerEndWithinTwoSecondsOfTheServer(t *testing.T) {
	base, shutDown := context.WithCancel(context.Background())
	defer shutDown()
	w := newWatcher()
	sh := newFakeShell()
	// The close has WriteWait here, a quarter of a second, and not the
	// second it has otherwise: the rest of the two seconds is for a
	// machine that is busy with other tests.
	asked := make(chan wire.ShellRequest, 1)
	calls := make(chan context.Context, 1)
	inner := New(Config{Dist: testDist(), Log: quiet, WriteWait: 250 * time.Millisecond, Controller: &fake{
		watch: w.watch,
		logs:  linesOf(nil, make(chan wire.LogLine), calls),
		shell: shells(sh, asked),
	}})
	var handlers sync.WaitGroup
	srv := serveHandler(t, base, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		inner.ServeHTTP(rw, r)
	}))
	before := runtime.NumGoroutine()
	// None of these is ever read: the close frame is written to each and
	// none answers it.
	for _, path := range []string{"/api/watch", "/api/watch", "/api/watch", "/api/zathras/" + string(zathrasA) + "/logs?follow=1", shellPath} {
		dial(t, srv, path)
	}
	// Every handler is there and has asked the controller.
	for range 3 {
		await(t, "the controller to be asked to watch", w.calls)
	}
	await(t, "the controller to be asked for the log", calls)
	await(t, "the controller to be asked for a shell", asked)

	shutDown()
	ended := make(chan struct{})
	go func() {
		handlers.Wait()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		buf := make([]byte, 1<<20)
		t.Fatalf("two seconds after the server ended, handlers of sockets have not returned:\n%s", buf[:runtime.Stack(buf, true)])
	}
	sh.isClosed(t)
	settled(t, before)
}

// The page that is there when the socket is closed sees the code and
// the reason, though nobody waits for its answer.
func TestPageThatIsThereSeesWhyItsSocketWasClosed(t *testing.T) {
	base, shutDown := context.WithCancel(context.Background())
	defer shutDown()
	w := newWatcher()
	srv := serveUnder(t, base, Config{Controller: &fake{watch: w.watch}})
	c := dial(t, srv, "/api/watch")
	<-w.calls
	shutDown()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	_, _, err := c.Read(ctx)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusGoingAway || closed.Reason != "draal is shutting down" {
		t.Errorf("the socket ended with %v, want it closed as going away with the reason", err)
	}
}
```

The shell, `web/shell_test.go`:

```go
package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)

// fakeShell is a draal.Shell of a test: it prints what the test puts
// into out, and hands the test what was typed and the sizes it was told.
type fakeShell struct {
	out    chan []byte       // what the shell prints; closed when the shell has ended
	typed  chan []byte       // what was typed, as it was written
	sizes  chan wire.WinSize // the sizes it was told
	status int               // its exit status
	failed error             // what Read ends with in place of io.EOF, if set before out is closed
	closed chan struct{}     // closed by Close
	// writing gets a value when Write is called, while it has room.
	writing chan struct{}

	once sync.Once
	rest []byte // of Read alone
}

func newFakeShell() *fakeShell {
	return &fakeShell{
		out:    make(chan []byte),
		typed:  make(chan []byte),
		sizes:  make(chan wire.WinSize, 16),
		closed: make(chan struct{}),

		writing: make(chan struct{}, 1),
	}
}

func (f *fakeShell) Read(p []byte) (int, error) {
	if len(f.rest) == 0 {
		select {
		case data, ok := <-f.out:
			if !ok {
				if f.failed != nil {
					return 0, f.failed
				}
				return 0, io.EOF
			}
			f.rest = data
		case <-f.closed:
			return 0, io.EOF
		}
	}
	n := copy(p, f.rest)
	f.rest = f.rest[n:]
	return n, nil
}

func (f *fakeShell) Write(p []byte) (int, error) {
	select {
	case f.writing <- struct{}{}:
	default:
	}
	select {
	case f.typed <- bytes.Clone(p):
		return len(p), nil
	case <-f.closed:
		return 0, errors.New("the shell is closed")
	}
}

func (f *fakeShell) Resize(cols, rows uint16) error {
	f.sizes <- wire.WinSize{Cols: cols, Rows: rows}
	return nil
}

func (f *fakeShell) Wait(ctx context.Context) (int, error) {
	return f.status, nil
}

func (f *fakeShell) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// isClosed waits until the shell was closed.
func (f *fakeShell) isClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be closed")
	}
}

// isWriting waits until Write was called.
func (f *fakeShell) isWriting(t *testing.T) {
	t.Helper()
	select {
	case <-f.writing:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be written to")
	}
}

// wasTyped returns what the shell is written next.
func (f *fakeShell) wasTyped(t *testing.T) string {
	t.Helper()
	select {
	case data := <-f.typed:
		return string(data)
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be written to")
	}
	return ""
}

// wasSized returns the size the shell is told next.
func (f *fakeShell) wasSized(t *testing.T) wire.WinSize {
	t.Helper()
	select {
	case size := <-f.sizes:
		return size
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be told a size")
	}
	return wire.WinSize{}
}

// shells is a fake's Shell that hands out sh and hands the test every
// request.
func shells(sh *fakeShell, asked chan<- wire.ShellRequest) func(context.Context, wire.ID, wire.ShellRequest) (draal.Shell, error) {
	return func(_ context.Context, id wire.ID, req wire.ShellRequest) (draal.Shell, error) {
		if req.Zathras != id {
			return nil, wire.Errorf(wire.CodeInternal, "the request names %q, and the zathras is %s", req.Zathras, id)
		}
		if asked != nil {
			asked <- req
		}
		return sh, nil
	}
}

const shellPath = "/api/zathras/" + string(zathrasA) + "/shell"

func TestShellRoundTrip(t *testing.T) {
	sh := newFakeShell()
	sh.status = 3
	asked := make(chan wire.ShellRequest, 1)
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, asked)}})
	c := dial(t, srv, shellPath+"?cols=120&rows=40&term=xterm-kitty")
	if req := <-asked; req != (wire.ShellRequest{Zathras: zathrasA, Cols: 120, Rows: 40, Term: "xterm-kitty"}) {
		t.Fatalf("the shell was asked for with %+v", req)
	}
	bg := context.Background()

	// What the shell prints comes as binary messages, bytes as they are.
	sh.out <- []byte("$ \x1b[1mprompt\x1b[0m \xff\x00")
	if typ, data := next(t, c); typ != websocket.MessageBinary || string(data) != "$ \x1b[1mprompt\x1b[0m \xff\x00" {
		t.Fatalf("the output is %v %q", typ, data)
	}
	// What is typed goes to the shell.
	if err := c.Write(bg, websocket.MessageBinary, []byte("echo hi\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasTyped(t); got != "echo hi\r" {
		t.Fatalf("the shell was written %q", got)
	}
	// A change of size.
	if err := c.Write(bg, websocket.MessageText, []byte(`{"resize": {"cols": 132, "rows": 50}}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 132, Rows: 50}) {
		t.Fatalf("the shell was told the size %+v", got)
	}
	// More output than one message carries arrives whole and in order.
	big := bytes.Repeat([]byte("0123456789abcdef"), 3*shellChunk/16)
	go func() { sh.out <- big }()
	var got []byte
	for len(got) < len(big) {
		typ, data := next(t, c)
		if typ != websocket.MessageBinary || len(data) > shellChunk {
			t.Fatalf("a message of %v with %d bytes, want binary of at most %d", typ, len(data), shellChunk)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("the output did not arrive as it was printed")
	}

	// The shell ends: its last output, its exit status, a normal close.
	go func() {
		sh.out <- []byte("bye\r\n")
		close(sh.out)
	}()
	if _, data := next(t, c); string(data) != "bye\r\n" {
		t.Fatalf("the last output is %q", data)
	}
	if typ, data := next(t, c); typ != websocket.MessageText || string(data) != `{"exit":3}` {
		t.Fatalf("the message is %v %s, want the text {\"exit\":3}", typ, data)
	}
	wantClose(t, c, websocket.StatusNormalClosure)
	sh.isClosed(t)
}

func TestShellHasDefaultsForItsTerminal(t *testing.T) {
	sh := newFakeShell()
	asked := make(chan wire.ShellRequest, 1)
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, asked)}})
	for _, query := range []string{"", "?cols=&rows=&term="} {
		c := dial(t, srv, shellPath+query)
		if req := <-asked; req != (wire.ShellRequest{Zathras: zathrasA, Cols: 80, Rows: 24, Term: "xterm-256color"}) {
			t.Errorf("%q: the shell was asked for with %+v", query, req)
		}
		c.CloseNow()
	}
}

func TestShellWithATerminalThatCannotBeIsRefusedBeforeTheUpgrade(t *testing.T) {
	srv := serveSockets(t, Config{Controller: &fake{shell: func(context.Context, wire.ID, wire.ShellRequest) (draal.Shell, error) {
		t.Error("the controller was asked for a shell")
		return nil, unset
	}}})
	for _, query := range []string{
		"?cols=0", "?rows=0", "?cols=-1", "?cols=1001", "?rows=1001", "?cols=80x", "?rows=1e2", "?cols=65536",
		"?term=xterm%20256", "?term=a%2Fb", "?term=%1b%5b", "?term=" + strings.Repeat("x", 65),
	} {
		if c, status := dialWith(t, srv, shellPath+query, nil); status != http.StatusBadRequest {
			t.Errorf("%s was answered %d, want 400", query, status)
			if c != nil {
				c.CloseNow()
			}
		}
	}
	if c, status := dialWith(t, srv, "/api/zathras/web-0/shell", nil); status != http.StatusBadRequest {
		t.Errorf("a shell of what is no endpoint ID was answered %d, want 400", status)
		if c != nil {
			c.CloseNow()
		}
	}
}

func TestShellThatDraalRefuses(t *testing.T) {
	srv := serveSockets(t, Config{Controller: &fake{shell: func(_ context.Context, id wire.ID, _ wire.ShellRequest) (draal.Shell, error) {
		return nil, wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", id)
	}}})
	if msg := wantError(t, dial(t, srv, shellPath), wire.CodeUnavailable); msg != "zathras "+string(zathrasA)+" is not connected" {
		t.Errorf("the message is %q, want the one of the controller", msg)
	}
}

func TestShellIsClosedWhenThePageClosesTheSocket(t *testing.T) {
	for _, how := range []string{"with a close", "by going"} {
		sh := newFakeShell()
		srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}})
		c := dial(t, srv, shellPath)
		sh.out <- []byte("$ ")
		next(t, c)
		if how == "with a close" {
			c.Close(websocket.StatusNormalClosure, "")
		} else {
			c.CloseNow()
		}
		sh.isClosed(t)
	}
}

// Review Focus 2: a zathras that goes while its shell is open. The
// session ends with the reason, after what the shell had printed.
func TestShellOfAZathrasThatGoesEndsWithTheReason(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}})
	c := dial(t, srv, shellPath)
	sh.out <- []byte("working")
	next(t, c)

	// What draal's session returns from Read when the stream to the
	// zathras fails.
	sh.failed = wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: closed", zathrasA)
	close(sh.out)
	if msg := wantError(t, c, wire.CodeUnavailable); !strings.Contains(msg, string(zathrasA)) {
		t.Errorf("the message is %q, want it to name the zathras", msg)
	}
	sh.isClosed(t)
}

func TestShellDropsATextMessageThatIsNoChangeOfSize(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}})
	c := dial(t, srv, shellPath)
	bg := context.Background()
	for _, message := range []string{
		`not json`,
		`{}`,
		`{"resize": null}`,
		`{"resize": {"cols": 0, "rows": 24}}`,
		`{"resize": {"cols": 80, "rows": 0}}`,
		`{"resize": {"cols": 1001, "rows": 24}}`,
		`{"resize": {"cols": 80, "rows": 70000}}`,
		`{"resize": {"cols": "80", "rows": 24}}`,
		`{"exit": 0}`,
		`ls -l`,
	} {
		if err := c.Write(bg, websocket.MessageText, []byte(message)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// Behind them a message that is one: it is the first the shell hears
	// of, and nothing was answered.
	if err := c.Write(bg, websocket.MessageText, []byte(`{"resize":{"cols":1000,"rows":1}}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 1000, Rows: 1}) {
		t.Fatalf("the shell was told the size %+v, want only the one that is valid", got)
	}
	select {
	case data := <-sh.typed:
		t.Fatalf("the shell was written %q", data)
	default:
	}
	sh.out <- []byte("still there")
	if typ, data := next(t, c); typ != websocket.MessageBinary || string(data) != "still there" {
		t.Fatalf("the next message is %v %q, want the output of the shell", typ, data)
	}
}

func TestShellGetsWhatWasSentWhileItWasStarted(t *testing.T) {
	sh := newFakeShell()
	begun, release := make(chan struct{}), make(chan struct{})
	srv := serveSockets(t, Config{Controller: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (draal.Shell, error) {
		close(begun)
		select {
		case <-release:
			return sh, nil
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}}})
	c := dial(t, srv, shellPath)
	<-begun
	bg := context.Background()
	for _, m := range []struct {
		typ  websocket.MessageType
		data string
	}{
		{websocket.MessageText, `{"resize":{"cols":100,"rows":30}}`},
		{websocket.MessageBinary, "ls"},
		{websocket.MessageText, `{"resize":{"cols":101,"rows":31}}`},
		{websocket.MessageBinary, " -l\r"},
	} {
		if err := c.Write(bg, m.typ, []byte(m.data)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// A message behind them that is answered shows that they were read:
	// the socket is read while the shell is started.
	pinged, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	reading, stop := context.WithCancel(bg)
	go c.Read(reading)
	if err := c.Ping(pinged); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	_ = stop // the read goes on: it is the one that will see the output

	close(release)
	// The last size, and all that was typed in its order.
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 101, Rows: 31}) {
		t.Errorf("the shell was told the size %+v, want the last one sent", got)
	}
	if got := sh.wasTyped(t); got != "ls -l\r" {
		t.Errorf("the shell was written %q, want what was typed", got)
	}
	stop()
}

func TestShellStartEndsWhenThePageGoes(t *testing.T) {
	begun := make(chan context.Context, 1)
	srv := serveSockets(t, Config{Controller: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (draal.Shell, error) {
		begun <- ctx
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}}})
	c := dial(t, srv, shellPath)
	ctx := <-begun
	c.CloseNow()
	isDone(t, "the start of the shell to end with the socket", ctx)
}

// Review Focus 5: what the page sends is handed to the shell as fast as
// the shell takes it, by a goroutine of its own. A shell that takes
// nothing for a while costs the page nothing: the socket is read all the
// while, its pings are answered, and what was typed waits, in its order.
func TestShellThatDoesNotTakeInputKeepsItsSocket(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}, PingEvery: 10 * time.Millisecond})
	var pings atomic.Int32
	c, _ := dialWith(t, srv, shellPath, &websocket.DialOptions{
		OnPingReceived: func(context.Context, []byte) bool { pings.Add(1); return true },
	})
	// The page reads all the while, as a browser does.
	output := make(chan []byte, 16)
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				close(output)
				return
			}
			output <- data
		}
	}()
	// Nobody takes what is typed: the first of it waits in the shell's
	// Write, the rest behind it.
	bg := context.Background()
	for _, text := range []string{"one ", "two ", "three"} {
		if err := c.Write(bg, websocket.MessageBinary, []byte(text)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// Pings go by, each answered by a page that is read all the while.
	end := time.Now().Add(deadline)
	for pings.Load() < 20 {
		if time.Now().After(end) {
			t.Fatalf("%d pings came, want 20", pings.Load())
		}
		time.Sleep(time.Millisecond)
	}
	// The shell takes its input at last, all of it and in order, and the
	// socket is there for its output.
	for got := ""; got != "one two three"; {
		got += sh.wasTyped(t)
		if !strings.HasPrefix("one two three", got) {
			t.Fatalf("the shell was written %q, want what was typed in its order", got)
		}
	}
	sh.out <- []byte("done")
	select {
	case data, ok := <-output:
		if !ok || string(data) != "done" {
			t.Fatalf("the page read %q (open: %v), want the output of the shell", data, ok)
		}
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the output of the shell")
	}
}

// typing is what a test types into a shell: n bytes that tell where in
// the whole each of them is.
func typing(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// A paste: megabytes are typed, in messages of 16 KiB as the page sends
// them, into a shell that takes its input at a modest pace. The socket
// is faster than the shell, so there is not always room; the reader of
// the socket waits for it, and the page with it. All of it arrives, in
// its order, and the session is alive at the end.
func TestPasteArrivesWholeAtThePaceTheShellTakesIt(t *testing.T) {
	const piece = 16 << 10
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}, PingEvery: 20 * time.Millisecond})
	c := dial(t, srv, shellPath)
	pasted := typing(4 << 20)
	sent := make(chan error, 1)
	go func() {
		for at := 0; at < len(pasted); at += piece {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			err := c.Write(ctx, websocket.MessageBinary, pasted[at:at+piece])
			cancel()
			if err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	// The page reads all the while, as a browser does; nothing comes but
	// pings, until the output at the end.
	output := make(chan []byte, 1)
	go func() {
		_, data, _ := c.Read(context.Background())
		output <- data
	}()

	var got []byte
	for len(got) < len(pasted) {
		data := []byte(sh.wasTyped(t))
		if len(data) > readLimit {
			t.Fatalf("the shell was written %d bytes at once: more than is ever kept", len(data))
		}
		got = append(got, data...)
		if !bytes.Equal(got, pasted[:min(len(got), len(pasted))]) {
			t.Fatalf("after %d bytes the shell was written what was not typed there", len(got)-len(data))
		}
		time.Sleep(time.Millisecond) // the pace of the shell
	}
	if len(got) != len(pasted) {
		t.Fatalf("the shell was written %d bytes, and %d were typed", len(got), len(pasted))
	}
	if err := await(t, "the page to have sent all of it", sent); err != nil {
		t.Fatalf("the page could not send what it typed: %v", err)
	}
	sh.out <- []byte("pasted")
	if data := await(t, "the output of the shell", output); string(data) != "pasted" {
		t.Fatalf("the page read %q, want the output of the shell: the session is to be alive", data)
	}
}

// What is kept is never more than its bound, however fast it is put in
// and however it is taken out, and nothing of it is lost or out of its
// order.
func TestShellInputNeverHoldsMoreThanItsBound(t *testing.T) {
	h := New(Config{Controller: &fake{}, Dist: testDist(), Log: quiet}).(*handler)
	in := newShellInput(h)
	defer in.end()
	typed := typing(8 << 20)
	go func() {
		// Messages of every length a message may have.
		for at, n := 0, 1; at < len(typed); at, n = at+n, n*3%readLimit+1 {
			n = min(n, len(typed)-at)
			in.message(websocket.MessageBinary, typed[at:at+n])
		}
	}()
	var got []byte
	for len(got) < len(typed) {
		in.mu.Lock()
		held := len(in.typed)
		in.mu.Unlock()
		if held > readLimit {
			t.Fatalf("%d bytes are kept, and %d is the most", held, readLimit)
		}
		select {
		case <-in.more:
		case <-time.After(deadline):
			t.Fatalf("timed out after %d bytes of %d", len(got), len(typed))
		}
		data, _ := in.take()
		if len(data) > readLimit {
			t.Fatalf("%d bytes were kept, and %d is the most", len(data), readLimit)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, typed) {
		t.Fatal("what was taken out is not what was put in")
	}
	if in.full.Err() != nil {
		t.Error("the input was given up, though it was taken all the time")
	}
}

// A shell that takes nothing: what is typed waits, up to its bound, and
// the page waits behind it. When that has lasted WriteWait the session
// ends, and says why; not before, and with nothing dropped from the
// middle of what was typed to go on with the rest.
func TestShellThatTakesNoInputForWriteWaitEndsTheSession(t *testing.T) {
	const writeWait = 500 * time.Millisecond
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}, WriteWait: writeWait})
	h := srv.Config.Handler.(*handler)
	c := dial(t, srv, shellPath)
	bg := context.Background()
	// The first message is taken to the shell's Write, where it stays.
	if err := c.Write(bg, websocket.MessageBinary, []byte("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sh.isWriting(t)
	// As much as is kept fits behind it.
	if err := c.Write(bg, websocket.MessageBinary, make([]byte, readLimit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// One byte more has no room, and waits for it.
	if err := c.Write(bg, websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waiting := time.Now()
	end := waiting.Add(deadline)
	for h.inputWaits.Load() == 0 {
		if time.Now().After(end) {
			t.Fatal("timed out waiting for the reader of the socket to wait for room")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-sh.closed:
		t.Fatal("the session ended as soon as there was no room")
	default:
	}

	ctx, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	_, _, err := c.Read(ctx)
	waited := time.Since(waiting)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusMessageTooBig || closed.Reason != "the shell does not take its input" {
		t.Fatalf("the socket ended with %v, want it closed as too big with the reason", err)
	}
	// The message was sent before the clock was read, so it has waited
	// no less than this.
	if waited < writeWait-50*time.Millisecond {
		t.Errorf("the session ended %s after there was no room, want it to wait %s", waited, writeWait)
	}
	sh.isClosed(t)
}

// The same before the shell runs: what is typed while it is started is
// kept whole, and when it finds no room for WriteWait the session ends.
// A shell is never given the beginning and the end of what was typed
// without the middle.
func TestInputWhileTheShellIsStartedIsKeptWholeOrTheSessionEnds(t *testing.T) {
	sh := newFakeShell()
	begun := make(chan context.Context, 1)
	srv := serveSockets(t, Config{WriteWait: 300 * time.Millisecond, Controller: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (draal.Shell, error) {
		begun <- ctx
		<-ctx.Done() // a start that takes its time
		return nil, context.Cause(ctx)
	}}})
	c := dial(t, srv, shellPath)
	starting := await(t, "the start of the shell", begun)
	bg := context.Background()
	for _, m := range []string{strings.Repeat("a", readLimit-10), "MIDDLE-THAT-DOES-NOT-FIT", "end"} {
		if err := c.Write(bg, websocket.MessageBinary, []byte(m)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	_, _, err := c.Read(ctx)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusMessageTooBig || closed.Reason != "the shell does not take its input" {
		t.Fatalf("the socket ended with %v, want it closed as too big with the reason", err)
	}
	// The start was ended with the session.
	isDone(t, "the start of the shell to end", starting)
	select {
	case data := <-sh.typed:
		t.Errorf("a shell was written %d bytes", len(data))
	default:
	}
}

// stubbornShell is a draal.Shell whose Write waits and is not ended by
// Close, as the Write of a stream is not that waits for the other side
// to take what was written; its Read does end at Close.
type stubbornShell struct {
	closed  chan struct{}
	writing chan struct{} // gets a value when Write is called
	release chan struct{} // closed by the test at its end
	once    sync.Once
}

func newStubbornShell(t *testing.T) *stubbornShell {
	sh := &stubbornShell{closed: make(chan struct{}), writing: make(chan struct{}, 16), release: make(chan struct{})}
	t.Cleanup(func() { close(sh.release) })
	return sh
}

func (sh *stubbornShell) Read(p []byte) (int, error) {
	<-sh.closed
	return 0, wire.Errorf(wire.CodeUnavailable, "the connection of the zathras failed: closed")
}

func (sh *stubbornShell) Write(p []byte) (int, error) {
	sh.writing <- struct{}{}
	<-sh.release
	return 0, io.ErrClosedPipe
}

func (sh *stubbornShell) Resize(cols, rows uint16) error        { return nil }
func (sh *stubbornShell) Wait(ctx context.Context) (int, error) { return 0, nil }
func (sh *stubbornShell) Close() error {
	sh.once.Do(func() { close(sh.closed) })
	return nil
}

// The handler of a shell returns on every way a session ends, whatever
// the Close of the Shell does to a Write that waits: it waits for none.
// The Write is the controller's to end. At each ending here a Write
// waits, what is kept is full, and the reader of the socket waits for
// room.
func TestShellHandlerReturnsThoughAWriteToTheShellNeverDoes(t *testing.T) {
	ways := map[string]func(c *websocket.Conn, shutDown func()){
		"the page goes":           func(c *websocket.Conn, _ func()) { c.CloseNow() },
		"the page closes":         func(c *websocket.Conn, _ func()) { go c.Close(websocket.StatusNormalClosure, "") },
		"the server ends":         func(_ *websocket.Conn, shutDown func()) { shutDown() },
		"the shell takes nothing": func(*websocket.Conn, func()) {},
		"the page types more and more": func(c *websocket.Conn, _ func()) {
			go c.Write(context.Background(), websocket.MessageBinary, make([]byte, readLimit))
		},
	}
	for way, end := range ways {
		sh := newStubbornShell(t)
		base, shutDown := context.WithCancel(context.Background())
		inner := New(Config{
			Controller: &fake{shell: func(context.Context, wire.ID, wire.ShellRequest) (draal.Shell, error) { return sh, nil }},
			Dist:       testDist(), Log: quiet,
			PingEvery: 20 * time.Millisecond, WriteWait: time.Second,
		})
		returned := make(chan struct{})
		srv := serveHandler(t, base, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(w, r)
			close(returned)
		}))
		c := dial(t, srv, shellPath)
		bg := context.Background()
		if err := c.Write(bg, websocket.MessageBinary, []byte("pasted")); err != nil {
			t.Fatalf("%s: Write: %v", way, err)
		}
		await(t, way+": the shell to be written to", sh.writing)
		// What is kept is filled, and one message more waits for room.
		for _, n := range []int{readLimit, 1} {
			if err := c.Write(bg, websocket.MessageBinary, make([]byte, n)); err != nil {
				t.Fatalf("%s: Write: %v", way, err)
			}
		}
		for waiting := time.Now().Add(deadline); inner.(*handler).inputWaits.Load() == 0; {
			if time.Now().After(waiting) {
				t.Fatalf("%s: timed out waiting for the reader of the socket to wait for room", way)
			}
			time.Sleep(time.Millisecond)
		}
		end(c, shutDown)
		select {
		case <-returned:
		case <-time.After(deadline):
			buf := make([]byte, 1<<20)
			t.Fatalf("%s: the handler of the shell has not returned:\n%s", way, buf[:runtime.Stack(buf, true)])
		}
		select {
		case <-sh.closed:
		default:
			t.Errorf("%s: the handler returned and the shell was not closed", way)
		}
		if waits := inner.(*handler).inputWaits.Load(); waits != 0 {
			t.Errorf("%s: the handler returned and %d readers wait for room", way, waits)
		}
		shutDown()
	}
}

// Review Focus 5: output is read from the session only as fast as the
// page takes it. A shell that prints without end to a page that does not
// read is read what the connection holds and no more, and then the
// socket is closed.
func TestShellOutputIsReadOnlyAsFastAsThePageTakesIt(t *testing.T) {
	// What a connection on loopback holds on its way is some hundred
	// kilobytes on a Mac and some megabytes on Linux.
	const most = 32 << 20
	sh := newFakeShell()
	srv := serveSockets(t, Config{Controller: &fake{shell: shells(sh, nil)}, WriteWait: 200 * time.Millisecond})
	dial(t, srv, shellPath) // never reads
	chunk := make([]byte, shellChunk)
	sent := 0
	for {
		select {
		case sh.out <- chunk:
			if sent += len(chunk); sent > most {
				t.Fatalf("more than %d bytes were read from a shell whose page does not read", most)
			}
			continue
		case <-sh.closed:
		case <-time.After(deadline):
			t.Fatal("timed out waiting for the shell to be closed")
		}
		break
	}
	t.Logf("%d bytes were read from the shell before its socket was closed", sent)
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./web/`
Expected: FAIL: the package does not build, with `undefined: shellChunk` and `undefined: readLimit`.

- [ ] **Step 4: Write the implementation**

In `web/web.go`, `New` gets the three routes:

```go
// New returns the handler of the API and of the page.
func New(cfg Config) http.Handler {
	cfg = cfg.withDefaults()
	h := &handler{
		cfg:   cfg,
		log:   cfg.Log,
		built: hasIndex(cfg.Dist, indexFile),
		mux:   http.NewServeMux(),

		answerWait: answerWait,
	}
	h.mux.HandleFunc("/api/services/{service}/rollouts", h.get(h.rollouts))
	h.mux.HandleFunc("/api/zathras/{id}/history", h.get(h.history))
	h.mux.HandleFunc("/api/watch", h.get(h.watch))
	h.mux.HandleFunc("/api/zathras/{id}/logs", h.get(h.logs))
	h.mux.HandleFunc("/api/zathras/{id}/shell", h.get(h.shell))
	h.mux.HandleFunc("/api/", h.noSuchPath)
	h.mux.HandleFunc("/", h.page)
	return h
}
```

The socket, `watch` and `logs`, `web/socket.go`:

```go
package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// readLimit is the most bytes one message of a page may have: plenty
	// for what is typed, pasted in pieces, and for a change of size.
	readLimit = 64 << 10
	// closeWait is the most time the close of a socket takes: for the
	// close frame to be written and the page to answer it. A page that
	// does not, in that time, has its connection cut.
	closeWait = time.Second
)

// socket is a WebSocket of a page, with the two goroutines that every one
// has: one that reads what the page sends, and one that pings while
// nothing is written.
//
// The handler that opened it is its one writer of messages.
type socket struct {
	h    *handler
	r    *http.Request
	conn *websocket.Conn
	raw  net.Conn // the connection under conn, to cut it

	// ctx is done when the page has closed the socket, when the socket has
	// failed, or when the server ends. Whatever is done for the page is
	// done under it.
	ctx    context.Context
	cancel context.CancelFunc

	wrote atomic.Int64 // when the last message was written, in nanoseconds of Unix time
	// busy is set while the reader hands a message on, which for a shell
	// may be a wait of WriteWait for room; read is when it last began to
	// read again, in nanoseconds of Unix time. While it is busy it reads
	// nothing, no pong either.
	busy atomic.Bool
	read atomic.Int64

	stopRead context.CancelFunc
	running  sync.WaitGroup
	shutting sync.Once
}

// taken is a ResponseWriter that keeps the connection the WebSocket
// library takes over from the server, which the library does not hand
// out: the socket needs it to end without waiting for the page.
type taken struct {
	http.ResponseWriter
	conn net.Conn
}

// Hijack takes the connection over, as the library asks, without the
// deadline its answer had as an answer.
func (t *taken) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(t.ResponseWriter).Hijack()
	if err == nil {
		conn.SetDeadline(time.Time{})
		t.conn = conn
	}
	return conn, rw, err
}

// sameOrigin says whether a request may open a socket: it has no Origin,
// as a program that is no browser, or its Origin has the host the request
// was sent to.
//
// A browser sends the Origin of the page with every WebSocket, and lets
// every page open one to every host: the same-origin policy does not
// hold for them. Without this, any page the operator has open could ask
// 127.0.0.1 for a shell.
//
// The Host is not looked at beyond this: a page under any name is
// draal's own when it was loaded from the host it asks. So this does not
// keep out a site that points a name of its own at draal's address (DNS
// rebinding), which is known and was decided so (the spec's section 4).
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// open makes a socket of a request, or answers the request with why not
// and returns nil. onMessage is called, by the one goroutine that reads,
// with every message the page sends; nil drops them. It may wait, for no
// longer than WriteWait and for nothing that the end of the socket's
// handler does not end: while it runs, the close of the page and its
// pongs are not seen.
//
// Who has opened a socket calls its done.
func (h *handler) open(w http.ResponseWriter, r *http.Request, onMessage func(websocket.MessageType, []byte)) *socket {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		h.refuse(w, r, wire.Errorf(wire.CodeBadRequest, "%s is a WebSocket", r.URL.Path))
		return nil
	}
	if !sameOrigin(r) {
		h.log.Warn("a WebSocket of another origin was refused", "origin", r.Header.Get("Origin"), "host", r.Host, "path", r.URL.Path)
		h.refuse(w, r, wire.Errorf(wire.CodeUnauthorized,
			"a WebSocket of %s is opened by its own page alone, not by one of %s", r.Host, r.Header.Get("Origin")))
		return nil
	}
	// A socket is counted from here to the end of its done.
	if open := h.sockets.Add(1); open > int64(h.cfg.MaxSockets) {
		h.sockets.Add(-1)
		h.log.Warn("a WebSocket more than may be open was refused", "open", open-1, "path", r.URL.Path)
		h.refuse(w, r, wire.Errorf(wire.CodeUnavailable, "%d WebSockets are open, and no more may be: close a page", open-1))
		return nil
	}
	// Accept checks the Origin as sameOrigin does. No compression: what
	// is sent is small or is the output of a shell, and a context of
	// compression for every socket is memory a flood should not get.
	under := &taken{ResponseWriter: w}
	conn, err := websocket.Accept(under, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		h.sockets.Add(-1)
		h.log.Debug("a WebSocket was not opened", "path", r.URL.Path, "error", err)
		return nil // Accept has answered
	}
	conn.SetReadLimit(readLimit)

	s := &socket{h: h, r: r, conn: conn, raw: under.conn}
	s.ctx, s.cancel = context.WithCancel(r.Context())
	s.wrote.Store(time.Now().UnixNano())
	// The reader has a context of its own: the library cuts a socket
	// whose Read is cancelled, and one that ends because the server ends
	// is to be told so first.
	readCtx, stopRead := context.WithCancel(context.Background())
	s.stopRead = stopRead
	s.running.Go(func() { s.reader(readCtx, onMessage) })
	s.running.Go(s.ping)
	return s
}

// read reads what the page sends until the socket ends, which is also
// how the close and the pongs of the page are seen, and then ends
// everything that is done for the page.
func (s *socket) reader(ctx context.Context, onMessage func(websocket.MessageType, []byte)) {
	defer s.cancel()
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return
		}
		if onMessage != nil {
			s.busy.Store(true)
			onMessage(typ, data)
			s.read.Store(time.Now().UnixNano())
			s.busy.Store(false)
		}
	}
}

// ping pings the page whenever nothing was written for PingEvery, and
// ends the socket of a page that does not answer within WriteWait. A
// pong that was not read because the reader waited for a shell to take
// its input is not held against the page: that wait is bounded by
// WriteWait too, and the ping after it finds a page that has gone.
func (s *socket) ping() {
	every := s.h.cfg.PingEvery
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(time.Unix(0, s.wrote.Load())) < every {
			continue
		}
		pinged := time.Now()
		ctx, cancel := context.WithTimeout(s.ctx, s.h.cfg.WriteWait)
		err := s.conn.Ping(ctx)
		cancel()
		switch {
		case err == nil:
		case s.ctx.Err() != nil:
			return
		case errors.Is(err, context.DeadlineExceeded) && (s.busy.Load() || s.read.Load() > pinged.UnixNano()):
			// The reader did not read for some of the time the pong had.
			// If it was the ping that could not be written, the library
			// has cut the socket, and the next ping says so.
		default:
			s.h.log.Debug("a page did not answer a ping", "path", s.r.URL.Path, "error", err)
			s.cancel()
			return
		}
	}
}

// write sends one message. A page that does not take it within WriteWait
// loses its socket: the library closes a socket whose write ran out of
// time, as half a message may be on its way.
func (s *socket) write(typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.h.cfg.WriteWait)
	defer cancel()
	if err := s.conn.Write(ctx, typ, data); err != nil {
		if s.ctx.Err() == nil {
			s.h.log.Debug("a page did not take a message", "path", s.r.URL.Path, "error", err)
		}
		s.cancel()
		return err
	}
	s.wrote.Store(time.Now().UnixNano())
	return nil
}

// writeJSON sends v as a text message.
func (s *socket) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		s.h.log.Error("encoding a message", "path", s.r.URL.Path, "error", err)
		s.cancel()
		return err
	}
	return s.write(websocket.MessageText, data)
}

// shut closes the socket with a code and a reason, once, and returns
// within closeWait, or within WriteWait if that is shorter: the close
// frame is written, the page has the rest of the time to answer it, and
// then the connection is cut. A page that is there has answered long
// before; one that is not is not waited for, as the library would for
// ten seconds.
//
// The wait for the answer is what gets the code to a page that is
// there: a connection that is closed while the page still sends is
// reset, and what was written to it can be lost with that.
func (s *socket) shut(code websocket.StatusCode, reason string) {
	s.shutting.Do(func() {
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			s.conn.Close(code, reason)
		}()
		wait := time.NewTimer(min(closeWait, s.h.cfg.WriteWait))
		defer wait.Stop()
		select {
		case <-closed:
		case <-wait.C:
		}
		// With the connection cut, Close of the library returns at once.
		s.raw.Close()
		<-closed
	})
}

// fail tells the page why the socket ends, as the last message, and
// closes the socket. A page that has gone is told nothing.
func (s *socket) fail(err error) {
	if s.ctx.Err() != nil {
		return
	}
	if s.writeJSON(refusal{Error: s.h.refusalOf(s.r, err)}) == nil {
		s.shut(websocket.StatusNormalClosure, "")
	}
}

// end closes the socket in the normal way: everything was sent.
func (s *socket) end() {
	if s.ctx.Err() == nil {
		s.shut(websocket.StatusNormalClosure, "")
	}
}

// done ends the socket and returns when its goroutines have ended, which
// they have within closeWait whatever the page does. A socket that is
// open still because the server ends is closed as going away, so that
// the page knows to come back.
func (s *socket) done() {
	if s.r.Context().Err() != nil {
		s.shut(websocket.StatusGoingAway, "draal is shutting down")
	}
	s.cancel()
	s.stopRead()
	s.raw.Close()
	s.conn.CloseNow()
	s.running.Wait()
	s.h.sockets.Add(-1)
}

// watch sends the fleet, at once and whenever it changes.
func (h *handler) watch(w http.ResponseWriter, r *http.Request) {
	s := h.open(w, r, nil)
	if s == nil {
		return
	}
	defer s.done()
	fleets, err := h.cfg.Controller.Watch(s.ctx)
	if err != nil {
		s.fail(err)
		return
	}
	// The controller keeps the newest fleet for a watcher that is slow
	// and drops the ones between, so there is no queue here: while a
	// message is written, nothing is taken from the channel.
	for {
		select {
		case fleet, ok := <-fleets:
			if !ok {
				s.fail(wire.Errorf(wire.CodeUnavailable, "draal has stopped sending the fleet"))
				return
			}
			if s.writeJSON(fleet) != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// logs sends the ring of a zathras, and with follow what follows.
func (h *handler) logs(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	follow, err := truth(r, "follow")
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	s := h.open(w, r, nil)
	if s == nil {
		return
	}
	defer s.done()
	lines, err := h.cfg.Controller.Logs(s.ctx, id, follow)
	if err != nil {
		s.fail(err)
		return
	}
	// A line is taken from the controller when the one before is written:
	// a page that reads slowly holds up its own log and nothing else, and
	// what it misses, the ring of the zathras counts as lost.
	for {
		select {
		case line, ok := <-lines:
			if ok {
				if s.writeJSON(line) != nil {
					return
				}
				continue
			}
			// The channel is closed for a log that has ended and for one
			// that broke off, and the Controller says no more than that.
			// So a ring that broke off half way, as when its zathras went
			// while it was sent, ends as a whole one does.
			if !follow {
				s.end()
				return
			}
			// Review Focus 2: a log that is followed ends only when the
			// zathras has gone, and the page is told so.
			s.fail(wire.Errorf(wire.CodeUnavailable, "the log of zathras %s has ended: the zathras is gone", id))
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// truth returns a parameter of the query that is a truth value: absent or
// empty is false.
func truth(r *http.Request, name string) (bool, error) {
	switch v := r.URL.Query().Get(name); v {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	default:
		return false, wire.Errorf(wire.CodeBadRequest, "%s is %q, and can be 1 or 0", name, v)
	}
}
```

The shell, `web/shell.go`:

```go
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// maxTerminal is the most columns or rows a terminal may have.
	maxTerminal = 1000
	// maxTerm is the most bytes the name of a terminal may have.
	maxTerm = 64
	// shellChunk is the most output of a shell one message carries.
	shellChunk = 32 << 10
)

// shellMessage is a text message of the page on the socket of a shell.
type shellMessage struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// shellExit is the text message that says how the shell ended.
type shellExit struct {
	Exit int `json:"exit"`
}

// shellInput is what the page has sent for a shell and the shell has not
// been given yet: what was typed, at most readLimit bytes of it, and the
// last size of the terminal.
//
// The reader of the socket puts it in. A goroutine of its own, pump,
// hands it to the shell, whose Write may wait for as long as the program
// in the shell reads nothing, and for good. Before the shell runs nobody
// takes anything out.
//
// When there is no room for what was typed, the reader waits for room,
// which pump makes as it takes, and reads nothing meanwhile: what the
// page sends backs up in the browser, and a paste goes to the shell at
// the pace the shell takes it. That wait, unlike one in Write, ends when
// the handler says so (end), and by itself after WriteWait: a shell that
// took nothing for that long does not take its input, and the session
// ends. Nothing is ever dropped from the middle of what was typed.
type shellInput struct {
	h *handler

	more chan struct{} // has room for one value, and gets one when something was put in
	room chan struct{} // has room for one value, and gets one when something was taken out
	done chan struct{} // closed by end: the handler returns
	once sync.Once

	// full is done when what was typed found no room for WriteWait.
	full     context.Context
	overflow context.CancelFunc

	mu    sync.Mutex
	typed []byte
	size  *wire.WinSize
}

func newShellInput(h *handler) *shellInput {
	in := &shellInput{h: h, more: make(chan struct{}, 1), room: make(chan struct{}, 1), done: make(chan struct{})}
	in.full, in.overflow = context.WithCancel(context.Background())
	return in
}

// end ends a wait for room: the session is over.
func (in *shellInput) end() {
	in.once.Do(func() { close(in.done) })
}

// message takes one message of the page. A binary one is what was typed.
// A text one is a change of size; one that is none is dropped, as the
// page gets no answer on this socket but output.
func (in *shellInput) message(typ websocket.MessageType, data []byte) {
	if typ == websocket.MessageBinary {
		if !in.put(data) {
			in.overflow()
		}
		return
	}
	var m shellMessage
	if err := json.Unmarshal(data, &m); err != nil || m.Resize == nil ||
		m.Resize.Cols < 1 || m.Resize.Cols > maxTerminal || m.Resize.Rows < 1 || m.Resize.Rows > maxTerminal {
		in.h.log.Debug("a message of a page that is no change of size was dropped", "message", string(data[:min(len(data), 200)]))
		return
	}
	in.mu.Lock()
	in.size = &wire.WinSize{Cols: uint16(m.Resize.Cols), Rows: uint16(m.Resize.Rows)}
	in.mu.Unlock()
	in.tell(in.more)
}

// tell puts a value into a channel that has room for one, if it is not
// there already.
func (in *shellInput) tell(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// put puts in what was typed, behind what waits. With no room for it, it
// waits for room, for at most WriteWait, and returns false when there
// was none in that time. A message is no longer than readLimit, so there
// is room for it at the latest when nothing waits.
func (in *shellInput) put(data []byte) bool {
	var late <-chan time.Time
	for {
		in.mu.Lock()
		if len(in.typed)+len(data) <= readLimit {
			in.typed = append(in.typed, data...)
			in.mu.Unlock()
			in.tell(in.more)
			return true
		}
		in.mu.Unlock()
		if late == nil {
			timer := time.NewTimer(in.h.cfg.WriteWait)
			defer timer.Stop()
			late = timer.C
			in.h.inputWaits.Add(1)
			defer in.h.inputWaits.Add(-1)
		}
		select {
		case <-in.room:
		case <-late:
			return false
		case <-in.done:
			return true // nobody is there to take it
		}
	}
}

// take takes out what waits, and so makes room.
func (in *shellInput) take() ([]byte, *wire.WinSize) {
	in.mu.Lock()
	typed, size := in.typed, in.size
	in.typed, in.size = nil, nil
	in.mu.Unlock()
	in.tell(in.room)
	return typed, size
}

// pump hands what the page sent to the shell, in its order, until stop
// is closed. An error ends nothing here: a session that has failed says
// so where its output is read.
//
// Nobody waits for pump. A Write that waits is ended by the Close of the
// shell, if the Shell keeps to that; if it does not, pump stays with it.
func (in *shellInput) pump(sh draal.Shell, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-in.more:
		}
		typed, size := in.take()
		if size != nil {
			if err := sh.Resize(size.Cols, size.Rows); err != nil {
				in.h.log.Debug("resizing a shell", "error", err)
			}
		}
		if len(typed) > 0 {
			if _, err := sh.Write(typed); err != nil {
				in.h.log.Debug("writing to a shell", "error", err)
			}
		}
	}
}

// shellOutput is a piece of what a shell printed, or how its output
// ended.
type shellOutput struct {
	data []byte
	err  error
}

// readShell reads what the shell prints and hands it on piece by piece,
// each when the one before was taken and written (next), until the
// output ends or stop is closed. Nobody waits for it either: a Read
// that waits is the Shell's to end at its Close.
func readShell(sh draal.Shell, pieces chan<- shellOutput, next, stop <-chan struct{}) {
	buf := make([]byte, shellChunk)
	for {
		n, err := sh.Read(buf)
		select {
		case pieces <- shellOutput{data: buf[:n], err: err}:
		case <-stop:
			return
		}
		if err != nil {
			return
		}
		select {
		case <-next:
		case <-stop:
			return
		}
	}
}

// shellRequest reads the size and the name of the terminal from the
// query: 80 columns, 24 rows and xterm-256color where it says nothing.
func shellRequest(r *http.Request, id wire.ID) (wire.ShellRequest, error) {
	req := wire.ShellRequest{Zathras: id, Cols: 80, Rows: 24, Term: "xterm-256color"}
	query := r.URL.Query()
	for name, to := range map[string]*uint16{"cols": &req.Cols, "rows": &req.Rows} {
		v := query.Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxTerminal {
			return req, wire.Errorf(wire.CodeBadRequest, "%s is %q, and can be 1 to %d", name, v, maxTerminal)
		}
		*to = uint16(n)
	}
	if term := query.Get("term"); term != "" {
		valid := len(term) <= maxTerm
		for i := 0; valid && i < len(term); i++ {
			c := term[i]
			valid = c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '+'
		}
		if !valid {
			return req, wire.Errorf(wire.CodeBadRequest,
				"term is %q, and can be up to %d of a to z, A to Z, 0 to 9, '-', '_', '.' and '+'", term, maxTerm)
		}
		req.Term = term
	}
	return req, nil
}

// shell runs a shell in the container of a zathras on a socket: binary
// messages are what is typed and what is printed.
//
// It returns when the session has ended, however that came about, and
// waits for nothing the Shell does but its Close: not for a Write, which
// may wait for a program that reads nothing, and not for a Read. What
// it does wait for, the reader of the socket, waits for room for input
// at most, and that wait it ends.
func (h *handler) shell(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	req, err := shellRequest(r, id)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	in := newShellInput(h)
	defer in.overflow()
	s := h.open(w, r, in.message)
	if s == nil {
		return
	}
	defer s.done()
	// Before done waits for the reader of the socket, a wait of the
	// reader for room is ended.
	defer in.end()
	// tooMuch ends the session of a page whose input found no room for
	// WriteWait: the shell does not take its input, and nothing of it is
	// dropped to go on.
	tooMuch := func() {
		s.shut(websocket.StatusMessageTooBig, "the shell does not take its input")
	}

	// The start ends with the socket, and when what was typed found no
	// room for a shell that is not there yet.
	starting, started := context.WithCancel(s.ctx)
	stopFull := context.AfterFunc(in.full, started)
	sh, err := h.cfg.Controller.Shell(starting, id, req)
	stopFull()
	started()
	if err != nil {
		if in.full.Err() != nil && s.ctx.Err() == nil {
			tooMuch()
			return
		}
		s.fail(err)
		return
	}
	// The session ends, and the shell with it, when this returns and when
	// the socket ends, whichever is first: the page closing the socket
	// ends the shell. Close is called once.
	var closing sync.Once
	closeShell := func() { closing.Do(func() { sh.Close() }) }
	defer closeShell()
	defer context.AfterFunc(s.ctx, closeShell)()

	stop := make(chan struct{})
	defer close(stop)
	go in.pump(sh, stop)

	// Output is read from the session only when the output before it has
	// been written to the page. So a page that reads slowly slows the
	// shell down, through draal and the zathras to the program that
	// prints, as a slow terminal does, and nothing between them grows
	// with what a program prints. This goroutine is also the one that
	// writes the exit status, so the page gets it behind all the output.
	pieces, next := make(chan shellOutput), make(chan struct{}, 1)
	go readShell(sh, pieces, next, stop)
	for {
		var piece shellOutput
		select {
		case piece = <-pieces:
		case <-in.full.Done():
			if s.ctx.Err() == nil {
				tooMuch()
			}
			return
		case <-s.ctx.Done():
			return // the page closed the socket, or the server ends
		}
		if len(piece.data) > 0 && s.write(websocket.MessageBinary, piece.data) != nil {
			return
		}
		if errors.Is(piece.err, io.EOF) {
			break
		}
		if piece.err != nil {
			// Review Focus 2: a session whose zathras goes ends with the
			// reason.
			s.fail(piece.err)
			return
		}
		next <- struct{}{}
	}
	if s.ctx.Err() != nil {
		return // the page closed the socket, which ended the shell
	}
	status, err := sh.Wait(s.ctx)
	if err != nil {
		s.fail(err)
		return
	}
	if s.writeJSON(shellExit{Exit: status}) == nil {
		s.end()
	}
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./web/`
Expected: `ok  	github.com/fables-for-robots/great-machine/web`

The tests of the package do not run in parallel: each one of the sockets counts the goroutines at its end against the count when its server was there and nobody had asked it anything.

Run: `nix develop .#bare --command go test -race -count=10 ./web/`
Expected: `ok`, ten times over (about two minutes).

Run: `nix develop .#bare --command sh -c 'gofmt -l web; go vet ./web/'`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum web/
git commit -m "Web: the fleet, the log and the shell of a zathras over WebSockets"
```
