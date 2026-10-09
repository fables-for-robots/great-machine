### Task 11: zathras, the agent

**Files:**
- Create: `zathras/agent.go` (the configuration, the connection, the registration, the reports, the streams draal opens)
- Create: `zathras/answer.go` (which request is answered by what; it grows with every cycle of this task, and is given whole each time)
- Create: `zathras/commands.go` (prepare and activate)
- Create: `zathras/logs.go`
- Create: `zathras/shell.go`
- Test: `zathras/agent_test.go` (the helpers of every test of the package, too), `zathras/commands_test.go`, `zathras/logs_test.go`, `zathras/shell_test.go`

**Interfaces:**
- Consumes:

  ```go
  // wire (Task 1)
  func Write(w io.Writer, msg any) error
  func Read(r io.Reader, msg any) error            // io.EOF when r ends before a frame
  func ReadResponse(r io.Reader) (*Response, error) // a Response with Error is returned as that *Error
  func Call(s io.ReadWriter, req *Request) (*Response, error)
  func WriteError(w io.Writer, err error) error
  func Errorf(code Code, format string, args ...any) *Error
  func CodeOf(err error) Code
  // and the types Request, Response, Register, Registered, Prepare, Prepared,
  // Activate, Activated, LogsRequest, LogLine, ShellRequest, ShellInput,
  // WinSize, ShellOutput, State, Sample, Phase (PhaseIdle, PhaseRunning),
  // Code (CodeBadRequest, CodeInternal, CodeUnavailable), and for the tests
  // WatchRequest and StoreRequest.

  // node (Task 2)
  type Stream interface {
  	io.Reader
  	io.Writer
  	CloseWrite() error // the peer reads io.EOF after what was written
  	Close() error      // both directions; a Read or Write of the peer that waits fails
  }
  type Conn interface {
  	RemoteID() wire.ID
  	OpenStream(ctx context.Context) (Stream, error)
  	AcceptStream(ctx context.Context) (Stream, error)
  	Done() <-chan struct{}
  	Close() error
  }
  type Listener interface {
  	ID() wire.ID
  	LocalAddr() netip.AddrPort
  	Accept(ctx context.Context) (Conn, error)
  	Close() error
  }

  // node/nodetest (Task 3), in the tests
  func New() *Net
  func (n *Net) Listen(id wire.ID) node.Listener
  func (n *Net) Dial(ctx context.Context, from, to wire.ID) (node.Conn, error) // fails when nobody listens as to
  func ID(n int) wire.ID

  // logring (Task 4)
  const Stdout, Stderr, Zathras = "stdout", "stderr", "zathras"
  func New(size, maxLine int) *Ring
  func (r *Ring) Add(stream, text string)
  func (r *Ring) Lines() []wire.LogLine
  func (r *Ring) Follow(ctx context.Context, buffer int) ([]wire.LogLine, <-chan wire.LogLine) // the channel is closed when ctx is done

  // supervisor (Task 5)
  type Config struct{ Ring *logring.Ring; StopTimeout time.Duration; Log *slog.Logger; /* and more */ }
  func New(cfg Config) *Supervisor
  func (s *Supervisor) Run(ctx context.Context) error
  func (s *Supervisor) Activate(generation uint64, binary string, args []string) error
  func (s *Supervisor) State() wire.State
  func (s *Supervisor) Changes() <-chan struct{}
  func CheckExecutable(path string) error
  var ErrNotExecutable, ErrStopped error

  // testservice/build (Task 5), in the tests
  func Binary(tb testing.TB) string // testservice [--stdout TEXT] [--stderr TEXT] runs until SIGTERM

  // stats (Task 6)
  type Reader struct{ Cgroup, NetDev string }
  func (r *Reader) Read(now time.Time) (wire.Sample, bool)

  // preload (Task 7)
  type Client struct{ URL string; HTTP *http.Client }
  func (c Client) Preload(ctx context.Context, names []string) error // *preload.Error for an answer that is not 200

  // ptyshell (Task 8)
  type Config struct{ Shell string; Env []string; Dir, Term string; Cols, Rows uint16 }
  func Start(cfg Config) (*Session, error)
  func (s *Session) Read(p []byte) (int, error)
  func (s *Session) Write(p []byte) (int, error)
  func (s *Session) Resize(cols, rows uint16) error
  func (s *Session) Wait() int
  func (s *Session) Close() error
  ```

- Produces:

  ```go
  package zathras

  type Config struct {
  	Service    string
  	Name       string
  	Version    string
  	Dial       func(ctx context.Context) (node.Conn, error) // dials draal
  	Supervisor *supervisor.Supervisor                        // the caller runs it
  	Ring       *logring.Ring
  	Stats      *stats.Reader   // nil: no samples
  	Preload    preload.Client
  	Shell      ptyshell.Config // Term, Cols and Rows come with the request
  	ReportEvery time.Duration  // 0: 2 seconds
  	RedialMin   time.Duration  // 0: 1 second
  	RedialMax   time.Duration  // 0: 30 seconds
  	Log         *slog.Logger
  }

  type Agent struct{ /* unexported */ }

  func New(cfg Config) *Agent

  // Run keeps a connection to draal until ctx is done: it dials, registers,
  // reports and answers prepare, activate, logs and shell, and dials again
  // when the connection has ended. It ends the shells before it returns.
  func (a *Agent) Run(ctx context.Context) error // always nil

  // State returns the whole state: the supervisor's, Prepared, Preparing
  // and the latest sample.
  func (a *Agent) State() wire.State
  ```

What the agent does where the contract leaves room, which the tasks of
draal rely on:

- **The end of a stream.** When the agent has written the last message of
  an answer (`Prepared`, `Activated`, an `Error`, the last line of `logs`
  without follow, the `Exit` of a shell) it ends its side with
  `CloseWrite`, and closes the stream only when draal has: it waits for the
  end of what it reads. So draal reads its answer and then `Close`s the
  stream, as "How streams are used" says, and nothing of an answer is lost
  to a `Close` that came too early.
- **Withdrawing.** On the streams of `prepare`, `activate` and `logs` the
  agent reads on after the request, and takes the end of what it reads
  (`io.EOF` as well as an error) for the end of the request: a prepare is
  ended that way, with its request to the sidecar, and so is a follower of
  the log. **draal must not `CloseWrite` such a stream** before it has its
  answer.
- **Codes.** `prepare`: `internal` when the sidecar refused or could not be
  reached, with what it said in the message
  (`the preload endpoint answered 404: <body>`); `bad-request` when the
  binary is no executable file, and for generation 0. `activate`:
  `bad-request` for a binary that is no executable file and for generation
  0; `unavailable` for `supervisor.ErrStopped`, as the zathras is about to
  go. `shell`: `internal` when the shell could not be started. Anything
  else, and a first frame that is no request: `bad-request`.
- **Text that travels.** A message whose text is no valid UTF-8 is refused
  by its reader, so every reason the agent writes goes through
  `strings.ToValidUTF8`. The lines of the ring are valid as they are.
- **Samples.** A sample is read at the registration and at every tick of
  `ReportEvery`. A report that a change brings between two ticks carries
  the sample of the last tick again, with the same `At`.
- **Preparing.** With several prepares at once, `Preparing` is the
  generation of the one that began last.
- **Times.** A dial is given `RedialMax`, and so is draal to answer
  `register`; after that the connection is closed and dialed again.

The tests are in the package `zathras` itself, as they read one unexported
counter, `handlers`, to see that a request has ended (a follower gone, a
shell closed). The test plays draal on a `nodetest.Net`; the supervisor is
a real one that runs `testservice`.

- [ ] **Step 1: Write the failing test of the connection**

`zathras/agent_test.go`:

```go
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
	"github.com/fables-for-robots/great-machine/preload"
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
	preloadURL string        // the preload endpoint; "": one that nothing listens at
	stats      *stats.Reader // nil: no samples
	shell      string        // the program of a shell; "": /bin/sh
	deaf       bool          // nobody listens as draal until the test does
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
	if s.preloadURL == "" {
		s.preloadURL = "http://127.0.0.1:1/v1/preload"
	}
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
		Preload:    preload.Client{URL: s.preloadURL},
		Shell: ptyshell.Config{
			Shell: s.shell,
			Env:   []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PS1=$ "},
		},
		ReportEvery: 20 * time.Millisecond,
		RedialMin:   5 * time.Millisecond,
		RedialMax:   40 * time.Millisecond,
		Log:         log,
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
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./zathras/`
Expected: FAIL (the build fails) — `undefined: Agent`, `undefined: New`, `undefined: Config`, `undefined: redial`

- [ ] **Step 3: Write the agent: connection, registration, reports**

`zathras/agent.go`:

```go
// Package zathras is the agent of an app container: it keeps a connection
// to draal, registers there, reports the state of its service, and answers
// what draal asks of it: prepare, activate, logs and shell.
//
// The service itself is run by a supervisor that the caller starts. The
// agent tells it what to run and reads its state, and nothing the
// connection does stops the service: a draal that cannot be reached is
// dialed again, for ever.
package zathras

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/preload"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what an agent is made of.
type Config struct {
	Service string // the service this zathras belongs to
	Name    string // what the operator sees
	Version string // of the program
	// Dial dials draal. Its context bounds the dial alone.
	Dial func(ctx context.Context) (node.Conn, error)
	// Supervisor runs the service. The caller runs it; the agent calls
	// Activate, State and Changes.
	Supervisor *supervisor.Supervisor
	// Ring holds the lines that logs answers with.
	Ring *logring.Ring
	// Stats reads the samples. nil: no samples.
	Stats *stats.Reader
	// Preload is the client of the sidecar, for prepare.
	Preload preload.Client
	// Shell is what a shell session is started with. Term, Cols and Rows
	// come with the request.
	Shell ptyshell.Config
	// ReportEvery is the longest time between two reports, and the time
	// between two samples. 0: 2 seconds.
	ReportEvery time.Duration
	// RedialMin is how long after the end of a connection draal is dialed
	// again. 0: 1 second.
	RedialMin time.Duration
	// RedialMax is the longest time between two dials, which is reached by
	// doubling. A dial, and a registration, are given as long. 0: 30
	// seconds.
	RedialMax time.Duration
	// Log is where the agent logs. nil: slog.Default().
	Log *slog.Logger
}

// Agent is the side of a zathras that talks to draal.
type Agent struct {
	cfg Config
	log *slog.Logger

	mu        sync.Mutex
	prepared  []uint64     // the generations that were prepared, ascending
	preparing []uint64     // the generations being prepared, oldest first
	sample    *wire.Sample // the latest one

	// changed has room for one value, and gets one when the state has
	// changed by something the supervisor does not tell: a prepare.
	changed chan struct{}

	// handlers is how many streams of draal are being answered. The tests
	// read it to see that a request has ended.
	handlers atomic.Int64
}

// New returns an agent. Run makes it work.
func New(cfg Config) *Agent {
	if cfg.ReportEvery <= 0 {
		cfg.ReportEvery = 2 * time.Second
	}
	if cfg.RedialMin <= 0 {
		cfg.RedialMin = time.Second
	}
	if cfg.RedialMax <= 0 {
		cfg.RedialMax = 30 * time.Second
	}
	if cfg.RedialMax < cfg.RedialMin {
		cfg.RedialMax = cfg.RedialMin
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Agent{cfg: cfg, log: cfg.Log, changed: make(chan struct{}, 1)}
}

// State returns the whole state: the supervisor's, Prepared, Preparing
// and the latest sample.
func (a *Agent) State() wire.State {
	st := a.cfg.Supervisor.State()
	a.mu.Lock()
	defer a.mu.Unlock()
	st.Prepared = slices.Clone(a.prepared)
	if n := len(a.preparing); n > 0 {
		st.Preparing = a.preparing[n-1]
	}
	if a.sample != nil {
		sample := *a.sample
		st.Sample = &sample
	}
	return st
}

// Run keeps a connection to draal until ctx is done: it dials, registers,
// reports and answers prepare, activate, logs and shell, and dials again
// when the connection has ended. It ends the shells before it returns.
func (a *Agent) Run(ctx context.Context) error {
	wait := redial{min: a.cfg.RedialMin, max: a.cfg.RedialMax}
	for ctx.Err() == nil {
		registered := a.connection(ctx)
		if ctx.Err() != nil {
			break
		}
		delay := wait.next(registered)
		a.log.Info("dialing draal again", "in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}

// redial says how long to wait before draal is dialed again: min after a
// connection that registered, and otherwise twice as long as the time
// before, up to max.
type redial struct {
	min, max time.Duration
	last     time.Duration // what next returned last; 0 at first
}

// next returns the time to wait after a connection that has ended, or
// that was not made. registered says whether it got as far as registering.
func (r *redial) next(registered bool) time.Duration {
	switch {
	case registered || r.last == 0:
		r.last = r.min
	case r.last >= r.max/2:
		r.last = r.max
	default:
		r.last *= 2
	}
	return r.last
}

// connection dials draal, registers, and reports and answers until the
// connection has ended or ctx is done. Everything that belonged to the
// connection has ended when it returns: the prepares, the followers of
// the log and the shells. It says whether the registration worked.
func (a *Agent) connection(ctx context.Context) (registered bool) {
	dialCtx, cancelDial := context.WithTimeout(ctx, a.cfg.RedialMax)
	conn, err := a.cfg.Dial(dialCtx)
	cancelDial()
	if err != nil {
		if ctx.Err() == nil {
			a.log.Warn("draal cannot be reached", "error", err)
		}
		return false
	}

	// ctx is the life of the connection from here on: it is done when the
	// connection has ended, and the connection is closed when it is done.
	ctx, cancel := context.WithCancel(ctx)
	var handlers sync.WaitGroup
	defer func() {
		cancel()
		conn.Close()
		handlers.Wait()
	}()
	go func() {
		select {
		case <-conn.Done():
			cancel()
		case <-ctx.Done():
			conn.Close()
		}
	}()

	reports, err := a.register(ctx, conn)
	if err != nil {
		if ctx.Err() == nil {
			a.log.Warn("registering with draal failed", "draal", conn.RemoteID(), "error", err)
		}
		return false
	}
	defer reports.Close()
	a.log.Info("registered with draal", "draal", conn.RemoteID())

	handlers.Go(func() {
		for {
			s, err := conn.AcceptStream(ctx)
			if err != nil {
				return
			}
			handlers.Go(func() { a.handle(ctx, s) })
		}
	})

	err = a.report(ctx, reports)
	if ctx.Err() == nil {
		a.log.Warn("reporting to draal failed", "error", err)
	} else {
		a.log.Info("the connection to draal has ended")
	}
	return true
}

// register opens the stream of the reports, sends the register request
// with the state as it is and waits for draal to answer it. A draal that
// does not answer within RedialMax loses the connection.
func (a *Agent) register(ctx context.Context, conn node.Conn) (node.Stream, error) {
	timeout := time.AfterFunc(a.cfg.RedialMax, func() { conn.Close() })
	defer timeout.Stop()

	s, err := conn.OpenStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening a stream: %w", err)
	}
	a.takeSample()
	resp, err := wire.Call(s, &wire.Request{Register: &wire.Register{
		Service: a.cfg.Service,
		Name:    a.cfg.Name,
		Version: a.cfg.Version,
		State:   a.State(),
	}})
	if err == nil && resp.Registered == nil {
		err = fmt.Errorf("draal answered something else than registered")
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// report writes the state to s whenever it has changed and at least every
// ReportEvery, until ctx is done or a write fails. A sample is taken
// every ReportEvery and goes out with the report of that time.
func (a *Agent) report(ctx context.Context, s io.Writer) error {
	ticker := time.NewTicker(a.cfg.ReportEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.takeSample()
		case <-a.cfg.Supervisor.Changes():
		case <-a.changed:
		}
		st := a.State()
		if err := wire.Write(s, &st); err != nil {
			return fmt.Errorf("writing a report: %w", err)
		}
	}
}

// takeSample reads a sample, which is the latest one from then on, or
// none where the system gives none. It is called by the goroutine of the
// connection alone, as a stats.Reader is used by one goroutine.
func (a *Agent) takeSample() {
	var sample *wire.Sample
	if a.cfg.Stats != nil {
		if s, ok := a.cfg.Stats.Read(time.Now()); ok {
			sample = &s
		}
	}
	a.mu.Lock()
	a.sample = sample
	a.mu.Unlock()
}

// stateChanged has the next report sent at once.
func (a *Agent) stateChanged() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// handle answers one stream that draal opened. ctx is done when the
// connection has ended.
func (a *Agent) handle(ctx context.Context, s node.Stream) {
	a.handlers.Add(1)
	defer a.handlers.Add(-1)
	defer s.Close()

	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		if err != io.EOF {
			wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "reading the request: %s", strings.ToValidUTF8(err.Error(), "\uFFFD")))
			s.CloseWrite()
			io.Copy(io.Discard, s) // until draal has read the answer and closed
		}
		return
	}
	if req.Shell != nil {
		// A shell reads its stream itself: what is typed comes on it.
		a.answerShell(ctx, s, req.Shell)
		return
	}

	// Nothing but the request is sent on any other stream, so that the
	// end of what is read, or an error, is the end of the request: draal
	// has closed the stream, or the connection has ended. That is how a
	// prepare is withdrawn and how a follower of the log goes.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()

	a.answer(ctx, s, &req)

	// The answer is complete. draal closes the stream when it has read it,
	// and only then is the stream closed here: a Close before that could
	// take away what draal has not read yet.
	s.CloseWrite()
	<-ctx.Done()
}
```

`zathras/answer.go`, which answers nothing yet:

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}

// answerShell answers a shell request, on a stream that it reads itself.
// A zathras has no shell yet.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras has no shell yet"))
	s.CloseWrite()
	io.Copy(io.Discard, s) // until draal has read the answer and closed
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./zathras/ && go test -race ./zathras/`
Expected: `ok`

- [ ] **Step 5: Commit**

```bash
git add zathras/
git commit -m "Zathras: the agent dials draal, registers and reports"
```

- [ ] **Step 6: Write the failing tests of prepare and activate**

`zathras/commands_test.go`:

```go
package zathras

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// sidecar is a preload endpoint of a test.
type sidecar struct {
	url     string
	bodies  chan string   // what was posted
	entered chan struct{} // gets a value when a request has come
	release chan struct{} // if hang is set, a request waits for a value
	gone    chan struct{} // gets a value when a request that waited was ended by its client
}

// newSidecar returns an endpoint that answers with status and answer. One
// that hangs answers only when the test sends on release, or never if the
// client goes first.
func newSidecar(t *testing.T, status int, answer string, hang bool) *sidecar {
	t.Helper()
	sc := &sidecar{
		bodies:  make(chan string, 16),
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
		gone:    make(chan struct{}, 16),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sc.bodies <- string(body)
		sc.entered <- struct{}{}
		if hang {
			select {
			case <-sc.release:
			case <-r.Context().Done():
				sc.gone <- struct{}{}
				return
			}
		}
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	sc.url = srv.URL + "/v1/preload"
	return sc
}

// notExecutable returns a file that is there and may not be executed.
func notExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func prepare(generation uint64, binary string, closure ...string) *wire.Request {
	return &wire.Request{Prepare: &wire.Prepare{Generation: generation, Binary: binary, Closure: closure}}
}

func activate(generation uint64, binary string, args ...string) *wire.Request {
	return &wire.Request{Activate: &wire.Activate{Generation: generation, Binary: binary, Args: args}}
}

func TestPrepare(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "fetched 2 paths\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()
	binary := build.Binary(t)

	resp, err := l.call(prepare(7, binary, "6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0", "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	want := "/nix/store/6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0\n/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0\n"
	if got := <-sc.bodies; got != want {
		t.Errorf("the endpoint was sent %q, want %q", got, want)
	}
	st := l.report("with generation 7 prepared", func(st wire.State) bool { return slices.Contains(st.Prepared, 7) })
	if st.Preparing != 0 || st.Phase != wire.PhaseIdle {
		t.Errorf("the report is %+v, want nothing preparing and the service idle: a prepare starts nothing", st)
	}

	// Prepared is kept ascending, and holds a generation once.
	for _, generation := range []uint64{3, 9, 7, 3} {
		if _, err := l.call(prepare(generation, binary, "a")); err != nil {
			t.Fatalf("prepare of generation %d: %v", generation, err)
		}
	}
	if got := w.agent.State().Prepared; !slices.Equal(got, []uint64{3, 7, 9}) {
		t.Errorf("State().Prepared = %v, want [3 7 9]", got)
	}
	l.report("with 3, 7 and 9 prepared", func(st wire.State) bool { return slices.Equal(st.Prepared, []uint64{3, 7, 9}) })
	w.idle()
}

func TestPrepareIsReportedWhileItRuns(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	s := l.open(prepare(4, build.Binary(t), "a"))
	st := l.report("with generation 4 preparing", func(st wire.State) bool { return st.Preparing == 4 })
	if len(st.Prepared) != 0 {
		t.Errorf("Prepared = %v while the prepare runs, want none", st.Prepared)
	}

	sc.release <- struct{}{}
	if resp, err := l.read(s); err != nil || resp.Prepared == nil {
		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
	}
	l.report("with generation 4 prepared and nothing preparing", func(st wire.State) bool {
		return st.Preparing == 0 && slices.Equal(st.Prepared, []uint64{4})
	})
}

func TestPrepareThatTheSidecarRefuses(t *testing.T) {
	const said = "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0: the server has no such path"
	sc := newSidecar(t, http.StatusNotFound, said+"\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
	if err == nil {
		t.Fatalf("prepare answered %+v, want an error", resp)
	}
	if !strings.Contains(err.Error(), said) || !strings.Contains(err.Error(), "404") {
		t.Errorf("the error is %q, want one with the status and what the sidecar said, %q", err, said)
	}
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that failed, want nothing prepared or preparing", st)
	}
}

func TestPrepareWithoutASidecar(t *testing.T) {
	w := newWorld(t, setup{}) // nothing listens at the preload endpoint
	l := w.accept()
	resp, err := l.call(prepare(5, build.Binary(t), "a"))
	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), "preload") {
		t.Fatalf("prepare answered %+v, %v, want an internal error that says preload", resp, err)
	}
}

func TestPrepareOfABinaryThatIsNotExecutable(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", false)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	for _, binary := range []string{notExecutable(t), filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		resp, err := l.call(prepare(5, binary, "a"))
		if wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), binary) {
			t.Errorf("prepare of %s answered %+v, %v, want bad-request with the path", binary, resp, err)
		}
	}
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 {
		t.Errorf("State().Prepared = %v, want none", st.Prepared)
	}
}

func TestPrepareWithoutAGeneration(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	if resp, err := l.call(prepare(0, build.Binary(t), "a")); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("prepare of generation 0 answered %+v, %v, want bad-request", resp, err)
	}
}

func TestPrepareIsWithdrawnByClosingItsStream(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	s := l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
	s.Close()

	// The request to the sidecar is ended, which is how its fetches end.
	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
	w.idle()
	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
		t.Errorf("State() = %+v after a prepare that was withdrawn, want nothing prepared or preparing", st)
	}
}

func TestPrepareEndsWithTheConnection(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "", true)
	w := newWorld(t, setup{preloadURL: sc.url})
	l := w.accept()

	l.open(prepare(6, build.Binary(t), "a"))
	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
	l.conn.Close()

	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
	w.idle()
	// And the agent is back.
	w.accept()
}

func TestActivate(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	binary := build.Binary(t)

	resp, err := l.call(activate(7, binary, "--stdout", "hello"))
	if err != nil || resp.Activated == nil {
		t.Fatalf("activate answered %+v, %v, want activated", resp, err)
	}
	st := l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })
	if st.Generation != 7 || st.Binary != binary || !slices.Equal(st.Args, []string{"--stdout", "hello"}) || st.PID == 0 {
		t.Errorf("the report is %+v, want generation 7, the binary, its arguments and a process ID", st)
	}
}

func TestActivateOfABinaryThatIsNotExecutable(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	binary := build.Binary(t)
	if _, err := l.call(activate(7, binary)); err != nil {
		t.Fatal(err)
	}
	before := l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	bad := notExecutable(t)
	resp, err := l.call(activate(8, bad))
	if wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), bad) {
		t.Fatalf("activate answered %+v, %v, want bad-request with the path", resp, err)
	}
	if resp, err := l.call(activate(0, binary)); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("activate of generation 0 answered %+v, %v, want bad-request", resp, err)
	}
	// What runs was left alone.
	st := w.agent.State()
	if st.Generation != 7 || st.Binary != binary || st.Phase != wire.PhaseRunning || st.PID != before.PID {
		t.Errorf("State() = %+v, want generation 7 running as process %d still", st, before.PID)
	}
}

// The service is not touched by what happens to the connection: the spec's
// "a draal that is down or unreachable stops no service".
func TestServiceOutlivesTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	first := w.accept()
	binary := build.Binary(t)
	if _, err := first.call(activate(7, binary)); err != nil {
		t.Fatal(err)
	}
	before := first.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	first.conn.Close()
	second := w.accept()

	// It registers with the state it has then.
	st := second.register.State
	if st.Phase != wire.PhaseRunning || st.Generation != 7 || st.Binary != binary || st.PID != before.PID {
		t.Errorf("registered again with %+v, want generation 7 running as process %d still", st, before.PID)
	}
	if st.Restarts != 0 {
		t.Errorf("Restarts = %d, want 0", st.Restarts)
	}
}

func TestStateIsTheWholeState(t *testing.T) {
	sc := newSidecar(t, http.StatusOK, "", false)
	w := newWorld(t, setup{preloadURL: sc.url, stats: fixtureStats(t)})
	l := w.accept()
	binary := build.Binary(t)

	if _, err := l.call(prepare(2, binary, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.call(activate(2, binary, "--stdout", "hello")); err != nil {
		t.Fatal(err)
	}
	l.report("of a service that runs", func(st wire.State) bool { return st.Phase == wire.PhaseRunning })

	st := w.agent.State()
	// The supervisor's part.
	if st.Generation != 2 || st.Binary != binary || st.Phase != wire.PhaseRunning || st.PID == 0 {
		t.Errorf("State() = %+v, want generation 2 running", st)
	}
	// The agent's.
	if !slices.Equal(st.Prepared, []uint64{2}) || st.Preparing != 0 {
		t.Errorf("State() has Prepared %v and Preparing %d, want [2] and 0", st.Prepared, st.Preparing)
	}
	if st.Sample == nil || st.Sample.Memory != 104857600 {
		t.Errorf("State().Sample = %+v, want the sample of the fixture", st.Sample)
	}
	// What State returns is the caller's.
	st.Prepared[0] = 99
	st.Sample.Memory = 1
	if again := w.agent.State(); again.Prepared[0] != 2 || again.Sample.Memory != 104857600 {
		t.Errorf("State() gave away its own slice or sample: %+v", again)
	}
}
```

- [ ] **Step 7: Run the tests to see them fail**

Run: `go test ./zathras/ -run 'Prepare|Activate|Service|State'`
Expected: FAIL — for instance `TestPrepare`: `prepare answered <nil>, bad-request: a zathras answers prepare, activate, logs and shell, and this request is none of them, want prepared`

- [ ] **Step 8: Write prepare and activate**

`zathras/commands.go`:

```go
package zathras

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
)

// prepare has the sidecar fetch the closure and then checks the binary.
// ctx is done when draal has withdrawn the prepare, which ends the request
// to the sidecar. The generation is in Preparing while this runs, and in
// Prepared when it has worked.
func (a *Agent) prepare(ctx context.Context, req *wire.Prepare) error {
	if req.Generation == 0 {
		return wire.Errorf(wire.CodeBadRequest, "a prepare needs a generation")
	}
	a.mu.Lock()
	a.preparing = append(a.preparing, req.Generation)
	a.mu.Unlock()
	a.stateChanged()

	err := a.fetch(ctx, req)

	a.mu.Lock()
	if i := slices.Index(a.preparing, req.Generation); i >= 0 {
		a.preparing = slices.Delete(a.preparing, i, i+1)
	}
	if err == nil {
		if i, found := slices.BinarySearch(a.prepared, req.Generation); !found {
			a.prepared = slices.Insert(a.prepared, i, req.Generation)
		}
	}
	a.mu.Unlock()
	a.stateChanged()

	if err != nil {
		a.log.Warn("prepare failed", "generation", req.Generation, "binary", req.Binary, "error", err)
		return err
	}
	a.log.Info("prepared", "generation", req.Generation, "binary", req.Binary, "paths", len(req.Closure))
	return nil
}

// fetch is the work of a prepare. Its errors are as they travel: the
// message is the reason, which for an answer of the sidecar that is not
// 200 holds what the sidecar said.
func (a *Agent) fetch(ctx context.Context, req *wire.Prepare) error {
	if err := a.cfg.Preload.Preload(ctx, req.Closure); err != nil {
		return wire.Errorf(wire.CodeInternal, "%s", reason(err))
	}
	if err := supervisor.CheckExecutable(req.Binary); err != nil {
		return wire.Errorf(wire.CodeBadRequest, "%s", reason(err))
	}
	return nil
}

// activate has the supervisor take the generation as its own. A binary
// that is no executable file is refused, and what runs is left alone.
func (a *Agent) activate(req *wire.Activate) error {
	if req.Generation == 0 {
		return wire.Errorf(wire.CodeBadRequest, "an activate needs a generation")
	}
	err := a.cfg.Supervisor.Activate(req.Generation, req.Binary, req.Args)
	switch {
	case err == nil:
		a.log.Info("activated", "generation", req.Generation, "binary", req.Binary)
		a.stateChanged()
		return nil
	case errors.Is(err, supervisor.ErrStopped):
		return wire.Errorf(wire.CodeUnavailable, "%s", reason(err))
	default:
		a.log.Warn("activate refused", "generation", req.Generation, "binary", req.Binary, "error", err)
		return wire.Errorf(wire.CodeBadRequest, "%s", reason(err))
	}
}

// reason returns the text of err as it can travel: a message whose text
// is no valid UTF-8 is refused by whoever reads it, and an error may quote
// anything.
func reason(err error) string {
	return strings.ToValidUTF8(err.Error(), "\uFFFD")
}
```

`zathras/answer.go`, whole:

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	case req.Prepare != nil:
		if err := a.prepare(ctx, req.Prepare); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Prepared: &wire.Prepared{}})
	case req.Activate != nil:
		if err := a.activate(req.Activate); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Activated: &wire.Activated{}})
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}

// answerShell answers a shell request, on a stream that it reads itself.
// A zathras has no shell yet.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras has no shell yet"))
	s.CloseWrite()
	io.Copy(io.Discard, s) // until draal has read the answer and closed
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go vet ./zathras/ && go test -race ./zathras/`
Expected: `ok`

- [ ] **Step 10: Commit**

```bash
git add zathras/
git commit -m "Zathras: the agent answers prepare and activate"
```

- [ ] **Step 11: Write the failing tests of logs**

`zathras/logs_test.go`:

```go
package zathras

import (
	"io"
	"slices"
	"testing"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

func logs(follow bool) *wire.Request {
	return &wire.Request{Logs: &wire.LogsRequest{Follow: follow}}
}

// has says whether lines holds a line of stream with text.
func has(lines []wire.LogLine, stream, text string) bool {
	return slices.ContainsFunc(lines, func(l wire.LogLine) bool { return l.Stream == stream && l.Text == text })
}

func TestLogs(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	if _, err := l.call(activate(1, build.Binary(t), "--stdout", "hello from the service", "--stderr", "and a warning")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the lines of the service to be in the ring", func() bool {
		lines := w.ring.Lines()
		return has(lines, logring.Stdout, "hello from the service") && has(lines, logring.Stderr, "and a warning")
	})
	w.ring.Add(logring.Zathras, "the last line")

	s := l.open(logs(false))
	var got []wire.LogLine
	for {
		resp, err := l.read(s)
		if err == io.EOF {
			break // the agent ends the stream after the last line
		}
		if err != nil || resp.Line == nil {
			t.Fatalf("logs answered %+v, %v, want a line", resp, err)
		}
		got = append(got, *resp.Line)
	}
	if !has(got, logring.Stdout, "hello from the service") || !has(got, logring.Stderr, "and a warning") {
		t.Errorf("the lines of the service are not among %+v", got)
	}
	if last := got[len(got)-1]; last.Text != "the last line" || last.Stream != logring.Zathras || last.At.IsZero() {
		t.Errorf("the last line is %+v, want the last of the ring, with its time", last)
	}
	s.Close()
	w.idle()
}

func TestLogsOfAnEmptyRing(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	s := l.open(logs(false))
	if resp, err := l.read(s); err != io.EOF {
		t.Fatalf("logs answered %+v, %v, want the end of the stream", resp, err)
	}
}

func TestLogsFollow(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	w.ring.Add(logring.Stdout, "one")
	w.ring.Add(logring.Stdout, "two")

	s := l.open(logs(true))
	for _, want := range []string{"one", "two"} {
		resp, err := l.read(s)
		if err != nil || resp.Line == nil || resp.Line.Text != want {
			t.Fatalf("logs answered %+v, %v, want the line %q", resp, err, want)
		}
	}
	// The stream goes on, and a line that is written later arrives.
	for _, want := range []string{"three", "four"} {
		w.ring.Add(logring.Stderr, want)
		resp, err := l.read(s)
		if err != nil || resp.Line == nil || resp.Line.Text != want || resp.Line.Stream != logring.Stderr {
			t.Fatalf("logs answered %+v, %v, want the line %q of stderr", resp, err, want)
		}
	}

	// draal closes the stream, and the follower is gone.
	s.Close()
	w.idle()
	w.ring.Add(logring.Stdout, "five") // to nobody
}

func TestLogsFollowEndsWithTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	w.ring.Add(logring.Stdout, "one")
	s := l.open(logs(true))
	if resp, err := l.read(s); err != nil || resp.Line == nil {
		t.Fatalf("logs answered %+v, %v, want a line", resp, err)
	}
	eventually(t, "the follower to be counted", func() bool { return w.agent.handlers.Load() == 1 })
	l.conn.Close()
	w.idle()
}
```

- [ ] **Step 12: Run the tests to see them fail**

Run: `go test ./zathras/ -run Logs`
Expected: FAIL — for instance `TestLogsFollow`: `logs answered <nil>, bad-request: a zathras answers prepare, activate, logs and shell, and this request is none of them, want the line "one"`

- [ ] **Step 13: Write logs**

`zathras/logs.go`:

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/wire"
)

// followBuffer is how many lines may wait for a follower of the log that
// reads slowly before it loses the oldest of them.
const followBuffer = 1024

// logs writes the lines of the ring to w, and with follow every line that
// comes after them, until ctx is done or a write fails.
func (a *Agent) logs(ctx context.Context, w io.Writer, req *wire.LogsRequest) {
	if !req.Follow {
		for _, line := range a.cfg.Ring.Lines() {
			if wire.Write(w, &wire.Response{Line: &line}) != nil {
				return
			}
		}
		return
	}
	// Follow ends the following, and closes the channel, when ctx is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, more := a.cfg.Ring.Follow(ctx, followBuffer)
	for _, line := range lines {
		if wire.Write(w, &wire.Response{Line: &line}) != nil {
			return
		}
	}
	for line := range more {
		if wire.Write(w, &wire.Response{Line: &line}) != nil {
			return
		}
	}
}
```

`zathras/answer.go`, whole:

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	case req.Prepare != nil:
		if err := a.prepare(ctx, req.Prepare); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Prepared: &wire.Prepared{}})
	case req.Activate != nil:
		if err := a.activate(req.Activate); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Activated: &wire.Activated{}})
	case req.Logs != nil:
		a.logs(ctx, s, req.Logs)
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}

// answerShell answers a shell request, on a stream that it reads itself.
// A zathras has no shell yet.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras has no shell yet"))
	s.CloseWrite()
	io.Copy(io.Discard, s) // until draal has read the answer and closed
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `go vet ./zathras/ && go test -race ./zathras/`
Expected: `ok`

- [ ] **Step 15: Commit**

```bash
git add zathras/
git commit -m "Zathras: the agent answers logs"
```

- [ ] **Step 16: Write the failing tests of the shell**

`zathras/shell_test.go`:

```go
package zathras

import (
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// remoteShell is a shell session as draal has it.
type remoteShell struct {
	t      *testing.T
	l      *link
	s      node.Stream
	output strings.Builder
}

// openShell asks for a shell and reads the output that says it runs.
func (l *link) openShell(req wire.ShellRequest) *remoteShell {
	l.t.Helper()
	s := l.open(&wire.Request{Shell: &req})
	resp, err := l.read(s)
	if err != nil {
		l.t.Fatalf("shell answered %v", err)
	}
	// The first output is empty and says that the shell runs.
	if resp.Shell == nil || len(resp.Shell.Data) != 0 || resp.Shell.Exit != nil {
		l.t.Fatalf("shell answered %+v first, want an empty output", resp)
	}
	return &remoteShell{t: l.t, l: l, s: s}
}

// typeIn sends a line as it is typed.
func (r *remoteShell) typeIn(line string) {
	r.t.Helper()
	if err := wire.Write(r.s, &wire.ShellInput{Data: []byte(line + "\n")}); err != nil {
		r.t.Fatalf("writing input: %v", err)
	}
}

// expect reads output until all of it holds a match of pattern, and
// returns the match's first group. The terminal echoes what is typed, so
// a pattern has to match what only the shell can have printed.
func (r *remoteShell) expect(pattern string) string {
	r.t.Helper()
	re := regexp.MustCompile(pattern)
	for {
		if m := re.FindStringSubmatch(r.output.String()); m != nil {
			return m[len(m)-1]
		}
		resp, err := r.l.read(r.s)
		if err != nil || resp.Shell == nil {
			r.t.Fatalf("waiting for %q the shell answered %+v, %v; the output so far:\n%s", pattern, resp, err, r.output.String())
		}
		if resp.Shell.Exit != nil {
			r.t.Fatalf("waiting for %q the shell ended with %d; the output so far:\n%s", pattern, *resp.Shell.Exit, r.output.String())
		}
		r.output.Write(resp.Shell.Data)
	}
}

// pid returns the process ID of the shell.
func (r *remoteShell) pid() int {
	r.t.Helper()
	r.typeIn("echo pid=$$.")
	pid, err := strconv.Atoi(r.expect(`pid=(\d+)\.`))
	if err != nil {
		r.t.Fatal(err)
	}
	return pid
}

// gone fails the test unless the process ends.
func gone(t *testing.T, pid int) {
	t.Helper()
	eventually(t, "process "+strconv.Itoa(pid)+" to end", func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

func TestShell(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 90, Rows: 30, Term: "vt100"})

	sh.typeIn("echo hello-$((6*7))")
	sh.expect(`hello-42`)

	// The terminal is as the request says.
	sh.typeIn("echo term=$TERM.")
	sh.expect(`term=vt100\.`)
	sh.typeIn("stty size")
	sh.expect(`30 90`)

	// A change of size is applied.
	if err := wire.Write(sh.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: 101, Rows: 37}}); err != nil {
		t.Fatal(err)
	}
	sh.typeIn("stty size")
	sh.expect(`37 101`)

	// The end of the shell is its exit status, and then the end of the
	// stream.
	sh.typeIn("exit 3")
	for {
		resp, err := l.read(sh.s)
		if err != nil || resp.Shell == nil {
			t.Fatalf("the shell answered %+v, %v, want output or the exit status", resp, err)
		}
		if resp.Shell.Exit != nil {
			if *resp.Shell.Exit != 3 {
				t.Fatalf("the exit status is %d, want 3", *resp.Shell.Exit)
			}
			break
		}
	}
	if resp, err := l.read(sh.s); err != io.EOF {
		t.Fatalf("after the exit status the shell answered %+v, %v, want the end of the stream", resp, err)
	}
	sh.s.Close()
	w.idle()
}

func TestShellEndsWithItsStream(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()
	sh.typeIn("sleep 1000")

	sh.s.Close()
	gone(t, pid)
	w.idle()
}

func TestShellEndsWithTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()

	l.conn.Close()
	gone(t, pid)
	w.idle()
}

func TestShellEndsWithRun(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()

	w.stop() // fails the test unless Run returns nil in time
	// Run ends the shells before it returns.
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the shell, process %d, is still there after Run has returned (kill 0: %v)", pid, err)
	}
}

func TestShellThatCannotBeStarted(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	w := newWorld(t, setup{shell: missing})
	l := w.accept()
	resp, err := l.call(&wire.Request{Shell: &wire.ShellRequest{Cols: 80, Rows: 24}})
	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), missing) {
		t.Fatalf("shell answered %+v, %v, want an internal error that names the shell", resp, err)
	}
	w.idle()
}

func TestTwoShellsAtOnce(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	one := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	two := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	if a, b := one.pid(), two.pid(); a == b {
		t.Fatalf("both sessions are process %d", a)
	}
	one.typeIn("exit 0")
	two.typeIn("echo still-$((6*7))")
	two.expect(`still-42`)
}
```

- [ ] **Step 17: Run the tests to see them fail**

Run: `go test ./zathras/ -run Shell`
Expected: FAIL — `shell answered bad-request: a zathras has no shell yet`

- [ ] **Step 18: Write the shell**

`zathras/shell.go`:

```go
package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/wire"
)

// answerShell runs a shell session on s: it starts the shell, says so
// with an empty output, and then copies what is typed to the shell and
// what the shell prints to the stream, until the shell has ended, which
// its exit status says, or the stream has, which ends the shell. ctx is
// done when the connection has ended.
func (a *Agent) answerShell(ctx context.Context, s node.Stream, req *wire.ShellRequest) {
	cfg := a.cfg.Shell
	cfg.Term, cfg.Cols, cfg.Rows = req.Term, req.Cols, req.Rows
	session, err := ptyshell.Start(cfg)
	if err != nil {
		a.log.Warn("a shell could not be started", "error", err)
		wire.WriteError(s, wire.Errorf(wire.CodeInternal, "%s", reason(err)))
		s.CloseWrite()
		io.Copy(io.Discard, s) // until draal has read the answer and closed
		return
	}
	a.log.Info("a shell has started")
	wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})

	// What is typed, and the changes of size, until the stream ends.
	streamEnded := make(chan struct{})
	go func() {
		defer close(streamEnded)
		for {
			var in wire.ShellInput
			if wire.Read(s, &in) != nil {
				return
			}
			if in.Resize != nil {
				session.Resize(in.Resize.Cols, in.Resize.Rows)
			}
			if len(in.Data) > 0 {
				// A Write that fails is one to a shell that has ended,
				// which the output says.
				session.Write(in.Data)
			}
		}
	}()

	// What the shell prints, and at its end the exit status.
	shellEnded := make(chan struct{})
	go func() {
		defer close(shellEnded)
		buf := make([]byte, 32<<10)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				if wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}) != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
		exit := session.Wait()
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &exit}})
		s.CloseWrite()
	}()

	select {
	case <-shellEnded:
		// The exit status is on its way. draal closes the stream when it
		// has read it.
		select {
		case <-streamEnded:
		case <-ctx.Done():
		}
	case <-streamEnded:
	case <-ctx.Done():
	}
	session.Close() // ends the shell if it runs, and with it the output
	s.Close()       // ends the input, and an output that waits to be written
	<-shellEnded
	<-streamEnded
	a.log.Info("a shell has ended", "status", session.Wait())
}
```

`zathras/answer.go`, whole, without the `answerShell` that `shell.go` now has:

```go
package zathras

import (
	"context"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	case req.Prepare != nil:
		if err := a.prepare(ctx, req.Prepare); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Prepared: &wire.Prepared{}})
	case req.Activate != nil:
		if err := a.activate(req.Activate); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Activated: &wire.Activated{}})
	case req.Logs != nil:
		a.logs(ctx, s, req.Logs)
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}
```

- [ ] **Step 19: Run the tests to see them pass**

Run: `go vet ./zathras/ && go test -race -count=3 ./zathras/`
Expected: `ok`, and `pgrep -fl 'testservice|sleep 1000'` prints nothing afterwards

- [ ] **Step 20: Commit**

```bash
git add zathras/
git commit -m "Zathras: the agent answers shell"
```
