package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/amber-store/jaccard-store/bucket/buckettest"
	jsserver "github.com/amber-store/jaccard-store/server"
	"github.com/coder/websocket"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// draalForTest is the configuration of a draal whose iroh endpoint stays
// on this machine, and whose store keeps its packs in a bucket in memory.
// The bucket sets the environment of the test, so a test that runs draal
// cannot be parallel.
func draalForTest(t *testing.T, webAddr string) draalConfig {
	t.Helper()
	return draalConfig{
		data:    t.TempDir(),
		objects: buckettest.New(t),
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

// served returns the endpoint ID of a draal that runs and the address its
// endpoint is bound to, as its log has them (servingInLog).
func served(t *testing.T, logged *buffer) (wire.ID, netip.AddrPort) {
	t.Helper()
	m := servingInLog.FindStringSubmatch(logged.String())
	if m == nil {
		t.Fatalf("draal did not log what it serves:\n%s", logged.String())
	}
	addr, err := netip.ParseAddrPort(m[2])
	if err != nil {
		t.Fatalf("the address in the log, %q: %v", m[2], err)
	}
	return wire.ID(m[1]), addr
}

// webStorePath makes a directory as a store path is one, with a program
// in it, and returns its path.
func webStorePath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "abc-web")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "web"), []byte("the program of web"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunDraalServesTheStore: draal is the store. Asked where the
// closures of a service go, it names itself and the prefix of the
// service; a store path pushed there with an operator's key is in the
// store, whose page is a part of draal's; and the database of the store
// is beside draal's own.
func TestRunDraalServesTheStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.operators = []wire.ID{opKey.ID()}
	var logged buffer
	url, done := startDraal(t, ctx, cfg, &logged)
	id, addr := served(t, &logged)

	asking, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	conn, err := node.Dial(asking, node.DialConfig{Key: opKey, Server: id, Addrs: []netip.AddrPort{addr}})
	if err != nil {
		t.Fatalf("dialing draal: %v", err)
	}
	client := operator.New(conn)
	store, err := client.Store(asking, "web")
	// The key of the operator has one endpoint at a time, as in rollout.
	client.Close()
	if want := (wire.StoreInfo{Server: id, Prefix: "/services/web/nix/store/"}); err != nil || store != want {
		t.Fatalf("Store = %+v, %v; want draal itself and the prefix of the service, %+v", store, err, want)
	}

	res, err := push.Push(asking, push.Config{
		Key:     opKey,
		Server:  store.Server,
		Addrs:   []netip.AddrPort{addr},
		Prefix:  store.Prefix,
		TempDir: t.TempDir(),
	}, []string{webStorePath(t)})
	if err != nil || res != (push.Result{Pushed: 1}) {
		t.Fatalf("Push to draal = %+v, %v; want one store path pushed", res, err)
	}

	get := func(path string) (int, string) {
		t.Helper()
		client := &http.Client{Timeout: 5 * time.Second}
		defer client.CloseIdleConnections()
		resp, err := client.Get(url + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		// The page of the store has a policy of its own, draal's page
		// another: an answer has one of them, never both, which a
		// browser would hold it to at once.
		if policies := resp.Header.Values("Content-Security-Policy"); len(policies) != 1 || !strings.Contains(policies[0], "default-src 'self'") {
			t.Errorf("GET %s: the policies are %q, want one that allows the page's own origin alone", path, policies)
		}
		return resp.StatusCode, string(body)
	}
	if status, body := get("store/api/refs"); status != http.StatusOK ||
		!strings.Contains(body, "/services/web/nix/store/abc-web") || !strings.Contains(body, string(opKey.ID())) {
		t.Errorf("GET store/api/refs = %d %s, want the reference that was pushed and who pushed it", status, body)
	}
	// The page of the store, with the script and the stylesheet it asks
	// for beside itself.
	if status, body := get("store/"); status != http.StatusOK || !strings.Contains(body, "jaccard-store") {
		t.Errorf("GET store/ = %d %q, want the page of the store", status, body)
	}
	for _, file := range []string{"store/app.js", "store/style.css"} {
		if status, _ := get(file); status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", file, status)
		}
	}
	for _, name := range []string{draalDBFile, "store.sqlite"} {
		if _, err := os.Stat(filepath.Join(cfg.data, name)); err != nil {
			t.Errorf("the data directory: %v", err)
		}
	}
	if !strings.Contains(logged.String(), "operators=1") {
		t.Errorf("the log does not say how many operators there are:\n%s", logged.String())
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
}

// heldBucket is a bucket that signs no upload: whoever asks waits until
// the request is ended.
type heldBucket struct {
	jsserver.Bucket
	asked chan struct{} // gets a value for every upload that is asked for
}

func (b *heldBucket) PresignPut(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	b.asked <- struct{}{}
	<-ctx.Done()
	return "", ctx.Err()
}

// TestRunDraalStopsWhileAPushIsUnderWay: draal is told to end while a
// store path is being pushed to it. The request under way has its grace
// and is then ended: draal returns with no error, and the push with one.
// Nothing waits for the other.
func TestRunDraalStopsWhileAPushIsUnderWay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.operators = []wire.ID{opKey.ID()}
	held := &heldBucket{Bucket: cfg.objects, asked: make(chan struct{}, 16)}
	cfg.objects = held
	cfg.storeGrace = 100 * time.Millisecond
	var logged buffer
	_, done := startDraal(t, ctx, cfg, &logged)
	id, addr := served(t, &logged)

	pushed := make(chan error, 1)
	go func() {
		_, err := push.Push(context.Background(), push.Config{
			Key:     opKey,
			Server:  id,
			Addrs:   []netip.AddrPort{addr},
			Prefix:  wire.StorePrefix("web"),
			TempDir: t.TempDir(),
		}, []string{webStorePath(t)})
		pushed <- err
	}()
	select {
	case <-held.asked:
	case err := <-pushed:
		t.Fatalf("the push ended before it was under way: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the push to be under way")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return while a push was under way\n%s", logged.String())
	}
	select {
	case err := <-pushed:
		if err == nil {
			t.Error("the push that draal ended reported no error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the push still waits for a draal that has stopped")
	}
	if !strings.Contains(logged.String(), "stopping the store with requests under way") {
		t.Errorf("draal does not say that it stopped with a request under way:\n%s", logged.String())
	}
}

// TestRunDraalStoreFails: a store that cannot be opened ends the command
// before anything is bound.
func TestRunDraalStoreFails(t *testing.T) {
	cfg := draalForTest(t, "127.0.0.1:0")
	// A file where the scratch directory of the store is to be.
	cfg.store.scratch = filepath.Join(t.TempDir(), "scratch")
	if err := os.WriteFile(cfg.store.scratch, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var logged buffer
	err := runDraal(context.Background(), cfg, quietLog(&logged))
	if err == nil || !strings.Contains(err.Error(), "the store: ") {
		t.Fatalf("runDraal returned %v, want an error of the store", err)
	}
	if strings.Contains(logged.String(), "msg=serving") {
		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
	}
}

// TestRunDraalWithoutARegion: the URLs of the store are signed for a
// region, which --s3-region gives or the AWS configuration has. A draal
// that has neither ends at its start, before anything is bound, and says
// what gives it one.
func TestRunDraalWithoutARegion(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	for _, name := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE"} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_CONFIG_FILE", absent)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", absent)
	cfg := draalConfig{data: t.TempDir(), local: true}
	cfg.store.bucket.Bucket = "packs"
	var logged buffer
	err := runDraal(context.Background(), cfg, quietLog(&logged))
	if err == nil {
		t.Fatal("runDraal returned nil for a bucket without a region")
	}
	for _, want := range []string{"the store: ", "no region", "--s3-region", "GREAT_MACHINE_S3_REGION", "AWS_REGION"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not have %q: %v", want, err)
		}
	}
	if strings.Contains(logged.String(), "msg=serving") {
		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
	}
}

// TestRunDraalWithAScratchThatIsItsDataDirectory: the store empties its
// scratch directory at every start. A draal that is told to keep
// everything in one place, --store-scratch being --data, loses nothing by
// it: its key and both databases are there after a run, and it is the
// same draal the next time.
func TestRunDraalWithAScratchThatIsItsDataDirectory(t *testing.T) {
	cfg := draalForTest(t, "127.0.0.1:0")
	cfg.store.scratch = cfg.data
	run := func() wire.ID {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var logged buffer
		_, done := startDraal(t, ctx, cfg, &logged)
		id, _ := served(t, &logged)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runDraal returned %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("runDraal did not return")
		}
		return id
	}
	first := run()
	for _, name := range []string{draalKeyFile, draalDBFile, "store.sqlite"} {
		if _, err := os.Stat(filepath.Join(cfg.data, name)); err != nil {
			t.Errorf("after one run of draal: %v", err)
		}
	}
	if second := run(); second != first {
		t.Errorf("draal was %s, and is %s after it was started once more", first, second)
	}
}
