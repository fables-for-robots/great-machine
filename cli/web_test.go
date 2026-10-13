package cli

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// quietLog is a logger that writes to out.
func quietLog(out io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}

// TestWebServerStopEndsWebSockets: a connection that a handler took over,
// as the handler of a WebSocket does, is ended by Stop through the context
// of its request, and Stop waits for the handler. Neither Shutdown nor
// Close of http.Server does that.
func TestWebServerStopEndsWebSockets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var taken, ended atomic.Bool
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		conn, _, err := rw.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n\r\n")
		taken.Store(true)
		<-r.Context().Done()
		ended.Store(true)
	})
	var logged buffer
	w := startWeb(listener, handler, 5*time.Second, quietLog(&logged))
	defer w.Stop()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /api/watch HTTP/1.1\r\nHost: localhost\r\n\r\n")
	waitFor(t, "the handler to take the connection over", taken.Load)

	begun := time.Now()
	w.Stop()
	if !ended.Load() {
		t.Error("Stop returned before the handler of the connection had ended")
	}
	if took := time.Since(begun); took > 4*time.Second {
		t.Errorf("Stop took %s: it waited for a time to pass and not for the handler", took)
	}
	// The browser's side: the answer, and then the end.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	rest, err := io.ReadAll(conn)
	if err != nil {
		t.Errorf("reading the connection to its end: %v", err)
	}
	if !strings.HasPrefix(string(rest), "HTTP/1.1 101") {
		t.Errorf("the connection carried %q", rest)
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second); err == nil {
		t.Error("the address still listens after Stop")
	}
	if logged.String() != "" {
		t.Errorf("Stop logged %q, want nothing", logged.String())
	}
	w.Stop() // a second time is nothing
}

// TestWebServerStopGivesUp: a handler that does not end with the context
// of its request holds Stop for the wait it was given, twice at most, and
// no longer.
func TestWebServerStopGivesUp(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var entered atomic.Bool
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered.Store(true)
		<-release
	})
	var logged buffer
	w := startWeb(listener, handler, 50*time.Millisecond, quietLog(&logged))
	defer close(release)

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	waitFor(t, "the handler to be entered", entered.Load)

	stopped := make(chan struct{})
	go func() {
		w.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waits for a handler that does not end")
	}
	if !strings.Contains(logged.String(), "still being answered") {
		t.Errorf("Stop logged %q, want it to say that requests were still being answered", logged.String())
	}
}

// TestWebServerAnswersARequestThatBeginsAfterStopWithItsReason: the one
// request that can begin after the server was told to stop, on a
// connection that was accepted just before the listener closed, is
// answered 503 with a line that says why, is not handed to the handler,
// and is not counted among those Stop waits for.
//
// When such a request comes through the server is a matter of an
// instant between Shutdown and Close. So the test does not wait for one:
// it asks the server's own handler, the one the guard is in, after Stop.
func TestWebServerAnswersARequestThatBeginsAfterStopWithItsReason(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var answered atomic.Int32
	handler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		answered.Add(1)
		io.WriteString(rw, "the page")
	})
	var logged buffer
	w := startWeb(listener, handler, 5*time.Second, quietLog(&logged))

	// Before Stop a request is the handler's.
	before := httptest.NewRecorder()
	w.server.Handler.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/", nil))
	if before.Code != http.StatusOK || before.Body.String() != "the page" || answered.Load() != 1 {
		t.Fatalf("before Stop: %d %q, and the handler was asked %d times; want 200, the page, once", before.Code, before.Body.String(), answered.Load())
	}

	w.Stop()
	for _, path := range []string{"/", "/api/watch", "/assets/index-1a2b.js"} {
		after := httptest.NewRecorder()
		w.server.Handler.ServeHTTP(after, httptest.NewRequest(http.MethodGet, path, nil))
		if after.Code != http.StatusServiceUnavailable || after.Body.String() != "great-machine: the web page is ending\n" {
			t.Errorf("%s after Stop: %d %q, want 503 and the line that says that the page is ending", path, after.Code, after.Body.String())
		}
		if ct := after.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s after Stop: the content type is %q, want plain text", path, ct)
		}
	}
	if answered.Load() != 1 {
		t.Errorf("the handler was asked %d times, want once: nothing after Stop", answered.Load())
	}
	// Nothing of them is waited for: the count is where Stop left it.
	ended := make(chan struct{})
	go func() {
		w.handlers.Wait()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Error("a request that was refused after Stop is counted among those being answered")
	}
}
