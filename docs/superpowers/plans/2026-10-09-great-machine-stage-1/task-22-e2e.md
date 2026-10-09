### Task 22: e2e — tests of the whole over go-iroh

**Files:**
- Create: `e2e/doc.go` (the package comment; the package has no other code)
- Test: `e2e/harness_test.go` (a draal, its zathrases and an operator for one test), `e2e/e2e_test.go`

**Interfaces:**
- Consumes:
  - `node`: `node.NewKey() (node.Key, error)`, `(node.Key).ID() wire.ID`, `node.Listen(ctx, node.ListenConfig{Key, Local: true, Log}) (node.Listener, error)`, `(node.Listener).LocalAddr() netip.AddrPort`, `node.Dial(ctx, node.DialConfig{Key, Server, Addrs, Log}) (node.Conn, error)`.
  - `db`: `db.Open(path string) (*db.DB, error)`, `(*DB).Close() error`.
  - `authz`: `authz.NewAllowlist(operators []wire.ID) authz.Allowlist`.
  - `draal`: `draal.New(cfg draal.Config) *draal.Draal`, `(*Draal).Serve(ctx, l node.Listener) error` (nil when ctx is done), `draal.Config` with **`StoreDir`**, the field Task 16 adds to the contract: the directory a rollout's binary has to be in.
  - `operator`: `operator.New(conn node.Conn) *operator.Client`, `(*Client).Close`, `Rollout`, `Rollback`, `Watch`, `Logs`, `Shell`; `(*operator.Shell).Write`, `Read`, `Wait`, `Close`.
  - `supervisor`: `supervisor.New(cfg supervisor.Config) *supervisor.Supervisor`, `(*Supervisor).Run(ctx) error`, `(*Supervisor).State() wire.State`.
  - `logring`: `logring.New(size, maxLine int) *logring.Ring`.
  - `zathras`: `zathras.New(cfg zathras.Config) *zathras.Agent`, `(*Agent).Run(ctx) error`, `(*Agent).State() wire.State`.
  - `preload`: `preload.Client{URL string}`. `ptyshell`: `ptyshell.Config{}`. `health`: `health.Handler(phase func() wire.Phase) http.Handler`.
  - `testservice/build`: `build.Binary(tb testing.TB) string`; `testservice [--stdout TEXT] [--exit-after D] [--exit-code N]`.
  - `wire`: the messages, `wire.CodeOf`.
- Produces: nothing that another task uses. `go test ./e2e/` is what Task 23's CI runs with the other tests.

How the harness gets a binary past draal: a rollout's binary has to be under draal's store directory, and a test cannot write to `/nix/store`. The harness makes `<tmp>/store/0123456789abcdfghijklmnpqrsvwxyz-testservice/bin/testservice` and gives draal `StoreDir: <tmp>/store`. A zathras needs nothing for that: its supervisor runs the path it is sent, and the fake preload endpoint records what it is posted, which is `/nix/store/<name>` for every name of the closure.

The durations: reports every 50 ms, a zathras dials again after 50 to 200 ms, draal's `StableAfter` is 300 ms. The supervisors' `QuickExit` is an hour, so that a service that ends has always ended quickly, however long the machine took to start it; that is what makes the halting and the health check certain.

The tests run in parallel and take about 6 seconds together (about 10 one after the other, about 20 with `-race -count=3`).

- [ ] **Step 1: Write the package comment and the harness**

`e2e/doc.go`:

```go
// Package e2e holds the tests of the whole: a draal, several zathrases
// with real supervisors and real child processes, and the operator's
// client, in one process over go-iroh on the loopback interface. Only the
// sidecar's preload endpoint and the Nix store are stand-ins: the first is
// an HTTP server of the test, the second a temporary directory that draal
// is pointed at with Config.StoreDir.
//
// The package has no code but its tests.
package e2e
```

`e2e/harness_test.go`:

```go
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/health"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/preload"
	"github.com/fables-for-robots/great-machine/ptyshell"
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
}

// harness is a draal, its zathrases and an operator.
type harness struct {
	t        *testing.T
	opts     options
	storeDir string // what draal takes for /nix/store
	binary   string // testservice, inside storeDir
	dbPath   string
	draalKey node.Key
	opKey    node.Key
	pods     []*pod
	op       *operator.Client

	mu        sync.Mutex
	addr      netip.AddrPort // where draal listens now
	stopDraal func()
}

// pod is a zathras with all that belongs to it.
type pod struct {
	name    string
	id      wire.ID
	sup     *supervisor.Supervisor
	ring    *logring.Ring
	agent   *zathras.Agent
	health  *httptest.Server
	preload *httptest.Server

	mu        sync.Mutex
	conn      node.Conn // the latest connection to draal
	preloaded []string  // the bodies the preload endpoint was sent
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
	ctx, cancel := context.WithCancel(context.Background())
	l, err := node.Listen(ctx, node.ListenConfig{Key: h.draalKey, Local: true, Log: quiet})
	if err != nil {
		cancel()
		database.Close()
		h.t.Fatalf("node.Listen: %v", err)
	}
	d := draal.New(draal.Config{
		DB:             database,
		Authz:          authz.NewAllowlist([]wire.ID{h.opKey.ID()}),
		Store:          wire.StoreInfo{Server: h.draalKey.ID(), Prefix: "test/"},
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
	h.stopDraal = func() {
		once.Do(func() {
			cancel()
			if err := <-served; err != nil {
				h.t.Errorf("Serve: %v", err)
			}
			l.Close()
			database.Close()
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

// startPod starts a zathras: its supervisor, its agent with a key of its
// own, its health check and its preload endpoint.
func (h *harness) startPod(name string) *pod {
	h.t.Helper()
	key, err := node.NewKey()
	if err != nil {
		h.t.Fatalf("NewKey: %v", err)
	}
	p := &pod{name: name, id: key.ID(), ring: logring.New(400, 16<<10)}
	p.preload = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.preloaded = append(p.preloaded, string(body))
		p.mu.Unlock()
	}))
	p.sup = supervisor.New(supervisor.Config{
		Ring:        p.ring,
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
		Preload:     preload.Client{URL: p.preload.URL},
		Shell:       ptyshell.Config{},
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
		p.preload.Close()
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

// preloads returns everything the preload endpoint was sent.
func (p *pod) preloads() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.preloaded, "\n")
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
```

- [ ] **Step 2: Write the tests**

`e2e/e2e_test.go`:

```go
package e2e

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

func TestRolloutCompletes(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 3})
	info := h.rollout("--stdout", "hello")
	f := h.await(info, wire.RolloutCompleted)

	if svc := serviceOf(f); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
	for _, p := range h.pods {
		if got := p.preloads(); !strings.Contains(got, "/nix/store/"+storeName) {
			t.Errorf("the preload endpoint of %s was sent %q, want the closure", p.name, got)
		}
		st := p.agent.State()
		if st.Generation != info.Generation || st.Phase != wire.PhaseRunning || st.Binary != h.binary {
			t.Errorf("%s has the state %+v, want it to run generation %d", p.name, st, info.Generation)
		}
		if !alive(st.PID) {
			t.Errorf("the process %d of %s is not alive", st.PID, p.name)
		}
	}
}

func TestRolloutOfABinaryThatExitsHaltsAndIsRolledBack(t *testing.T) {
	t.Parallel()
	// A service counts as running the target when it has run for
	// StableAfter. On a machine that is busy with the tests of every
	// package, a program that exits ten milliseconds after its start can
	// take some hundred milliseconds to get there, so StableAfter is far
	// above that here, and the first rollout goes to all three at once to
	// make up for the time.
	h := start(t, options{zathrases: 3, stableAfter: 3 * time.Second})
	good := h.rolloutParallel(0)
	h.await(good, wire.RolloutCompleted)
	pids := map[wire.ID]int{}
	for _, p := range h.pods {
		pids[p.id] = p.agent.State().PID
	}

	bad := h.rollout("--exit-after", "10ms", "--exit-code", "3")
	f := h.await(bad, wire.RolloutHalted)
	if reason := rolloutOf(f).Reason; !strings.Contains(reason, "web-0") || !strings.Contains(reason, "status 3") {
		t.Errorf("the reason is %q, want the first zathras and its exit status in it", reason)
	}
	if got := h.pods[0].agent.State().Generation; got != bad.Generation {
		t.Errorf("web-0 has generation %d, want the one that failed, %d", got, bad.Generation)
	}
	for _, p := range h.pods[1:] {
		st := p.agent.State()
		if st.Generation != good.Generation || st.Phase != wire.PhaseRunning || st.PID != pids[p.id] {
			t.Errorf("%s has the state %+v, want it to run generation %d as before", p.name, st, good.Generation)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	back, err := h.op.Rollback(ctx, wire.RollbackRequest{Service: service, Parallel: 1})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if back.Generation != good.Generation {
		t.Fatalf("the rollback targets generation %d, want %d", back.Generation, good.Generation)
	}
	h.await(back, wire.RolloutCompleted)
	if st := h.pods[0].agent.State(); st.Generation != good.Generation || st.Phase != wire.PhaseRunning || !alive(st.PID) {
		t.Errorf("after the rollback web-0 has the state %+v, want it to run generation %d", st, good.Generation)
	}
}

func TestLogs(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.pods[0]
	h.await(h.rollout("--stdout", "hello"), wire.RolloutCompleted)

	// The ring holds what the service wrote, and the line of the
	// supervisor about its start.
	has := func(lines []wire.LogLine, stream, text string) bool {
		for _, line := range lines {
			if line.Stream == stream && strings.Contains(line.Text, text) {
				return true
			}
		}
		return false
	}
	var ring []wire.LogLine
	eventually(t, "the line of the service in the ring", func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		lines, err := h.op.Logs(ctx, p.id, false)
		if err != nil {
			t.Fatalf("Logs: %v", err)
		}
		ring = nil
		for line := range lines {
			ring = append(ring, line)
		}
		return has(ring, "stdout", "hello")
	})
	if !has(ring, "zathras", "started generation 1") {
		t.Errorf("the ring lacks the supervisor's line about the start: %+v", ring)
	}

	// With follow, what is written later arrives.
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	lines, err := h.op.Logs(ctx, p.id, true)
	if err != nil {
		t.Fatalf("Logs with follow: %v", err)
	}
	h.rollout("--stdout", "again")
	for line := range lines {
		if line.Stream == "stdout" && line.Text == "again" {
			return
		}
	}
	t.Fatal("the line of the second rollout did not arrive while following")
}

func TestShell(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	sh, err := h.op.Shell(ctx, h.pods[0].id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	// The terminal echoes the command, quotes and all; only its output
	// has the word in one piece.
	if _, err := sh.Write([]byte("echo mar''ker; exit 3\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	output, err := io.ReadAll(sh)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(output), "marker") {
		t.Errorf("the output is %q, want marker in it", output)
	}
	if status, err := sh.Wait(ctx); err != nil || status != 3 {
		t.Errorf("Wait = %d, %v; want 3", status, err)
	}
}

func TestWatch(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 3})
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	fleets, err := h.op.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	first := <-fleets
	zs := zathrasesOf(first)
	if len(zs) != 3 {
		t.Fatalf("the fleet has %d zathrases, want 3", len(zs))
	}
	for i, z := range zs {
		if z.Name != h.pods[i].name || z.ID != h.pods[i].id || z.State.Phase != wire.PhaseIdle || z.Connected.IsZero() {
			t.Errorf("zathras %d of the fleet is %+v, want %s (%s), idle", i, z, h.pods[i].name, h.pods[i].id)
		}
	}

	info := h.rollout()
	for f := range fleets {
		if r := rolloutOf(f); r != nil && r.ID == info.ID && r.State == wire.RolloutCompleted && runs(f, info.Generation, 3) {
			return
		}
	}
	t.Fatal("the watcher did not see every zathras run the generation and the rollout complete")
}

func TestAPeerThatIsNoOperatorIsRefused(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	key, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	stranger := h.operator(key)
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	if _, err := stranger.Watch(ctx); wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Watch = %v, want unauthorized", err)
	}
	_, err = stranger.Rollout(ctx, wire.RolloutRequest{
		Service: service, Binary: h.binary, Closure: []string{storeName}, Parallel: 1,
	})
	if wire.CodeOf(err) != wire.CodeUnauthorized {
		t.Errorf("Rollout = %v, want unauthorized", err)
	}
	if svc := serviceOf(h.fleetWhere("the fleet", func(wire.Fleet) bool { return true })); svc == nil || svc.Rollout != nil {
		t.Errorf("after a refused rollout the service is %+v, want it without a rollout", svc)
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.pods[0]
	if status, body := p.healthz(t); status != http.StatusServiceUnavailable || body != "idle" {
		t.Errorf("before any rollout /healthz = %d %q, want 503 idle", status, body)
	}

	h.await(h.rollout(), wire.RolloutCompleted)
	if status, body := p.healthz(t); status != http.StatusOK || body != "running" {
		t.Errorf("while the service runs /healthz = %d %q, want 200 running", status, body)
	}

	h.rollout("--exit-after", "10ms", "--exit-code", "3")
	eventually(t, "the health check to fail with backoff", func() bool {
		status, body := p.healthz(t)
		return status == http.StatusServiceUnavailable && body == "backoff"
	})
}

func TestAZathrasWhoseConnectionIsCutComesBack(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 2})
	p := h.pods[0]
	info := h.rollout()
	f := h.await(info, wire.RolloutCompleted)
	before, _ := zathrasOf(f, p.id)
	pid := p.agent.State().PID

	p.cut()
	f = h.fleetWhere("the zathras to be in the fleet again", func(f wire.Fleet) bool {
		z, ok := zathrasOf(f, p.id)
		return ok && z.Connected.After(before.Connected)
	})
	z, _ := zathrasOf(f, p.id)
	if z.Name != p.name || z.State.Generation != info.Generation {
		t.Errorf("the zathras came back as %+v, want %s with generation %d", z, p.name, info.Generation)
	}
	if st := p.agent.State(); st.PID != pid || st.Phase != wire.PhaseRunning || !alive(pid) {
		t.Errorf("after the cut the service is %+v, want it to run still as process %d", st, pid)
	}
}

func TestDraalRestartsInTheMiddleOfARollout(t *testing.T) {
	t.Parallel()
	// Settle is longer than a zathras takes to dial again, and every
	// zathras takes a second to be done, so that the rollout is under
	// way for three.
	h := start(t, options{zathrases: 3, settle: time.Second, stableAfter: time.Second})
	h.await(h.rollout(), wire.RolloutCompleted)

	info := h.rollout("--stdout", "second")
	h.fleetWhere("the first zathras to run the target", func(f wire.Fleet) bool {
		r := rolloutOf(f)
		if r == nil || r.ID != info.ID || r.State != wire.RolloutActivating {
			return false
		}
		for _, z := range zathrasesOf(f) {
			if z.State.Generation == info.Generation {
				return true
			}
		}
		return false
	})

	h.restartDraal()
	f := h.await(info, wire.RolloutCompleted)
	if !runs(f, info.Generation, 3) {
		t.Errorf("after the rollout the zathrases are %+v, want all three to run generation %d", zathrasesOf(f), info.Generation)
	}
	if svc := serviceOf(f); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
}
```

- [ ] **Step 3: Run the tests**

Everything they test is written, so they pass at once. A failure here is a fault in one of the packages, or between two of them, and the name of the test says where to look.

Run: `test -z "$(gofmt -l e2e/)" && go vet ./e2e/ && go test -count=1 ./e2e/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/e2e`) in about 6 seconds.

- [ ] **Step 4: See that they can fail, and that nothing is left behind**

Break the harness for a moment: in `e2e/harness_test.go`, in `startDraal`, change `StoreDir:       h.storeDir,` to `StoreDir:       "/nix/store",`.

Run: `go test -count=1 ./e2e/ -run TestRolloutCompletes`
Expected: FAIL — `Rollout: bad-request: the binary "…/store/0123456789abcdfghijklmnpqrsvwxyz-testservice/bin/testservice" is no path of a file under /nix/store/`

Change the line back to `StoreDir:       h.storeDir,`.

Run: `go test -race -count=3 ./e2e/ && ! pgrep -fl testservice`
Expected: PASS, and no line of `pgrep`: no test service is left running.

- [ ] **Step 5: Commit**

```bash
git add e2e/
git commit -m "E2E: tests of the whole over go-iroh on loopback"
```
