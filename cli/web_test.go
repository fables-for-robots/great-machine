package cli

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebURL(t *testing.T) {
	bound := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4321}
	tests := []struct{ addr, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:4321/"},
		{"127.0.0.1:0", "http://127.0.0.1:4321/"},
		{"localhost:8080", "http://localhost:4321/"},
		{"[::1]:8080", "http://[::1]:4321/"},
		{":8080", "http://localhost:4321/"},
		{"0.0.0.0:8080", "http://localhost:4321/"},
		{"[::]:8080", "http://localhost:4321/"},
		{"10.1.2.3:8080", "http://10.1.2.3:4321/"},
	}
	for _, tt := range tests {
		if got := webURL(tt.addr, bound); got != tt.want {
			t.Errorf("webURL(%q) is %q, want %q", tt.addr, got, tt.want)
		}
	}
}

// quietLog is a logger that writes to out.
func quietLog(out io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, nil))
}

// TestAnnounceWeb: the line of the log, and the two warnings.
func TestAnnounceWeb(t *testing.T) {
	bound := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}
	const notBuilt, notLoopback = "the web page was not built into this program", "not loopback: it has no authentication, and whoever reaches it has a shell in every container"

	var logged buffer
	announceWeb(quietLog(&logged), "127.0.0.1:8080", bound, true, true)
	if got, want := logged.String(), `level=INFO msg="web page" addr=127.0.0.1:8080 url=http://127.0.0.1:8080/`; !strings.Contains(got, want) {
		t.Errorf("logged %q, want %q in it", got, want)
	}
	if got := logged.String(); strings.Count(got, "\n") != 1 {
		t.Errorf("a page that is built, on loopback, is one line: got %q", got)
	}

	logged = buffer{}
	announceWeb(quietLog(&logged), "127.0.0.1:8080", bound, true, false)
	if got := logged.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, notBuilt) || strings.Contains(got, notLoopback) {
		t.Errorf("a page that is not built: logged %q", got)
	}

	logged = buffer{}
	announceWeb(quietLog(&logged), ":8080", &net.TCPAddr{IP: net.IPv6unspecified, Port: 8080}, false, true)
	if got := logged.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, notLoopback) || !strings.Contains(got, "url=http://localhost:8080/") || strings.Contains(got, notBuilt) {
		t.Errorf("an address that is not loopback: logged %q", got)
	}
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
