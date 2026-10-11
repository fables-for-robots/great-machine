package draal

import (
	"context"
	"io"
	"slices"
	"strings"
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
		"store":    {Store: &wire.StoreRequest{Service: "web"}},
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

func TestARefusalAboutAZathrasNamesNoService(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "secretsvc", "secret-1", idle())
	stranger := w.dial(strangerID)

	connected := requests(z.id)
	absent := requests(nodetest.ID(77))
	for _, name := range []string{"history", "logs", "shell"} {
		_, _, errIn := ask(t, stranger, connected[name])
		_, _, errOut := ask(t, stranger, absent[name])
		if wire.CodeOf(errIn) != wire.CodeUnauthorized || wire.CodeOf(errOut) != wire.CodeUnauthorized {
			t.Fatalf("%s as a stranger = %v, %v; want unauthorized twice", name, errIn, errOut)
		}
		if errIn.Error() != errOut.Error() {
			t.Errorf("%s: the refusal differs with the zathras: %q and %q", name, errIn, errOut)
		}
		if strings.Contains(errIn.Error(), "secretsvc") {
			t.Errorf("%s: the refusal names the service: %q", name, errIn)
		}
	}
}

func TestProtocolStore(t *testing.T) {
	cfg := testConfig(t)
	w := startWorld(t, cfg)
	op := w.dial(operatorID)
	_, resp, err := ask(t, op, &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
	want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: wire.StorePrefix("web")}
	if err != nil || resp.Store == nil || *resp.Store != want {
		t.Fatalf("store of web = %+v, %v; want %+v", resp, err, want)
	}
	// The request names its service: one that names none, or what is no
	// name of a service, is refused.
	for _, service := range []string{"", "a/b"} {
		if _, _, err := ask(t, op, &wire.Request{Store: &wire.StoreRequest{Service: service}}); wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("store of %q = %v, want a bad-request", service, err)
		}
	}
	// It is authorized for the service it names.
	_, _, err = ask(t, w.dial(strangerID), &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
	if wire.CodeOf(err) != wire.CodeUnauthorized || !strings.Contains(err.Error(), "on service web") {
		t.Errorf("store of web as a stranger = %v, want unauthorized, naming the service", err)
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

// A request asks for one thing: one that asks for two is refused before
// anything of it is looked at, whoever asks.
func TestARequestWithTwoFieldsIsRefused(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle())
	for _, peer := range []wire.ID{operatorID, strangerID} {
		conn := w.dial(peer)
		for name, req := range map[string]*wire.Request{
			"watch and shell": {Watch: &wire.WatchRequest{}, Shell: &wire.ShellRequest{Zathras: z.id, Cols: 80, Rows: 24}},
			"register and logs": {
				Register: &wire.Register{Service: "web", Name: "web-2", State: idle()},
				Logs:     &wire.LogsRequest{Zathras: z.id},
			},
		} {
			if _, _, err := ask(t, conn, req); wire.CodeOf(err) != wire.CodeBadRequest {
				t.Errorf("%s from %s = %v, want a bad-request", name, peer, err)
			}
		}
	}
	if _, ok := w.member("web", nodetest.ID(2)); ok {
		t.Error("a request with two fields registered the operator")
	}
}

func TestServeEndsWithItsContext(t *testing.T) {
	w := startWorld(t, testConfig(t))
	z := w.zathras(10, "web", "web-1", idle())
	w.stop()
	eventually(t, "the connection of the zathras to be closed", func() bool { return closed(z.conn) })
}
