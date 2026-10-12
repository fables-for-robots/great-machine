package zathras

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
)

// patience is how long a test waits for something that is to happen.
const patience = 10 * time.Second

var (
	draalID   = nodetest.ID(1)
	zathrasID = nodetest.ID(2)
)

// eventually fails the test unless ok returns true within patience.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", patience, what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// within returns what f returns, and fails the test when f has not
// returned within patience.
func within[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	result := make(chan T, 1)
	go func() { result <- f() }()
	select {
	case v := <-result:
		return v
	case <-time.After(patience):
		t.Fatalf("waited %v for %s", patience, what)
		panic("not reached")
	}
}

// setup is what a test wants of its world to be other than usual.
type setup struct {
	store Preloader     // the store of the zathras; nil: it has none of its own
	stats *stats.Reader // nil: no samples
	shell string        // the program of a shell; "": /bin/sh
	deaf  bool          // nobody listens as draal until the test does
}

// world is an agent that runs, its supervisor, which runs too, and the
// test's side of the network, where the test plays draal.
type world struct {
	t        *testing.T
	net      *nodetest.Net
	listener node.Listener // nil while deaf
	ring     *logring.Ring
	sup      *supervisor.Supervisor
	agent    *Agent
	dials    atomic.Int64 // how often the agent has dialed
	stop     func()       // ends Run and the supervisor
	ran      chan error   // what Run returned
}

// newWorld starts a supervisor and an agent, which are stopped at the end
// of the test.
func newWorld(t *testing.T, s setup) *world {
	t.Helper()
	w := &world{t: t, net: nodetest.New(), ring: logring.New(400, 16<<10), ran: make(chan error, 1)}
	if !s.deaf {
		w.listener = w.net.Listen(draalID)
	}
	log := slog.New(slog.DiscardHandler)
	w.sup = supervisor.New(supervisor.Config{
		Ring:        w.ring,
		StopTimeout: 5 * time.Second,
		Log:         log,
	})
	w.agent = New(Config{
		Service: "web",
		Name:    "web-0",
		Version: "v1.2.3",
		Dial: func(ctx context.Context) (node.Conn, error) {
			w.dials.Add(1)
			return w.net.Dial(ctx, zathrasID, draalID)
		},
		Supervisor: w.sup,
		Ring:       w.ring,
		Stats:      s.stats,
		Preload:    s.store,
		Shell: ptyshell.Config{
			Shell: s.shell,
			Env:   []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PS1=$ "},
		},
		ReportEvery: 20 * time.Millisecond,
		RedialMin:   5 * time.Millisecond,
		// Far above what a loaded machine can stall: it also bounds how long
		// draal may take to answer register.
		RedialMax: 2 * time.Second,
		Log:       log,
	})

	ctx, cancel := context.WithCancel(context.Background())
	supervised := make(chan error, 1)
	go func() { supervised <- w.sup.Run(ctx) }()
	go func() { w.ran <- w.agent.Run(ctx) }()
	stopped := false
	w.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := within(t, "Run to return", func() error { return <-w.ran }); err != nil {
			t.Errorf("Run() = %v, want nil", err)
		}
		within(t, "the supervisor to stop", func() error { return <-supervised })
	}
	t.Cleanup(w.stop)
	return w
}

// link is a connection of the agent, as draal has it.
type link struct {
	t        *testing.T
	conn     node.Conn
	register wire.Register   // what the agent registered with
	reports  chan wire.State // what it reported since
}

// accept plays the part of draal in a registration: it accepts the
// connection, reads the register request and answers it. The reports that
// follow are read into the link.
func (w *world) accept() *link {
	w.t.Helper()
	if w.listener == nil {
		w.listener = w.net.Listen(draalID)
	}
	type accepted struct {
		l   *link
		err error
	}
	got := within(w.t, "the agent to register", func() accepted {
		conn, err := w.listener.Accept(context.Background())
		if err != nil {
			return accepted{err: err}
		}
		s, err := conn.AcceptStream(context.Background())
		if err != nil {
			return accepted{err: err}
		}
		var req wire.Request
		if err := wire.Read(s, &req); err != nil {
			return accepted{err: err}
		}
		if req.Register == nil {
			w.t.Errorf("the first request is %+v, want a register", req)
			return accepted{err: io.ErrUnexpectedEOF}
		}
		if err := wire.Write(s, &wire.Response{Registered: &wire.Registered{}}); err != nil {
			return accepted{err: err}
		}
		l := &link{t: w.t, conn: conn, register: *req.Register, reports: make(chan wire.State, 4096)}
		go func() {
			for {
				var st wire.State
				if wire.Read(s, &st) != nil {
					return
				}
				select {
				case l.reports <- st:
				default: // a test that does not read them
				}
			}
		}()
		return accepted{l: l}
	})
	if got.err != nil {
		w.t.Fatalf("accepting the agent: %v", got.err)
	}
	w.t.Cleanup(func() { got.l.conn.Close() })
	return got.l
}

// report returns the next report for which ok returns true.
func (l *link) report(what string, ok func(wire.State) bool) wire.State {
	l.t.Helper()
	deadline := time.After(patience)
	for {
		select {
		case st := <-l.reports:
			if ok(st) {
				return st
			}
		case <-deadline:
			l.t.Fatalf("waited %v for a report %s", patience, what)
		}
	}
}

// open opens a stream and writes req on it. The stream is closed at the
// end of the test, if the test has not closed it.
func (l *link) open(req *wire.Request) node.Stream {
	l.t.Helper()
	s, err := l.conn.OpenStream(context.Background())
	if err != nil {
		l.t.Fatalf("opening a stream: %v", err)
	}
	l.t.Cleanup(func() { s.Close() })
	if err := wire.Write(s, req); err != nil {
		l.t.Fatalf("writing the request: %v", err)
	}
	return s
}

// read reads one response of s.
func (l *link) read(s node.Stream) (*wire.Response, error) {
	l.t.Helper()
	type answer struct {
		resp *wire.Response
		err  error
	}
	got := within(l.t, "a response", func() answer {
		resp, err := wire.ReadResponse(s)
		return answer{resp, err}
	})
	return got.resp, got.err
}

// call sends req on a stream of its own, reads one response and closes
// the stream, as draal does.
func (l *link) call(req *wire.Request) (*wire.Response, error) {
	l.t.Helper()
	s := l.open(req)
	defer s.Close()
	return l.read(s)
}

// idle fails the test unless the agent comes to answer no stream at all.
func (w *world) idle() {
	w.t.Helper()
	eventually(w.t, "every request to have ended", func() bool { return w.agent.handlers.Load() == 0 })
}

// The files of a cgroup and a /proc/net/dev, for a stats.Reader.
func fixtureStats(t *testing.T) *stats.Reader {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"cpu.stat":       "usage_usec 5000000\nuser_usec 3200000\nsystem_usec 1800000\n",
		"memory.current": "104857600\n",
		"memory.max":     "536870912\n",
		"io.stat":        "8:0 rbytes=1000000 wbytes=2000000 rios=30 wios=40 dbytes=0 dios=0\n",
		"net_dev": "Inter-|   Receive                                                |  Transmit\n" +
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
			"  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &stats.Reader{Cgroup: dir, NetDev: filepath.Join(dir, "net_dev")}
}

func TestRegistersAndReports(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()

	if l.conn.RemoteID() != zathrasID {
		t.Errorf("the connection is from %s, want %s", l.conn.RemoteID(), zathrasID)
	}
	reg := l.register
	if reg.Service != "web" || reg.Name != "web-0" || reg.Version != "v1.2.3" {
		t.Errorf("registered as %q, %q, %q, want web, web-0, v1.2.3", reg.Service, reg.Name, reg.Version)
	}
	if reg.State.Phase != wire.PhaseIdle || reg.State.Generation != 0 {
		t.Errorf("registered with phase %q and generation %d, want idle and 0", reg.State.Phase, reg.State.Generation)
	}
	if reg.State.Sample != nil {
		t.Errorf("registered with a sample, %+v, though the agent has no stats", reg.State.Sample)
	}

	// Nothing changes, and the reports come all the same.
	for i := range 3 {
		st := l.report("at all", func(wire.State) bool { return true })
		if st.Phase != wire.PhaseIdle || st.Sample != nil {
			t.Fatalf("report %d = %+v, want idle and no sample", i, st)
		}
	}
}

func TestReportsCarryASample(t *testing.T) {
	w := newWorld(t, setup{stats: fixtureStats(t)})
	l := w.accept()

	if s := l.register.State.Sample; s == nil || s.Memory != 104857600 {
		t.Errorf("registered with the sample %+v, want one with 104857600 bytes of memory", s)
	}
	first := l.report("with a sample", func(st wire.State) bool { return st.Sample != nil })
	if first.Sample.Memory != 104857600 || first.Sample.MemoryLimit != 536870912 {
		t.Errorf("the sample is %+v, want 104857600 bytes of 536870912", first.Sample)
	}
	// A sample is taken for every report that the time brings.
	l.report("with a later sample", func(st wire.State) bool {
		return st.Sample != nil && st.Sample.At.After(first.Sample.At)
	})
	if s := w.agent.State().Sample; s == nil || s.Memory != 104857600 {
		t.Errorf("State().Sample = %+v, want the latest sample", s)
	}
}

func TestNoSampleWhereTheSystemGivesNone(t *testing.T) {
	// A reader of a directory without the files.
	dir := t.TempDir()
	w := newWorld(t, setup{stats: &stats.Reader{Cgroup: dir, NetDev: filepath.Join(dir, "net_dev")}})
	l := w.accept()
	if l.register.State.Sample != nil {
		t.Errorf("registered with the sample %+v, want none", l.register.State.Sample)
	}
	for range 2 {
		if st := l.report("at all", func(wire.State) bool { return true }); st.Sample != nil {
			t.Fatalf("a report has the sample %+v, want none", st.Sample)
		}
	}
}

func TestRequestsThatAreNoneOfTheFour(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	cases := []struct {
		name string
		req  *wire.Request
	}{
		{"empty", &wire.Request{}},
		{"watch", &wire.Request{Watch: &wire.WatchRequest{}}},
		{"store", &wire.Request{Store: &wire.StoreRequest{}}},
		{"register", &wire.Request{Register: &wire.Register{Service: "web"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := l.call(c.req)
			if wire.CodeOf(err) != wire.CodeBadRequest {
				t.Fatalf("answered %+v, %v, want bad-request", resp, err)
			}
		})
	}
	w.idle()
}

func TestRequestThatIsNoMessage(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	s, err := l.conn.OpenStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A frame of three bytes that are no CBOR map.
	if _, err := s.Write([]byte{0, 0, 0, 3, 0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.read(s); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("answered %v, want bad-request", err)
	}
}

func TestRegistersAgainWhenTheConnectionHasEnded(t *testing.T) {
	w := newWorld(t, setup{})
	first := w.accept()
	first.conn.Close()

	second := w.accept()
	if second.register.Service != "web" || second.register.State.Phase != wire.PhaseIdle {
		t.Errorf("registered again with %+v", second.register)
	}
	second.report("on the new connection", func(wire.State) bool { return true })
}

func TestKeepsDialingUntilDraalIsThere(t *testing.T) {
	w := newWorld(t, setup{deaf: true})
	eventually(t, "the agent to have dialed five times", func() bool { return w.dials.Load() >= 5 })

	l := w.accept() // listens from here on
	if l.register.Name != "web-0" {
		t.Errorf("registered as %q", l.register.Name)
	}
	l.report("after the registration", func(wire.State) bool { return true })
}

func TestRedial(t *testing.T) {
	// The spec's values: 1 second, twice as long each time, up to 30, and
	// 1 second again after a registration that worked.
	r := redial{min: time.Second, max: 30 * time.Second}
	steps := []struct {
		registered bool
		want       time.Duration
	}{
		{false, 1 * time.Second},
		{false, 2 * time.Second},
		{false, 4 * time.Second},
		{false, 8 * time.Second},
		{false, 16 * time.Second},
		{false, 30 * time.Second},
		{false, 30 * time.Second},
		{true, 1 * time.Second},
		{false, 2 * time.Second},
		{true, 1 * time.Second},
		{true, 1 * time.Second},
	}
	for i, step := range steps {
		if got := r.next(step.registered); got != step.want {
			t.Fatalf("step %d: next(%v) = %v, want %v", i, step.registered, got, step.want)
		}
	}
}

func TestDefaults(t *testing.T) {
	a := New(Config{})
	if a.cfg.ReportEvery != 2*time.Second || a.cfg.RedialMin != time.Second || a.cfg.RedialMax != 30*time.Second {
		t.Fatalf("New(Config{}) reports every %v and redials after %v up to %v, want 2s, 1s and 30s",
			a.cfg.ReportEvery, a.cfg.RedialMin, a.cfg.RedialMax)
	}
}

func TestRunEndsWithItsContext(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	w.stop() // fails the test unless Run returns nil in time
	select {
	case <-l.conn.Done():
	case <-time.After(patience):
		t.Fatal("the connection was not closed")
	}
}

func TestRunEndsWhileItWaitsToDial(t *testing.T) {
	w := newWorld(t, setup{deaf: true})
	eventually(t, "the agent to have dialed", func() bool { return w.dials.Load() >= 1 })
	w.stop()
}
