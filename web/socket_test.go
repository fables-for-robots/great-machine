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
