package cli

import (
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amber-store/jaccard-store/bucket/buckettest"
	jsnode "github.com/amber-store/jaccard-store/node"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
	irohkey "github.com/tmc/go-iroh/key"
)

// draalForTest is the configuration of a draal whose iroh endpoint stays
// on this machine, and whose store keeps its packs in a bucket in memory.
// The bucket sets the environment of the test, so a test that runs draal
// cannot be parallel.
func draalForTest(t *testing.T) draalConfig {
	t.Helper()
	return draalConfig{
		data:    t.TempDir(),
		objects: buckettest.New(t),
		local:   true,
	}
}

// startDraal runs draal in a goroutine, waits until it serves, as its
// log says, and returns the channel of what runDraal returns.
func startDraal(t *testing.T, ctx context.Context, cfg draalConfig, logged *buffer) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- runDraal(ctx, cfg, quietLog(logged)) }()
	waitFor(t, "draal to serve", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runDraal returned early: %v\n%s", err, logged.String())
		default:
		}
		return strings.Contains(logged.String(), "msg=serving")
	})
	return done
}

// TestRunDraalServesNoWebPage: draal serves its endpoint and no web
// page: it says nothing of one, and when the context of the command is
// done it stops with no error. The page is `great-machine web`'s, on the
// machine of an operator.
func TestRunDraalServesNoWebPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged buffer
	done := startDraal(t, ctx, draalForTest(t), &logged)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
	}
	if strings.Contains(logged.String(), "web page") {
		t.Errorf("draal logged about a web page, and serves none:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "msg=stopping") {
		t.Errorf("draal did not log that it stops:\n%s", logged.String())
	}
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
// service; a store path is pushed there with an operator's key; and the
// database of the store is beside draal's own.
func TestRunDraalServesTheStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := draalForTest(t)
	cfg.admins = []wire.ID{opKey.ID()}
	var logged buffer
	done := startDraal(t, ctx, cfg, &logged)
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

	for _, name := range []string{draalDBFile, "store.sqlite"} {
		if _, err := os.Stat(filepath.Join(cfg.data, name)); err != nil {
			t.Errorf("the data directory: %v", err)
		}
	}
	if !strings.Contains(logged.String(), "admins=1") {
		t.Errorf("the log does not say how many admins the command line has:\n%s", logged.String())
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
	cfg := draalForTest(t)
	cfg.admins = []wire.ID{opKey.ID()}
	held := &heldBucket{Bucket: cfg.objects, asked: make(chan struct{}, 16)}
	cfg.objects = held
	cfg.storeGrace = 100 * time.Millisecond
	var logged buffer
	done := startDraal(t, ctx, cfg, &logged)
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

// gatedBucket is a bucket whose URLs of a pull can be held back.
type gatedBucket struct {
	jsserver.Bucket

	mu   sync.Mutex
	hold chan struct{} // while set, PresignGet waits for it to be closed, or for its context
	// waiting gets a value for every PresignGet that begins to wait.
	waiting chan struct{}
}

// holdPulls makes every pull from now on wait until what it returns is
// called, or until the request is ended.
func (b *gatedBucket) holdPulls() (release func()) {
	hold := make(chan struct{})
	b.mu.Lock()
	b.hold = hold
	b.mu.Unlock()
	return sync.OnceFunc(func() { close(hold) })
}

func (b *gatedBucket) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	b.mu.Lock()
	hold := b.hold
	b.mu.Unlock()
	if hold != nil {
		b.waiting <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return b.Bucket.PresignGet(ctx, objectKey, ttl)
}

// dialStore connects to the store of a draal, for jaccard-store's
// protocol, with a key nobody knows: a pull is every peer's. The
// connection ends with the test.
func dialStore(t *testing.T, k node.Key, id wire.ID, addr netip.AddrPort) *jsnode.Conn {
	t.Helper()
	server, err := irohkey.ParseEndpointID(string(id))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    k.Iroh(),
		ALPN:   jswire.ALPN,
		Server: server,
		Addrs:  []netip.AddrPort{addr},
		Log:    quietLog(io.Discard),
	})
	if err != nil {
		t.Fatalf("dialing the store: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// askStore sends one request of jaccard-store's protocol on a stream of
// its own and returns what the store answered. An answer that is a
// refusal is returned as the answer, not as an error.
func askStore(conn *jsnode.Conn, wait time.Duration, req jswire.Request) (jswire.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	s, err := conn.OpenStreamConn(ctx)
	if err != nil {
		return jswire.Response{}, err
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(wait))
	if err := jswire.WriteFrame(s, req); err != nil {
		return jswire.Response{}, err
	}
	var resp jswire.Response
	err = jswire.ReadFrame(s, &resp)
	return resp, err
}

// TestRunDraalLetsARequestOfTheStoreEndWhenItIsToldToEnd: draal is told
// to end while a pull is under way. The store is stopped before the
// endpoint is closed, so the pull has its grace on a connection that is
// still there: its answer reaches the client, and draal returns when it
// has.
func TestRunDraalLetsARequestOfTheStoreEndWhenItIsToldToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := draalForTest(t)
	cfg.admins = []wire.ID{opKey.ID()}
	gated := &gatedBucket{Bucket: cfg.objects, waiting: make(chan struct{}, 16)}
	cfg.objects = gated
	// Longer than the test waits for anything: it does not pass by
	// waiting the grace out.
	cfg.storeGrace = 30 * time.Second
	var logged buffer
	done := startDraal(t, ctx, cfg, &logged)
	id, addr := served(t, &logged)

	if _, err := push.Push(context.Background(), push.Config{
		Key:     opKey,
		Server:  id,
		Addrs:   []netip.AddrPort{addr},
		Prefix:  wire.StorePrefix("web"),
		TempDir: t.TempDir(),
	}, []string{webStorePath(t)}); err != nil {
		t.Fatalf("the push: %v", err)
	}

	// Dialed with the key of the admin, one after the other: a pull is
	// nobody's who was granted nothing, and a key has one endpoint at a
	// time.
	pulling := dialStore(t, opKey, id, addr)
	idle := pulling
	// The store has the connection before draal is told to end.
	if _, err := askStore(idle, 20*time.Second, jswire.Request{Op: jswire.OpPull, Name: wire.StorePrefix("web") + "nothing"}); err != nil {
		t.Fatalf("asking on the connection that will be idle: %v", err)
	}
	release := gated.holdPulls()
	defer release()
	answer := make(chan error, 1)
	go func() {
		resp, err := askStore(pulling, 20*time.Second, jswire.Request{Op: jswire.OpPull, Name: wire.StorePrefix("web") + "abc-web"})
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		answer <- err
	}()
	select {
	case <-gated.waiting:
	case err := <-answer:
		t.Fatalf("the pull ended before it was under way: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the pull to be under way")
	}

	cancel()
	// The store is being stopped when a connection of it that has
	// nothing under way is closed: no request is answered on it.
	waitFor(t, "the store to close the connection that has nothing under way", func() bool {
		_, err := askStore(idle, time.Second, jswire.Request{Op: jswire.OpPull, Name: "nothing"})
		return err != nil
	})
	select {
	case err := <-done:
		t.Fatalf("runDraal returned while a pull had its grace: %v", err)
	case err := <-answer:
		t.Fatalf("the pull under way was ended when draal was told to end: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-answer:
		if err != nil {
			t.Errorf("the pull that was under way when draal was told to end: %v, want its answer", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for the answer of the pull")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runDraal returned %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("runDraal did not return when the pull had ended\n%s", logged.String())
	}
	if strings.Contains(logged.String(), "stopping the store with requests under way") {
		t.Errorf("draal says that it stopped with a request under way, and the request had ended:\n%s", logged.String())
	}
}

// TestRunDraalStoreFails: a store that cannot be opened ends the command
// before anything is bound.
func TestRunDraalStoreFails(t *testing.T) {
	cfg := draalForTest(t)
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
	cfg := draalForTest(t)
	cfg.store.scratch = cfg.data
	run := func() wire.ID {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var logged buffer
		done := startDraal(t, ctx, cfg, &logged)
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
