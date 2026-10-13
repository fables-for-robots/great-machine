package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

// fakeShell is a Shell of a test: it prints what the test puts
// into out, and hands the test what was typed and the sizes it was told.
type fakeShell struct {
	out    chan []byte       // what the shell prints; closed when the shell has ended
	typed  chan []byte       // what was typed, as it was written
	sizes  chan wire.WinSize // the sizes it was told
	status int               // its exit status
	failed error             // what Read ends with in place of io.EOF, if set before out is closed
	closed chan struct{}     // closed by Close
	// writing gets a value when Write is called, while it has room.
	writing chan struct{}

	once sync.Once
	rest []byte // of Read alone
}

func newFakeShell() *fakeShell {
	return &fakeShell{
		out:    make(chan []byte),
		typed:  make(chan []byte),
		sizes:  make(chan wire.WinSize, 16),
		closed: make(chan struct{}),

		writing: make(chan struct{}, 1),
	}
}

func (f *fakeShell) Read(p []byte) (int, error) {
	if len(f.rest) == 0 {
		select {
		case data, ok := <-f.out:
			if !ok {
				if f.failed != nil {
					return 0, f.failed
				}
				return 0, io.EOF
			}
			f.rest = data
		case <-f.closed:
			return 0, io.EOF
		}
	}
	n := copy(p, f.rest)
	f.rest = f.rest[n:]
	return n, nil
}

func (f *fakeShell) Write(p []byte) (int, error) {
	select {
	case f.writing <- struct{}{}:
	default:
	}
	select {
	case f.typed <- bytes.Clone(p):
		return len(p), nil
	case <-f.closed:
		return 0, errors.New("the shell is closed")
	}
}

func (f *fakeShell) Resize(cols, rows uint16) error {
	f.sizes <- wire.WinSize{Cols: cols, Rows: rows}
	return nil
}

func (f *fakeShell) Wait(ctx context.Context) (int, error) {
	return f.status, nil
}

func (f *fakeShell) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// isClosed waits until the shell was closed.
func (f *fakeShell) isClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be closed")
	}
}

// isWriting waits until Write was called.
func (f *fakeShell) isWriting(t *testing.T) {
	t.Helper()
	select {
	case <-f.writing:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be written to")
	}
}

// wasTyped returns what the shell is written next.
func (f *fakeShell) wasTyped(t *testing.T) string {
	t.Helper()
	select {
	case data := <-f.typed:
		return string(data)
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be written to")
	}
	return ""
}

// wasSized returns the size the shell is told next.
func (f *fakeShell) wasSized(t *testing.T) wire.WinSize {
	t.Helper()
	select {
	case size := <-f.sizes:
		return size
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the shell to be told a size")
	}
	return wire.WinSize{}
}

// shells is a fake's Shell that hands out sh and hands the test every
// request.
func shells(sh *fakeShell, asked chan<- wire.ShellRequest) func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
	return func(_ context.Context, id wire.ID, req wire.ShellRequest) (Shell, error) {
		if req.Zathras != id {
			return nil, wire.Errorf(wire.CodeInternal, "the request names %q, and the zathras is %s", req.Zathras, id)
		}
		if asked != nil {
			asked <- req
		}
		return sh, nil
	}
}

const shellPath = "/api/zathras/" + string(zathrasA) + "/shell"

func TestShellRoundTrip(t *testing.T) {
	sh := newFakeShell()
	sh.status = 3
	asked := make(chan wire.ShellRequest, 1)
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, asked)}})
	c := dial(t, srv, shellPath+"?cols=120&rows=40&term=xterm-kitty")
	if req := <-asked; req != (wire.ShellRequest{Zathras: zathrasA, Cols: 120, Rows: 40, Term: "xterm-kitty"}) {
		t.Fatalf("the shell was asked for with %+v", req)
	}
	bg := context.Background()

	// What the shell prints comes as binary messages, bytes as they are.
	sh.out <- []byte("$ \x1b[1mprompt\x1b[0m \xff\x00")
	if typ, data := next(t, c); typ != websocket.MessageBinary || string(data) != "$ \x1b[1mprompt\x1b[0m \xff\x00" {
		t.Fatalf("the output is %v %q", typ, data)
	}
	// What is typed goes to the shell.
	if err := c.Write(bg, websocket.MessageBinary, []byte("echo hi\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasTyped(t); got != "echo hi\r" {
		t.Fatalf("the shell was written %q", got)
	}
	// A change of size.
	if err := c.Write(bg, websocket.MessageText, []byte(`{"resize": {"cols": 132, "rows": 50}}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 132, Rows: 50}) {
		t.Fatalf("the shell was told the size %+v", got)
	}
	// More output than one message carries arrives whole and in order.
	big := bytes.Repeat([]byte("0123456789abcdef"), 3*shellChunk/16)
	go func() { sh.out <- big }()
	var got []byte
	for len(got) < len(big) {
		typ, data := next(t, c)
		if typ != websocket.MessageBinary || len(data) > shellChunk {
			t.Fatalf("a message of %v with %d bytes, want binary of at most %d", typ, len(data), shellChunk)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("the output did not arrive as it was printed")
	}

	// The shell ends: its last output, its exit status, a normal close.
	go func() {
		sh.out <- []byte("bye\r\n")
		close(sh.out)
	}()
	if _, data := next(t, c); string(data) != "bye\r\n" {
		t.Fatalf("the last output is %q", data)
	}
	if typ, data := next(t, c); typ != websocket.MessageText || string(data) != `{"exit":3}` {
		t.Fatalf("the message is %v %s, want the text {\"exit\":3}", typ, data)
	}
	wantClose(t, c, websocket.StatusNormalClosure)
	sh.isClosed(t)
}

func TestShellHasDefaultsForItsTerminal(t *testing.T) {
	sh := newFakeShell()
	asked := make(chan wire.ShellRequest, 1)
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, asked)}})
	for _, query := range []string{"", "?cols=&rows=&term="} {
		c := dial(t, srv, shellPath+query)
		if req := <-asked; req != (wire.ShellRequest{Zathras: zathrasA, Cols: 80, Rows: 24, Term: "xterm-256color"}) {
			t.Errorf("%q: the shell was asked for with %+v", query, req)
		}
		c.CloseNow()
	}
}

func TestShellWithATerminalThatCannotBeIsRefusedBeforeTheUpgrade(t *testing.T) {
	srv := serveSockets(t, Config{Client: &fake{shell: func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) {
		t.Error("the controller was asked for a shell")
		return nil, unset
	}}})
	for _, query := range []string{
		"?cols=0", "?rows=0", "?cols=-1", "?cols=1001", "?rows=1001", "?cols=80x", "?rows=1e2", "?cols=65536",
		"?term=xterm%20256", "?term=a%2Fb", "?term=%1b%5b", "?term=" + strings.Repeat("x", 65),
	} {
		if c, status := dialWith(t, srv, shellPath+query, nil); status != http.StatusBadRequest {
			t.Errorf("%s was answered %d, want 400", query, status)
			if c != nil {
				c.CloseNow()
			}
		}
	}
	if c, status := dialWith(t, srv, "/api/zathras/web-0/shell", nil); status != http.StatusBadRequest {
		t.Errorf("a shell of what is no endpoint ID was answered %d, want 400", status)
		if c != nil {
			c.CloseNow()
		}
	}
}

func TestShellThatDraalRefuses(t *testing.T) {
	srv := serveSockets(t, Config{Client: &fake{shell: func(_ context.Context, id wire.ID, _ wire.ShellRequest) (Shell, error) {
		return nil, wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", id)
	}}})
	if msg := wantError(t, dial(t, srv, shellPath), wire.CodeUnavailable); msg != "zathras "+string(zathrasA)+" is not connected" {
		t.Errorf("the message is %q, want the one of the controller", msg)
	}
}

func TestShellIsClosedWhenThePageClosesTheSocket(t *testing.T) {
	for _, how := range []string{"with a close", "by going"} {
		sh := newFakeShell()
		srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}})
		c := dial(t, srv, shellPath)
		sh.out <- []byte("$ ")
		next(t, c)
		if how == "with a close" {
			c.Close(websocket.StatusNormalClosure, "")
		} else {
			c.CloseNow()
		}
		sh.isClosed(t)
	}
}

// Review Focus 2: a zathras that goes while its shell is open. The
// session ends with the reason, after what the shell had printed.
func TestShellOfAZathrasThatGoesEndsWithTheReason(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}})
	c := dial(t, srv, shellPath)
	sh.out <- []byte("working")
	next(t, c)

	// What draal's session returns from Read when the stream to the
	// zathras fails.
	sh.failed = wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: closed", zathrasA)
	close(sh.out)
	if msg := wantError(t, c, wire.CodeUnavailable); !strings.Contains(msg, string(zathrasA)) {
		t.Errorf("the message is %q, want it to name the zathras", msg)
	}
	sh.isClosed(t)
}

func TestShellDropsATextMessageThatIsNoChangeOfSize(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}})
	c := dial(t, srv, shellPath)
	bg := context.Background()
	for _, message := range []string{
		`not json`,
		`{}`,
		`{"resize": null}`,
		`{"resize": {"cols": 0, "rows": 24}}`,
		`{"resize": {"cols": 80, "rows": 0}}`,
		`{"resize": {"cols": 1001, "rows": 24}}`,
		`{"resize": {"cols": 80, "rows": 70000}}`,
		`{"resize": {"cols": "80", "rows": 24}}`,
		`{"exit": 0}`,
		`ls -l`,
	} {
		if err := c.Write(bg, websocket.MessageText, []byte(message)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// Behind them a message that is one: it is the first the shell hears
	// of, and nothing was answered.
	if err := c.Write(bg, websocket.MessageText, []byte(`{"resize":{"cols":1000,"rows":1}}`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 1000, Rows: 1}) {
		t.Fatalf("the shell was told the size %+v, want only the one that is valid", got)
	}
	select {
	case data := <-sh.typed:
		t.Fatalf("the shell was written %q", data)
	default:
	}
	sh.out <- []byte("still there")
	if typ, data := next(t, c); typ != websocket.MessageBinary || string(data) != "still there" {
		t.Fatalf("the next message is %v %q, want the output of the shell", typ, data)
	}
}

func TestShellGetsWhatWasSentWhileItWasStarted(t *testing.T) {
	sh := newFakeShell()
	begun, release := make(chan struct{}), make(chan struct{})
	srv := serveSockets(t, Config{Client: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		close(begun)
		select {
		case <-release:
			return sh, nil
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}}})
	c := dial(t, srv, shellPath)
	<-begun
	bg := context.Background()
	for _, m := range []struct {
		typ  websocket.MessageType
		data string
	}{
		{websocket.MessageText, `{"resize":{"cols":100,"rows":30}}`},
		{websocket.MessageBinary, "ls"},
		{websocket.MessageText, `{"resize":{"cols":101,"rows":31}}`},
		{websocket.MessageBinary, " -l\r"},
	} {
		if err := c.Write(bg, m.typ, []byte(m.data)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// A message behind them that is answered shows that they were read:
	// the socket is read while the shell is started.
	pinged, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	reading, stop := context.WithCancel(bg)
	go c.Read(reading)
	if err := c.Ping(pinged); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	_ = stop // the read goes on: it is the one that will see the output

	close(release)
	// The last size, and all that was typed in its order.
	if got := sh.wasSized(t); got != (wire.WinSize{Cols: 101, Rows: 31}) {
		t.Errorf("the shell was told the size %+v, want the last one sent", got)
	}
	if got := sh.wasTyped(t); got != "ls -l\r" {
		t.Errorf("the shell was written %q, want what was typed", got)
	}
	stop()
}

func TestShellStartEndsWhenThePageGoes(t *testing.T) {
	begun := make(chan context.Context, 1)
	srv := serveSockets(t, Config{Client: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		begun <- ctx
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}}})
	c := dial(t, srv, shellPath)
	ctx := <-begun
	c.CloseNow()
	isDone(t, "the start of the shell to end with the socket", ctx)
}

// Review Focus 5: what the page sends is handed to the shell as fast as
// the shell takes it, by a goroutine of its own. A shell that takes
// nothing for a while costs the page nothing: the socket is read all the
// while, its pings are answered, and what was typed waits, in its order.
func TestShellThatDoesNotTakeInputKeepsItsSocket(t *testing.T) {
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}, PingEvery: 10 * time.Millisecond})
	var pings atomic.Int32
	c, _ := dialWith(t, srv, shellPath, &websocket.DialOptions{
		OnPingReceived: func(context.Context, []byte) bool { pings.Add(1); return true },
	})
	// The page reads all the while, as a browser does.
	output := make(chan []byte, 16)
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				close(output)
				return
			}
			output <- data
		}
	}()
	// Nobody takes what is typed: the first of it waits in the shell's
	// Write, the rest behind it.
	bg := context.Background()
	for _, text := range []string{"one ", "two ", "three"} {
		if err := c.Write(bg, websocket.MessageBinary, []byte(text)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// Pings go by, each answered by a page that is read all the while.
	end := time.Now().Add(deadline)
	for pings.Load() < 20 {
		if time.Now().After(end) {
			t.Fatalf("%d pings came, want 20", pings.Load())
		}
		time.Sleep(time.Millisecond)
	}
	// The shell takes its input at last, all of it and in order, and the
	// socket is there for its output.
	for got := ""; got != "one two three"; {
		got += sh.wasTyped(t)
		if !strings.HasPrefix("one two three", got) {
			t.Fatalf("the shell was written %q, want what was typed in its order", got)
		}
	}
	sh.out <- []byte("done")
	select {
	case data, ok := <-output:
		if !ok || string(data) != "done" {
			t.Fatalf("the page read %q (open: %v), want the output of the shell", data, ok)
		}
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the output of the shell")
	}
}

// typing is what a test types into a shell: n bytes that tell where in
// the whole each of them is.
func typing(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// A paste: megabytes are typed, in messages of 16 KiB as the page sends
// them, into a shell that takes its input at a modest pace. The socket
// is faster than the shell, so there is not always room; the reader of
// the socket waits for it, and the page with it. All of it arrives, in
// its order, and the session is alive at the end.
func TestPasteArrivesWholeAtThePaceTheShellTakesIt(t *testing.T) {
	const piece = 16 << 10
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}, PingEvery: 20 * time.Millisecond})
	c := dial(t, srv, shellPath)
	pasted := typing(4 << 20)
	sent := make(chan error, 1)
	go func() {
		for at := 0; at < len(pasted); at += piece {
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			err := c.Write(ctx, websocket.MessageBinary, pasted[at:at+piece])
			cancel()
			if err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()
	// The page reads all the while, as a browser does; nothing comes but
	// pings, until the output at the end.
	output := make(chan []byte, 1)
	go func() {
		_, data, _ := c.Read(context.Background())
		output <- data
	}()

	var got []byte
	for len(got) < len(pasted) {
		data := []byte(sh.wasTyped(t))
		if len(data) > readLimit {
			t.Fatalf("the shell was written %d bytes at once: more than is ever kept", len(data))
		}
		got = append(got, data...)
		if !bytes.Equal(got, pasted[:min(len(got), len(pasted))]) {
			t.Fatalf("after %d bytes the shell was written what was not typed there", len(got)-len(data))
		}
		time.Sleep(time.Millisecond) // the pace of the shell
	}
	if len(got) != len(pasted) {
		t.Fatalf("the shell was written %d bytes, and %d were typed", len(got), len(pasted))
	}
	if err := await(t, "the page to have sent all of it", sent); err != nil {
		t.Fatalf("the page could not send what it typed: %v", err)
	}
	sh.out <- []byte("pasted")
	if data := await(t, "the output of the shell", output); string(data) != "pasted" {
		t.Fatalf("the page read %q, want the output of the shell: the session is to be alive", data)
	}
}

// What is kept is never more than its bound, however fast it is put in
// and however it is taken out, and nothing of it is lost or out of its
// order.
func TestShellInputNeverHoldsMoreThanItsBound(t *testing.T) {
	h := New(Config{Client: &fake{}, Dist: testDist(), Log: quiet}).(*handler)
	in := newShellInput(h)
	defer in.end()
	typed := typing(8 << 20)
	go func() {
		// Messages of every length a message may have.
		for at, n := 0, 1; at < len(typed); at, n = at+n, n*3%readLimit+1 {
			n = min(n, len(typed)-at)
			in.message(websocket.MessageBinary, typed[at:at+n])
		}
	}()
	var got []byte
	for len(got) < len(typed) {
		in.mu.Lock()
		held := len(in.typed)
		in.mu.Unlock()
		if held > readLimit {
			t.Fatalf("%d bytes are kept, and %d is the most", held, readLimit)
		}
		select {
		case <-in.more:
		case <-time.After(deadline):
			t.Fatalf("timed out after %d bytes of %d", len(got), len(typed))
		}
		data, _ := in.take()
		if len(data) > readLimit {
			t.Fatalf("%d bytes were kept, and %d is the most", len(data), readLimit)
		}
		got = append(got, data...)
	}
	if !bytes.Equal(got, typed) {
		t.Fatal("what was taken out is not what was put in")
	}
	if in.full.Err() != nil {
		t.Error("the input was given up, though it was taken all the time")
	}
}

// Once a message of what was typed has been given up, nothing more of what
// was typed is taken: what comes after the hole must not reach the shell,
// which would get the beginning and the end without the middle.
func TestShellInputTakesNothingAfterItGaveAMessageUp(t *testing.T) {
	h := New(Config{Client: &fake{}, Dist: testDist(), Log: quiet, WriteWait: 100 * time.Millisecond}).(*handler)
	in := newShellInput(h)
	defer in.end()
	in.message(websocket.MessageBinary, make([]byte, readLimit-10))
	in.message(websocket.MessageBinary, []byte("MIDDLE-THAT-DOES-NOT-FIT"))
	if in.full.Err() == nil {
		t.Fatal("the message that found no room was not given up")
	}
	// There would be room for these three bytes.
	in.message(websocket.MessageBinary, []byte("end"))
	data, _ := in.take()
	if len(data) != readLimit-10 || bytes.HasSuffix(data, []byte("end")) {
		t.Fatalf("the shell would be given %d bytes, ending in %q: the end without the middle, want only the %d bytes before the message that was given up", len(data), data[max(0, len(data)-3):], readLimit-10)
	}
	if in.put([]byte("x")) {
		t.Error("a message was taken after one was given up")
	}
}

// A shell that takes nothing: what is typed waits, up to its bound, and
// the page waits behind it. When that has lasted WriteWait the session
// ends, and says why; not before, and with nothing dropped from the
// middle of what was typed to go on with the rest.
func TestShellThatTakesNoInputForWriteWaitEndsTheSession(t *testing.T) {
	const writeWait = 500 * time.Millisecond
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}, WriteWait: writeWait})
	h := srv.Config.Handler.(*handler)
	c := dial(t, srv, shellPath)
	bg := context.Background()
	// The first message is taken to the shell's Write, where it stays.
	if err := c.Write(bg, websocket.MessageBinary, []byte("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	sh.isWriting(t)
	// As much as is kept fits behind it.
	if err := c.Write(bg, websocket.MessageBinary, make([]byte, readLimit)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// One byte more has no room, and waits for it.
	if err := c.Write(bg, websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waiting := time.Now()
	end := waiting.Add(deadline)
	for h.inputWaits.Load() == 0 {
		if time.Now().After(end) {
			t.Fatal("timed out waiting for the reader of the socket to wait for room")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-sh.closed:
		t.Fatal("the session ended as soon as there was no room")
	default:
	}

	ctx, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	_, _, err := c.Read(ctx)
	waited := time.Since(waiting)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusMessageTooBig || closed.Reason != "the shell does not take its input" {
		t.Fatalf("the socket ended with %v, want it closed as too big with the reason", err)
	}
	// The message was sent before the clock was read, so it has waited
	// no less than this.
	if waited < writeWait-50*time.Millisecond {
		t.Errorf("the session ended %s after there was no room, want it to wait %s", waited, writeWait)
	}
	sh.isClosed(t)
}

// The same before the shell runs: what is typed while it is started is
// kept whole, and when it finds no room for WriteWait the session ends.
// A shell is never given the beginning and the end of what was typed
// without the middle.
func TestInputWhileTheShellIsStartedIsKeptWholeOrTheSessionEnds(t *testing.T) {
	sh := newFakeShell()
	begun := make(chan context.Context, 1)
	srv := serveSockets(t, Config{WriteWait: 300 * time.Millisecond, Client: &fake{shell: func(ctx context.Context, _ wire.ID, _ wire.ShellRequest) (Shell, error) {
		begun <- ctx
		<-ctx.Done() // a start that takes its time
		return nil, context.Cause(ctx)
	}}})
	c := dial(t, srv, shellPath)
	starting := await(t, "the start of the shell", begun)
	bg := context.Background()
	for _, m := range []string{strings.Repeat("a", readLimit-10), "MIDDLE-THAT-DOES-NOT-FIT", "end"} {
		if err := c.Write(bg, websocket.MessageBinary, []byte(m)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(bg, deadline)
	defer cancel()
	_, _, err := c.Read(ctx)
	var closed websocket.CloseError
	if !errors.As(err, &closed) || closed.Code != websocket.StatusMessageTooBig || closed.Reason != "the shell does not take its input" {
		t.Fatalf("the socket ended with %v, want it closed as too big with the reason", err)
	}
	// The start was ended with the session.
	isDone(t, "the start of the shell to end", starting)
	select {
	case data := <-sh.typed:
		t.Errorf("a shell was written %d bytes", len(data))
	default:
	}
}

// stubbornShell is a Shell whose Write waits and is not ended by
// Close, as the Write of a stream is not that waits for the other side
// to take what was written; its Read does end at Close.
type stubbornShell struct {
	closed  chan struct{}
	writing chan struct{} // gets a value when Write is called
	release chan struct{} // closed by the test at its end
	once    sync.Once
}

func newStubbornShell(t *testing.T) *stubbornShell {
	sh := &stubbornShell{closed: make(chan struct{}), writing: make(chan struct{}, 16), release: make(chan struct{})}
	t.Cleanup(func() { close(sh.release) })
	return sh
}

func (sh *stubbornShell) Read(p []byte) (int, error) {
	<-sh.closed
	return 0, wire.Errorf(wire.CodeUnavailable, "the connection of the zathras failed: closed")
}

func (sh *stubbornShell) Write(p []byte) (int, error) {
	sh.writing <- struct{}{}
	<-sh.release
	return 0, io.ErrClosedPipe
}

func (sh *stubbornShell) Resize(cols, rows uint16) error        { return nil }
func (sh *stubbornShell) Wait(ctx context.Context) (int, error) { return 0, nil }
func (sh *stubbornShell) Close() error {
	sh.once.Do(func() { close(sh.closed) })
	return nil
}

// The handler of a shell returns on every way a session ends, whatever
// the Close of the Shell does to a Write that waits: it waits for none.
// The Write is the controller's to end. At each ending here a Write
// waits, what is kept is full, and the reader of the socket waits for
// room.
func TestShellHandlerReturnsThoughAWriteToTheShellNeverDoes(t *testing.T) {
	ways := map[string]func(c *websocket.Conn, shutDown func()){
		"the page goes":           func(c *websocket.Conn, _ func()) { c.CloseNow() },
		"the page closes":         func(c *websocket.Conn, _ func()) { go c.Close(websocket.StatusNormalClosure, "") },
		"the server ends":         func(_ *websocket.Conn, shutDown func()) { shutDown() },
		"the shell takes nothing": func(*websocket.Conn, func()) {},
		"the page types more and more": func(c *websocket.Conn, _ func()) {
			go c.Write(context.Background(), websocket.MessageBinary, make([]byte, readLimit))
		},
	}
	for way, end := range ways {
		sh := newStubbornShell(t)
		base, shutDown := context.WithCancel(context.Background())
		inner := New(Config{
			Client: &fake{shell: func(context.Context, wire.ID, wire.ShellRequest) (Shell, error) { return sh, nil }},
			Dist:   testDist(), Log: quiet,
			PingEvery: 20 * time.Millisecond, WriteWait: time.Second,
		})
		returned := make(chan struct{})
		srv := serveHandler(t, base, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner.ServeHTTP(w, r)
			close(returned)
		}))
		c := dial(t, srv, shellPath)
		bg := context.Background()
		if err := c.Write(bg, websocket.MessageBinary, []byte("pasted")); err != nil {
			t.Fatalf("%s: Write: %v", way, err)
		}
		await(t, way+": the shell to be written to", sh.writing)
		// What is kept is filled, and one message more waits for room.
		for _, n := range []int{readLimit, 1} {
			if err := c.Write(bg, websocket.MessageBinary, make([]byte, n)); err != nil {
				t.Fatalf("%s: Write: %v", way, err)
			}
		}
		for waiting := time.Now().Add(deadline); inner.(*handler).inputWaits.Load() == 0; {
			if time.Now().After(waiting) {
				t.Fatalf("%s: timed out waiting for the reader of the socket to wait for room", way)
			}
			time.Sleep(time.Millisecond)
		}
		end(c, shutDown)
		select {
		case <-returned:
		case <-time.After(deadline):
			buf := make([]byte, 1<<20)
			t.Fatalf("%s: the handler of the shell has not returned:\n%s", way, buf[:runtime.Stack(buf, true)])
		}
		select {
		case <-sh.closed:
		default:
			t.Errorf("%s: the handler returned and the shell was not closed", way)
		}
		if waits := inner.(*handler).inputWaits.Load(); waits != 0 {
			t.Errorf("%s: the handler returned and %d readers wait for room", way, waits)
		}
		shutDown()
	}
}

// Review Focus 5: output is read from the session only as fast as the
// page takes it. A shell that prints without end to a page that does not
// read is read what the connection holds and no more, and then the
// socket is closed.
func TestShellOutputIsReadOnlyAsFastAsThePageTakesIt(t *testing.T) {
	// What a connection on loopback holds on its way is some hundred
	// kilobytes on a Mac and some megabytes on Linux.
	const most = 32 << 20
	sh := newFakeShell()
	srv := serveSockets(t, Config{Client: &fake{shell: shells(sh, nil)}, WriteWait: 200 * time.Millisecond})
	dial(t, srv, shellPath) // never reads
	chunk := make([]byte, shellChunk)
	sent := 0
	for {
		select {
		case sh.out <- chunk:
			if sent += len(chunk); sent > most {
				t.Fatalf("more than %d bytes were read from a shell whose page does not read", most)
			}
			continue
		case <-sh.closed:
		case <-time.After(deadline):
			t.Fatal("timed out waiting for the shell to be closed")
		}
		break
	}
	t.Logf("%d bytes were read from the shell before its socket was closed", sent)
}
