### Task 16: draal — the controller

**Files:**
- Create: `draal/doc.go` (the package comment)
- Create: `draal/draal.go` (`Config`, `Shell`, `Controller`, `Draal`, `New`)
- Create: `draal/control.go` (`Store`, `ServiceOf`, `History`, `Rollouts`, `Rollout`, `Rollback` and what a rollout is refused for)
- Create: `draal/watch.go` (`Watch` and the fleet as it is sent)
- Create: `draal/serve.go` (`Serve`: the accept loop, the session of a zathras, the zathrases that are gone)
- Create: `draal/handler.go` (the handler of the protocol for an operator's requests)
- Create: `draal/relay.go` (`Logs` and `Shell`: streams to a zathras, passed on)
- Create: `draal/reconcile.go` (the reconciler loop, which carries out what `rollout.Decide` says)
- Test: `draal/control_test.go`, `draal/watch_test.go`, `draal/serve_test.go` (with the fake zathras), `draal/relay_test.go`, `draal/reconcile_test.go`

**Interfaces:**
- Consumes:
  - `wire`: every message of the contract; `wire.Read(r io.Reader, msg any) error`, `wire.Write(w io.Writer, msg any) error`, `wire.Call(s io.ReadWriter, req *wire.Request) (*wire.Response, error)`, `wire.ReadResponse(r io.Reader) (*wire.Response, error)`, `wire.WriteError(w io.Writer, err error) error`, `wire.Errorf(code wire.Code, format string, args ...any) *wire.Error`, `wire.CodeOf(err error) wire.Code`, `wire.ValidService(name string) error`, `(wire.RolloutState).UnderWay() bool`.
  - `node`: `node.Listener` (`Accept(ctx) (node.Conn, error)`), `node.Conn` (`RemoteID() wire.ID`, `OpenStream(ctx) (node.Stream, error)`, `AcceptStream(ctx) (node.Stream, error)`, `Done() <-chan struct{}`, `Close() error`), `node.Stream` (`io.Reader`, `io.Writer`, `CloseWrite() error`, `Close() error`). A stream is seen by the peer once its opener has written. `Stream.Close` ends the sending in order and cancels the reading: the peer reads what was written and then `io.EOF`. `Conn.Close` loses what has not arrived.
  - `node/nodetest` (tests): `nodetest.New() *nodetest.Net`, `(*Net).Listen(id wire.ID) node.Listener`, `(*Net).Dial(ctx, from, to wire.ID) (node.Conn, error)`, `nodetest.ID(n int) wire.ID`.
  - `db`: `db.Open(path string) (*db.DB, error)`, `(*DB).Close() error`, `(*DB).Services(ctx) ([]db.Service, error)`, `(*DB).Service(ctx, name string) (db.Service, error)`, `(*DB).Generation(ctx, service string, number uint64) (db.Generation, error)`, `(*DB).NewRollout(ctx, req wire.RolloutRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)`, `(*DB).NewRollback(ctx, req wire.RollbackRequest, by wire.ID, now time.Time) (wire.RolloutInfo, error)`, `(*DB).LastRollout(ctx, service string) (wire.RolloutInfo, bool, error)`, `(*DB).Rollouts(ctx, service string, limit int) ([]wire.RolloutInfo, error)`, `(*DB).SetRolloutState(ctx, id uint64, state wire.RolloutState, reason string, now time.Time) (bool, error)`, `db.ErrNotFound`, `db.ErrNoStable`, `db.ErrNothingToRollBack` (found with `errors.Is`).
  - `authz`: `authz.Authorizer` (`Authorize(peer wire.ID, op authz.Operation, service string) error`), the operations `authz.OpRegister`, `OpStore`, `OpRollout`, `OpRollback`, `OpWatch`, `OpRollouts`, `OpHistory`, `OpLogs`, `OpShell`; in tests `authz.NewAllowlist(operators []wire.ID) authz.Allowlist`.
  - `fleet`: `fleet.New(history time.Duration) *fleet.Registry`, `fleet.Member{ID, Service, Name, Connected, State, Reported}`, `(*Registry).Join(m fleet.Member) (epoch uint64)`, `Report(id wire.ID, epoch uint64, st wire.State, now time.Time) bool`, `Leave(id wire.ID, epoch uint64) bool`, `Get(id wire.ID) (fleet.Member, bool)`, `Service(name string) []fleet.Member`, `Services() []string`, `History(id wire.ID) []wire.Sample`, `Stale(now time.Time, after time.Duration) []wire.ID`, `Subscribe() (<-chan struct{}, func())`.
  - `rollout`: `rollout.Decide(in rollout.Input) rollout.Decision`, `rollout.Input`, `rollout.Failure{Generation, Activate, Reason, At}`, `rollout.Command{Zathras, Activate, Generation}`, `rollout.Decision{Commands, State, Reason, Prepared, Activated, Awaited, Recheck}`.
- Produces: the contract's `draal` section: `draal.Config`, `draal.Shell`, `draal.Controller`, `draal.Draal`, `draal.New(cfg draal.Config) *draal.Draal`, `(*Draal).Serve(ctx context.Context, l node.Listener) error` and the methods of `Controller` on `*Draal`.
  - **One field more than the contract has: `Config.StoreDir string`** ("": `/nix/store`), the directory a rollout's binary has to be in. Task 22 points it at a temporary directory; nothing else sets it.
  - `Serve` returns nil when its context is done, having closed every connection, and the listener's error otherwise. It does not close the listener.
  - What counts as a failure of a command (`rollout.Failure`): an answer of the zathras that is a `*wire.Error` with another code than `unavailable`, with its `Message` as the reason and `Config.Now()` as `At`. `unavailable`, and a stream that ends without an answer, only take the command out of the books; the next decision sends it again if the zathras is still there.
  - A request about a zathras that is not connected is answered `unauthorized` to a peer that is no operator, and `unavailable` to an operator.
  - A prepare can reach a zathras twice: its answer and the report that lists the generation come on two streams, and when the answer is first, the next decision still sees a zathras that has not prepared. The tests allow for it.

The task has five cycles: the operator's requests that need no zathras; `Watch`; the connections and the protocol; logs and shells; the reconciler. All commands run in the repository's root. The tests of this package take about five seconds in all.

- [ ] **Step 1: Write the failing tests of the requests that need no zathras**

`draal/control_test.go`:

```go
package draal

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// The endpoint IDs of the tests.
var (
	draalID    = nodetest.ID(1)
	operatorID = nodetest.ID(2)
	strangerID = nodetest.ID(3)
)

// deadline is how long a test waits for something that is to happen.
const deadline = 10 * time.Second

// eventually polls cond until it holds, and fails the test with what when
// it does not within the deadline.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// openDB opens a database in a directory of the test.
func openDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// testConfig returns a Config with a database of the test, the operator
// of the tests and short durations.
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DB:             openDB(t, filepath.Join(t.TempDir(), "draal.sqlite")),
		Authz:          authz.NewAllowlist([]wire.ID{operatorID}),
		Store:          wire.StoreInfo{Server: nodetest.ID(99), Prefix: "nix/"},
		Settle:         time.Millisecond,
		StableAfter:    50 * time.Millisecond,
		PrepareTimeout: time.Minute,
		RetryAfter:     50 * time.Millisecond,
		GoneAfter:      5 * time.Second,
		WatchEvery:     time.Millisecond,
		History:        time.Minute,
		Log:            slog.New(slog.DiscardHandler),
	}
}

// webRollout returns a rollout of the service web that draal accepts.
func webRollout(parallel int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  "web",
		Binary:   "/nix/store/abc-web/bin/web",
		Args:     []string{"--port", "80"},
		Closure:  []string{"abc-web", "def-libc"},
		Parallel: parallel,
	}
}

func TestStore(t *testing.T) {
	cfg := testConfig(t)
	d := New(cfg)
	got, err := d.Store(context.Background())
	if err != nil || got != cfg.Store {
		t.Fatalf("Store = %+v, %v; want %+v", got, err, cfg.Store)
	}
}

func TestRolloutIsRecorded(t *testing.T) {
	cfg := testConfig(t)
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return at }
	d := New(cfg)
	ctx := context.Background()

	info, err := d.Rollout(ctx, operatorID, webRollout(2))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if info.Service != "web" || info.Generation != 1 || info.Parallel != 2 ||
		info.State != wire.RolloutPreparing || info.CreatedBy != operatorID || !info.Created.Equal(at) {
		t.Errorf("Rollout = %+v", info)
	}
	gen, err := cfg.DB.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if gen.Binary != "/nix/store/abc-web/bin/web" || strings.Join(gen.Args, " ") != "--port 80" ||
		strings.Join(gen.Closure, " ") != "abc-web def-libc" {
		t.Errorf("generation = %+v", gen)
	}

	again, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("second Rollout: %v", err)
	}
	if again.Generation != 2 || again.ID == info.ID {
		t.Errorf("second Rollout = %+v, after %+v", again, info)
	}
}

func TestRolloutRefusals(t *testing.T) {
	change := func(f func(*wire.RolloutRequest)) wire.RolloutRequest {
		req := webRollout(1)
		f(&req)
		return req
	}
	cases := []struct {
		name string
		req  wire.RolloutRequest
	}{
		{"no service name", change(func(r *wire.RolloutRequest) { r.Service = "" })},
		{"a service name with a capital", change(func(r *wire.RolloutRequest) { r.Service = "Web" })},
		{"a service name with a slash", change(func(r *wire.RolloutRequest) { r.Service = "a/b" })},
		{"no binary", change(func(r *wire.RolloutRequest) { r.Binary = "" })},
		{"a relative binary", change(func(r *wire.RolloutRequest) { r.Binary = "nix/store/abc-web/bin/web" })},
		{"a binary outside the store", change(func(r *wire.RolloutRequest) { r.Binary = "/usr/bin/web" })},
		{"a binary that leaves the store", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web/../../../bin/sh" })},
		{"a binary that is the store", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/" })},
		{"a binary that is a store path", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web" })},
		{"a binary that is a store path and a slash", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web/" })},
		{"no closure", change(func(r *wire.RolloutRequest) { r.Closure = nil })},
		{"a closure without the binary's store path", change(func(r *wire.RolloutRequest) { r.Closure = []string{"def-libc"} })},
		{"a path in the closure", change(func(r *wire.RolloutRequest) { r.Closure = []string{"abc-web", "/nix/store/def-libc"} })},
		{"parallel below 0", change(func(r *wire.RolloutRequest) { r.Parallel = -1 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			d := New(cfg)
			_, err := d.Rollout(context.Background(), operatorID, tc.req)
			if wire.CodeOf(err) != wire.CodeBadRequest {
				t.Fatalf("Rollout = %v, want a bad-request", err)
			}
			services, err := cfg.DB.Services(context.Background())
			if err != nil || len(services) != 0 {
				t.Fatalf("after a refusal the database has %v, %v; want nothing", services, err)
			}
		})
	}
}

func TestRolloutStoreDir(t *testing.T) {
	cfg := testConfig(t)
	cfg.StoreDir = "/tmp/store"
	d := New(cfg)
	req := webRollout(1)
	if _, err := d.Rollout(context.Background(), operatorID, req); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("Rollout of %s with the store at /tmp/store = %v, want a bad-request", req.Binary, err)
	}
	req.Binary = "/tmp/store/abc-web/bin/web"
	if _, err := d.Rollout(context.Background(), operatorID, req); err != nil {
		t.Fatalf("Rollout of %s with the store at /tmp/store: %v", req.Binary, err)
	}
}

func TestRollback(t *testing.T) {
	cfg := testConfig(t)
	d := New(cfg)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		req  wire.RollbackRequest
		want wire.Code
	}{
		{"no service name", wire.RollbackRequest{Service: "", Parallel: 1}, wire.CodeBadRequest},
		{"parallel below 0", wire.RollbackRequest{Service: "web", Parallel: -1}, wire.CodeBadRequest},
		{"a service that is not there", wire.RollbackRequest{Service: "web", Parallel: 1}, wire.CodeNotFound},
	} {
		if _, err := d.Rollback(ctx, operatorID, tc.req); wire.CodeOf(err) != tc.want {
			t.Errorf("Rollback with %s = %v, want %s", tc.name, err, tc.want)
		}
	}

	first, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if _, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web"}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollback without a stable generation = %v, want a bad-request", err)
	}
	if ok, err := cfg.DB.SetRolloutState(ctx, first.ID, wire.RolloutCompleted, "", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}
	if _, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web"}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollback after a rollout that completed = %v, want a bad-request", err)
	}

	second, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if ok, err := cfg.DB.SetRolloutState(ctx, second.ID, wire.RolloutHalted, "web-1: no", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}
	back, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web", Parallel: 3})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if back.Generation != first.Generation || back.Parallel != 3 || back.State != wire.RolloutPreparing || back.CreatedBy != operatorID {
		t.Errorf("Rollback = %+v, want a rollout to generation %d", back, first.Generation)
	}
}

func TestRollouts(t *testing.T) {
	d := New(testConfig(t))
	ctx := context.Background()

	if _, err := d.Rollouts(ctx, "No Name"); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollouts of a bad name = %v, want a bad-request", err)
	}
	if list, err := d.Rollouts(ctx, "web"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts of a service without any = %v, %v", list, err)
	}
	var last wire.RolloutInfo
	for range 22 {
		var err error
		if last, err = d.Rollout(ctx, operatorID, webRollout(1)); err != nil {
			t.Fatalf("Rollout: %v", err)
		}
	}
	list, err := d.Rollouts(ctx, "web")
	if err != nil {
		t.Fatalf("Rollouts: %v", err)
	}
	if len(list) != 20 {
		t.Fatalf("Rollouts returned %d, want the last 20", len(list))
	}
	if list[0].ID != last.ID || list[0].Generation != 22 || list[19].Generation != 3 {
		t.Errorf("Rollouts go from generation %d to %d, want from 22 to 3", list[0].Generation, list[19].Generation)
	}
}

func TestOfAZathrasThatIsNotConnected(t *testing.T) {
	d := New(testConfig(t))
	if service, ok := d.ServiceOf(nodetest.ID(10)); ok {
		t.Errorf("ServiceOf = %q, true for a zathras that is not connected", service)
	}
	if _, err := d.History(context.Background(), nodetest.ID(10)); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("History = %v, want unavailable", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./draal/`
Expected: FAIL — the package does not build: `undefined: Config`, `undefined: New`

- [ ] **Step 3: Write the types and the requests**

`draal/doc.go`:

```go
// Package draal is the controller: it accepts the connections of the
// zathrases and of the operators, keeps the fleet, carries out what
// rollout.Decide says, and answers the operator's requests.
//
// What an operator can do is the interface Controller, which *Draal
// implements. The handler of the protocol is written against those methods
// alone, and so is the HTTP API of stage 2.
package draal
```

`draal/draal.go`:

```go
package draal

import (
	"context"
	"io"
	"log/slog"
	"path"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what a Draal is made of. The zero value of a duration stands
// for the value of the spec.
type Config struct {
	DB    *db.DB
	Authz authz.Authorizer
	Store wire.StoreInfo
	// StoreDir is the directory a rollout's binary has to be in. "":
	// /nix/store. The tests of the whole point it at a directory they can
	// write to.
	StoreDir       string
	Settle         time.Duration    // 0: 15 seconds
	StableAfter    time.Duration    // 0: 30 seconds
	PrepareTimeout time.Duration    // 0: 30 minutes
	RetryAfter     time.Duration    // 0: 30 seconds
	GoneAfter      time.Duration    // 0: 10 seconds
	WatchEvery     time.Duration    // 0: 1 second
	History        time.Duration    // 0: 15 minutes
	Now            func() time.Time // nil: time.Now
	Log            *slog.Logger     // nil: slog.Default()
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.StoreDir == "" {
		cfg.StoreDir = "/nix/store"
	}
	cfg.StoreDir = path.Clean(cfg.StoreDir)
	if cfg.Settle == 0 {
		cfg.Settle = 15 * time.Second
	}
	if cfg.StableAfter == 0 {
		cfg.StableAfter = 30 * time.Second
	}
	if cfg.PrepareTimeout == 0 {
		cfg.PrepareTimeout = 30 * time.Minute
	}
	if cfg.RetryAfter == 0 {
		cfg.RetryAfter = 30 * time.Second
	}
	if cfg.GoneAfter == 0 {
		cfg.GoneAfter = 10 * time.Second
	}
	if cfg.WatchEvery == 0 {
		cfg.WatchEvery = time.Second
	}
	if cfg.History == 0 {
		cfg.History = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// Shell is a shell session as an operator has it.
type Shell interface {
	io.Reader // output; io.EOF when the shell has ended
	io.Writer // input
	// Resize tells the shell the new size of the terminal.
	Resize(cols, rows uint16) error
	// Wait returns the exit status when the shell has ended. The status
	// comes behind the output, so Wait returns only once Read has reached
	// the end.
	Wait(ctx context.Context) (int, error)
	// Close ends the session, and the shell if it runs.
	Close() error
}

// Controller is what an operator can do. The handler of the protocol and,
// in stage 2, the HTTP API are written against it.
type Controller interface {
	// Store returns the jaccard-store server and the prefix that closures
	// are pushed to.
	Store(ctx context.Context) (wire.StoreInfo, error)
	// Rollout records a new generation of a service and the rollout to it.
	Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error)
	// Rollback records a rollout to the stable generation of a service.
	Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error)
	// Watch sends the fleet at once and after every change, at most once
	// in WatchEvery, until ctx is done, and then closes the channel.
	Watch(ctx context.Context) (<-chan wire.Fleet, error)
	// Rollouts returns the last 20 rollouts of a service, newest first.
	Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)
	// History returns the samples kept of a connected zathras, oldest first.
	History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)
	// Logs sends the ring of the zathras, and with follow what follows,
	// and closes the channel at the end or when ctx is done.
	Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	// Shell starts a shell in the container of the zathras. ctx bounds the
	// start alone; the session lasts until its Close.
	Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error)
	// ServiceOf returns the service of a connected zathras, for Authorize.
	ServiceOf(zathras wire.ID) (string, bool)
}

// Draal is the controller. It is a Controller.
type Draal struct {
	cfg   Config
	log   *slog.Logger
	fleet *fleet.Registry

	mu       sync.Mutex
	sessions map[wire.ID]*session       // the zathrases that are connected
	tallies  map[string]tally           // by service: the counts of the last decision
	subs     map[chan struct{}]struct{} // who is told that something changed
}

// session is the connection of a registered zathras, on which draal opens
// the streams of its commands, logs and shells.
type session struct {
	id      wire.ID
	service string
	conn    node.Conn
}

// tally is the counts of the last decision about a rollout.
type tally struct {
	rollout                      uint64
	prepared, activated, awaited int
}

// New returns a Draal. Its methods of Controller work at once; zathrases
// are connected, and rollouts carried out, while Serve runs.
func New(cfg Config) *Draal {
	cfg = cfg.withDefaults()
	return &Draal{
		cfg:      cfg,
		log:      cfg.Log,
		fleet:    fleet.New(cfg.History),
		sessions: map[wire.ID]*session{},
		tallies:  map[string]tally{},
		subs:     map[chan struct{}]struct{}{},
	}
}

// subscribe returns a channel with room for one value that gets one
// whenever changed is called, and a function that ends the subscription.
func (d *Draal) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	d.mu.Lock()
	d.subs[ch] = struct{}{}
	d.mu.Unlock()
	return ch, func() {
		d.mu.Lock()
		delete(d.subs, ch)
		d.mu.Unlock()
	}
}

// changed tells the reconciler and every watcher that a rollout was
// recorded or has moved on.
func (d *Draal) changed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch := range d.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// session returns the session of a connected zathras, or nil.
func (d *Draal) session(id wire.ID) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[id]
}
```

`draal/control.go`:

```go
package draal

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/wire"
)

// rolloutsListed is how many rollouts of a service Rollouts returns.
const rolloutsListed = 20

// Store returns the jaccard-store server and the prefix draal was given.
func (d *Draal) Store(ctx context.Context) (wire.StoreInfo, error) {
	return d.cfg.Store, nil
}

// ServiceOf returns the service of a connected zathras.
func (d *Draal) ServiceOf(zathras wire.ID) (string, bool) {
	m, ok := d.fleet.Get(zathras)
	return m.Service, ok
}

// History returns the samples kept of a connected zathras, oldest first,
// and unavailable for a zathras that is not connected.
func (d *Draal) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	if _, ok := d.fleet.Get(zathras); !ok {
		return nil, notConnected(zathras)
	}
	return d.fleet.History(zathras), nil
}

// Rollouts returns the last 20 rollouts of a service, newest first. A
// service that never had one has none.
func (d *Draal) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	if err := wire.ValidService(service); err != nil {
		return nil, err
	}
	list, err := d.cfg.DB.Rollouts(ctx, service, rolloutsListed)
	if err != nil {
		return nil, wire.Errorf(wire.CodeInternal, "reading the rollouts of %s: %v", service, err)
	}
	return list, nil
}

// Rollout checks the request, records a new generation and the rollout to
// it, and returns the rollout. The reconciler carries it out.
func (d *Draal) Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	if err := d.checkRollout(req); err != nil {
		return wire.RolloutInfo{}, err
	}
	info, err := d.cfg.DB.NewRollout(ctx, req, by, d.cfg.Now())
	if err != nil {
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeInternal, "recording the rollout of %s: %v", req.Service, err)
	}
	d.log.Info("rollout recorded", "service", info.Service, "rollout", info.ID, "generation", info.Generation, "by", by)
	d.changed()
	return info, nil
}

// Rollback checks the request and records a rollout to the stable
// generation of the service.
func (d *Draal) Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	if err := wire.ValidService(req.Service); err != nil {
		return wire.RolloutInfo{}, err
	}
	if req.Parallel < 0 {
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "parallel is %d, and cannot be below 0", req.Parallel)
	}
	info, err := d.cfg.DB.NewRollback(ctx, req, by, d.cfg.Now())
	switch {
	case errors.Is(err, db.ErrNotFound):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeNotFound, "no service %q", req.Service)
	case errors.Is(err, db.ErrNoStable):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "service %q has no stable generation to roll back to", req.Service)
	case errors.Is(err, db.ErrNothingToRollBack):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "the last rollout of service %q completed: there is nothing to roll back", req.Service)
	case err != nil:
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeInternal, "recording the rollback of %s: %v", req.Service, err)
	}
	d.log.Info("rollback recorded", "service", info.Service, "rollout", info.ID, "generation", info.Generation, "by", by)
	d.changed()
	return info, nil
}

// checkRollout returns the bad-request a rollout is refused with, or nil.
func (d *Draal) checkRollout(req wire.RolloutRequest) error {
	if err := wire.ValidService(req.Service); err != nil {
		return err
	}
	name, err := storeName(d.cfg.StoreDir, req.Binary)
	if err != nil {
		return err
	}
	if len(req.Closure) == 0 {
		return wire.Errorf(wire.CodeBadRequest, "the closure is empty")
	}
	for _, n := range req.Closure {
		if strings.Contains(n, "/") {
			return wire.Errorf(wire.CodeBadRequest, "%q in the closure is no name of a store path", n)
		}
	}
	if !slices.Contains(req.Closure, name) {
		return wire.Errorf(wire.CodeBadRequest, "the closure does not hold %s, the store path of the binary", name)
	}
	if req.Parallel < 0 {
		return wire.Errorf(wire.CodeBadRequest, "parallel is %d, and cannot be below 0", req.Parallel)
	}
	return nil
}

// storeName returns the name of the store path that binary is in. It
// refuses a binary that is not an absolute path under storeDir with a
// file name behind the store path.
func storeName(storeDir, binary string) (string, error) {
	rest, ok := strings.CutPrefix(binary, storeDir+"/")
	if !ok || path.Clean(binary) != binary {
		return "", wire.Errorf(wire.CodeBadRequest, "the binary %q is no path of a file under %s/", binary, storeDir)
	}
	name, file, _ := strings.Cut(rest, "/")
	if name == "" || file == "" {
		return "", wire.Errorf(wire.CodeBadRequest, "the binary %q names no file inside a store path", binary)
	}
	return name, nil
}

// notConnected is the answer for a zathras draal has no connection of.
func notConnected(zathras wire.ID) *wire.Error {
	return wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", zathras)
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./draal/ && go test ./draal/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/draal`)

- [ ] **Step 5: Commit**

```bash
git add draal/
git commit -m "Draal: the controller's types, rollout and rollback"
```

- [ ] **Step 6: Write the failing tests of Watch**

`draal/watch_test.go`:

```go
package draal

import (
	"context"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// nextFleet reads one fleet of a watcher.
func nextFleet(t *testing.T, fleets <-chan wire.Fleet) wire.Fleet {
	t.Helper()
	select {
	case f, ok := <-fleets:
		if !ok {
			t.Fatal("the watcher's channel was closed")
		}
		return f
	case <-time.After(deadline):
		t.Fatal("timed out waiting for a fleet")
	}
	return wire.Fleet{}
}

// fleetWhere reads fleets of a watcher until one meets cond, and returns
// it and how many were read.
func fleetWhere(t *testing.T, fleets <-chan wire.Fleet, cond func(wire.Fleet) bool) (wire.Fleet, int) {
	t.Helper()
	for n := 1; ; n++ {
		if f := nextFleet(t, fleets); cond(f) {
			return f, n
		}
	}
}

// rolloutOf records a rollout of a service of the given name.
func rolloutOf(t *testing.T, d *Draal, service string) wire.RolloutInfo {
	t.Helper()
	req := webRollout(1)
	req.Service = service
	info, err := d.Rollout(context.Background(), operatorID, req)
	if err != nil {
		t.Fatalf("Rollout of %s: %v", service, err)
	}
	return info
}

func TestWatchSendsAtOnceAndAfterAChange(t *testing.T) {
	d := New(testConfig(t))
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := nextFleet(t, fleets); len(f.Services) != 0 {
		t.Fatalf("the first fleet = %+v, want no service", f)
	}

	rolloutOf(t, d, "web")
	info := rolloutOf(t, d, "api")
	f, _ := fleetWhere(t, fleets, func(f wire.Fleet) bool { return len(f.Services) == 2 })
	if f.Services[0].Name != "api" || f.Services[1].Name != "web" {
		t.Fatalf("services = %q, %q; want api, web", f.Services[0].Name, f.Services[1].Name)
	}
	api := f.Services[0]
	if api.Stable != 0 || api.Rollout == nil || api.Rollout.ID != info.ID || api.Rollout.State != wire.RolloutPreparing {
		t.Errorf("api = %+v with rollout %+v", api, api.Rollout)
	}
	if api.Rollout.Prepared != 0 || api.Rollout.Activated != 0 || api.Rollout.Awaited != 0 {
		t.Errorf("the counts of a rollout nobody decided about = %+v, want zeros", api.Rollout)
	}
	if len(api.Zathrases) != 0 {
		t.Errorf("zathrases = %v, want none", api.Zathrases)
	}
}

func TestWatchNotMoreOftenThanWatchEvery(t *testing.T) {
	cfg := testConfig(t)
	cfg.WatchEvery = 300 * time.Millisecond
	d := New(cfg)

	start := time.Now()
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextFleet(t, fleets)
	rolloutOf(t, d, "a")
	rolloutOf(t, d, "b")
	rolloutOf(t, d, "c")
	f := nextFleet(t, fleets)
	if took := time.Since(start); took < cfg.WatchEvery {
		t.Errorf("the second fleet came after %v, sooner than WatchEvery (%v)", took, cfg.WatchEvery)
	}
	if len(f.Services) != 3 {
		t.Errorf("the second fleet has %d services, want the three changes in one", len(f.Services))
	}
}

func TestWatchSlowWatcherHoldsNobodyUp(t *testing.T) {
	d := New(testConfig(t))
	slow, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	quick, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Ten changes, each of which the quick watcher sees while the slow
	// one reads nothing.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for i, name := range names {
		rolloutOf(t, d, name)
		fleetWhere(t, quick, func(f wire.Fleet) bool { return len(f.Services) == i+1 })
	}

	// The slow one gets the newest, not the ten between.
	_, read := fleetWhere(t, slow, func(f wire.Fleet) bool { return len(f.Services) == len(names) })
	if read > 3 {
		t.Errorf("the slow watcher read %d fleets before the newest, want it to miss those between", read)
	}
}

func TestWatchClosesWithTheContext(t *testing.T) {
	d := New(testConfig(t))
	ctx, cancel := context.WithCancel(t.Context())
	fleets, err := d.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextFleet(t, fleets)
	cancel()
	select {
	case f, ok := <-fleets:
		if ok {
			// A fleet that was under way may still come; the end follows.
			if _, ok := <-fleets; ok {
				t.Fatalf("after the context was done the channel gave %+v and stayed open", f)
			}
		}
	case <-time.After(deadline):
		t.Fatal("the channel was not closed when the context was done")
	}
}
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `go test ./draal/`
Expected: FAIL — `d.Watch undefined (type *Draal has no field or method Watch)`

- [ ] **Step 8: Write Watch**

Every watcher has a goroutine and a channel with room for one fleet of its own, so one that does not read holds nobody up: its fleet is replaced by the newest.

`draal/watch.go`:

```go
package draal

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/wire"
)

// Watch sends the fleet at once and after every change, at most once in
// WatchEvery, until ctx is done, and then closes the channel. A watcher
// that does not read misses the fleets between: what it reads next is the
// newest. No watcher waits for another.
func (d *Draal) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	members, stopMembers := d.fleet.Subscribe()
	rollouts, stopRollouts := d.subscribe()
	first, err := d.snapshot(ctx)
	if err != nil {
		stopMembers()
		stopRollouts()
		return nil, wire.Errorf(wire.CodeInternal, "reading the fleet: %v", err)
	}
	out := make(chan wire.Fleet, 1)
	out <- first
	go func() {
		defer close(out)
		defer stopMembers()
		defer stopRollouts()
		pause := time.NewTimer(d.cfg.WatchEvery)
		defer pause.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-members:
			case <-rollouts:
			}
			// Not before WatchEvery has passed since the fleet before.
			select {
			case <-ctx.Done():
				return
			case <-pause.C:
			}
			f, err := d.snapshot(ctx)
			if err != nil {
				if ctx.Err() == nil {
					d.log.Error("reading the fleet for a watcher", "error", err)
				}
			} else {
				// This goroutine alone sends: once the fleet that
				// nobody read is taken out, there is room.
				select {
				case out <- f:
				default:
					select {
					case <-out:
					default:
					}
					out <- f
				}
			}
			pause.Reset(d.cfg.WatchEvery)
		}
	}()
	return out, nil
}

// serviceNames returns the names of the services in the database and of
// those with members, sorted.
func (d *Draal) serviceNames(ctx context.Context) ([]string, error) {
	rows, err := d.cfg.DB.Services(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	for _, name := range d.fleet.Services() {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// snapshot builds the fleet as it is now.
func (d *Draal) snapshot(ctx context.Context) (wire.Fleet, error) {
	names, err := d.serviceNames(ctx)
	if err != nil {
		return wire.Fleet{}, err
	}
	f := wire.Fleet{Services: make([]wire.Service, 0, len(names))}
	for _, name := range names {
		svc := wire.Service{Name: name, Zathrases: []wire.Zathras{}}
		last, ok, err := d.cfg.DB.LastRollout(ctx, name)
		if err != nil {
			return wire.Fleet{}, err
		}
		// The stable generation is read after the rollout, so that a
		// rollout that shows as completed never comes with the stable
		// generation of before.
		row, err := d.cfg.DB.Service(ctx, name)
		switch {
		case err == nil:
			svc.Stable = row.Stable
		case !errors.Is(err, db.ErrNotFound):
			return wire.Fleet{}, err
		}
		if ok {
			d.mu.Lock()
			t := d.tallies[name]
			d.mu.Unlock()
			if t.rollout == last.ID {
				last.Prepared, last.Activated, last.Awaited = t.prepared, t.activated, t.awaited
			}
			svc.Rollout = &last
		}
		for _, m := range d.fleet.Service(name) {
			svc.Zathrases = append(svc.Zathrases, wire.Zathras{ID: m.ID, Name: m.Name, Connected: m.Connected, State: m.State})
		}
		f.Services = append(f.Services, svc)
	}
	return f, nil
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go vet ./draal/ && go test -race ./draal/`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add draal/
git commit -m "Draal: Watch, the fleet at once and after every change"
```

- [ ] **Step 11: Write the fake zathras and the failing tests of the connections**

The fake zathras of this file is used by every test that follows: it dials, registers, reports every 5 ms and answers `prepare`, `activate`, `logs` and `shell` by functions a test sets.

`draal/serve_test.go`:

```go
package draal

import (
	"context"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// world is a draal that serves on a network in memory.
type world struct {
	t    *testing.T
	net  *nodetest.Net
	cfg  Config
	d    *Draal
	stop func() // ends Serve and waits for it; may be called twice

	mu     sync.Mutex
	events []string // what the fake zathrases were sent, in order
}

// startWorld makes a draal out of cfg and serves it as draalID.
func startWorld(t *testing.T, cfg Config) *world {
	t.Helper()
	w := &world{t: t, net: nodetest.New(), cfg: cfg}
	w.serve(cfg)
	return w
}

// serve starts a new draal out of cfg. The one before has to be stopped.
func (w *world) serve(cfg Config) {
	w.t.Helper()
	w.cfg = cfg
	w.d = New(cfg)
	l := w.net.Listen(draalID)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	d := w.d
	go func() { done <- d.Serve(ctx, l) }()
	var once sync.Once
	w.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				w.t.Errorf("Serve: %v", err)
			}
			l.Close()
		})
	}
	w.t.Cleanup(w.stop)
}

// note adds an event.
func (w *world) note(event string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
}

// noted returns the events so far.
func (w *world) noted() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.events)
}

// dial connects to draal as from.
func (w *world) dial(from wire.ID) node.Conn {
	w.t.Helper()
	conn, err := w.net.Dial(context.Background(), from, draalID)
	if err != nil {
		w.t.Fatalf("Dial: %v", err)
	}
	w.t.Cleanup(func() { conn.Close() })
	return conn
}

// ask opens a stream on conn, sends req and returns the stream and the
// first answer.
func ask(t *testing.T, conn node.Conn, req *wire.Request) (node.Stream, *wire.Response, error) {
	t.Helper()
	s, err := conn.OpenStream(context.Background())
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	resp, err := wire.Call(s, req)
	return s, resp, err
}

// fleetNow returns the fleet as a new watcher gets it at once.
func (w *world) fleetNow() wire.Fleet {
	w.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fleets, err := w.d.Watch(ctx)
	if err != nil {
		w.t.Fatalf("Watch: %v", err)
	}
	return nextFleet(w.t, fleets)
}

// service returns a service of the fleet as it is now, and whether it is
// there.
func (w *world) service(name string) (wire.Service, bool) {
	w.t.Helper()
	for _, svc := range w.fleetNow().Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return wire.Service{}, false
}

// member returns a zathras of the fleet as it is now.
func (w *world) member(service string, id wire.ID) (wire.Zathras, bool) {
	w.t.Helper()
	svc, _ := w.service(service)
	for _, z := range svc.Zathrases {
		if z.ID == id {
			return z, true
		}
	}
	return wire.Zathras{}, false
}

// fake is a zathras of the test. It registers, reports its state every
// few milliseconds and answers the streams draal opens.
type fake struct {
	t       *testing.T
	w       *world
	id      wire.ID
	service string
	name    string

	mu      sync.Mutex
	conn    node.Conn
	reg     node.Stream
	state   wire.State
	started time.Time // of the service, while the phase is running
	silent  bool      // sends no reports
	got     commands

	// What answers. nil: prepare and activate succeed, logs has no
	// lines, shell is refused.
	onPrepare  func(p wire.Prepare, s node.Stream) error
	onActivate func(a wire.Activate) error
	// afterActivate changes the state an activate has led to.
	afterActivate func(st *wire.State)
	onLogs        func(req wire.LogsRequest, s node.Stream)
	onShell       func(req wire.ShellRequest, s node.Stream)
}

// commands is what a fake zathras was sent, and when.
type commands struct {
	prepares    []wire.Prepare
	prepareAt   []time.Time
	activates   []wire.Activate
	activatedAt []time.Time
}

// idle returns the state of a zathras that runs nothing.
func idle() wire.State { return wire.State{Phase: wire.PhaseIdle} }

// running returns the state of a zathras that has run a generation for
// an hour.
func running(generation uint64) wire.State {
	return wire.State{
		Generation: generation,
		Binary:     "/nix/store/old/bin/web",
		Phase:      wire.PhaseRunning,
		PID:        4000,
		Prepared:   []uint64{generation},
	}
}

// newFake returns a zathras that is not connected yet.
func (w *world) newFake(n int, service, name string, state wire.State) *fake {
	return &fake{
		t: w.t, w: w, id: nodetest.ID(n), service: service, name: name,
		state: state, started: time.Now().Add(-time.Hour),
	}
}

// zathras returns a zathras that has registered.
func (w *world) zathras(n int, service, name string, state wire.State) *fake {
	w.t.Helper()
	f := w.newFake(n, service, name, state)
	f.connect()
	return f
}

// current returns the state as a report gives it.
func (f *fake) current() wire.State {
	st := f.state
	st.Prepared = slices.Clone(st.Prepared)
	if st.Phase == wire.PhaseRunning {
		st.RunningFor = time.Since(f.started)
	}
	return st
}

// connect dials draal, registers with the state the fake has, and starts
// to report and to answer. It may be called again when the connection of
// before has ended.
func (f *fake) connect() {
	f.t.Helper()
	conn := f.w.dial(f.id)
	f.mu.Lock()
	st := f.current()
	f.mu.Unlock()
	reg, resp, err := ask(f.t, conn, &wire.Request{Register: &wire.Register{
		Service: f.service, Name: f.name, Version: "test", State: st,
	}})
	if err != nil || resp.Registered == nil {
		f.t.Fatalf("register %s: %+v, %v", f.name, resp, err)
	}
	f.mu.Lock()
	f.conn, f.reg = conn, reg
	f.mu.Unlock()
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-conn.Done():
				return
			case <-tick.C:
				f.report()
			}
		}
	}()
	go func() {
		for {
			s, err := conn.AcceptStream(context.Background())
			if err != nil {
				return
			}
			go f.answer(s)
		}
	}()
}

// report sends the state, unless the fake is silent.
func (f *fake) report() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.silent || f.reg == nil {
		return
	}
	st := f.current()
	wire.Write(f.reg, &st)
}

// set changes the state, and reports it.
func (f *fake) set(change func(st *wire.State)) {
	f.mu.Lock()
	change(&f.state)
	f.mu.Unlock()
	f.report()
}

// sent returns the commands that came so far.
func (f *fake) sent() commands {
	f.mu.Lock()
	defer f.mu.Unlock()
	return commands{
		prepares:    slices.Clone(f.got.prepares),
		prepareAt:   slices.Clone(f.got.prepareAt),
		activates:   slices.Clone(f.got.activates),
		activatedAt: slices.Clone(f.got.activatedAt),
	}
}

// done ends an answer: what the fake sends ends, and it waits for draal
// to close the stream.
func done(s node.Stream) {
	s.CloseWrite()
	io.Copy(io.Discard, s)
}

// answer answers one stream that draal opened.
func (f *fake) answer(s node.Stream) {
	defer s.Close()
	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		return
	}
	f.mu.Lock()
	onLogs, onShell := f.onLogs, f.onShell
	f.mu.Unlock()
	switch {
	case req.Prepare != nil:
		p := *req.Prepare
		f.mu.Lock()
		f.got.prepares = append(f.got.prepares, p)
		f.got.prepareAt = append(f.got.prepareAt, time.Now())
		hook := f.onPrepare
		f.mu.Unlock()
		f.w.note("prepare " + f.name)
		if hook != nil {
			if err := hook(p, s); err != nil {
				wire.WriteError(s, err)
				done(s)
				return
			}
		}
		f.set(func(st *wire.State) {
			if !slices.Contains(st.Prepared, p.Generation) {
				st.Prepared = append(st.Prepared, p.Generation)
				slices.Sort(st.Prepared)
			}
		})
		wire.Write(s, &wire.Response{Prepared: &wire.Prepared{}})
		done(s)
	case req.Activate != nil:
		a := *req.Activate
		f.mu.Lock()
		f.got.activates = append(f.got.activates, a)
		f.got.activatedAt = append(f.got.activatedAt, time.Now())
		hook, after := f.onActivate, f.afterActivate
		f.mu.Unlock()
		f.w.note("activate " + f.name)
		if hook != nil {
			if err := hook(a); err != nil {
				wire.WriteError(s, err)
				done(s)
				return
			}
		}
		f.mu.Lock()
		f.started = time.Now()
		f.mu.Unlock()
		f.set(func(st *wire.State) {
			st.Generation, st.Binary, st.Args = a.Generation, a.Binary, a.Args
			st.Phase, st.PID = wire.PhaseRunning, 5000
			if after != nil {
				after(st)
			}
		})
		wire.Write(s, &wire.Response{Activated: &wire.Activated{}})
		done(s)
	case req.Logs != nil && onLogs != nil:
		onLogs(*req.Logs, s)
	case req.Shell != nil && onShell != nil:
		onShell(*req.Shell, s)
	case req.Shell != nil:
		wire.WriteError(s, wire.Errorf(wire.CodeInternal, "no shell here"))
		done(s)
	default:
		done(s)
	}
}

// closed says whether a connection has ended.
func closed(conn node.Conn) bool {
	select {
	case <-conn.Done():
		return true
	default:
		return false
	}
}

func TestRegisterWithABadServiceName(t *testing.T) {
	w := startWorld(t, testConfig(t))
	conn := w.dial(nodetest.ID(10))
	_, _, err := ask(t, conn, &wire.Request{Register: &wire.Register{Service: "Not A Name", Name: "x", State: idle()}})
	if wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("register = %v, want a bad-request", err)
	}
	if f := w.fleetNow(); len(f.Services) != 0 {
		t.Errorf("the fleet has %+v after a refused register", f.Services)
	}
}

func TestARegisteredZathrasIsInTheFleet(t *testing.T) {
	w := startWorld(t, testConfig(t))
	before := time.Now()
	z := w.zathras(10, "web", "web-1", idle())
	w.zathras(11, "web", "web-0", idle())
	w.zathras(12, "api", "api-0", idle())

	f := w.fleetNow()
	if len(f.Services) != 2 || f.Services[0].Name != "api" || f.Services[1].Name != "web" {
		t.Fatalf("services = %+v, want api and web", f.Services)
	}
	web := f.Services[1]
	if web.Stable != 0 || web.Rollout != nil {
		t.Errorf("web = %+v, want no stable generation and no rollout", web)
	}
	if len(web.Zathrases) != 2 || web.Zathrases[0].Name != "web-0" || web.Zathrases[1].Name != "web-1" {
		t.Fatalf("zathrases of web = %+v, want web-0 and web-1", web.Zathrases)
	}
	got := web.Zathrases[1]
	if got.ID != z.id || got.State.Phase != wire.PhaseIdle {
		t.Errorf("web-1 = %+v", got)
	}
	if got.Connected.Before(before.Add(-time.Second)) || got.Connected.After(time.Now().Add(time.Second)) {
		t.Errorf("web-1 connected at %v, want about %v", got.Connected, before)
	}
	if service, ok := w.d.ServiceOf(z.id); !ok || service != "web" {
		t.Errorf("ServiceOf = %q, %v; want web", service, ok)
	}

	// A report changes what the fleet shows, for a watcher that waits.
	fleets, err := w.d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	z.set(func(st *wire.State) { st.Restarts = 7 })
	fleetWhere(t, fleets, func(f wire.Fleet) bool {
		for _, svc := range f.Services {
			for _, m := range svc.Zathrases {
				if m.ID == z.id && m.State.Restarts == 7 {
					return true
				}
			}
		}
		return false
	})
}

func TestASecondConnectionReplacesTheFirst(t *testing.T) {
	w := startWorld(t, testConfig(t))
	first := w.zathras(10, "web", "old", idle())
	second := w.zathras(10, "web", "new", idle())

	eventually(t, "the first connection to be closed", func() bool { return closed(first.conn) })
	// Another zathras comes and goes, so that draal has had the time to
	// see the end of the first connection.
	other := w.zathras(11, "web", "other", idle())
	other.conn.Close()
	eventually(t, "the other zathras to be gone", func() bool {
		_, ok := w.member("web", other.id)
		return !ok
	})

	got, ok := w.member("web", second.id)
	if !ok || got.Name != "new" {
		t.Fatalf("after the first connection ended the fleet has %+v, %v; want the second", got, ok)
	}
	if closed(second.conn) {
		t.Fatal("the second connection was closed")
	}
	second.set(func(st *wire.State) { st.Restarts = 3 })
	eventually(t, "a report of the second connection", func() bool {
		got, _ := w.member("web", second.id)
		return got.State.Restarts == 3
	})
}

func TestAZathrasWithoutReportsIsDropped(t *testing.T) {
	cfg := testConfig(t)
	cfg.GoneAfter = 100 * time.Millisecond
	w := startWorld(t, cfg)
	quiet := w.newFake(10, "web", "quiet", idle())
	quiet.silent = true
	quiet.connect()
	loud := w.zathras(11, "web", "loud", idle())
	if _, ok := w.member("web", quiet.id); !ok {
		t.Fatal("the zathras is not in the fleet after it registered")
	}

	eventually(t, "the connection of the silent zathras to be closed", func() bool { return closed(quiet.conn) })
	eventually(t, "the silent zathras to leave the fleet", func() bool {
		_, ok := w.member("web", quiet.id)
		return !ok
	})
	if _, ok := w.member("web", loud.id); !ok || closed(loud.conn) {
		t.Error("the zathras that reports was dropped too")
	}
}

func TestAZathrasThatClosesItsConnectionIsDropped(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle())
	z.conn.Close()
	eventually(t, "the zathras to leave the fleet", func() bool {
		_, ok := w.member("web", z.id)
		return !ok
	})
	if _, ok := w.d.ServiceOf(z.id); ok {
		t.Error("ServiceOf still knows the zathras")
	}
	if _, err := w.d.History(context.Background(), z.id); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("History = %v, want unavailable", err)
	}
}

func TestHistory(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.newFake(10, "web", "web-1", idle())
	z.silent = true
	z.connect()
	z.mu.Lock()
	z.silent = false
	z.mu.Unlock()
	base := time.Now().Add(-10 * time.Second)
	for i := range 3 {
		z.set(func(st *wire.State) {
			st.Sample = &wire.Sample{At: base.Add(time.Duration(i) * time.Second), Memory: uint64(i + 1)}
		})
	}
	var samples []wire.Sample
	eventually(t, "three samples", func() bool {
		var err error
		samples, err = w.d.History(context.Background(), z.id)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		return len(samples) >= 3
	})
	for i := 1; i < len(samples); i++ {
		if samples[i].At.Before(samples[i-1].At) {
			t.Fatalf("samples are not oldest first: %v before %v", samples[i-1].At, samples[i].At)
		}
	}
	if samples[0].Memory != 1 {
		t.Errorf("the oldest sample has memory %d, want that of the first report", samples[0].Memory)
	}
}

// requests returns one request of every kind an operator sends, about
// the zathras given.
func requests(zathras wire.ID) map[string]*wire.Request {
	rollout := webRollout(1)
	return map[string]*wire.Request{
		"store":    {Store: &wire.StoreRequest{}},
		"rollout":  {Rollout: &rollout},
		"rollback": {Rollback: &wire.RollbackRequest{Service: "web"}},
		"watch":    {Watch: &wire.WatchRequest{}},
		"rollouts": {Rollouts: &wire.RolloutsRequest{Service: "web"}},
		"history":  {History: &wire.HistoryRequest{Zathras: zathras}},
		"logs":     {Logs: &wire.LogsRequest{Zathras: zathras}},
		"shell":    {Shell: &wire.ShellRequest{Zathras: zathras, Cols: 80, Rows: 24}},
	}
}

func TestAPeerThatIsNoOperatorIsRefused(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle())
	stranger := w.dial(strangerID)

	for name, req := range requests(z.id) {
		if _, _, err := ask(t, stranger, req); wire.CodeOf(err) != wire.CodeUnauthorized {
			t.Errorf("%s as a stranger = %v, want unauthorized", name, err)
		}
	}
	// Not even whether a zathras is connected is told.
	for name, req := range requests(nodetest.ID(77)) {
		if _, _, err := ask(t, stranger, req); wire.CodeOf(err) != wire.CodeUnauthorized {
			t.Errorf("%s of a zathras that is not there, as a stranger = %v, want unauthorized", name, err)
		}
	}
	if list, err := w.d.Rollouts(context.Background(), "web"); err != nil || len(list) != 0 {
		t.Errorf("a refused rollout was recorded: %v, %v", list, err)
	}
	// It may register all the same.
	_, resp, err := ask(t, stranger, &wire.Request{Register: &wire.Register{Service: "web", Name: "me", State: idle()}})
	if err != nil || resp.Registered == nil {
		t.Errorf("register as a stranger = %+v, %v", resp, err)
	}
}

func TestProtocolStore(t *testing.T) {
	cfg := testConfig(t)
	w := startWorld(t, cfg)
	_, resp, err := ask(t, w.dial(operatorID), &wire.Request{Store: &wire.StoreRequest{}})
	if err != nil || resp.Store == nil || *resp.Store != cfg.Store {
		t.Fatalf("store = %+v, %v; want %+v", resp, err, cfg.Store)
	}
}

func TestProtocolRolloutRollbackAndRollouts(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settle = time.Hour // nothing is carried out in this test
	w := startWorld(t, cfg)
	op := w.dial(operatorID)

	bad := webRollout(1)
	bad.Binary = "/bin/sh"
	if _, _, err := ask(t, op, &wire.Request{Rollout: &bad}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("rollout of /bin/sh = %v, want a bad-request", err)
	}
	good := webRollout(1)
	_, resp, err := ask(t, op, &wire.Request{Rollout: &good})
	if err != nil || resp.Rollout == nil {
		t.Fatalf("rollout = %+v, %v", resp, err)
	}
	if resp.Rollout.Generation != 1 || resp.Rollout.CreatedBy != operatorID || resp.Rollout.State != wire.RolloutPreparing {
		t.Errorf("rollout = %+v", resp.Rollout)
	}

	if _, _, err := ask(t, op, &wire.Request{Rollback: &wire.RollbackRequest{Service: "nothing"}}); wire.CodeOf(err) != wire.CodeNotFound {
		t.Errorf("rollback of a service that is not there = %v, want not-found", err)
	}
	if _, _, err := ask(t, op, &wire.Request{Rollback: &wire.RollbackRequest{Service: "web"}}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("rollback without a stable generation = %v, want a bad-request", err)
	}

	_, resp, err = ask(t, op, &wire.Request{Rollouts: &wire.RolloutsRequest{Service: "web"}})
	if err != nil || resp.Rollouts == nil || len(resp.Rollouts.Rollouts) != 1 || resp.Rollouts.Rollouts[0].Generation != 1 {
		t.Errorf("rollouts = %+v, %v; want the one rollout", resp, err)
	}
}

func TestProtocolWatch(t *testing.T) {
	cfg := testConfig(t)
	cfg.Settle = time.Hour
	w := startWorld(t, cfg)
	s, resp, err := ask(t, w.dial(operatorID), &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil || resp.Fleet == nil || len(resp.Fleet.Services) != 0 {
		t.Fatalf("watch = %+v, %v; want an empty fleet at once", resp, err)
	}
	z := w.zathras(10, "web", "web-1", idle())
	for {
		resp, err := wire.ReadResponse(s)
		if err != nil || resp.Fleet == nil {
			t.Fatalf("watch = %+v, %v; want another fleet", resp, err)
		}
		if f := resp.Fleet; len(f.Services) == 1 && len(f.Services[0].Zathrases) == 1 && f.Services[0].Zathrases[0].ID == z.id {
			break
		}
	}
	// The end of the stream ends the watcher.
	before := w.watchers()
	if before == 0 {
		t.Fatal("draal has no watcher while the stream is open")
	}
	s.Close()
	eventually(t, "the watcher to end with its stream", func() bool { return w.watchers() < before })
}

// watchers returns how many subscriptions draal has: one of the
// reconciler and one of every watcher.
func (w *world) watchers() int {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return len(w.d.subs)
}

func TestProtocolHistory(t *testing.T) {
	w := startWorld(t, testConfig(t))
	op := w.dial(operatorID)
	z := w.zathras(10, "web", "web-1", idle())
	z.set(func(st *wire.State) { st.Sample = &wire.Sample{At: time.Now(), Memory: 42} })
	eventually(t, "a sample over the protocol", func() bool {
		_, resp, err := ask(t, op, &wire.Request{History: &wire.HistoryRequest{Zathras: z.id}})
		if err != nil || resp.History == nil {
			t.Fatalf("history = %+v, %v", resp, err)
		}
		return len(resp.History.Samples) > 0 && resp.History.Samples[0].Memory == 42
	})
	if _, _, err := ask(t, op, &wire.Request{History: &wire.HistoryRequest{Zathras: nodetest.ID(77)}}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("history of a zathras that is not connected = %v, want unavailable", err)
	}
}

func TestProtocolRequestsDraalDoesNotAnswer(t *testing.T) {
	w := startWorld(t, testConfig(t))
	op := w.dial(operatorID)
	for name, req := range map[string]*wire.Request{
		"nothing": {},
		"prepare": {Prepare: &wire.Prepare{Generation: 1}},
	} {
		if _, _, err := ask(t, op, req); wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("a request with %s = %v, want a bad-request", name, err)
		}
	}
}

func TestServeEndsWithItsContext(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle())
	w.stop()
	eventually(t, "the connection of the zathras to be closed", func() bool { return closed(z.conn) })
}
```

- [ ] **Step 12: Run the tests to see them fail**

Run: `go test ./draal/`
Expected: FAIL — the package does not build: `d.Serve undefined (type *Draal has no field or method Serve)`

- [ ] **Step 13: Write Serve and the handler of the protocol**

`Serve` does not start the reconciler yet, and the handler does not answer `logs` and `shell` yet: Steps 18 and 23 add those.

`draal/serve.go`:

```go
package draal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// Serve accepts connections from l and runs the reconciler until ctx is
// done, and then returns nil, having closed every connection. It returns
// the error of l when l fails before that. Call it once.
func (d *Draal) Serve(ctx context.Context, l node.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { d.reap(ctx) })
	var err error
	for {
		var conn node.Conn
		conn, err = l.Accept(ctx)
		if err != nil {
			break
		}
		wg.Go(func() { d.serveConn(ctx, conn) })
	}
	stopped := ctx.Err() != nil
	cancel()
	wg.Wait()
	if stopped {
		return nil
	}
	return fmt.Errorf("draal: accepting a connection: %w", err)
}

// serveConn answers the streams of one connection, each a request, until
// the connection ends or ctx is done, and closes the connection.
func (d *Draal) serveConn(ctx context.Context, conn node.Conn) {
	var wg sync.WaitGroup
	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			break
		}
		wg.Go(func() { d.serveStream(ctx, conn, s) })
	}
	conn.Close()
	wg.Wait()
}

// serveStream reads the request of a stream and answers it.
func (d *Draal) serveStream(ctx context.Context, conn node.Conn, s node.Stream) {
	defer s.Close()
	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		d.log.Debug("reading a request", "peer", conn.RemoteID(), "error", err)
		return
	}
	if req.Register != nil {
		d.register(ctx, conn, s, req.Register)
		return
	}
	d.operate(ctx, conn.RemoteID(), s, &req)
}

// refuse answers a request with err, and waits for the peer to end the
// stream so that the answer is not lost with it.
func refuse(s node.Stream, err error) {
	wire.WriteError(s, err)
	s.CloseWrite()
	var rest [512]byte
	for {
		if _, err := s.Read(rest[:]); err != nil {
			return
		}
	}
}

// register is the session of a zathras: it joins the fleet, its reports
// are taken until the stream or the connection ends, and then it leaves.
func (d *Draal) register(ctx context.Context, conn node.Conn, s node.Stream, reg *wire.Register) {
	id := conn.RemoteID()
	if err := d.cfg.Authz.Authorize(id, authz.OpRegister, reg.Service); err != nil {
		d.log.Warn("refused", "peer", id, "operation", authz.OpRegister, "service", reg.Service)
		refuse(s, err)
		return
	}
	if err := wire.ValidService(reg.Service); err != nil {
		refuse(s, err)
		return
	}
	now := d.cfg.Now()
	sess := &session{id: id, service: reg.Service, conn: conn}
	// The fleet and the sessions change together, so that whoever sees
	// the member finds its connection.
	d.mu.Lock()
	epoch := d.fleet.Join(fleet.Member{
		ID: id, Service: reg.Service, Name: reg.Name,
		Connected: now, State: reg.State, Reported: now,
	})
	replaced := d.sessions[id]
	d.sessions[id] = sess
	d.mu.Unlock()
	if replaced != nil && replaced.conn != conn {
		d.log.Info("a zathras connected again: closing its connection of before", "zathras", id, "name", reg.Name)
		replaced.conn.Close()
	}
	d.log.Info("zathras registered", "zathras", id, "service", reg.Service, "name", reg.Name, "version", reg.Version)

	// A connection that ends is to end the reading, whatever the stream does.
	left := make(chan struct{})
	defer close(left)
	go func() {
		select {
		case <-conn.Done():
			s.Close()
		case <-left:
		}
	}()

	if err := wire.Write(s, &wire.Response{Registered: &wire.Registered{}}); err == nil {
		for {
			var st wire.State
			if err := wire.Read(s, &st); err != nil {
				break
			}
			d.fleet.Report(id, epoch, st, d.cfg.Now())
		}
	}

	d.fleet.Leave(id, epoch)
	d.mu.Lock()
	current := d.sessions[id] == sess
	if current {
		delete(d.sessions, id)
	}
	d.mu.Unlock()
	if current {
		// What is left of the connection is closed, which ends the
		// commands, logs and shells under way on it.
		conn.Close()
		d.log.Info("zathras gone", "zathras", id, "service", reg.Service, "name", reg.Name)
	}
}

// reap closes the connection of every zathras that has not reported for
// GoneAfter, which makes it leave.
func (d *Draal) reap(ctx context.Context) {
	tick := time.NewTicker(d.cfg.GoneAfter / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for _, id := range d.fleet.Stale(d.cfg.Now(), d.cfg.GoneAfter) {
			if sess := d.session(id); sess != nil {
				d.log.Info("no report of a zathras: closing its connection", "zathras", id, "after", d.cfg.GoneAfter)
				sess.conn.Close()
			}
		}
	}
}
```

`draal/handler.go`:

```go
package draal

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// admit maps a request of an operator to its operation and its service
// and asks Authorize. A refusal is logged. A zathras that is not connected
// is unavailable, for a peer that may ask at all.
func (d *Draal) admit(peer wire.ID, req *wire.Request) error {
	var (
		op      authz.Operation
		service string
		zathras *wire.ID
	)
	switch {
	case req.Store != nil:
		op = authz.OpStore
	case req.Rollout != nil:
		op, service = authz.OpRollout, req.Rollout.Service
	case req.Rollback != nil:
		op, service = authz.OpRollback, req.Rollback.Service
	case req.Watch != nil:
		op = authz.OpWatch
	case req.Rollouts != nil:
		op, service = authz.OpRollouts, req.Rollouts.Service
	case req.History != nil:
		op, zathras = authz.OpHistory, &req.History.Zathras
	case req.Logs != nil:
		op, zathras = authz.OpLogs, &req.Logs.Zathras
	case req.Shell != nil:
		op, zathras = authz.OpShell, &req.Shell.Zathras
	default:
		return wire.Errorf(wire.CodeBadRequest, "draal answers no such request")
	}
	connected := true
	if zathras != nil {
		service, connected = d.ServiceOf(*zathras)
	}
	if err := d.cfg.Authz.Authorize(peer, op, service); err != nil {
		d.log.Warn("refused", "peer", peer, "operation", op, "service", service)
		return err
	}
	if !connected {
		return notConnected(*zathras)
	}
	return nil
}

// operate answers one request of an operator through the methods of
// Controller, and returns when the peer has ended the stream.
func (d *Draal) operate(ctx context.Context, peer wire.ID, s node.Stream, req *wire.Request) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := d.admit(peer, req)
	// The peer writes nothing more: the end of what it sends, or an
	// error, is the end of the request.
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()
	// send writes one response, and ends the request when it cannot.
	send := func(resp *wire.Response) bool {
		if err := wire.Write(s, resp); err != nil {
			cancel()
			return false
		}
		return true
	}
	if err == nil {
		err = d.respond(ctx, peer, req, send)
	}
	if err != nil {
		wire.WriteError(s, err)
	}
	s.CloseWrite()
	<-ctx.Done()
}

// respond does what req asks and sends what answers it. An error it
// returns is the answer.
func (d *Draal) respond(ctx context.Context, peer wire.ID, req *wire.Request, send func(*wire.Response) bool) error {
	switch {
	case req.Store != nil:
		info, err := d.Store(ctx)
		if err != nil {
			return err
		}
		send(&wire.Response{Store: &info})
	case req.Rollout != nil:
		info, err := d.Rollout(ctx, peer, *req.Rollout)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Rollback != nil:
		info, err := d.Rollback(ctx, peer, *req.Rollback)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Watch != nil:
		fleets, err := d.Watch(ctx)
		if err != nil {
			return err
		}
		for f := range fleets {
			if !send(&wire.Response{Fleet: &f}) {
				break
			}
		}
	case req.Rollouts != nil:
		list, err := d.Rollouts(ctx, req.Rollouts.Service)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollouts: &wire.RolloutList{Rollouts: list}})
	case req.History != nil:
		samples, err := d.History(ctx, req.History.Zathras)
		if err != nil {
			return err
		}
		send(&wire.Response{History: &wire.SampleList{Samples: samples}})
	}
	return nil
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `go vet ./draal/ && go test -race ./draal/`
Expected: PASS

- [ ] **Step 15: Commit**

```bash
git add draal/
git commit -m "Draal: connections, the sessions of zathrases and the protocol"
```

- [ ] **Step 16: Write the failing tests of logs and shells**

`draal/relay_test.go`:

```go
package draal

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// logsOf returns what a fake zathras answers logs with: the lines, and
// then the end, or with follow nothing more until draal closes the
// stream. It tells of every request on asked and of every stream that
// draal closed on ended.
func logsOf(lines []wire.LogLine, asked chan<- wire.LogsRequest, ended chan<- struct{}) func(wire.LogsRequest, node.Stream) {
	return func(req wire.LogsRequest, s node.Stream) {
		asked <- req
		for _, line := range lines {
			wire.Write(s, &wire.Response{Line: &line})
		}
		if !req.Follow {
			s.CloseWrite()
		}
		io.Copy(io.Discard, s)
		ended <- struct{}{}
	}
}

// echoShell returns what a fake zathras answers shell with: a shell that
// answers input with "echo: " and the input, tells of changes of size,
// and exits with 3 on "exit\n".
func echoShell(asked chan<- wire.ShellRequest, resized chan<- wire.WinSize, ended chan<- struct{}) func(wire.ShellRequest, node.Stream) {
	return func(req wire.ShellRequest, s node.Stream) {
		asked <- req
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
		for {
			var in wire.ShellInput
			if err := wire.Read(s, &in); err != nil {
				ended <- struct{}{}
				return
			}
			if in.Resize != nil {
				resized <- *in.Resize
			}
			if string(in.Data) == "exit\n" {
				status := 3
				wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &status}})
				done(s)
				ended <- struct{}{}
				return
			}
			if len(in.Data) > 0 {
				wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: append([]byte("echo: "), in.Data...)}})
			}
		}
	}
}

// wait reads one value of a channel of the test.
func wait[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %s", what)
	}
	panic("not reached")
}

// someLines returns two lines and a note of lines that were lost.
func someLines() []wire.LogLine {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return []wire.LogLine{
		{At: at, Stream: "zathras", Text: "started generation 1"},
		{At: at.Add(time.Second), Stream: "zathras", Lost: 4},
		{At: at.Add(2 * time.Second), Stream: "stdout", Text: "hello"},
	}
}

// sameLine says whether two lines are the same.
func sameLine(a, b wire.LogLine) bool {
	return a.At.Equal(b.At) && a.Stream == b.Stream && a.Text == b.Text && a.Lost == b.Lost
}

func TestLogs(t *testing.T) {
	w := startWorld(t, testConfig(t))
	asked := make(chan wire.LogsRequest, 1)
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onLogs = logsOf(someLines(), asked, ended)
	z.connect()

	lines, err := w.d.Logs(t.Context(), z.id, false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if req := wait(t, "the request", asked); req.Zathras != "" || req.Follow {
		t.Errorf("the zathras was asked %+v, want no zathras named and no follow", req)
	}
	for i, want := range someLines() {
		if got := wait(t, "a line", lines); !sameLine(got, want) {
			t.Errorf("line %d = %+v, want %+v", i, got, want)
		}
	}
	select {
	case line, ok := <-lines:
		if ok {
			t.Errorf("after the last line came %+v, want the end", line)
		}
	case <-time.After(deadline):
		t.Fatal("the channel was not closed at the end of the stream")
	}
	wait(t, "draal to close the stream to the zathras", ended)
}

func TestLogsFollowEndsWithTheContext(t *testing.T) {
	w := startWorld(t, testConfig(t))
	asked := make(chan wire.LogsRequest, 1)
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onLogs = logsOf(someLines(), asked, ended)
	z.connect()

	ctx, cancel := context.WithCancel(t.Context())
	lines, err := w.d.Logs(ctx, z.id, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if req := wait(t, "the request", asked); !req.Follow {
		t.Errorf("the zathras was asked %+v, want follow", req)
	}
	for range someLines() {
		wait(t, "a line", lines)
	}
	select {
	case <-ended:
		t.Fatal("the stream to the zathras was closed while the operator follows")
	case line, ok := <-lines:
		t.Fatalf("the channel gave %+v, %v while nothing was written", line, ok)
	default:
	}
	cancel()
	wait(t, "draal to close the stream to the zathras", ended)
	if end := wait(t, "the end of the channel", receiveOrEnd(lines)); end.ok {
		t.Errorf("after the context was done came %+v", end.line)
	}
}

// ending is a line of a channel, or its end.
type ending struct {
	line wire.LogLine
	ok   bool
}

// receiveOrEnd returns a channel that gets the next line of lines, or its
// end.
func receiveOrEnd(lines <-chan wire.LogLine) <-chan ending {
	out := make(chan ending, 1)
	go func() {
		line, ok := <-lines
		out <- ending{line, ok}
	}()
	return out
}

func TestLogsOfAZathrasThatIsNotConnected(t *testing.T) {
	w := startWorld(t, testConfig(t))
	if _, err := w.d.Logs(t.Context(), nodetest.ID(77), false); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Logs = %v, want unavailable", err)
	}
	if _, err := w.d.Shell(t.Context(), nodetest.ID(77), wire.ShellRequest{}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Shell = %v, want unavailable", err)
	}
}

func TestShell(t *testing.T) {
	w := startWorld(t, testConfig(t))
	asked := make(chan wire.ShellRequest, 1)
	resized := make(chan wire.WinSize, 1)
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = echoShell(asked, resized, ended)
	z.connect()

	sh, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{Zathras: z.id, Cols: 100, Rows: 30, Term: "vt100"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	if req := wait(t, "the request", asked); req.Zathras != "" || req.Cols != 100 || req.Rows != 30 || req.Term != "vt100" {
		t.Errorf("the zathras was asked %+v", req)
	}

	if _, err := sh.Write([]byte("uptime\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "echo: uptime\n"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(sh, got); err != nil || string(got) != want {
		t.Fatalf("Read = %q, %v; want %q", got, err, want)
	}

	if err := sh.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if size := wait(t, "the change of size", resized); size.Cols != 120 || size.Rows != 40 {
		t.Errorf("the zathras was told %+v, want 120 by 40", size)
	}

	if _, err := sh.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rest, err := io.ReadAll(sh); err != nil || len(rest) != 0 {
		t.Errorf("after exit Read gave %q, %v; want the end", rest, err)
	}
	if status, err := sh.Wait(t.Context()); err != nil || status != 3 {
		t.Errorf("Wait = %d, %v; want 3", status, err)
	}
}

func TestShellWaitIsBoundByItsContext(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = echoShell(make(chan wire.ShellRequest, 1), make(chan wire.WinSize, 1), make(chan struct{}, 1))
	z.connect()
	sh, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sh.Wait(ctx); err == nil {
		t.Error("Wait of a shell that runs returned no error when its context was done")
	}
}

func TestShellThatIsRefused(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle()) // refuses a shell
	_, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{})
	if wire.CodeOf(err) != wire.CodeInternal || err.Error() != "internal: no shell here" {
		t.Errorf("Shell = %v, want what the zathras answered", err)
	}
}

func TestShellCloseClosesTheStream(t *testing.T) {
	w := startWorld(t, testConfig(t))
	ended := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = echoShell(make(chan wire.ShellRequest, 1), make(chan wire.WinSize, 1), ended)
	z.connect()
	sh, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, "the stream to the zathras to end", ended)
	if _, err := sh.Read(make([]byte, 1)); err == nil || err == io.EOF {
		t.Errorf("Read after Close = %v, want an error", err)
	}
	if _, err := sh.Wait(t.Context()); err == nil {
		t.Error("Wait after Close returned no error")
	}
	if err := sh.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestShellOfAZathrasThatGoes(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = echoShell(make(chan wire.ShellRequest, 1), make(chan wire.WinSize, 1), make(chan struct{}, 1))
	z.connect()
	sh, err := w.d.Shell(t.Context(), z.id, wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	z.conn.Close()
	if _, err := sh.Read(make([]byte, 1)); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Read = %v, want unavailable", err)
	}
	if _, err := sh.Wait(t.Context()); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Wait = %v, want unavailable", err)
	}
}

func TestProtocolLogs(t *testing.T) {
	w := startWorld(t, testConfig(t))
	asked := make(chan wire.LogsRequest, 2)
	ended := make(chan struct{}, 2)
	z := w.newFake(10, "web", "web-1", idle())
	z.onLogs = logsOf(someLines(), asked, ended)
	z.connect()
	op := w.dial(operatorID)

	s, resp, err := ask(t, op, &wire.Request{Logs: &wire.LogsRequest{Zathras: z.id}})
	for i, want := range someLines() {
		if err != nil || resp.Line == nil || !sameLine(*resp.Line, want) {
			t.Fatalf("line %d = %+v, %v; want %+v", i, resp, err, want)
		}
		resp, err = wire.ReadResponse(s)
	}
	if err != io.EOF {
		t.Errorf("after the last line came %+v, %v; want the end of the stream", resp, err)
	}

	// With follow the stream stays open, and its end reaches the zathras.
	wait(t, "the first request to end", ended)
	s, _, err = ask(t, op, &wire.Request{Logs: &wire.LogsRequest{Zathras: z.id, Follow: true}})
	if err != nil {
		t.Fatalf("logs with follow: %v", err)
	}
	s.Close()
	wait(t, "the stream to the zathras to end with the operator's", ended)

	if _, _, err := ask(t, op, &wire.Request{Logs: &wire.LogsRequest{Zathras: nodetest.ID(77)}}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("logs of a zathras that is not connected = %v, want unavailable", err)
	}
}

func TestProtocolShell(t *testing.T) {
	w := startWorld(t, testConfig(t))
	asked := make(chan wire.ShellRequest, 2)
	resized := make(chan wire.WinSize, 2)
	ended := make(chan struct{}, 2)
	z := w.newFake(10, "web", "web-1", idle())
	z.onShell = echoShell(asked, resized, ended)
	z.connect()
	op := w.dial(operatorID)

	s, resp, err := ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: z.id, Cols: 80, Rows: 24}})
	if err != nil || resp.Shell == nil || len(resp.Shell.Data) != 0 || resp.Shell.Exit != nil {
		t.Fatalf("shell = %+v, %v; want the empty output that says it runs", resp, err)
	}
	if err := wire.Write(s, &wire.ShellInput{Resize: &wire.WinSize{Cols: 90, Rows: 20}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if size := wait(t, "the change of size", resized); size.Cols != 90 || size.Rows != 20 {
		t.Errorf("the zathras was told %+v, want 90 by 20", size)
	}
	if err := wire.Write(s, &wire.ShellInput{Data: []byte("hi\n")}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var output []byte
	for string(output) != "echo: hi\n" {
		resp, err := wire.ReadResponse(s)
		if err != nil || resp.Shell == nil || resp.Shell.Exit != nil {
			t.Fatalf("shell = %+v, %v; want output (%q so far)", resp, err, output)
		}
		output = append(output, resp.Shell.Data...)
	}
	if err := wire.Write(s, &wire.ShellInput{Data: []byte("exit\n")}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	resp, err = wire.ReadResponse(s)
	if err != nil || resp.Shell == nil || resp.Shell.Exit == nil || *resp.Shell.Exit != 3 {
		t.Fatalf("shell = %+v, %v; want the exit status 3", resp, err)
	}
	if _, err := wire.ReadResponse(s); err != io.EOF {
		t.Errorf("after the exit status came %v, want the end of the stream", err)
	}
	wait(t, "the first shell to end", ended)

	// The end of the operator's stream ends the shell.
	s, _, err = ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: z.id}})
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	s.Close()
	wait(t, "the stream to the zathras to end with the operator's", ended)

	// What the zathras refuses, and a zathras that is not there.
	z.mu.Lock()
	z.onShell = nil
	z.mu.Unlock()
	if _, _, err := ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: z.id}}); wire.CodeOf(err) != wire.CodeInternal {
		t.Errorf("a shell the zathras refuses = %v, want its error", err)
	}
	if _, _, err := ask(t, op, &wire.Request{Shell: &wire.ShellRequest{Zathras: nodetest.ID(77)}}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("a shell of a zathras that is not connected = %v, want unavailable", err)
	}
}
```

- [ ] **Step 17: Run the tests to see them fail**

Run: `go test ./draal/`
Expected: FAIL — the package does not build: `w.d.Logs undefined (type *Draal has no field or method Logs)` and `w.d.Shell undefined`

- [ ] **Step 18: Write Logs and Shell, and let the handler answer them**

`draal/relay.go`:

```go
package draal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// A Draal is a Controller.
var _ Controller = (*Draal)(nil)

// shellChunk is the most input one ShellInput carries.
const shellChunk = 16 << 10

// lost makes of an error of a stream to a zathras the answer for an
// operator: what the zathras answered as it is, and unavailable for a
// stream that failed.
func lost(zathras wire.ID, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	return wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: %v", zathras, err)
}

// open opens a stream on the connection of a connected zathras.
func (d *Draal) open(ctx context.Context, zathras wire.ID) (node.Stream, error) {
	sess := d.session(zathras)
	if sess == nil {
		return nil, notConnected(zathras)
	}
	s, err := sess.conn.OpenStream(ctx)
	if err != nil {
		return nil, lost(zathras, err)
	}
	return s, nil
}

// Logs asks the zathras for its ring and passes the lines on. The channel
// is closed at the end of the stream, or when ctx is done, which closes
// the stream so that the zathras stops following.
func (d *Draal) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	s, err := d.open(ctx, zathras)
	if err != nil {
		return nil, err
	}
	if err := wire.Write(s, &wire.Request{Logs: &wire.LogsRequest{Follow: follow}}); err != nil {
		s.Close()
		return nil, lost(zathras, err)
	}
	out := make(chan wire.LogLine)
	stop := context.AfterFunc(ctx, func() { s.Close() })
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			resp, err := wire.ReadResponse(s)
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					d.log.Warn("the log of a zathras ended", "zathras", zathras, "error", err)
				}
				return
			}
			if resp.Line == nil {
				continue
			}
			select {
			case out <- *resp.Line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Shell asks the zathras for a shell and returns the session once the
// shell runs, or the error the zathras answered.
func (d *Draal) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (Shell, error) {
	s, err := d.open(ctx, zathras)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { s.Close() })
	req.Zathras = ""
	resp, err := wire.Call(s, &wire.Request{Shell: &req})
	if !stop() {
		s.Close()
		return nil, fmt.Errorf("draal: starting a shell of zathras %s: %w", zathras, context.Cause(ctx))
	}
	if err != nil {
		s.Close()
		return nil, lost(zathras, err)
	}
	if resp.Shell == nil {
		s.Close()
		return nil, wire.Errorf(wire.CodeInternal, "zathras %s answered a shell request with something else", zathras)
	}
	return &relayShell{zathras: zathras, s: s, ended: make(chan struct{})}, nil
}

// relayShell is a Shell over the stream to the zathras.
type relayShell struct {
	zathras wire.ID
	s       node.Stream

	rest []byte // output that was received and not read yet; Read's alone

	ended chan struct{} // closed when exit or err is set
	exit  int
	err   error

	wmu   sync.Mutex // one frame at a time
	close sync.Once
}

// Read returns output of the shell, and io.EOF once its exit status came.
func (r *relayShell) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		select {
		case <-r.ended:
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		default:
		}
		resp, err := wire.ReadResponse(r.s)
		switch {
		case err == io.EOF:
			r.err = wire.Errorf(wire.CodeUnavailable, "the shell of zathras %s ended without an exit status", r.zathras)
			close(r.ended)
		case err != nil:
			r.err = lost(r.zathras, err)
			close(r.ended)
		case resp.Shell == nil:
		case resp.Shell.Exit != nil:
			r.exit = *resp.Shell.Exit
			r.rest = resp.Shell.Data
			close(r.ended)
			if len(r.rest) == 0 {
				return 0, io.EOF
			}
		default:
			r.rest = resp.Shell.Data
		}
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// Write sends p as input of the shell.
func (r *relayShell) Write(p []byte) (int, error) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), shellChunk)]
		if err := wire.Write(r.s, &wire.ShellInput{Data: chunk}); err != nil {
			return written, lost(r.zathras, err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Resize tells the shell the new size of the terminal.
func (r *relayShell) Resize(cols, rows uint16) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if err := wire.Write(r.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: cols, Rows: rows}}); err != nil {
		return lost(r.zathras, err)
	}
	return nil
}

// Wait returns the exit status once Read has reached it, or the error the
// session ended with instead.
func (r *relayShell) Wait(ctx context.Context) (int, error) {
	select {
	case <-r.ended:
		if r.err != nil {
			return 0, r.err
		}
		return r.exit, nil
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// Close closes the stream, which ends the shell if it runs.
func (r *relayShell) Close() error {
	r.close.Do(func() { r.s.Close() })
	return nil
}
```

Replace `draal/handler.go` with this, which has the branch for `shell` in `operate`, the case for `logs` in `respond`, and `serveShell`:

`draal/handler.go`:

```go
package draal

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// admit maps a request of an operator to its operation and its service
// and asks Authorize. A refusal is logged. A zathras that is not connected
// is unavailable, for a peer that may ask at all.
func (d *Draal) admit(peer wire.ID, req *wire.Request) error {
	var (
		op      authz.Operation
		service string
		zathras *wire.ID
	)
	switch {
	case req.Store != nil:
		op = authz.OpStore
	case req.Rollout != nil:
		op, service = authz.OpRollout, req.Rollout.Service
	case req.Rollback != nil:
		op, service = authz.OpRollback, req.Rollback.Service
	case req.Watch != nil:
		op = authz.OpWatch
	case req.Rollouts != nil:
		op, service = authz.OpRollouts, req.Rollouts.Service
	case req.History != nil:
		op, zathras = authz.OpHistory, &req.History.Zathras
	case req.Logs != nil:
		op, zathras = authz.OpLogs, &req.Logs.Zathras
	case req.Shell != nil:
		op, zathras = authz.OpShell, &req.Shell.Zathras
	default:
		return wire.Errorf(wire.CodeBadRequest, "draal answers no such request")
	}
	connected := true
	if zathras != nil {
		service, connected = d.ServiceOf(*zathras)
	}
	if err := d.cfg.Authz.Authorize(peer, op, service); err != nil {
		d.log.Warn("refused", "peer", peer, "operation", op, "service", service)
		return err
	}
	if !connected {
		return notConnected(*zathras)
	}
	return nil
}

// operate answers one request of an operator through the methods of
// Controller, and returns when the peer has ended the stream.
func (d *Draal) operate(ctx context.Context, peer wire.ID, s node.Stream, req *wire.Request) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := d.admit(peer, req)
	if err == nil && req.Shell != nil {
		d.serveShell(ctx, cancel, s, *req.Shell)
		return
	}
	// The peer writes nothing more: the end of what it sends, or an
	// error, is the end of the request.
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()
	// send writes one response, and ends the request when it cannot.
	send := func(resp *wire.Response) bool {
		if err := wire.Write(s, resp); err != nil {
			cancel()
			return false
		}
		return true
	}
	if err == nil {
		err = d.respond(ctx, peer, req, send)
	}
	if err != nil {
		wire.WriteError(s, err)
	}
	s.CloseWrite()
	<-ctx.Done()
}

// respond does what req asks and sends what answers it. An error it
// returns is the answer.
func (d *Draal) respond(ctx context.Context, peer wire.ID, req *wire.Request, send func(*wire.Response) bool) error {
	switch {
	case req.Store != nil:
		info, err := d.Store(ctx)
		if err != nil {
			return err
		}
		send(&wire.Response{Store: &info})
	case req.Rollout != nil:
		info, err := d.Rollout(ctx, peer, *req.Rollout)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Rollback != nil:
		info, err := d.Rollback(ctx, peer, *req.Rollback)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Watch != nil:
		fleets, err := d.Watch(ctx)
		if err != nil {
			return err
		}
		for f := range fleets {
			if !send(&wire.Response{Fleet: &f}) {
				break
			}
		}
	case req.Rollouts != nil:
		list, err := d.Rollouts(ctx, req.Rollouts.Service)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollouts: &wire.RolloutList{Rollouts: list}})
	case req.History != nil:
		samples, err := d.History(ctx, req.History.Zathras)
		if err != nil {
			return err
		}
		send(&wire.Response{History: &wire.SampleList{Samples: samples}})
	case req.Logs != nil:
		lines, err := d.Logs(ctx, req.Logs.Zathras, req.Logs.Follow)
		if err != nil {
			return err
		}
		for line := range lines {
			if !send(&wire.Response{Line: &line}) {
				break
			}
		}
	}
	return nil
}

// serveShell relays a shell session between the stream of an operator and
// the Shell of the Controller.
func (d *Draal) serveShell(ctx context.Context, cancel context.CancelFunc, s node.Stream, req wire.ShellRequest) {
	sh, err := d.Shell(ctx, req.Zathras, req)
	if err != nil {
		refuse(s, err)
		return
	}
	defer sh.Close()
	if err := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}}); err != nil {
		return
	}
	// Input, until the operator ends the stream, which ends the session.
	go func() {
		defer cancel()
		defer sh.Close()
		for {
			var in wire.ShellInput
			if err := wire.Read(s, &in); err != nil {
				return
			}
			if in.Resize != nil {
				if err := sh.Resize(in.Resize.Cols, in.Resize.Rows); err != nil {
					return
				}
			}
			if len(in.Data) > 0 {
				if _, err := sh.Write(in.Data); err != nil {
					return
				}
			}
		}
	}()
	// Output, and behind it the exit status.
	buf := make([]byte, 16<<10)
	for {
		n, err := sh.Read(buf)
		if n > 0 {
			if werr := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}); werr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			wire.WriteError(s, err)
			s.CloseWrite()
			<-ctx.Done()
			return
		}
	}
	status, err := sh.Wait(ctx)
	sh.Close() // the zathras need not wait for the operator
	if err != nil {
		wire.WriteError(s, err)
	} else {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &status}})
	}
	s.CloseWrite()
	<-ctx.Done()
}
```

- [ ] **Step 19: Run the tests to see them pass**

Run: `go vet ./draal/ && go test -race ./draal/`
Expected: PASS

- [ ] **Step 20: Commit**

```bash
git add draal/
git commit -m "Draal: logs and shells of a zathras, passed on"
```

- [ ] **Step 21: Write the failing tests of the reconciler**

`draal/reconcile_test.go`:

```go
package draal

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// rollout asks draal for a rollout.
func (w *world) rollout(req wire.RolloutRequest) wire.RolloutInfo {
	w.t.Helper()
	info, err := w.d.Rollout(context.Background(), operatorID, req)
	if err != nil {
		w.t.Fatalf("Rollout: %v", err)
	}
	return info
}

// awaitRollout waits until a rollout is in the state given, and returns it.
func (w *world) awaitRollout(info wire.RolloutInfo, state wire.RolloutState) wire.RolloutInfo {
	w.t.Helper()
	var got wire.RolloutInfo
	end := time.Now().Add(deadline)
	for {
		list, err := w.d.Rollouts(context.Background(), info.Service)
		if err != nil {
			w.t.Fatalf("Rollouts: %v", err)
		}
		for _, r := range list {
			if r.ID == info.ID {
				got = r
			}
		}
		if got.State == state {
			return got
		}
		if time.Now().After(end) {
			w.t.Fatalf("rollout %d is %s (%s), want %s", info.ID, got.State, got.Reason, state)
		}
		time.Sleep(time.Millisecond)
	}
}

// stableWeb rolls generation 1 of web out while no zathras is connected,
// which completes at once and makes it the stable generation.
func (w *world) stableWeb() wire.RolloutInfo {
	w.t.Helper()
	return w.awaitRollout(w.rollout(webRollout(1)), wire.RolloutCompleted)
}

// newWeb returns a rollout of another binary of web.
func newWeb(parallel int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  "web",
		Binary:   "/nix/store/xyz-web/bin/web",
		Args:     []string{"--new"},
		Closure:  []string{"xyz-web", "def-libc"},
		Parallel: parallel,
	}
}

// rolloutIs returns a condition on a fleet: the last rollout of web is
// the one given and in that state.
func rolloutIs(info wire.RolloutInfo, state wire.RolloutState) func(wire.Fleet) bool {
	return func(f wire.Fleet) bool {
		for _, svc := range f.Services {
			if svc.Name == info.Service && svc.Rollout != nil {
				return svc.Rollout.ID == info.ID && svc.Rollout.State == state
			}
		}
		return false
	}
}

func TestARolloutRunsToCompleted(t *testing.T) {
	cfg := testConfig(t)
	cfg.StableAfter = 100 * time.Millisecond
	w := startWorld(t, cfg)
	w.stableWeb()

	// Two zathrases that run generation 1, and whose prepare waits for
	// the test.
	gate := make(chan struct{})
	hold := func(wire.Prepare, node.Stream) error { <-gate; return nil }
	a := w.newFake(10, "web", "web-a", running(1))
	b := w.newFake(11, "web", "web-b", running(1))
	a.onPrepare, b.onPrepare = hold, hold
	a.connect()
	b.connect()

	fleets, err := w.d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	info := w.rollout(newWeb(1))
	if info.Generation != 2 {
		t.Fatalf("the rollout targets generation %d, want 2", info.Generation)
	}

	fleetWhere(t, fleets, rolloutIs(info, wire.RolloutPreparing))
	eventually(t, "a prepare for both", func() bool {
		return len(a.sent().prepares) == 1 && len(b.sent().prepares) == 1
	})
	if len(a.sent().activates)+len(b.sent().activates) != 0 {
		t.Fatal("an activate was sent while the rollout prepares")
	}
	close(gate)

	f, _ := fleetWhere(t, fleets, rolloutIs(info, wire.RolloutActivating))
	for _, svc := range f.Services {
		if svc.Name == "web" && svc.Rollout.Prepared != 2 {
			t.Errorf("while activating the fleet counts %d prepared, want 2", svc.Rollout.Prepared)
		}
	}
	f, _ = fleetWhere(t, fleets, rolloutIs(info, wire.RolloutCompleted))
	for _, svc := range f.Services {
		if svc.Name == "web" && svc.Stable != 2 {
			t.Errorf("after the rollout the stable generation is %d, want 2", svc.Stable)
		}
	}

	want := newWeb(1)
	var activated []time.Time
	for _, z := range []*fake{a, b} {
		got := z.sent()
		// A prepare may be sent twice: its answer can come before the
		// report that lists the generation.
		if len(got.prepares) == 0 || len(got.activates) != 1 {
			t.Fatalf("%s got %d prepares and %d activates, want a prepare and one activate", z.name, len(got.prepares), len(got.activates))
		}
		for _, p := range got.prepares {
			if p.Generation != 2 || p.Binary != want.Binary || !slices.Equal(p.Closure, want.Closure) {
				t.Errorf("%s was sent the prepare %+v", z.name, p)
			}
		}
		if act := got.activates[0]; act.Generation != 2 || act.Binary != want.Binary || !slices.Equal(act.Args, want.Args) {
			t.Errorf("%s was sent the activate %+v", z.name, act)
		}
		activated = append(activated, got.activatedAt[0])
	}
	// With Parallel 1 the second is activated when the first has run
	// for StableAfter.
	if gap := activated[1].Sub(activated[0]).Abs(); gap < cfg.StableAfter {
		t.Errorf("the two were activated %v apart, less than StableAfter (%v): not one after the other", gap, cfg.StableAfter)
	}
	events := w.noted()
	if last := events[len(events)-2:]; !strings.HasPrefix(last[0], "activate ") || !strings.HasPrefix(last[1], "activate ") {
		t.Errorf("the last commands were %q, want the two activates behind every prepare", last)
	}
}

func TestAPrepareThatFailsHaltsTheRollout(t *testing.T) {
	w := startWorld(t, testConfig(t))
	w.stableWeb()
	a := w.newFake(10, "web", "web-a", running(1))
	a.onPrepare = func(wire.Prepare, node.Stream) error {
		return wire.Errorf(wire.CodeInternal, "the sidecar said no")
	}
	a.connect()
	b := w.zathras(11, "web", "web-b", running(1))

	halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
	if !strings.Contains(halted.Reason, "web-a") || !strings.Contains(halted.Reason, "the sidecar said no") {
		t.Errorf("the reason is %q, want the name of the zathras and what it answered", halted.Reason)
	}
	if halted.Finished.IsZero() {
		t.Error("a halted rollout has no time it finished")
	}
	for _, z := range []*fake{a, b} {
		if len(z.sent().activates) != 0 {
			t.Errorf("%s was sent an activate in a rollout that halted while preparing", z.name)
		}
	}
	if svc, _ := w.service("web"); svc.Stable != 1 {
		t.Errorf("the stable generation is %d, want 1 still", svc.Stable)
	}
}

func TestAZathrasThatFailsHaltsTheRollout(t *testing.T) {
	cases := []struct {
		name string
		rig  func(z *fake)
		want string
	}{
		{"its service ends at once", func(z *fake) {
			z.afterActivate = func(st *wire.State) {
				st.Phase, st.PID = wire.PhaseBackoff, 0
				st.RestartAt = time.Now().Add(time.Minute)
				st.LastExit = &wire.Exit{Generation: st.Generation, Status: 3, At: time.Now(), Ran: 10 * time.Millisecond}
			}
		}, "web-"},
		{"it refuses activate", func(z *fake) {
			z.onActivate = func(wire.Activate) error {
				return wire.Errorf(wire.CodeInternal, "not an executable file")
			}
		}, ": not an executable file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := startWorld(t, testConfig(t))
			w.stableWeb()
			// Both would fail: the rollout halts at the one that is
			// activated first, and the other is left alone.
			a := w.newFake(10, "web", "web-a", running(1))
			b := w.newFake(11, "web", "web-b", running(1))
			tc.rig(a)
			tc.rig(b)
			a.connect()
			b.connect()

			halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
			if !strings.Contains(halted.Reason, tc.want) {
				t.Errorf("the reason is %q, want %q in it", halted.Reason, tc.want)
			}
			if n := len(a.sent().activates) + len(b.sent().activates); n != 1 {
				t.Errorf("%d activates were sent, want the rollout to halt at the first", n)
			}
		})
	}
}

func TestWhatIsNoAnswerHaltsNothing(t *testing.T) {
	cases := []struct {
		name string
		fail func(s node.Stream) error // what the first prepare and the first activate do
	}{
		// The zathras says that it shuts down.
		{"unavailable", func(node.Stream) error {
			return wire.Errorf(wire.CodeUnavailable, "the supervisor has stopped")
		}},
		// The stream ends without an answer.
		{"a broken stream", func(s node.Stream) error {
			s.Close()
			return errors.New("nothing is answered")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := startWorld(t, testConfig(t))
			w.stableWeb()
			z := w.newFake(10, "web", "web-a", running(1))
			prepares := 0
			z.onPrepare = func(_ wire.Prepare, s node.Stream) error {
				if prepares++; prepares == 1 {
					return tc.fail(s)
				}
				return nil
			}
			z.connect()

			// The command is sent again, and the rollout goes on.
			info := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutCompleted)
			got := z.sent()
			if len(got.prepares) < 2 {
				t.Errorf("the zathras got %d prepares, want the one that was not answered sent again", len(got.prepares))
			}
			if len(got.activates) != 1 || got.activates[0].Generation != info.Generation {
				t.Errorf("activates = %+v, want one for generation %d", got.activates, info.Generation)
			}
		})
	}
}

func TestAZathrasThatComesLaterIsSentTheStableGeneration(t *testing.T) {
	w := startWorld(t, testConfig(t))
	first := w.stableWeb() // with nobody connected it completes
	if svc, _ := w.service("web"); svc.Stable != first.Generation {
		t.Fatalf("the stable generation is %d, want %d", svc.Stable, first.Generation)
	}

	// Its first prepare fails, as when the store cannot be reached: it
	// is tried again after RetryAfter.
	z := w.newFake(10, "web", "web-new", idle())
	failed := false
	z.onPrepare = func(wire.Prepare, node.Stream) error {
		if !failed {
			failed = true
			return wire.Errorf(wire.CodeInternal, "the store cannot be reached")
		}
		return nil
	}
	z.connect()

	eventually(t, "the zathras to run the stable generation", func() bool {
		got, _ := w.member("web", z.id)
		return got.State.Generation == first.Generation && got.State.Phase == wire.PhaseRunning
	})
	want := webRollout(1)
	got := z.sent()
	if len(got.prepares) < 2 || len(got.activates) != 1 {
		t.Fatalf("the zathras got %d prepares and %d activates, want a second prepare and one activate", len(got.prepares), len(got.activates))
	}
	if p := got.prepares[1]; p.Generation != 1 || p.Binary != want.Binary || !slices.Equal(p.Closure, want.Closure) {
		t.Errorf("prepare = %+v", p)
	}
	if act := got.activates[0]; act.Generation != 1 || act.Binary != want.Binary || !slices.Equal(act.Args, want.Args) {
		t.Errorf("activate = %+v", act)
	}
	if gap := got.prepareAt[1].Sub(got.prepareAt[0]); gap < w.cfg.RetryAfter {
		t.Errorf("the prepare was sent again after %v, sooner than RetryAfter (%v)", gap, w.cfg.RetryAfter)
	}
	if last, _ := w.service("web"); last.Rollout.State != wire.RolloutCompleted {
		t.Errorf("the rollout is %s, want completed still", last.Rollout.State)
	}
}

func TestASecondRolloutSupersedesTheFirst(t *testing.T) {
	w := startWorld(t, testConfig(t))
	w.stableWeb()
	withdrawn := make(chan struct{}, 1)
	z := w.newFake(10, "web", "web-a", running(1))
	z.onPrepare = func(p wire.Prepare, s node.Stream) error {
		if p.Generation != 2 {
			return nil
		}
		// Generation 2 never gets prepared: this waits until draal
		// closes the stream.
		io.Copy(io.Discard, s)
		withdrawn <- struct{}{}
		return errors.New("withdrawn")
	}
	z.connect()

	first := w.rollout(newWeb(1))
	eventually(t, "the prepare of the first rollout", func() bool { return len(z.sent().prepares) == 1 })
	second := w.rollout(newWeb(1))
	wait(t, "draal to close the stream of the prepare that hangs", withdrawn)

	w.awaitRollout(second, wire.RolloutCompleted)
	w.awaitRollout(first, wire.RolloutSuperseded)
	got := z.sent()
	if got.prepares[0].Generation != first.Generation {
		t.Errorf("the first prepare was for generation %d, want %d", got.prepares[0].Generation, first.Generation)
	}
	for _, p := range got.prepares[1:] {
		if p.Generation != second.Generation {
			t.Errorf("after the second rollout a prepare for generation %d was sent, want %d", p.Generation, second.Generation)
		}
	}
	if len(got.activates) != 1 || got.activates[0].Generation != second.Generation {
		t.Errorf("activates = %+v, want one for generation %d", got.activates, second.Generation)
	}
	if closed(z.conn) {
		t.Error("the connection of the zathras was closed with the prepare")
	}
}

func TestNothingIsSentBeforeSettle(t *testing.T) {
	cfg := testConfig(t)
	// A stable generation, recorded by a draal that does not serve.
	before := New(cfg)
	info, err := before.Rollout(context.Background(), operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if ok, err := cfg.DB.SetRolloutState(context.Background(), info.ID, wire.RolloutCompleted, "", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}

	cfg.Settle = 300 * time.Millisecond
	start := time.Now()
	w := startWorld(t, cfg)
	z := w.zathras(10, "web", "web-new", idle())
	eventually(t, "a prepare", func() bool { return len(z.sent().prepares) > 0 })
	if after := z.sent().prepareAt[0].Sub(start); after < cfg.Settle {
		t.Errorf("a command was sent %v after the start, before Settle (%v) had passed", after, cfg.Settle)
	}
}

func TestANewDraalCarriesARolloutOn(t *testing.T) {
	cfg := testConfig(t)
	cfg.StableAfter = time.Hour // nobody is done while the first draal runs
	w := startWorld(t, cfg)
	w.stableWeb()
	a := w.zathras(10, "web", "web-a", running(1))
	b := w.zathras(11, "web", "web-b", running(1))

	info := w.rollout(newWeb(1))
	w.awaitRollout(info, wire.RolloutActivating)
	eventually(t, "the first activate", func() bool {
		return len(a.sent().activates)+len(b.sent().activates) == 1
	})
	eventually(t, "the reports that list the generation as prepared", func() bool {
		for _, z := range []*fake{a, b} {
			if got, _ := w.member("web", z.id); !slices.Contains(got.State.Prepared, info.Generation) {
				return false
			}
		}
		return true
	})
	prepares := len(a.sent().prepares) + len(b.sent().prepares)

	// draal ends, and another begins on the same database.
	w.stop()
	eventually(t, "the connections to end with draal", func() bool { return closed(a.conn) && closed(b.conn) })
	if n := len(a.sent().activates) + len(b.sent().activates); n != 1 {
		t.Fatalf("%d activates were sent while the first is under way, want 1", n)
	}
	cfg.StableAfter = 50 * time.Millisecond
	w.serve(cfg)
	if svc, _ := w.service("web"); svc.Rollout == nil || svc.Rollout.ID != info.ID || svc.Rollout.State != wire.RolloutActivating {
		t.Fatalf("the new draal has the rollout %+v, want it activating", svc.Rollout)
	}
	a.connect()
	b.connect()

	w.awaitRollout(info, wire.RolloutCompleted)
	for _, z := range []*fake{a, b} {
		if got := z.sent(); len(got.activates) != 1 || got.activates[0].Generation != info.Generation {
			t.Errorf("%s got the activates %+v, want one for generation %d", z.name, got.activates, info.Generation)
		}
	}
	if n := len(a.sent().prepares) + len(b.sent().prepares); n != prepares {
		t.Errorf("the new draal sent %d prepares to zathrases that had prepared", n-prepares)
	}
	if svc, _ := w.service("web"); svc.Stable != info.Generation {
		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
	}
}
```

- [ ] **Step 22: Run a test to see it fail**

Nothing carries a rollout out yet, so each of these tests waits for its 10 seconds. One is enough to see it:

Run: `go test ./draal/ -run TestAZathrasThatComesLaterIsSentTheStableGeneration`
Expected: FAIL after 10 seconds — `rollout 1 is preparing (), want completed`

- [ ] **Step 23: Write the reconciler, and start it in Serve**

`draal/reconcile.go`:

```go
package draal

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/rollout"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// breath is the least time between two looks at the fleet. Every
	// report of every zathras asks for a look, and a Recheck goes by the
	// last report and can lie in the past until the next one comes: those
	// that come within a breath are answered by one look.
	breath = 20 * time.Millisecond
	// rounds is how often a service is decided about in one pass: every
	// move of its rollout asks for one more decision.
	rounds = 4
)

// command is a prepare or an activate that was sent to a zathras.
type command struct {
	sess       *session
	activate   bool
	generation uint64
	rollout    uint64 // the rollout under way it was sent for; 0 for none
	cancel     context.CancelFunc
	accepted   bool // an activate that was answered and that no report shows yet
}

// failure is the last command a zathras refused or failed, and the
// session it was sent on.
type failure struct {
	sess *session
	rollout.Failure
}

// result is the answer to a command.
type result struct {
	cmd *command
	err error
}

// reconciler is the state of the reconciler loop. Its goroutine alone
// touches it; the answers to commands come back over results.
type reconciler struct {
	d          *Draal
	ctx        context.Context
	sending    sync.WaitGroup
	results    chan result
	preparing  map[wire.ID]*command
	activating map[wire.ID]*command
	failed     map[wire.ID]failure
	seen       map[string]uint64 // by service: the last rollout that was looked at
}

// reconcile is the reconciler loop. It does nothing before Settle has
// passed. Then it looks at every service whenever the fleet changed, a
// rollout was recorded, a command was answered or a Recheck has come.
func (d *Draal) reconcile(ctx context.Context) {
	r := &reconciler{
		d:          d,
		ctx:        ctx,
		results:    make(chan result),
		preparing:  map[wire.ID]*command{},
		activating: map[wire.ID]*command{},
		failed:     map[wire.ID]failure{},
		seen:       map[string]uint64{},
	}
	defer r.sending.Wait()
	members, stopMembers := d.fleet.Subscribe()
	defer stopMembers()
	rollouts, stopRollouts := d.subscribe()
	defer stopRollouts()

	settle := time.NewTimer(d.cfg.Settle)
	defer settle.Stop()
	select {
	case <-ctx.Done():
		return
	case <-settle.C:
	}
	for {
		recheck := r.pass()
		rest := time.NewTimer(breath)
		var due <-chan time.Time
		var timer *time.Timer
		if !recheck.IsZero() {
			timer = time.NewTimer(recheck.Sub(d.cfg.Now()))
			due = timer.C
		}
		select {
		case <-ctx.Done():
		case <-members:
		case <-rollouts:
		case res := <-r.results:
			r.answered(res)
		case <-due:
		}
		if timer != nil {
			timer.Stop()
		}
		// What else comes within the breath is taken with it.
		for resting := true; resting; {
			select {
			case <-ctx.Done():
				resting = false
			case res := <-r.results:
				r.answered(res)
			case <-rest.C:
				resting = false
			}
		}
		rest.Stop()
		if ctx.Err() != nil {
			return
		}
	}
}

// pass looks at every service once, and returns when to look again if
// nothing happens before, or the zero time.
func (r *reconciler) pass() time.Time {
	d := r.d
	r.forget()
	names, err := d.serviceNames(r.ctx)
	if err != nil {
		if r.ctx.Err() == nil {
			d.log.Error("reconciler: reading the services", "error", err)
		}
		return d.cfg.Now().Add(d.cfg.RetryAfter)
	}
	var recheck time.Time
	changed := false
	for _, name := range names {
		at, moved, err := r.service(name)
		if err != nil {
			if r.ctx.Err() == nil {
				d.log.Error("reconciler: a service", "service", name, "error", err)
			}
			at = d.cfg.Now().Add(d.cfg.RetryAfter)
		}
		changed = changed || moved
		if !at.IsZero() && (recheck.IsZero() || at.Before(recheck)) {
			recheck = at
		}
	}
	if changed {
		d.changed()
	}
	return recheck
}

// forget drops what is kept of a zathras that left, or that came back on
// another connection, and the activations that a report shows.
func (r *reconciler) forget() {
	for id, cmd := range r.preparing {
		if r.d.session(id) != cmd.sess {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
	for id, cmd := range r.activating {
		if r.d.session(id) != cmd.sess {
			cmd.cancel()
			delete(r.activating, id)
			continue
		}
		if m, ok := r.d.fleet.Get(id); ok && cmd.accepted && m.State.Generation == cmd.generation {
			delete(r.activating, id)
		}
	}
	for id, f := range r.failed {
		if r.d.session(id) != f.sess {
			delete(r.failed, id)
		}
	}
}

// service decides about one service and does what the decision says. It
// returns the Recheck of the decision and whether the rollout or its
// counts changed.
func (r *reconciler) service(name string) (recheck time.Time, changed bool, err error) {
	d := r.d
	for range rounds {
		var stable uint64
		row, err := d.cfg.DB.Service(r.ctx, name)
		switch {
		case err == nil:
			stable = row.Stable
		case !errors.Is(err, db.ErrNotFound):
			return time.Time{}, changed, err
		}
		last, has, err := d.cfg.DB.LastRollout(r.ctx, name)
		if err != nil {
			return time.Time{}, changed, err
		}
		var current *wire.RolloutInfo
		if has {
			current = &last
			if r.seen[name] != last.ID {
				r.seen[name] = last.ID
				r.recorded(name, last.Generation)
			}
		}

		in := rollout.Input{
			Now:            d.cfg.Now(),
			Stable:         stable,
			Rollout:        current,
			Members:        d.fleet.Service(name),
			Preparing:      map[wire.ID]uint64{},
			Activating:     map[wire.ID]uint64{},
			Failed:         map[wire.ID]rollout.Failure{},
			StableAfter:    d.cfg.StableAfter,
			PrepareTimeout: d.cfg.PrepareTimeout,
			RetryAfter:     d.cfg.RetryAfter,
		}
		for _, m := range in.Members {
			if cmd := r.preparing[m.ID]; cmd != nil {
				in.Preparing[m.ID] = cmd.generation
			}
			if cmd := r.activating[m.ID]; cmd != nil {
				in.Activating[m.ID] = cmd.generation
			}
			if f, ok := r.failed[m.ID]; ok {
				in.Failed[m.ID] = f.Failure
			}
		}
		dec := rollout.Decide(in)

		moved := false
		if dec.State != "" && has {
			ok, err := d.cfg.DB.SetRolloutState(r.ctx, last.ID, dec.State, dec.Reason, d.cfg.Now())
			if err != nil {
				return time.Time{}, changed, err
			}
			if !ok {
				// Superseded meanwhile: the decision was about a
				// rollout that is over.
				continue
			}
			d.log.Info("rollout", "service", name, "rollout", last.ID, "state", dec.State, "reason", dec.Reason)
			last.State, last.Reason = dec.State, dec.Reason
			moved, changed = true, true
		}
		if has {
			t := tally{rollout: last.ID, prepared: dec.Prepared, activated: dec.Activated, awaited: dec.Awaited}
			d.mu.Lock()
			if d.tallies[name] != t {
				d.tallies[name] = t
				changed = true
			}
			d.mu.Unlock()
		}
		r.withdraw(name, current)
		for _, c := range dec.Commands {
			r.send(name, current, c)
		}
		if !moved {
			return dec.Recheck, changed, nil
		}
	}
	return d.cfg.Now(), changed, nil
}

// recorded is called when a service has a rollout that was not seen
// before: the failures of its zathrases are forgotten, and the prepares
// under way for another generation are withdrawn.
func (r *reconciler) recorded(service string, target uint64) {
	for id, f := range r.failed {
		if f.sess.service == service {
			delete(r.failed, id)
		}
	}
	for id, cmd := range r.preparing {
		if cmd.sess.service == service && cmd.generation != target {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
}

// withdraw ends the prepares that were sent for a rollout which is not
// under way any more.
func (r *reconciler) withdraw(service string, current *wire.RolloutInfo) {
	for id, cmd := range r.preparing {
		if cmd.sess.service != service || cmd.rollout == 0 {
			continue
		}
		if current == nil || current.ID != cmd.rollout || !current.State.UnderWay() {
			cmd.cancel()
			delete(r.preparing, id)
		}
	}
}

// send sends a command to a zathras, in a goroutine of its own.
func (r *reconciler) send(service string, current *wire.RolloutInfo, c rollout.Command) {
	d := r.d
	if r.preparing[c.Zathras] != nil || r.activating[c.Zathras] != nil {
		return
	}
	sess := d.session(c.Zathras)
	if sess == nil {
		return
	}
	gen, err := d.cfg.DB.Generation(r.ctx, service, c.Generation)
	if err != nil {
		d.log.Error("reconciler: reading a generation", "service", service, "generation", c.Generation, "error", err)
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	cmd := &command{sess: sess, activate: c.Activate, generation: c.Generation, cancel: cancel}
	if current != nil && current.State.UnderWay() {
		cmd.rollout = current.ID
	}
	var req wire.Request
	if c.Activate {
		req.Activate = &wire.Activate{Generation: gen.Number, Binary: gen.Binary, Args: gen.Args}
		r.activating[c.Zathras] = cmd
	} else {
		req.Prepare = &wire.Prepare{Generation: gen.Number, Binary: gen.Binary, Closure: gen.Closure}
		r.preparing[c.Zathras] = cmd
	}
	d.log.Info("command sent", "service", service, "zathras", c.Zathras, "activate", c.Activate, "generation", c.Generation)
	r.sending.Go(func() {
		err := call(ctx, sess.conn, &req)
		select {
		case r.results <- result{cmd: cmd, err: err}:
		case <-r.ctx.Done():
		}
	})
}

// call sends one request on a stream of its own and returns the error it
// was answered with. The stream is closed when ctx is done, which
// withdraws the request.
func call(ctx context.Context, conn node.Conn, req *wire.Request) error {
	s, err := conn.OpenStream(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	_, err = wire.Call(s, req)
	return err
}

// answered takes the answer to a command into the books. Only what the
// zathras answered counts as a failure: a command that ended without an
// answer, because the stream or the connection broke, and one the zathras
// answered with unavailable, because it shuts down, just leave the books.
// If the zathras is still there, the next decision sends the command again.
func (r *reconciler) answered(res result) {
	cmd := res.cmd
	cmd.cancel()
	id := cmd.sess.id
	sent := r.preparing
	if cmd.activate {
		sent = r.activating
	}
	if sent[id] != cmd {
		return // withdrawn, or the zathras left
	}
	var refusal *wire.Error
	switch {
	case res.err == nil && cmd.activate:
		// It stays until a report shows the generation.
		cmd.accepted = true
	case res.err == nil:
		delete(sent, id)
	case errors.As(res.err, &refusal) && refusal.Code != wire.CodeUnavailable:
		delete(sent, id)
		r.failed[id] = failure{sess: cmd.sess, Failure: rollout.Failure{
			Generation: cmd.generation,
			Activate:   cmd.activate,
			Reason:     refusal.Message,
			At:         r.d.cfg.Now(),
		}}
	default:
		delete(sent, id)
		r.d.log.Warn("a command was not answered", "zathras", id, "activate", cmd.activate, "generation", cmd.generation, "error", res.err)
	}
}
```

In `draal/serve.go`, in `Serve`, add one line behind `wg.Go(func() { d.reap(ctx) })`, so that the two read:

```go
	wg.Go(func() { d.reap(ctx) })
	wg.Go(func() { d.reconcile(ctx) })
```

- [ ] **Step 24: Run the tests to see them pass**

Run: `test -z "$(gofmt -l draal/)" && go vet ./draal/ && go test -race -count=3 ./draal/`
Expected: PASS, in about 15 seconds for the three runs.

- [ ] **Step 25: Commit**

```bash
git add draal/
git commit -m "Draal: the reconciler carries out what Decide says"
```
