package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// draalForTest is the configuration of a draal whose iroh endpoint stays
// on this machine.
func draalForTest(t *testing.T, webAddr string) draalConfig {
	t.Helper()
	return draalConfig{
		data:    t.TempDir(),
		store:   wire.StoreInfo{Server: wire.ID(idB), Prefix: "nix/"},
		webAddr: webAddr,
		local:   true,
	}
}

var urlInLog = regexp.MustCompile(`msg="web page" addr=\S+ url=(\S+)`)

// TestRunDraalWeb runs draal with a web page on a free port: it answers
// HTTP, and when the context of the command is done it stops with no
// error and leaves nothing listening.
func TestRunDraalWeb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	done := make(chan error, 1)
	cfg := draalForTest(t, "127.0.0.1:0")
	go func() { done <- runDraal(ctx, cfg, quietLog(&logged)) }()

	var url string
	waitFor(t, "draal to log the address of the web page", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runDraal returned early: %v\n%s", err, logged.String())
		default:
		}
		m := urlInLog.FindStringSubmatch(logged.String())
		if m == nil {
			return false
		}
		url = m[1]
		return true
	})
	if !strings.HasPrefix(url, "http://127.0.0.1:") || strings.HasSuffix(url, ":0/") {
		t.Fatalf("the url in the log is %q, want that of the port that was bound", url)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	client.CloseIdleConnections()
	// The tests run with the page built and without it.
	notBuilt := strings.Contains(logged.String(), "the web page was not built into this program")
	if web.Built() {
		if resp.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
			t.Errorf("GET / is %d %q, want the page", resp.StatusCode, body)
		}
		if notBuilt {
			t.Errorf("draal warns that the page was not built, and it was:\n%s", logged.String())
		}
	} else {
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("GET / is %d %q, want 501 as the page was not built", resp.StatusCode, body)
		}
		if !notBuilt {
			t.Errorf("draal does not warn that the page was not built:\n%s", logged.String())
		}
	}
	if strings.Contains(logged.String(), "not loopback") {
		t.Errorf("draal warns about a loopback address:\n%s", logged.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
	}
	if conn, err := net.DialTimeout("tcp", strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/"), time.Second); err == nil {
		conn.Close()
		t.Error("the web page still listens after draal has stopped")
	}
}

// TestRunDraalNoWeb: with an empty address there is no web page.
func TestRunDraalNoWeb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	done := make(chan error, 1)
	cfg := draalForTest(t, "")
	go func() { done <- runDraal(ctx, cfg, quietLog(&logged)) }()
	waitFor(t, "draal to serve", func() bool { return strings.Contains(logged.String(), "msg=serving") })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("runDraal did not return when its context was done")
	}
	if strings.Contains(logged.String(), "web page") {
		t.Errorf("draal without a web address logged about a web page:\n%s", logged.String())
	}
}

// TestRunDraalWebAddrTaken: an address that is taken ends the command
// with an error, and the iroh endpoint that was bound before it is closed.
func TestRunDraalWebAddrTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	// A UDP port for the iroh endpoint that the test can ask for again.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	bind := netip.MustParseAddrPort(probe.LocalAddr().String())
	probe.Close()

	cfg := draalForTest(t, taken.Addr().String())
	cfg.bind = bind
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runDraal(context.Background(), cfg, quietLog(&logged)) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the web page: listen tcp "+taken.Addr().String()) {
			t.Errorf("runDraal returned %v, want an error that names the web page and the address", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return with an address that is taken\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "msg=serving") {
		t.Fatalf("the iroh endpoint was not bound before the web page:\n%s", logged.String())
	}
	// The endpoint is closed: its port can be bound again.
	waitFor(t, "the port of the iroh endpoint to be free", func() bool {
		again, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(bind))
		if err != nil {
			return false
		}
		again.Close()
		return true
	})
}

// startDraal runs draal in a goroutine and returns the URL of its web
// page, as its log has it, and the channel of what runDraal returns.
func startDraal(t *testing.T, ctx context.Context, cfg draalConfig, logged *buffer) (string, <-chan error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- runDraal(ctx, cfg, quietLog(logged)) }()
	var url string
	waitFor(t, "draal to log the address of the web page", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runDraal returned early: %v\n%s", err, logged.String())
		default:
		}
		m := urlInLog.FindStringSubmatch(logged.String())
		if m == nil {
			return false
		}
		url = m[1]
		return true
	})
	return url, done
}

// TestRunDraalWebUnderAnyHost: draal answers the page under whatever
// host name it is asked as, on a loopback address too: through a tunnel,
// a forward or a proxy the browser's name for it is not draal's own. No
// name is refused.
func TestRunDraalWebUnderAnyHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	url, done := startDraal(t, ctx, draalForTest(t, "127.0.0.1:0"), &logged)

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	// The tests run with the page built and without it.
	want := http.StatusNotImplemented
	if web.Built() {
		want = http.StatusOK
	}
	for _, host := range []string{"localhost", "127.0.0.1:8080", "draal.example.com", "Draal.Example.COM.:8443", "10.1.2.3", "[2001:db8::1]:8080"} {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s as %s: %v", url, host, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("asked as %s: %d, want %d", host, resp.StatusCode, want)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("runDraal did not return when its context was done")
	}
}

// TestRunDraalStopsWithPagesThatDoNotAnswer: pages that hold a socket and
// read no more, as a laptop asleep or a tab frozen in the background,
// neither answer a ping nor the close of their socket. draal stops all
// the same, well within the time it gives the handlers, and with every
// handler ended: it does not close its endpoint and its database under
// one that still runs.
func TestRunDraalStopsWithPagesThatDoNotAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	url, done := startDraal(t, ctx, draalForTest(t, "127.0.0.1:0"), &logged)

	for range 3 {
		dialing, stop := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(dialing, "ws"+strings.TrimPrefix(url, "http")+"api/watch", nil)
		if err != nil {
			stop()
			t.Fatalf("the socket of the fleet: %v", err)
		}
		defer conn.CloseNow()
		// The first fleet: the handler runs. From here on the page reads
		// nothing, and so answers nothing.
		_, _, err = conn.Read(dialing)
		stop()
		if err != nil {
			t.Fatalf("the first fleet: %v", err)
		}
	}

	begun := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
	}
	if took := time.Since(begun); took > webStopWait-time.Second {
		t.Errorf("draal took %s to stop, and gives its handlers %s", took, webStopWait)
	}
	if strings.Contains(logged.String(), "still being answered") {
		t.Errorf("draal gave up on handlers that still ran:\n%s", logged.String())
	}
}

// TestRunDraalWebFails: when the server of the page ends by itself, draal
// ends with its error, and closes its iroh endpoint.
func TestRunDraalWebFails(t *testing.T) {
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	bind := netip.MustParseAddrPort(probe.LocalAddr().String())
	probe.Close()

	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.bind = bind
	bound := make(chan net.Listener, 1)
	cfg.listenWeb = func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err == nil {
			bound <- l
		}
		return l, err
	}
	var logged buffer
	_, done := startDraal(t, context.Background(), cfg, &logged)

	// The listener is closed under the server, which is how its Accept
	// fails for good.
	(<-bound).Close()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the web page: ") || !errors.Is(err, net.ErrClosed) {
			t.Errorf("runDraal returned %v, want the error of the web page", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when the server of the page had failed\n%s", logged.String())
	}
	if strings.Contains(logged.String(), "msg=stopping") {
		t.Errorf("draal logged an end that was asked for:\n%s", logged.String())
	}
	waitFor(t, "the port of the iroh endpoint to be free", func() bool {
		again, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(bind))
		if err != nil {
			return false
		}
		again.Close()
		return true
	})
}
