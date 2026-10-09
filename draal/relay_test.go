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
