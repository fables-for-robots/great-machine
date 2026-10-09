### Task 17: operator — the client of draal

**Files:**
- Create: `operator/client.go` (the package comment, `Client`, `New`, `Close` and every request but `shell`)
- Create: `operator/shell.go` (`(*Client).Shell` and `Shell`)
- Create: `operator/resolve.go` (`Resolve`, `MinIDPrefix`)
- Test: `operator/client_test.go`, `operator/resolve_test.go`

**Interfaces:**
- Consumes:
  - `wire`: every message of the contract; `wire.Call(s io.ReadWriter, req *wire.Request) (*wire.Response, error)`, `wire.ReadResponse(r io.Reader) (*wire.Response, error)` (a `Response` with an `Error` comes back as that `*wire.Error`; the clean end of a stream as `io.EOF`), `wire.Write(w io.Writer, msg any) error`, `wire.Read`, `wire.WriteError`, `wire.Errorf`, `wire.CodeOf` (the last four in tests).
  - `node`: `node.Conn` (`OpenStream(ctx) (node.Stream, error)`, `Done() <-chan struct{}`, `Close() error`), `node.Stream` (`io.Reader`, `io.Writer`, `CloseWrite() error`, `Close() error`).
  - `node/nodetest` (tests): `nodetest.New() *nodetest.Net`, `(*Net).Listen(id wire.ID) node.Listener`, `(*Net).Dial(ctx, from, to wire.ID) (node.Conn, error)`, `nodetest.ID(n int) wire.ID`.
- Produces: the contract's `operator` section: `operator.New(conn node.Conn) *operator.Client`, `(*Client).Close`, `Store`, `Rollout`, `Rollback`, `Watch`, `Rollouts`, `History`, `Logs`, `Shell`, `operator.Shell` with `Read`, `Write`, `Resize`, `Wait`, `Close`, and `operator.Resolve(f wire.Fleet, service, name string) (wire.Zathras, error)`. And, where the contract leaves room:
  - `(*Client).Close` may be called more than once; a second call returns nil.
  - `History`, `Logs` and `Shell` put their `zathras` parameter into the request themselves (`Shell` overwrites `req.Zathras`).
  - What draal refuses comes back as the `*wire.Error` itself, unwrapped. Every other error starts with `operator: <request>: `; one that the context caused has the context's error in its chain.
  - `Watch` returns once the first fleet has come, so that a refusal is its error. Its channel is closed when ctx is done or the stream or the connection has ended.
  - `Logs` returns once the first line has come, the stream has ended without one (a closed channel and no error), or draal has refused. The protocol has no other answer to `logs` than lines, so with `follow` and an empty ring `Logs` returns only when the first line is written or ctx is done. Its channel is closed at the end of the stream, when ctx is done, or when the connection has ended.
  - `Shell.Wait` returns once `Read` has reached the end, as the exit status comes behind the output. `Shell.Read` returns `io.EOF` only when the exit status came; a session that ends otherwise ends with an error, a `*wire.Error` if draal sent one.
  - `operator.MinIDPrefix = 4`: the beginning of an endpoint ID names a zathras from four characters on (in either case); anything shorter is the beginning of a name alone.
  - The errors of `Resolve`: `draal knows no service "<service>"`; `service "<service>" has no zathras connected`; and three that end with a listing, one zathras on a line as two spaces, the name padded to the longest, two spaces and the ID: every zathras of the service when there is no match or no name was given, and those that match when there are several.

- [ ] **Step 1: Write the failing tests of the client**

The fake draal of the tests is a listener of `nodetest` whose streams a function of the test answers.

`operator/client_test.go`:

```go
package operator

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 10 * time.Second

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

// waitEnd waits for a channel to be closed, and fails on a value.
func waitEnd[T any](t *testing.T, what string, ch <-chan T) {
	t.Helper()
	select {
	case v, ok := <-ch:
		if ok {
			t.Fatalf("%s: got %+v, want the end of the channel", what, v)
		}
	case <-time.After(deadline):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// fakeDraal is a draal of the test: it answers every stream with handle.
type fakeDraal struct {
	conns chan node.Conn // the connections it accepted
}

// connect starts a fake draal whose streams handle answers, and returns
// it and a client that is connected to it. handle gets the request and
// the stream, which is closed when it returns.
func connect(t *testing.T, handle func(req *wire.Request, s node.Stream)) (*fakeDraal, *Client) {
	t.Helper()
	net := nodetest.New()
	l := net.Listen(nodetest.ID(1))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		l.Close()
	})
	d := &fakeDraal{conns: make(chan node.Conn, 1)}
	go func() {
		for {
			conn, err := l.Accept(ctx)
			if err != nil {
				return
			}
			d.conns <- conn
			go func() {
				for {
					s, err := conn.AcceptStream(ctx)
					if err != nil {
						return
					}
					go func() {
						defer s.Close()
						var req wire.Request
						if err := wire.Read(s, &req); err != nil {
							return
						}
						handle(&req, s)
					}()
				}
			}()
		}
	}()
	conn, err := net.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c := New(conn)
	t.Cleanup(func() { c.Close() })
	return d, c
}

// answer writes one response, ends what the fake sends and waits for the
// client to close the stream.
func answer(s node.Stream, resp *wire.Response) {
	wire.Write(s, resp)
	s.CloseWrite()
	io.Copy(io.Discard, s)
}

// refuse answers with an error.
func refuse(s node.Stream, code wire.Code, message string) {
	wire.WriteError(s, wire.Errorf(code, "%s", message))
	s.CloseWrite()
	io.Copy(io.Discard, s)
}

func TestStore(t *testing.T) {
	want := wire.StoreInfo{Server: nodetest.ID(9), Prefix: "nix/"}
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		if req.Store == nil {
			refuse(s, wire.CodeBadRequest, "not a store request")
			return
		}
		answer(s, &wire.Response{Store: &want})
	})
	got, err := c.Store(t.Context())
	if err != nil || got != want {
		t.Fatalf("Store = %+v, %v; want %+v", got, err, want)
	}
}

func TestRolloutAndRollback(t *testing.T) {
	asked := make(chan wire.Request, 2)
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		asked <- *req
		answer(s, &wire.Response{Rollout: &wire.RolloutInfo{ID: 7, Service: "web", Generation: 3, State: wire.RolloutPreparing}})
	})
	req := wire.RolloutRequest{Service: "web", Binary: "/nix/store/abc-web/bin/web", Args: []string{"-v"}, Closure: []string{"abc-web"}, Parallel: 2}
	info, err := c.Rollout(t.Context(), req)
	if err != nil || info.ID != 7 || info.Generation != 3 || info.State != wire.RolloutPreparing {
		t.Fatalf("Rollout = %+v, %v", info, err)
	}
	if got := wait(t, "the request", asked).Rollout; got == nil || got.Service != "web" || got.Binary != req.Binary ||
		len(got.Args) != 1 || len(got.Closure) != 1 || got.Parallel != 2 {
		t.Errorf("draal was asked %+v, want %+v", got, req)
	}

	info, err = c.Rollback(t.Context(), wire.RollbackRequest{Service: "web", Parallel: 4})
	if err != nil || info.ID != 7 {
		t.Fatalf("Rollback = %+v, %v", info, err)
	}
	if got := wait(t, "the request", asked).Rollback; got == nil || got.Service != "web" || got.Parallel != 4 {
		t.Errorf("draal was asked %+v, want a rollback of web with parallel 4", got)
	}
}

func TestRolloutsAndHistory(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		switch {
		case req.Rollouts != nil && req.Rollouts.Service == "web":
			answer(s, &wire.Response{Rollouts: &wire.RolloutList{Rollouts: []wire.RolloutInfo{{ID: 2}, {ID: 1}}}})
		case req.History != nil && req.History.Zathras == nodetest.ID(10):
			answer(s, &wire.Response{History: &wire.SampleList{Samples: []wire.Sample{{At: at, Memory: 1}, {At: at.Add(time.Second), Memory: 2}}}})
		default:
			refuse(s, wire.CodeBadRequest, "not what the test asks")
		}
	})
	list, err := c.Rollouts(t.Context(), "web")
	if err != nil || len(list) != 2 || list[0].ID != 2 || list[1].ID != 1 {
		t.Errorf("Rollouts = %+v, %v", list, err)
	}
	// History names the zathras in the request by itself, as Logs and
	// Shell do.
	samples, err := c.History(t.Context(), nodetest.ID(10))
	if err != nil || len(samples) != 2 || samples[0].Memory != 1 || !samples[1].At.Equal(at.Add(time.Second)) {
		t.Errorf("History = %+v, %v", samples, err)
	}
}

func TestAnErrorOfDraalIsAWireError(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		switch {
		case req.Rollback != nil:
			refuse(s, wire.CodeNotFound, "no service \"web\"")
		case req.History != nil, req.Logs != nil, req.Shell != nil:
			refuse(s, wire.CodeUnavailable, "the zathras is not connected")
		default:
			refuse(s, wire.CodeUnauthorized, "not an operator")
		}
	})
	ctx := t.Context()
	check := func(what string, err error, code wire.Code) {
		t.Helper()
		var refusal *wire.Error
		if !errors.As(err, &refusal) || refusal.Code != code {
			t.Errorf("%s = %v, want a *wire.Error with the code %s", what, err, code)
		}
	}
	_, err := c.Store(ctx)
	check("Store", err, wire.CodeUnauthorized)
	_, err = c.Rollout(ctx, wire.RolloutRequest{Service: "web"})
	check("Rollout", err, wire.CodeUnauthorized)
	_, err = c.Rollback(ctx, wire.RollbackRequest{Service: "web"})
	check("Rollback", err, wire.CodeNotFound)
	if err == nil || err.Error() != "not-found: no service \"web\"" {
		t.Errorf("Rollback = %v, want the error as draal wrote it", err)
	}
	_, err = c.Watch(ctx)
	check("Watch", err, wire.CodeUnauthorized)
	_, err = c.Rollouts(ctx, "web")
	check("Rollouts", err, wire.CodeUnauthorized)
	_, err = c.History(ctx, nodetest.ID(10))
	check("History", err, wire.CodeUnavailable)
	_, err = c.Logs(ctx, nodetest.ID(10), false)
	check("Logs", err, wire.CodeUnavailable)
	_, err = c.Shell(ctx, nodetest.ID(10), wire.ShellRequest{})
	check("Shell", err, wire.CodeUnavailable)
}

func TestAnAnswerOfAnotherKindIsAnError(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		answer(s, &wire.Response{Registered: &wire.Registered{}})
	})
	if _, err := c.Store(t.Context()); err == nil {
		t.Error("Store took an answer that is no store")
	}
	if _, err := c.Watch(t.Context()); err == nil {
		t.Error("Watch took an answer that is no fleet")
	}
	if _, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{}); err == nil {
		t.Error("Shell took an answer that is no shell")
	}
}

// fleetOf returns a fleet with n services.
func fleetOf(n int) *wire.Fleet {
	f := &wire.Fleet{}
	for i := range n {
		f.Services = append(f.Services, wire.Service{Name: string(rune('a' + i))})
	}
	return f
}

func TestWatch(t *testing.T) {
	more := make(chan int)
	ended := make(chan struct{})
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		wire.Write(s, &wire.Response{Fleet: fleetOf(0)})
		go func() {
			// The client sends nothing more: this ends with the stream.
			io.Copy(io.Discard, s)
			close(ended)
		}()
		for n := range more {
			wire.Write(s, &wire.Response{Fleet: fleetOf(n)})
		}
		<-ended
	})
	ctx, cancel := context.WithCancel(t.Context())
	fleets, err := c.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := wait(t, "the first fleet", fleets); len(f.Services) != 0 {
		t.Errorf("the first fleet = %+v, want no service", f)
	}
	for n := 1; n <= 3; n++ {
		more <- n
		if f := wait(t, "a fleet", fleets); len(f.Services) != n {
			t.Errorf("fleet %d has %d services", n, len(f.Services))
		}
	}
	select {
	case <-ended:
		t.Fatal("the stream ended while the client watches")
	default:
	}
	cancel()
	waitEnd(t, "the channel to close with the context", fleets)
	wait(t, "the fake to see the stream end", ended)
	close(more)
}

func TestWatchEndsWithTheConnection(t *testing.T) {
	d, c := connect(t, func(req *wire.Request, s node.Stream) {
		wire.Write(s, &wire.Response{Fleet: fleetOf(1)})
		io.Copy(io.Discard, s)
	})
	fleets, err := c.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	wait(t, "the first fleet", fleets)
	wait(t, "the connection", d.conns).Close()
	waitEnd(t, "the channel to close with the connection", fleets)
}

// someLines returns two lines and, between them, a note of lost lines.
func someLines() []wire.LogLine {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return []wire.LogLine{
		{At: at, Stream: "zathras", Text: "started generation 1"},
		{At: at.Add(time.Second), Stream: "zathras", Lost: 4},
		{At: at.Add(2 * time.Second), Stream: "stdout", Text: "hello"},
	}
}

func TestLogs(t *testing.T) {
	asked := make(chan wire.LogsRequest, 1)
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		asked <- *req.Logs
		for _, line := range someLines() {
			wire.Write(s, &wire.Response{Line: &line})
		}
		s.CloseWrite()
		io.Copy(io.Discard, s)
	})
	lines, err := c.Logs(t.Context(), nodetest.ID(10), false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if req := wait(t, "the request", asked); req.Zathras != nodetest.ID(10) || req.Follow {
		t.Errorf("draal was asked %+v", req)
	}
	for i, want := range someLines() {
		got := wait(t, "a line", lines)
		if !got.At.Equal(want.At) || got.Stream != want.Stream || got.Text != want.Text || got.Lost != want.Lost {
			t.Errorf("line %d = %+v, want %+v", i, got, want)
		}
	}
	waitEnd(t, "the channel to close at the end of the stream", lines)
}

func TestLogsOfAnEmptyRing(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		s.CloseWrite()
		io.Copy(io.Discard, s)
	})
	lines, err := c.Logs(t.Context(), nodetest.ID(10), false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	waitEnd(t, "the channel of an empty ring to be closed", lines)
}

func TestLogsFollowEndsWithTheConnection(t *testing.T) {
	d, c := connect(t, func(req *wire.Request, s node.Stream) {
		wire.Write(s, &wire.Response{Line: &someLines()[0]})
		io.Copy(io.Discard, s)
	})
	lines, err := c.Logs(t.Context(), nodetest.ID(10), true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	wait(t, "the line", lines)
	wait(t, "the connection", d.conns).Close()
	waitEnd(t, "the channel to close with the connection", lines)
}

func TestLogsFollowEndsWithTheContext(t *testing.T) {
	asked := make(chan wire.LogsRequest, 1)
	ended := make(chan struct{})
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		asked <- *req.Logs
		wire.Write(s, &wire.Response{Line: &someLines()[0]})
		io.Copy(io.Discard, s)
		close(ended)
	})
	ctx, cancel := context.WithCancel(t.Context())
	lines, err := c.Logs(ctx, nodetest.ID(10), true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if req := wait(t, "the request", asked); !req.Follow {
		t.Errorf("draal was asked %+v, want follow", req)
	}
	wait(t, "the line", lines)
	cancel()
	waitEnd(t, "the channel to close with the context", lines)
	wait(t, "the fake to see the stream end", ended)
}

// echoShell is a shell of the fake draal: it answers input with "echo: "
// and the input, tells of changes of size, and exits with 3 on "exit\n".
func echoShell(asked chan<- wire.ShellRequest, resized chan<- wire.WinSize, ended chan<- struct{}) func(*wire.Request, node.Stream) {
	return func(req *wire.Request, s node.Stream) {
		asked <- *req.Shell
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
		defer func() { ended <- struct{}{} }()
		for {
			var in wire.ShellInput
			if err := wire.Read(s, &in); err != nil {
				return
			}
			if in.Resize != nil {
				resized <- *in.Resize
			}
			if string(in.Data) == "exit\n" {
				status := 3
				wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: []byte("bye\n")}})
				answer(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &status}})
				return
			}
			if len(in.Data) > 0 {
				wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: append([]byte("echo: "), in.Data...)}})
			}
		}
	}
}

func TestShell(t *testing.T) {
	asked := make(chan wire.ShellRequest, 1)
	resized := make(chan wire.WinSize, 1)
	ended := make(chan struct{}, 1)
	_, c := connect(t, echoShell(asked, resized, ended))

	sh, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{Cols: 100, Rows: 30, Term: "vt100"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if req := wait(t, "the request", asked); req.Zathras != nodetest.ID(10) || req.Cols != 100 || req.Rows != 30 || req.Term != "vt100" {
		t.Errorf("draal was asked %+v", req)
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
		t.Errorf("draal was told %+v, want 120 by 40", size)
	}

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sh.Wait(waitCtx); err == nil {
		t.Error("Wait of a shell that runs returned no error when its context was done")
	}

	if _, err := sh.Write([]byte("exit\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rest, err := io.ReadAll(sh); err != nil || string(rest) != "bye\n" {
		t.Errorf("the last output = %q, %v; want bye and the end", rest, err)
	}
	if status, err := sh.Wait(t.Context()); err != nil || status != 3 {
		t.Errorf("Wait = %d, %v; want 3", status, err)
	}
	if err := sh.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	wait(t, "the fake to see the stream end", ended)
}

func TestShellCloseEndsTheSession(t *testing.T) {
	ended := make(chan struct{}, 1)
	_, c := connect(t, echoShell(make(chan wire.ShellRequest, 1), make(chan wire.WinSize, 1), ended))
	sh, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if err := sh.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, "the fake to see the stream end", ended)
	if _, err := sh.Read(make([]byte, 1)); err == nil || err == io.EOF {
		t.Errorf("Read after Close = %v, want an error", err)
	}
	if _, err := sh.Wait(t.Context()); err == nil {
		t.Error("Wait of a session that ended without an exit status returned no error")
	}
	if err := sh.Close(); err != nil {
		t.Errorf("a second Close: %v", err)
	}
}

func TestShellThatEndsWithAnError(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}})
		refuse(s, wire.CodeUnavailable, "the zathras has gone")
	})
	sh, err := c.Shell(t.Context(), nodetest.ID(10), wire.ShellRequest{})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	if _, err := sh.Read(make([]byte, 1)); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Read = %v, want unavailable", err)
	}
	if _, err := sh.Wait(t.Context()); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Wait = %v, want unavailable", err)
	}
}

func TestARequestEndsWithItsContext(t *testing.T) {
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		io.Copy(io.Discard, s) // never answers
	})
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.Store(ctx)
		failed <- err
	}()
	cancel()
	if err := wait(t, "Store to return", failed); !errors.Is(err, context.Canceled) {
		t.Errorf("Store = %v, want the error of its context", err)
	}
}

func TestCloseClosesTheConnection(t *testing.T) {
	d, c := connect(t, func(req *wire.Request, s node.Stream) {})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wait(t, "the connection to end at draal", wait(t, "the connection", d.conns).Done())
	if _, err := c.Store(t.Context()); err == nil {
		t.Error("Store on a closed client returned no error")
	}
	if err := c.Close(); err != nil {
		t.Errorf("a second Close = %v, want nil", err)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./operator/`
Expected: FAIL — the package does not build: `undefined: Client`, `undefined: New`

- [ ] **Step 3: Write the client**

`operator/client.go`:

```go
// Package operator is the client of draal: what the commands and the
// terminal UI use to ask it. Every method of Client is one request of the
// protocol on a stream of its own. What draal refuses comes back as a
// *wire.Error, which errors.As finds.
package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// Client talks to draal on one connection. It is safe for concurrent use.
type Client struct {
	conn  node.Conn
	close sync.Once
}

// New returns a client on a connection to draal. The client owns the
// connection from then on.
func New(conn node.Conn) *Client {
	return &Client{conn: conn}
}

// Close closes the connection, which ends every request under way and
// closes the channels of Watch and Logs. It may be called more than once:
// a second Close does nothing and returns nil.
func (c *Client) Close() error {
	var err error
	c.close.Do(func() { err = c.conn.Close() })
	return err
}

// failed makes the error of a request: what draal answered as it is, the
// error of ctx when that ended the request, and any other with what was
// asked in front.
func failed(ctx context.Context, what string, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	if ctx.Err() != nil {
		return fmt.Errorf("operator: %s: %w", what, context.Cause(ctx))
	}
	return fmt.Errorf("operator: %s: %w", what, err)
}

// open opens a stream, writes req and reads the first answer. The stream
// is closed when ctx is done; stop takes that back.
func (c *Client) open(ctx context.Context, what string, req *wire.Request) (s node.Stream, resp *wire.Response, stop func() bool, err error) {
	s, err = c.conn.OpenStream(ctx)
	if err != nil {
		return nil, nil, nil, failed(ctx, what, err)
	}
	stop = context.AfterFunc(ctx, func() { s.Close() })
	resp, err = wire.Call(s, req)
	if err != nil {
		stop()
		s.Close()
		return nil, nil, nil, failed(ctx, what, err)
	}
	return s, resp, stop, nil
}

// call does a request that has one answer.
func (c *Client) call(ctx context.Context, what string, req *wire.Request) (*wire.Response, error) {
	s, resp, stop, err := c.open(ctx, what, req)
	if err != nil {
		return nil, err
	}
	stop()
	s.Close()
	return resp, nil
}

// unexpected is the error for an answer of another kind than was asked for.
func unexpected(what string) error {
	return fmt.Errorf("operator: %s: draal answered with something else", what)
}

// Store asks for the jaccard-store server and the prefix to push to.
func (c *Client) Store(ctx context.Context) (wire.StoreInfo, error) {
	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{}})
	if err != nil {
		return wire.StoreInfo{}, err
	}
	if resp.Store == nil {
		return wire.StoreInfo{}, unexpected("store")
	}
	return *resp.Store, nil
}

// Rollout asks for a rollout, and returns it as draal recorded it.
func (c *Client) Rollout(ctx context.Context, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollout", &wire.Request{Rollout: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollout")
	}
	return *resp.Rollout, nil
}

// Rollback asks for a rollout to the stable generation of a service.
func (c *Client) Rollback(ctx context.Context, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollback", &wire.Request{Rollback: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollback")
	}
	return *resp.Rollout, nil
}

// Rollouts returns the last rollouts of a service, newest first.
func (c *Client) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollouts", &wire.Request{Rollouts: &wire.RolloutsRequest{Service: service}})
	if err != nil {
		return nil, err
	}
	if resp.Rollouts == nil {
		return nil, unexpected("rollouts")
	}
	return resp.Rollouts.Rollouts, nil
}

// History returns the samples draal has of the zathras, oldest first.
func (c *Client) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	resp, err := c.call(ctx, "history", &wire.Request{History: &wire.HistoryRequest{Zathras: zathras}})
	if err != nil {
		return nil, err
	}
	if resp.History == nil {
		return nil, unexpected("history")
	}
	return resp.History.Samples, nil
}

// Watch returns a channel of the fleet: as it is now, and again whenever
// it changed. The channel is closed when ctx is done, which ends the
// request, or when the connection has ended. Watch returns when the first
// fleet has come, or with what draal refused. A channel that is closed
// while ctx is not done says that draal has gone.
func (c *Client) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	s, resp, stop, err := c.open(ctx, "watch", &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil {
		return nil, err
	}
	if resp.Fleet == nil {
		stop()
		s.Close()
		return nil, unexpected("watch")
	}
	out := make(chan wire.Fleet)
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			select {
			case out <- *resp.Fleet:
			case <-ctx.Done():
				return
			}
			for {
				var err error
				if resp, err = wire.ReadResponse(s); err != nil {
					return
				}
				if resp.Fleet != nil {
					break
				}
			}
		}
	}()
	return out, nil
}

// Logs returns a channel of the lines of the ring of a zathras, and with
// follow of those that follow. The channel is closed at the end of the
// stream, or when ctx is done, which ends the request.
//
// The protocol answers logs with lines alone, so Logs returns when the
// first line has come, when the stream has ended without one, or with
// what draal refused. With follow and an empty ring that is when the
// first line is written, or when ctx is done. A channel of a followed log
// that is closed while ctx is not done says that draal or the zathras has
// gone.
func (c *Client) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	s, resp, stop, err := c.open(ctx, "logs", &wire.Request{Logs: &wire.LogsRequest{Zathras: zathras, Follow: follow}})
	if errors.Is(err, io.EOF) && ctx.Err() == nil {
		// The ring is empty.
		out := make(chan wire.LogLine)
		close(out)
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(chan wire.LogLine)
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			if resp.Line != nil {
				select {
				case out <- *resp.Line:
				case <-ctx.Done():
					return
				}
			}
			var err error
			if resp, err = wire.ReadResponse(s); err != nil {
				return
			}
		}
	}()
	return out, nil
}
```

`operator/shell.go`:

```go
package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// shellChunk is the most input one message carries.
const shellChunk = 16 << 10

// Shell asks for a shell in the container of a zathras, and returns the
// session once the shell runs, or what draal or the zathras refused. The
// zathras is the one given, whatever req.Zathras says. ctx bounds the
// start alone: the session lasts until its Close.
func (c *Client) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (*Shell, error) {
	req.Zathras = zathras
	s, resp, stop, err := c.open(ctx, "shell", &wire.Request{Shell: &req})
	if err != nil {
		return nil, err
	}
	if !stop() {
		s.Close()
		return nil, fmt.Errorf("operator: shell: %w", context.Cause(ctx))
	}
	if resp.Shell == nil {
		s.Close()
		return nil, unexpected("shell")
	}
	return &Shell{s: s, ended: make(chan struct{})}, nil
}

// Shell is a session. It has the methods of draal.Shell. Read is for one
// goroutine; Write, Resize and Close may be called from others.
type Shell struct {
	s node.Stream

	rest []byte // output that was received and not read yet; Read's alone

	ended chan struct{} // closed when exit or err is set
	exit  int
	err   error

	wmu   sync.Mutex // one message at a time
	close sync.Once
}

// Read returns output of the shell, and io.EOF once its exit status has
// come. A session that ends without one ends with an error.
func (s *Shell) Read(p []byte) (int, error) {
	for len(s.rest) == 0 {
		select {
		case <-s.ended:
			if s.err != nil {
				return 0, s.err
			}
			return 0, io.EOF
		default:
		}
		resp, err := wire.ReadResponse(s.s)
		var refusal *wire.Error
		switch {
		case err == io.EOF:
			s.err = errors.New("operator: the shell session ended without an exit status")
			close(s.ended)
		case errors.As(err, &refusal):
			s.err = refusal
			close(s.ended)
		case err != nil:
			s.err = fmt.Errorf("operator: the shell session failed: %w", err)
			close(s.ended)
		case resp.Shell == nil:
		case resp.Shell.Exit != nil:
			s.exit = *resp.Shell.Exit
			s.rest = resp.Shell.Data
			close(s.ended)
			if len(s.rest) == 0 {
				return 0, io.EOF
			}
		default:
			s.rest = resp.Shell.Data
		}
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}

// Write sends p as input of the shell.
func (s *Shell) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), shellChunk)]
		if err := wire.Write(s.s, &wire.ShellInput{Data: chunk}); err != nil {
			return written, fmt.Errorf("operator: writing to the shell: %w", err)
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Resize tells the shell the new size of the terminal.
func (s *Shell) Resize(cols, rows uint16) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := wire.Write(s.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: cols, Rows: rows}}); err != nil {
		return fmt.Errorf("operator: resizing the shell: %w", err)
	}
	return nil
}

// Wait returns the exit status when the shell has ended, or the error the
// session ended with instead. The status comes behind the output, so Wait
// returns only once Read has reached the end.
func (s *Shell) Wait(ctx context.Context) (int, error) {
	select {
	case <-s.ended:
		if s.err != nil {
			return 0, s.err
		}
		return s.exit, nil
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// Close ends the session, and the shell if it runs. It may be called
// more than once.
func (s *Shell) Close() error {
	s.close.Do(func() { s.s.Close() })
	return nil
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./operator/ && go test -race ./operator/`
Expected: PASS (`ok  	github.com/fables-for-robots/great-machine/operator`)

- [ ] **Step 5: Commit**

```bash
git add operator/
git commit -m "Operator: the client of draal"
```

- [ ] **Step 6: Write the failing test of Resolve**

This is the test of Review Focus 4.

`operator/resolve_test.go`:

```go
package operator

import (
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

// Endpoint IDs of the test: 64 hexadecimal characters each.
var (
	idA = wire.ID("a1b2c3" + strings.Repeat("0", 58))
	idB = wire.ID("a1b2d4" + strings.Repeat("1", 58))
	idC = wire.ID("c0ffee" + strings.Repeat("2", 58))
	idD = wire.ID("deadbe" + strings.Repeat("3", 58))
	idE = wire.ID("beef00" + strings.Repeat("4", 58))
)

// TestResolve pins Review Focus 4: two zathrases of a service with the
// same name are listed apart, a command that names them fails and prints
// their endpoint IDs, and the beginning of an endpoint ID is accepted
// where a name is.
func TestResolve(t *testing.T) {
	fleet := wire.Fleet{Services: []wire.Service{
		{Name: "web", Zathrases: []wire.Zathras{
			{ID: idA, Name: "web-1"},
			{ID: idB, Name: "web-10"},
			{ID: idC, Name: "worker"},
			{ID: idD, Name: "twin"},
			{ID: idE, Name: "twin"},
		}},
		{Name: "api", Zathrases: []wire.Zathras{{ID: idC, Name: "api-0"}}},
		{Name: "empty"},
	}}
	all := "  web-1   " + string(idA) + "\n" +
		"  web-10  " + string(idB) + "\n" +
		"  worker  " + string(idC) + "\n" +
		"  twin    " + string(idD) + "\n" +
		"  twin    " + string(idE)

	cases := []struct {
		about   string
		service string
		name    string
		want    wire.ID // of the zathras, or "" for an error
		errHas  []string
		errNot  []string
	}{
		{about: "the exact name wins over a name it begins", service: "web", name: "web-1", want: idA},
		{about: "the exact name of the longer one", service: "web", name: "web-10", want: idB},
		{about: "a unique beginning of a name", service: "web", name: "wo", want: idC},
		{about: "one letter is enough for a name", service: "web", name: "t", errHas: []string{string(idD), string(idE)}, errNot: []string{string(idA)}},
		{about: "a unique beginning of an ID", service: "web", name: "c0ff", want: idC},
		{about: "the beginning of an ID in capitals", service: "web", name: "C0FFEE", want: idC},
		{about: "a whole ID", service: "web", name: string(idB), want: idB},
		{about: "the beginning of an ID that is too short", service: "web", name: "c0f",
			errHas: []string{`no zathras "c0f"`, all}},
		{about: "the beginning of two IDs", service: "web", name: "a1b2",
			errHas: []string{"web-1 ", "web-10", string(idA), string(idB)}, errNot: []string{"worker"}},
		{about: "the beginning of two names", service: "web", name: "web",
			errHas: []string{string(idA), string(idB)}, errNot: []string{"worker", "twin"}},
		{about: "no name with one zathras", service: "api", name: "", want: idC},
		{about: "no name with several", service: "web", name: "", errHas: []string{"5 zathrases", all}},
		{about: "two zathrases with the same name", service: "web", name: "twin",
			errHas: []string{`named "twin"`, "  twin  " + string(idD) + "\n  twin  " + string(idE)}, errNot: []string{"worker"}},
		{about: "the beginning of the ID of one twin", service: "web", name: "dead", want: idD},
		{about: "the beginning of the ID of the other twin", service: "web", name: "beef0", want: idE},
		{about: "no match", service: "web", name: "db", errHas: []string{`no zathras "db"`, all}},
		{about: "a service that is not in the fleet", service: "db", name: "", errHas: []string{`no service "db"`}},
		{about: "a service that is not in the fleet, with a name", service: "db", name: "web-1", errHas: []string{`no service "db"`}},
		{about: "a service without a zathras", service: "empty", name: "", errHas: []string{`"empty"`, "no zathras connected"}},
		{about: "a service without a zathras, with a name", service: "empty", name: "x", errHas: []string{"no zathras connected"}},
	}
	for _, tc := range cases {
		t.Run(tc.about, func(t *testing.T) {
			got, err := Resolve(fleet, tc.service, tc.name)
			if tc.want != "" {
				if err != nil || got.ID != tc.want {
					t.Fatalf("Resolve(%q, %q) = %s (%s), %v; want %s", tc.service, tc.name, got.Name, got.ID, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Resolve(%q, %q) = %s (%s), want an error", tc.service, tc.name, got.Name, got.ID)
			}
			for _, has := range tc.errHas {
				if !strings.Contains(err.Error(), has) {
					t.Errorf("the error lacks %q:\n%v", has, err)
				}
			}
			for _, not := range tc.errNot {
				if strings.Contains(err.Error(), not) {
					t.Errorf("the error has %q, which it should not:\n%v", not, err)
				}
			}
		})
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `go test ./operator/ -run TestResolve`
Expected: FAIL — the package does not build: `undefined: Resolve`

- [ ] **Step 8: Write Resolve**

`operator/resolve.go`:

```go
package operator

import (
	"fmt"
	"strings"

	"github.com/fables-for-robots/great-machine/wire"
)

// MinIDPrefix is the least number of characters by which the beginning of
// an endpoint ID names a zathras. Anything shorter is taken as the
// beginning of a name alone, so that a few letters meant as a name never
// pick a zathras by its ID.
const MinIDPrefix = 4

// Resolve finds the zathras of a service that name names: the one whose
// name is name; or else the one whose name, or whose ID, begins with
// name; or, with name "", the only one. The beginning of an ID counts
// from MinIDPrefix characters on, in either case of its letters. Any
// other outcome is an error that lists the zathrases of the service, or
// those that name matches when there are several, one on a line with
// name and ID, so that it can be printed as it is.
func Resolve(f wire.Fleet, service, name string) (wire.Zathras, error) {
	var all []wire.Zathras
	found := false
	for _, svc := range f.Services {
		if svc.Name == service {
			all, found = svc.Zathrases, true
		}
	}
	if !found {
		return wire.Zathras{}, fmt.Errorf("draal knows no service %q", service)
	}
	if len(all) == 0 {
		return wire.Zathras{}, fmt.Errorf("service %q has no zathras connected", service)
	}
	if name == "" {
		if len(all) == 1 {
			return all[0], nil
		}
		return wire.Zathras{}, fmt.Errorf("service %q has %d zathrases; name one of them:\n%s", service, len(all), listing(all))
	}

	var exact, begins []wire.Zathras
	id := strings.ToLower(name)
	for _, z := range all {
		if z.Name == name {
			exact = append(exact, z)
		}
		if strings.HasPrefix(z.Name, name) || (len(name) >= MinIDPrefix && strings.HasPrefix(string(z.ID), id)) {
			begins = append(begins, z)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return wire.Zathras{}, fmt.Errorf("%d zathrases of service %q are named %q; name one by the beginning of its ID:\n%s",
			len(exact), service, name, listing(exact))
	case len(begins) == 1:
		return begins[0], nil
	case len(begins) > 1:
		return wire.Zathras{}, fmt.Errorf("%q is the beginning of %d zathrases of service %q:\n%s",
			name, len(begins), service, listing(begins))
	}
	return wire.Zathras{}, fmt.Errorf("service %q has no zathras %q; it has:\n%s", service, name, listing(all))
}

// listing writes zathrases one on a line, with name and ID.
func listing(zs []wire.Zathras) string {
	width := 0
	for _, z := range zs {
		width = max(width, len(z.Name))
	}
	var b strings.Builder
	for i, z := range zs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "  %-*s  %s", width, z.Name, z.ID)
	}
	return b.String()
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `test -z "$(gofmt -l operator/)" && go vet ./operator/ && go test -race -count=3 ./operator/`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add operator/
git commit -m "Operator: Resolve finds a zathras by name or by the beginning of its ID"
```
