package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/amber-store/jaccard-store/bucket/buckettest"
	jsserver "github.com/amber-store/jaccard-store/server"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/health"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/storeserver"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/fables-for-robots/great-machine/zathras"
)

const (
	// deadline is how long a test waits for something that is to happen.
	deadline = 30 * time.Second
	// service is the service of every zathras of a harness.
	service = "web"
	// storeName is the name of the store path the service is in.
	storeName = "0123456789abcdfghijklmnpqrsvwxyz-testservice"
)

// quiet is a logger that writes nothing.
var quiet = slog.New(slog.DiscardHandler)

// options is what a test asks of its harness.
type options struct {
	zathrases   int
	settle      time.Duration // 0: 10 ms
	stableAfter time.Duration // 0: 300 ms
	// store gives draal its store, over a bucket in memory. The bucket
	// sets the environment of the test, so a test that asks for it cannot
	// be parallel. Without it draal answers great-machine's protocol
	// alone, and a dial for the store's is refused.
	store bool
}

// harness is a draal, its zathrases and an operator.
type harness struct {
	t        *testing.T
	opts     options
	storeDir string // what draal takes for /nix/store
	binary   string // testservice, inside storeDir
	dbPath   string
	data     string          // the directory of the store's database
	bucket   jsserver.Bucket // the bucket of the store; nil without options.store
	draalKey node.Key
	opKey    node.Key
	pods     []*pod
	op       *operator.Client
	// podStore and podStart are what a pod that is started from now on
	// fetches a closure with and starts its processes with. nil: the pod
	// itself, which fetches nothing, and a start as it is. A test whose
	// pod has a real store sets them (storedir_test.go).
	podStore zathras.Preloader
	podStart func(cmd *exec.Cmd, start func() error) error

	mu        sync.Mutex
	addr      netip.AddrPort     // where draal listens now
	draal     *draal.Draal       // the draal that runs now
	store     *storeserver.Store // its store; nil without options.store
	stopDraal func()
}

// pod is a zathras with all that belongs to it.
type pod struct {
	name   string
	id     wire.ID
	sup    *supervisor.Supervisor
	ring   *logring.Ring
	agent  *zathras.Agent
	health *httptest.Server

	mu    sync.Mutex
	conn  node.Conn // the latest connection to draal
	away  bool      // it does not dial draal: see keepAway
	asked []string  // the store paths the store was asked to fetch
}

// start builds testservice into a store of the test, starts a draal, the
// zathrases and an operator, and waits until every zathras has
// registered. Everything is stopped when the test ends.
func start(t *testing.T, opts options) *harness {
	t.Helper()
	if opts.settle == 0 {
		opts.settle = 10 * time.Millisecond
	}
	if opts.stableAfter == 0 {
		opts.stableAfter = 300 * time.Millisecond
	}
	dir := t.TempDir()
	h := &harness{
		t:        t,
		opts:     opts,
		storeDir: filepath.Join(dir, "store"),
		dbPath:   filepath.Join(dir, "draal.sqlite"),
		data:     filepath.Join(dir, "data"),
	}
	if opts.store {
		h.bucket = buckettest.New(t)
	}
	h.binary = filepath.Join(h.storeDir, storeName, "bin", "testservice")
	if err := os.MkdirAll(filepath.Dir(h.binary), 0o755); err != nil {
		t.Fatalf("making the store: %v", err)
	}
	program, err := os.ReadFile(build.Binary(t))
	if err != nil {
		t.Fatalf("reading testservice: %v", err)
	}
	if err := os.WriteFile(h.binary, program, 0o755); err != nil {
		t.Fatalf("writing testservice into the store: %v", err)
	}
	if h.draalKey, err = node.NewKey(); err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	if h.opKey, err = node.NewKey(); err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	h.startDraal()
	t.Cleanup(func() { h.stopDraal() })
	for i := range opts.zathrases {
		h.pods = append(h.pods, h.startPod(fmt.Sprintf("web-%d", i)))
	}
	h.op = h.operator(h.opKey)
	h.fleetWhere("every zathras to register", func(f wire.Fleet) bool {
		return len(zathrasesOf(f)) == opts.zathrases
	})
	return h
}

// startDraal opens the database and serves a new draal on a new endpoint
// with draal's key.
func (h *harness) startDraal() {
	h.t.Helper()
	database, err := db.Open(h.dbPath)
	if err != nil {
		h.t.Fatalf("db.Open: %v", err)
	}
	allow := authz.NewAllowlist([]wire.ID{h.opKey.ID()})
	// The store is draal's own, answered on draal's endpoint, as the
	// command puts the two together.
	var store *storeserver.Store
	var handlers map[string]func(node.Conn)
	if h.opts.store {
		store, err = storeserver.Open(context.Background(), storeserver.Config{Dir: h.data, Objects: h.bucket, Authz: allow, Log: quiet})
		if err != nil {
			database.Close()
			h.t.Fatalf("storeserver.Open: %v", err)
		}
		handlers = map[string]func(node.Conn){storeserver.ALPN: store.ServeConn}
	}
	// What was opened is closed again, in the order of the command: the
	// store is stopped, the endpoint closed, and then the databases.
	closeAll := func(l node.Listener) {
		if store != nil {
			store.Stop()
		}
		if l != nil {
			l.Close()
		}
		if store != nil {
			store.Close()
		}
		database.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := node.Listen(ctx, node.ListenConfig{Key: h.draalKey, Local: true, Handlers: handlers, Log: quiet})
	if err != nil {
		cancel()
		closeAll(nil)
		h.t.Fatalf("node.Listen: %v", err)
	}
	d := draal.New(draal.Config{
		DB:             database,
		Authz:          allow,
		ID:             h.draalKey.ID(),
		StoreDir:       h.storeDir,
		Settle:         h.opts.settle,
		StableAfter:    h.opts.stableAfter,
		PrepareTimeout: time.Minute,
		RetryAfter:     200 * time.Millisecond,
		GoneAfter:      2 * time.Second,
		WatchEvery:     20 * time.Millisecond,
		History:        time.Minute,
		Log:            quiet,
	})
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx, l) }()

	var once sync.Once
	h.mu.Lock()
	h.addr = l.LocalAddr()
	h.draal = d
	h.store = store
	h.stopDraal = func() {
		once.Do(func() {
			cancel()
			if err := <-served; err != nil {
				h.t.Errorf("Serve: %v", err)
			}
			closeAll(l)
		})
	}
	h.mu.Unlock()
}

// restartDraal stops draal and starts another on the same database, and
// gives the harness a new operator, as the connection of the old one
// ended with the draal it was to.
func (h *harness) restartDraal() {
	h.t.Helper()
	h.stopDraal()
	h.startDraal()
	h.t.Cleanup(h.stopDraal)
	h.op = h.operator(h.opKey)
}

// dial connects to draal, where it listens now, with the key given.
func (h *harness) dial(ctx context.Context, key node.Key) (node.Conn, error) {
	h.mu.Lock()
	addr := h.addr
	h.mu.Unlock()
	return node.Dial(ctx, node.DialConfig{
		Key:    key,
		Server: h.draalKey.ID(),
		Addrs:  []netip.AddrPort{addr},
		Log:    quiet,
	})
}

// operator returns a client of draal with the key given. It is closed
// when the test ends.
func (h *harness) operator(key node.Key) *operator.Client {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	conn, err := h.dial(ctx, key)
	if err != nil {
		h.t.Fatalf("dialing draal: %v", err)
	}
	c := operator.New(conn)
	h.t.Cleanup(func() { c.Close() })
	return c
}

// Preload is the pod as the store of its zathras: it fetches nothing, and
// notes what it was asked to fetch. The store of the tests is a directory
// that is there from the start.
func (p *pod) Preload(ctx context.Context, names []string) error {
	p.mu.Lock()
	p.asked = append(p.asked, names...)
	p.mu.Unlock()
	return nil
}

// startPod starts a zathras: its supervisor, its agent with a key of its
// own, its health check and its store.
func (h *harness) startPod(name string) *pod {
	h.t.Helper()
	key, err := node.NewKey()
	if err != nil {
		h.t.Fatalf("NewKey: %v", err)
	}
	p := &pod{name: name, id: key.ID(), ring: logring.New(400, 16<<10)}
	var store zathras.Preloader = p
	if h.podStore != nil {
		store = h.podStore
	}
	p.sup = supervisor.New(supervisor.Config{
		Ring:        p.ring,
		Start:       h.podStart,
		StopTimeout: 5 * time.Second,
		// A service of these tests that ends has ended quickly, however
		// long the machine took to start it.
		QuickExit:  time.Hour,
		BackoffMin: 100 * time.Millisecond,
		BackoffMax: 200 * time.Millisecond,
		Log:        quiet,
	})
	p.health = httptest.NewServer(health.Handler(func() wire.Phase { return p.sup.State().Phase }))
	p.agent = zathras.New(zathras.Config{
		Service: service,
		Name:    name,
		Version: "e2e",
		Dial: func(ctx context.Context) (node.Conn, error) {
			p.mu.Lock()
			away := p.away
			p.mu.Unlock()
			if away {
				return nil, errors.New("the test keeps this zathras away from draal")
			}
			conn, err := h.dial(ctx, key)
			if err == nil {
				p.mu.Lock()
				p.conn = conn
				p.mu.Unlock()
			}
			return conn, err
		},
		Supervisor:  p.sup,
		Ring:        p.ring,
		Preload:     store,
		Shell:       ptyshell.Config{Start: h.podStart},
		ReportEvery: 50 * time.Millisecond,
		RedialMin:   50 * time.Millisecond,
		RedialMax:   200 * time.Millisecond,
		Log:         quiet,
	})

	// The supervisor runs before the agent can be sent an activate, and
	// both are waited for at the end, so that no process is left behind.
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() { p.sup.Run(ctx) })
	running.Go(func() { p.agent.Run(ctx) })
	h.t.Cleanup(func() {
		cancel()
		running.Wait()
		p.health.Close()
	})
	return p
}

// cut closes the connection the zathras has to draal now.
func (p *pod) cut() {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// keepAway closes the connection the zathras has to draal, and keeps it
// from dialing draal again until comeBack.
func (p *pod) keepAway() {
	p.mu.Lock()
	p.away = true
	p.mu.Unlock()
	p.cut()
}

// comeBack lets the zathras dial draal again.
func (p *pod) comeBack() {
	p.mu.Lock()
	p.away = false
	p.mu.Unlock()
}

// fetched returns the store paths the store of the zathras was asked to
// fetch, by their names.
func (p *pod) fetched() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.asked)
}

// healthz asks the health check, and returns the status and the body.
func (p *pod) healthz(t *testing.T) (int, string) {
	t.Helper()
	resp, err := http.Get(p.health.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

// rollout rolls testservice out with the arguments given, one zathras at
// a time.
func (h *harness) rollout(args ...string) wire.RolloutInfo {
	h.t.Helper()
	return h.rolloutParallel(1, args...)
}

// rolloutParallel is rollout with so many zathrases at a time; 0 is all
// of them at once.
func (h *harness) rolloutParallel(parallel int, args ...string) wire.RolloutInfo {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	info, err := h.op.Rollout(ctx, wire.RolloutRequest{
		Service:  service,
		Binary:   h.binary,
		Args:     args,
		Closure:  []string{storeName},
		Parallel: parallel,
	})
	if err != nil {
		h.t.Fatalf("Rollout: %v", err)
	}
	return info
}

// fleetWhere watches the fleet until it meets cond, and returns it.
func (h *harness) fleetWhere(what string, cond func(wire.Fleet) bool) wire.Fleet {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	fleets, err := h.op.Watch(ctx)
	if err != nil {
		h.t.Fatalf("Watch: %v", err)
	}
	var last wire.Fleet
	for f := range fleets {
		if cond(f) {
			return f
		}
		last = f
	}
	h.t.Fatalf("timed out waiting for %s; the last fleet was %+v", what, last)
	return wire.Fleet{}
}

// await waits until the rollout is in the state given, and returns the
// fleet that shows it.
func (h *harness) await(info wire.RolloutInfo, state wire.RolloutState) wire.Fleet {
	h.t.Helper()
	return h.fleetWhere(fmt.Sprintf("rollout %d to be %s", info.ID, state), func(f wire.Fleet) bool {
		r := rolloutOf(f)
		return r != nil && r.ID == info.ID && r.State == state
	})
}

// serviceOf returns the service of the harness in a fleet, or nil.
func serviceOf(f wire.Fleet) *wire.Service {
	for i := range f.Services {
		if f.Services[i].Name == service {
			return &f.Services[i]
		}
	}
	return nil
}

// rolloutOf returns the last rollout of the service in a fleet, or nil.
func rolloutOf(f wire.Fleet) *wire.RolloutInfo {
	if svc := serviceOf(f); svc != nil {
		return svc.Rollout
	}
	return nil
}

// zathrasesOf returns the zathrases of the service in a fleet.
func zathrasesOf(f wire.Fleet) []wire.Zathras {
	if svc := serviceOf(f); svc != nil {
		return svc.Zathrases
	}
	return nil
}

// zathrasOf returns a zathras in a fleet, and whether it is there.
func zathrasOf(f wire.Fleet, id wire.ID) (wire.Zathras, bool) {
	for _, z := range zathrasesOf(f) {
		if z.ID == id {
			return z, true
		}
	}
	return wire.Zathras{}, false
}

// runs says whether every zathras of a fleet runs the generation.
func runs(f wire.Fleet, generation uint64, want int) bool {
	zs := zathrasesOf(f)
	if len(zs) != want {
		return false
	}
	for _, z := range zs {
		if z.State.Generation != generation || z.State.Phase != wire.PhaseRunning {
			return false
		}
	}
	return true
}

// eventually polls cond until it holds, and fails the test with what when
// it does not within the deadline.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// alive says whether a process is there and no zombie.
func alive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // a system without /proc
	}
	// The state is the field behind the name, which is in parentheses.
	i := bytes.LastIndexByte(stat, ')')
	return i < 0 || i+2 >= len(stat) || stat[i+2] != 'Z'
}
