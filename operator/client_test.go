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
	want := wire.StoreInfo{Server: nodetest.ID(9), Prefix: "/services/web/nix/store/"}
	_, c := connect(t, func(req *wire.Request, s node.Stream) {
		if req.Store == nil || req.Store.Service != "web" {
			refuse(s, wire.CodeBadRequest, "not a store request for the service web")
			return
		}
		answer(s, &wire.Response{Store: &want})
	})
	got, err := c.Store(t.Context(), "web")
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
	_, err := c.Store(ctx, "web")
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
	if _, err := c.Store(t.Context(), "web"); err == nil {
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
		_, err := c.Store(ctx, "web")
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
	if _, err := c.Store(t.Context(), "web"); err == nil {
		t.Error("Store on a closed client returned no error")
	}
	if err := c.Close(); err != nil {
		t.Errorf("a second Close = %v, want nil", err)
	}
}
