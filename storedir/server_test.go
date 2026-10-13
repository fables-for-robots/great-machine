package storedir

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/amber-store/core/packstore"
	"github.com/amber-store/jaccard-store/bucket/buckettest"
	"github.com/amber-store/jaccard-store/client"
	jsdb "github.com/amber-store/jaccard-store/db"
	jsnode "github.com/amber-store/jaccard-store/node"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
)

// testTimeout is how long one step of a test may take before the test
// gives up. Nothing waits for it when all is well.
const testTimeout = 30 * time.Second

// quiet drops what the server, the endpoints and the store log.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// testServer is a jaccard-store server in the process of the test, as
// the tests of push have one: its database in a temporary directory, its
// bucket a fake S3 in memory, its endpoint on the loopback interface.
type testServer struct {
	t     *testing.T
	id    wire.ID
	addrs []netip.AddrPort
}

// startServer starts a server that is stopped and removed when the test
// ends. The bucket sets the environment of the test with t.Setenv, so a
// test that starts a server cannot be parallel.
func startServer(t *testing.T) *testServer {
	t.Helper()
	b := buckettest.New(t)
	dir := t.TempDir()

	database, err := jsdb.Open(filepath.Join(dir, "store.sqlite"))
	if err != nil {
		t.Fatalf("opening the server's database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	srv, err := jsserver.New(jsserver.Config{DB: database, Bucket: b, Scratch: filepath.Join(dir, "scratch"), Log: quiet})
	if err != nil {
		t.Fatalf("the server: %v", err)
	}
	sk, err := jsnode.LoadOrCreateKey(filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatalf("the server's key: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	ep, err := jsnode.Bind(ctx, jsnode.ServerConfig{Key: sk, ALPN: jswire.ALPN, Local: true, Log: quiet})
	if err != nil {
		stop()
		t.Fatalf("binding the server's endpoint: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.Serve(ctx, ep)
	}()
	// Cleanups run last in, first out: the serving ends before the
	// database under it is closed.
	t.Cleanup(func() {
		stop()
		<-served
		ep.Shutdown(context.Background())
	})
	return &testServer{t: t, id: wire.ID(ep.ID().String()), addrs: []netip.AddrPort{ep.LocalAddr()}}
}

// push pushes the store paths for the service, as a rollout of the
// service does.
func (s *testServer) push(service string, storePaths ...string) {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	cfg := push.Config{Key: k, Server: s.id, Addrs: s.addrs, Prefix: wire.StorePrefix(service), TempDir: s.t.TempDir()}
	if _, err := push.Push(ctx, cfg, storePaths); err != nil {
		s.t.Fatalf("pushing %v for %s: %v", storePaths, service, err)
	}
}

// dialer returns what a store of a test is given to dial the server
// with: a dial of jaccard-store's own, with a key of the test's. A
// zathras is given one that dials from the endpoint of its connection to
// draal.
func (s *testServer) dialer() func(context.Context) (node.Opener, error) {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	server, err := irohkey.ParseEndpointID(string(s.id))
	if err != nil {
		s.t.Fatal(err)
	}
	return func(ctx context.Context) (node.Opener, error) {
		return jsnode.Dial(ctx, jsnode.DialConfig{Key: k.Iroh(), ALPN: jswire.ALPN, Server: server, Addrs: s.addrs, Log: quiet})
	}
}

// tap is between a store and its connections: it counts what the store
// asks, and can hold it up or fail it.
type tap struct {
	mu        sync.Mutex
	dials     int
	failDials int                          // how many of the next dials fail
	closes    int                          // connections that were closed
	failPulls int                          // how many of the next pulls fail, on the connection they are asked on
	pulls     map[string]int               // by the name of the reference
	results   map[string]client.PullResult // of the pulls that worked
	running   int                          // pulls under way
	most      int                          // the most there were at a time
	ended     []string                     // the pulls whose context was done

	// gate, when it is set, holds every pull until it is closed or the
	// context of the pull is done.
	gate chan struct{}
	// started is told of every pull that begins.
	started chan string
	// linger is how long a pull takes at the least.
	linger time.Duration
	// blockDials makes every dial wait for the end of its context, and
	// fail with it: a store that does not answer.
	blockDials bool
}

func newTap() *tap {
	return &tap{pulls: map[string]int{}, results: map[string]client.PullResult{}, started: make(chan string, 256)}
}

// wrap is Config.wrapDial.
func (tp *tap) wrap(dial func(context.Context) (conn, error)) func(context.Context) (conn, error) {
	return func(ctx context.Context) (conn, error) {
		tp.mu.Lock()
		tp.dials++
		fail := tp.failDials > 0
		if fail {
			tp.failDials--
		}
		block := tp.blockDials
		tp.mu.Unlock()
		if block {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		if fail {
			return nil, os.ErrDeadlineExceeded
		}
		c, err := dial(ctx)
		if err != nil {
			return nil, err
		}
		return &tapped{conn: c, tp: tp}, nil
	}
}

// count returns how often the reference of name was pulled.
func (tp *tap) count(ref string) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.pulls[ref]
}

// dialed returns how often the store was dialed.
func (tp *tap) dialed() int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.dials
}

// closed returns how many connections were closed.
func (tp *tap) closed() int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.closes
}

// tapped is a connection that tells its tap what is asked of it.
type tapped struct {
	conn
	tp *tap
}

func (c *tapped) Close() error {
	c.tp.mu.Lock()
	c.tp.closes++
	c.tp.mu.Unlock()
	return c.conn.Close()
}

func (c *tapped) Pull(ctx context.Context, objects *packstore.Store, name string, opts client.PullOptions) (client.PullResult, error) {
	tp := c.tp
	tp.mu.Lock()
	tp.pulls[name]++
	tp.running++
	tp.most = max(tp.most, tp.running)
	gate, linger := tp.gate, tp.linger
	fail := tp.failPulls > 0
	if fail {
		tp.failPulls--
	}
	tp.mu.Unlock()
	defer func() {
		tp.mu.Lock()
		tp.running--
		tp.mu.Unlock()
	}()
	select {
	case tp.started <- name:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			tp.mu.Lock()
			tp.ended = append(tp.ended, name)
			tp.mu.Unlock()
			return client.PullResult{}, ctx.Err()
		}
	}
	if linger > 0 {
		time.Sleep(linger)
	}
	if fail {
		return client.PullResult{}, errors.New("the bucket is away")
	}
	res, err := c.conn.Pull(ctx, objects, name, opts)
	if err == nil {
		tp.mu.Lock()
		tp.results[name] = res
		tp.mu.Unlock()
	}
	return res, err
}
