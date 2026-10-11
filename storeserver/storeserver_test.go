package storeserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/jaccard-store/bucket"
	"github.com/amber-store/jaccard-store/bucket/buckettest"
	"github.com/amber-store/jaccard-store/client"
	jsnode "github.com/amber-store/jaccard-store/node"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
)

// patience is how long a test waits for what has to happen. Nothing waits
// for it when all is well.
const patience = 30 * time.Second

// quiet drops what the endpoints log.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// logBuffer is what the store of a test logs to.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// gate is the bucket of a test, with two ways to make it misbehave: the
// URLs of a pull can be held back, and those of an upload can fail.
type gate struct {
	jsserver.Bucket

	mu     sync.Mutex
	hold   chan struct{} // while set and open, PresignGet waits for it, or for its context
	deaf   bool          // PresignGet waits for hold alone, whatever its context says
	putErr error         // while set, what PresignPut fails with
	// waiting gets a value for every PresignGet that begins to wait.
	waiting chan struct{}
}

// holdPulls makes every pull from now on wait until what it returns is
// called, or until the request is ended.
func (g *gate) holdPulls() (release func()) {
	hold := make(chan struct{})
	g.mu.Lock()
	g.hold = hold
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.hold = nil
			g.mu.Unlock()
			close(hold)
		})
	}
}

func (g *gate) failUploads(err error) {
	g.mu.Lock()
	g.putErr = err
	g.mu.Unlock()
}

func (g *gate) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	g.mu.Lock()
	hold, deaf := g.hold, g.deaf
	g.mu.Unlock()
	if hold != nil {
		g.waiting <- struct{}{}
		if deaf {
			<-hold
			return "", errors.New("the bucket of the test was held")
		}
		select {
		case <-hold:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return g.Bucket.PresignGet(ctx, objectKey, ttl)
}

func (g *gate) PresignPut(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	g.mu.Lock()
	err := g.putErr
	g.mu.Unlock()
	if err != nil {
		return "", err
	}
	return g.Bucket.PresignPut(ctx, objectKey, ttl)
}

// world is a store behind an endpoint on loopback, as draal has it.
type world struct {
	t        *testing.T
	store    *Store
	listener node.Listener
	operator node.Key
	bucket   *gate
	log      *logBuffer
}

func newKey(t *testing.T) node.Key {
	t.Helper()
	k, err := node.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

// start opens a store over a bucket in memory and binds an endpoint that
// gives it the connections of its protocol. change, if it is given, may
// change the Config before the store is opened. All of it ends with the
// test. The bucket sets the environment of the test, so a test that calls
// start cannot be parallel.
func start(t *testing.T, change func(*Config)) *world {
	t.Helper()
	w := &world{t: t, operator: newKey(t), log: &logBuffer{}}
	w.bucket = &gate{Bucket: buckettest.New(t), waiting: make(chan struct{}, 64)}
	cfg := Config{
		Dir:     t.TempDir(),
		Objects: w.bucket,
		Authz:   authz.NewAllowlist([]wire.ID{w.operator.ID()}),
		Log:     slog.New(slog.NewTextHandler(w.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if change != nil {
		change(&cfg)
	}
	store, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w.store = store

	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{
		Key:      newKey(t),
		Local:    true,
		Handlers: map[string]func(node.Conn){ALPN: store.ServeConn},
		Log:      quiet,
	})
	if err != nil {
		store.Close()
		t.Fatalf("Listen: %v", err)
	}
	w.listener = l
	// A connection of the store's protocol is handed to the store by
	// Accept, so something has to accept.
	accepting, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept(accepting)
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	t.Cleanup(func() {
		store.Stop()
		stop()
		l.Close()
		<-done
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return w
}

// dial connects to the store as k, for jaccard-store's protocol. The
// connection ends with the test.
func (w *world) dial(k node.Key) *jsnode.Conn {
	w.t.Helper()
	server, err := irohkey.ParseEndpointID(string(w.listener.ID()))
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    k.Iroh(),
		ALPN:   jswire.ALPN,
		Server: server,
		Addrs:  []netip.AddrPort{w.listener.LocalAddr()},
		Log:    quiet,
	})
	if err != nil {
		w.t.Fatalf("dialing the store: %v", err)
	}
	w.t.Cleanup(func() { conn.Close() })
	return conn
}

// storePath makes a directory of the name given, as a store path is one,
// with a file of the content given in it, and returns its path.
func storePath(t *testing.T, name, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "program"), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pushDir imports dir and pushes it as the reference name over conn, as
// jaccard-store's client does it, and returns the root of its tree.
func pushDir(t *testing.T, conn *jsnode.Conn, dir, name string) (key.Key, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	temp := t.TempDir()
	objects, err := packstore.Open(filepath.Join(temp, "packstore"), packstore.WithSync(false))
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	root, _, err := ingest.Dir(objects, dir, ingest.Opts{NoIgnore: true})
	if err != nil {
		t.Fatalf("importing %s: %v", dir, err)
	}
	_, err = client.New(conn, nil).Push(ctx, objects, name, root, client.PushOptions{MinDedup: 0.5, TempDir: temp})
	return root, err
}

// pull fetches the reference name over conn into a store of its own, and
// returns the root the reference has.
func pull(t *testing.T, conn *jsnode.Conn, name string) (key.Key, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	objects, err := packstore.Open(filepath.Join(t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	res, err := client.New(conn, nil).Pull(ctx, objects, name, client.PullOptions{})
	return res.Root, err
}

// ask sends one request on a stream of its own, as it is, and returns
// what the store answered. An answer that is a refusal is returned as the
// answer, not as an error.
func ask(t *testing.T, conn *jsnode.Conn, req jswire.Request) (jswire.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	s, err := conn.OpenStreamConn(ctx)
	if err != nil {
		return jswire.Response{}, err
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(patience))
	if err := jswire.WriteFrame(s, req); err != nil {
		return jswire.Response{}, err
	}
	var resp jswire.Response
	err = jswire.ReadFrame(s, &resp)
	return resp, err
}

// eventually fails the test unless cond comes true in time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(patience)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// gone fails the test unless the connection of a client has ended: no
// request is answered on it any more.
func gone(t *testing.T, what string, conn *jsnode.Conn) {
	t.Helper()
	eventually(t, "the connection to end: "+what, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s, err := conn.OpenStreamConn(ctx)
		if err != nil {
			return true
		}
		defer s.Close()
		s.SetDeadline(time.Now().Add(time.Second))
		if err := jswire.WriteFrame(s, jswire.Request{Op: jswire.OpPull, Name: "nothing"}); err != nil {
			return true
		}
		var resp jswire.Response
		return jswire.ReadFrame(s, &resp) != nil
	})
}

const webRef = "/services/web/nix/store/abc-web"

// The store answers jaccard-store's protocol, whatever that is called.
func TestALPNIsThatOfJaccardStore(t *testing.T) {
	if ALPN != jswire.ALPN {
		t.Errorf("ALPN is %q, and jaccard-store's is %q", ALPN, jswire.ALPN)
	}
}

func TestAnOperatorPushesAndAStrangerPulls(t *testing.T) {
	w := start(t, nil)
	dir := storePath(t, "abc-web", "the program of web")
	pushed, err := pushDir(t, w.dial(w.operator), dir, webRef)
	if err != nil {
		t.Fatalf("the push of an operator: %v", err)
	}
	// Whoever knows the ID pulls: the mount of a zathras does so with a
	// key that draal has never heard of.
	stranger := newKey(t)
	pulled, err := pull(t, w.dial(stranger), webRef)
	if err != nil {
		t.Fatalf("the pull of a stranger: %v", err)
	}
	if pulled != pushed {
		t.Errorf("pulled the root %s, and %s was pushed", pulled, pushed)
	}
	// The pack is recorded as uploaded by the endpoint that pushed it.
	body := get(t, w.store.Admin(), "/api/refs")
	if !strings.Contains(body, string(w.operator.ID())) {
		t.Errorf("the store's page does not name the operator as the one who pushed: %s", body)
	}
	// A name the store has no reference for is what jaccard-store's
	// client knows as no such reference, for a stranger as for anyone:
	// the mount of a zathras tells a store path that was never pushed
	// from one that could not be fetched by it.
	if _, err = pull(t, w.dial(stranger), "/services/web/nix/store/nothing"); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("the pull of a name that is not there: %v, want client.ErrNotFound", err)
	}
}

// Everything of the store but a pull is an operator's: a stranger's is
// refused with a sentence, done to nothing, and logged.
func TestAStrangerMayNotWrite(t *testing.T) {
	w := start(t, nil)
	dir := storePath(t, "abc-web", "the program of web")
	root, err := pushDir(t, w.dial(w.operator), dir, webRef)
	if err != nil {
		t.Fatalf("the push of an operator: %v", err)
	}
	stranger := newKey(t)
	conn := w.dial(stranger)

	// As jaccard-store's client meets it.
	if _, err := pushDir(t, conn, storePath(t, "def-evil", "not the program of web"), "/services/web/nix/store/def-evil"); err == nil ||
		!strings.Contains(err.Error(), "unauthorized: push-start is for operators") {
		t.Errorf("the push of a stranger: %v, want it refused as unauthorized: push-start is for operators", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	if refs, err := client.New(conn, nil).List(ctx, ""); err == nil || !strings.Contains(err.Error(), "unauthorized: list is for operators") {
		t.Errorf("the list of a stranger: %v, %v; want it refused as unauthorized: list is for operators", refs, err)
	}
	if err := client.New(conn, nil).Delete(ctx, webRef); err == nil || !strings.Contains(err.Error(), "unauthorized: delete is for operators") {
		t.Errorf("the delete of a stranger: %v, want it refused as unauthorized: delete is for operators", err)
	}

	// Every operation of a push by itself, as a client of its own making
	// would send them.
	for _, op := range []jswire.Op{jswire.OpPushStart, jswire.OpPushUpload, jswire.OpPushCommit, jswire.OpList, jswire.OpDelete} {
		// Three of the five were refused above already: what is held is
		// that this one leaves a line of its own.
		line := "refused peer=" + string(stranger.ID()) + " operation=" + string(op)
		before := strings.Count(w.log.String(), line)
		resp, err := ask(t, conn, jswire.Request{Op: op, Name: webRef, Root: root[:], UploadID: "1"})
		if err != nil {
			t.Errorf("%s of a stranger: %v, want a refusal", op, err)
			continue
		}
		want := "unauthorized: " + string(op) + " is for operators"
		if resp.Error == nil || resp.Error.Code != jswire.CodeBadRequest || resp.Error.Message != want {
			t.Errorf("%s of a stranger: %+v, want the refusal %q", op, resp.Error, want)
		}
		if logged := strings.Count(w.log.String(), line) - before; logged != 1 {
			t.Errorf("the refusal of %s left %d lines with the peer and the operation, want one:\n%s", op, logged, w.log.String())
		}
	}

	// Nothing came of any of it: the operator finds what it pushed, and
	// nothing else.
	refs, err := client.New(w.dial(w.operator), nil).List(ctx, "")
	if err != nil || len(refs) != 1 || refs[0].Name != webRef || refs[0].Root != root {
		t.Errorf("the store holds %+v, %v; want the one reference the operator pushed", refs, err)
	}
	// And the operator may delete it.
	if err := client.New(w.dial(w.operator), nil).Delete(ctx, webRef); err != nil {
		t.Errorf("the delete of an operator: %v", err)
	}
}

// What great-machine rollout prints when the key it runs with is no
// operator's: the push of this module asks the store what it has before
// it pushes, and that is refused in words a person understands.
func TestAPushByNoOperatorSaysSo(t *testing.T) {
	w := start(t, nil)
	dir := storePath(t, "abc-web", "the program of web")
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	_, err := push.Push(ctx, push.Config{
		Key:     newKey(t),
		Server:  w.listener.ID(),
		Addrs:   []netip.AddrPort{w.listener.LocalAddr()},
		Prefix:  wire.StorePrefix("web"),
		TempDir: t.TempDir(),
	}, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "unauthorized: list is for operators") {
		t.Fatalf("Push as a stranger: %v, want it to say unauthorized: list is for operators", err)
	}
	// With the operator's key the same push goes through, to the same
	// endpoint.
	res, err := push.Push(ctx, push.Config{
		Key:     w.operator,
		Server:  w.listener.ID(),
		Addrs:   []netip.AddrPort{w.listener.LocalAddr()},
		Prefix:  wire.StorePrefix("web"),
		TempDir: t.TempDir(),
	}, []string{dir})
	if err != nil || res != (push.Result{Pushed: 1}) {
		t.Fatalf("Push as the operator = %+v, %v; want one pushed", res, err)
	}
}

func TestAnOperationTheStoreDoesNotKnow(t *testing.T) {
	w := start(t, nil)
	for _, k := range []node.Key{w.operator, newKey(t)} {
		resp, err := ask(t, w.dial(k), jswire.Request{Op: "frobnicate", Name: webRef})
		if err != nil {
			t.Fatalf("asking: %v", err)
		}
		if resp.Error == nil || resp.Error.Code != jswire.CodeBadRequest || !strings.Contains(resp.Error.Message, "the store answers no such request") {
			t.Errorf("an operation the store does not know: %+v, want a bad_request that says so", resp.Error)
		}
	}
}

// A stream whose request does not come is closed after RequestWait,
// without an answer, and costs nothing after that.
func TestNoRequestWithinRequestWait(t *testing.T) {
	w := start(t, func(cfg *Config) { cfg.RequestWait = 50 * time.Millisecond })
	conn := w.dial(newKey(t))
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	for range 8 {
		s, err := conn.OpenStreamConn(ctx)
		if err != nil {
			t.Fatalf("OpenStreamConn: %v", err)
		}
		defer s.Close()
		// The store sees a stream when something was written to it: half
		// of the length of a frame, and no more.
		if _, err := s.Write([]byte{0, 0}); err != nil {
			t.Fatalf("writing: %v", err)
		}
		s.SetReadDeadline(time.Now().Add(patience))
		if n, err := io.Copy(io.Discard, s); n != 0 {
			t.Fatalf("the store answered %d bytes to a request that never came (%v)", n, err)
		}
	}
	eventually(t, "the streams to be let go", func() bool { return w.store.streams.Load() == 0 })
	// The connection is as good as before.
	if resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: "nothing"}); err != nil || resp.Error == nil || resp.Error.Code != jswire.CodeNotFound {
		t.Errorf("a request after the silent streams: %+v, %v; want not_found", resp, err)
	}
}

// At most MaxStreams streams of a connection are answered at a time; one
// beyond them is closed without an answer, which is logged once.
func TestOneStreamMoreThanMaxStreams(t *testing.T) {
	w := start(t, func(cfg *Config) { cfg.MaxStreams = 2 })
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Fatalf("the push: %v", err)
	}
	conn := w.dial(newKey(t))
	release := w.bucket.holdPulls()
	defer release()

	answers := make(chan error, 2)
	for range 2 {
		go func() {
			resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef})
			if err == nil && resp.Error != nil {
				err = resp.Error
			}
			answers <- err
		}()
	}
	for range 2 {
		select {
		case <-w.bucket.waiting:
		case <-time.After(patience):
			t.Fatal("timed out waiting for two pulls to be under way")
		}
	}
	for range 3 {
		if resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef}); err == nil {
			t.Fatalf("a stream beyond MaxStreams was answered: %+v", resp)
		}
	}
	if n := strings.Count(w.log.String(), "more streams than the store answers at a time"); n != 1 {
		t.Errorf("the streams beyond MaxStreams were logged %d times, want once:\n%s", n, w.log.String())
	}
	release()
	for range 2 {
		select {
		case err := <-answers:
			if err != nil {
				t.Errorf("a pull within MaxStreams: %v", err)
			}
		case <-time.After(patience):
			t.Fatal("timed out waiting for the pulls that were held")
		}
	}
	eventually(t, "the streams to be let go", func() bool { return w.store.streams.Load() == 0 })
	// There is room again.
	if resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef}); err != nil || resp.Error != nil {
		t.Errorf("a pull after the others have ended: %+v, %v", resp.Error, err)
	}
}

// Stop lets the requests under way end, begins no new one, and closes the
// connections when the last has ended.
func TestStopWaitsForTheRequestsUnderWay(t *testing.T) {
	w := start(t, nil)
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Fatalf("the push: %v", err)
	}
	conn, idle := w.dial(newKey(t)), w.dial(newKey(t))
	// The store has both connections before it is stopped.
	if _, err := ask(t, idle, jswire.Request{Op: jswire.OpPull, Name: "nothing"}); err != nil {
		t.Fatalf("asking on the connection that will be idle: %v", err)
	}
	release := w.bucket.holdPulls()
	defer release()
	answer := make(chan error, 1)
	go func() {
		resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef})
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		answer <- err
	}()
	select {
	case <-w.bucket.waiting:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the pull to be under way")
	}

	stopped := make(chan struct{})
	go func() {
		w.store.Stop()
		close(stopped)
	}()
	// A connection with nothing under way is closed at once, and Stop
	// waits for the other.
	gone(t, "it has no request under way", idle)
	select {
	case <-stopped:
		t.Fatal("Stop returned while a request was under way and had its grace")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-answer:
		if err != nil {
			t.Errorf("the request that was under way when Stop was called: %v, want its answer", err)
		}
	case <-time.After(patience):
		t.Fatal("timed out waiting for the answer of the request under way")
	}
	select {
	case <-stopped:
	case <-time.After(patience):
		t.Fatal("Stop did not return when the last request had ended")
	}
	gone(t, "the store is stopped", conn)
	// A connection that comes now is closed at once.
	gone(t, "the store was stopped before it came", w.dial(newKey(t)))
	// Stop may be called again, and Close closes what is left.
	w.store.Stop()
	if err := w.store.Close(); err != nil {
		t.Errorf("Close after Stop: %v", err)
	}
	if err := w.store.Close(); err != nil {
		t.Errorf("Close, a second time: %v", err)
	}
}

// A request that outlives StopGrace is ended, and Stop returns.
func TestStopEndsARequestThatOutlivesTheGrace(t *testing.T) {
	w := start(t, func(cfg *Config) { cfg.StopGrace = 50 * time.Millisecond })
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Fatalf("the push: %v", err)
	}
	conn := w.dial(newKey(t))
	release := w.bucket.holdPulls()
	defer release()
	answer := make(chan error, 1)
	go func() {
		resp, err := ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef})
		if err == nil && resp.Error != nil {
			err = resp.Error
		}
		answer <- err
	}()
	select {
	case <-w.bucket.waiting:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the pull to be under way")
	}
	stopped := make(chan struct{})
	go func() {
		w.store.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(patience):
		t.Fatal("Stop did not return though the grace was over")
	}
	select {
	case err := <-answer:
		if err == nil {
			t.Error("the request that outlived the grace was answered as if nothing had happened")
		}
	case <-time.After(patience):
		t.Fatal("the client of the request that was ended still waits")
	}
	gone(t, "the store is stopped", conn)
	if n := w.store.streams.Load(); n != 0 {
		t.Errorf("%d streams are still being served after Stop", n)
	}
}

// A request that does not end with its context, as a verification that
// walks a large pack does not until it is through: Stop gives it the
// grace a second time, says so, and returns. draal is not held by it.
func TestStopReturnsThoughARequestDoesNotEnd(t *testing.T) {
	w := start(t, func(cfg *Config) { cfg.StopGrace = 50 * time.Millisecond })
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Fatalf("the push: %v", err)
	}
	conn := w.dial(newKey(t))
	w.bucket.mu.Lock()
	w.bucket.deaf = true
	w.bucket.mu.Unlock()
	release := w.bucket.holdPulls()
	// The request is let go before the store is closed, which the
	// cleanup of start does.
	t.Cleanup(func() {
		release()
		eventually(t, "the request that was held to end", func() bool { return w.store.streams.Load() == 0 })
	})
	go ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef})
	select {
	case <-w.bucket.waiting:
	case <-time.After(patience):
		t.Fatal("timed out waiting for the pull to be under way")
	}
	stopped := make(chan struct{})
	began := time.Now()
	go func() {
		w.store.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waits for a request that does not end with its context")
	}
	if took := time.Since(began); took < 100*time.Millisecond {
		t.Errorf("Stop returned after %s: the request had no second grace", took)
	}
	if n := w.store.streams.Load(); n != 1 {
		t.Errorf("%d streams are being served, want the one that was held", n)
	}
	if log := w.log.String(); !strings.Contains(log, "have not ended") {
		t.Errorf("the store did not say that it stopped with a request that has not ended:\n%s", log)
	}
	gone(t, "the store is stopped", conn)
}

// A bucket that cannot do its part: the client is told which step
// failed, and why is in the log of the store, as jaccard-store has it.
func TestABucketThatFails(t *testing.T) {
	w := start(t, nil)
	w.bucket.failUploads(errors.New("the bucket is on fire"))
	_, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef)
	var refusal *jswire.Error
	if !errors.As(err, &refusal) || refusal.Code != jswire.CodeInternal || !strings.Contains(err.Error(), "signing the index URL failed") {
		t.Fatalf("a push while the bucket fails: %v, want internal: signing the index URL failed", err)
	}
	if !strings.Contains(w.log.String(), "the bucket is on fire") {
		t.Errorf("the log does not say why:\n%s", w.log.String())
	}
	// The store is none the worse: the push goes through once the bucket
	// does its part.
	w.bucket.failUploads(nil)
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Errorf("the push after the bucket recovered: %v", err)
	}
}

// get asks a handler for a path and returns the body of an answer of 200.
func get(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d, %v: %s", path, resp.StatusCode, err, body)
	}
	return string(body)
}

func TestAdminServesThePageAndTheFigures(t *testing.T) {
	w := start(t, nil)
	if page := get(t, w.store.Admin(), "/"); !strings.Contains(page, "jaccard-store") || !strings.Contains(page, "app.js") {
		t.Errorf("the page of the store is not jaccard-store's admin page: %s", page)
	}
	var stats struct {
		Refs int64 `json:"refs"`
	}
	if err := json.Unmarshal([]byte(get(t, w.store.Admin(), "/api/stats")), &stats); err != nil || stats.Refs != 0 {
		t.Fatalf("the figures of an empty store: %+v, %v", stats, err)
	}
	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
		t.Fatalf("the push: %v", err)
	}
	if err := json.Unmarshal([]byte(get(t, w.store.Admin(), "/api/stats")), &stats); err != nil || stats.Refs != 1 {
		t.Errorf("the figures after a push: %+v, %v; want one reference", stats, err)
	}
	if refs := get(t, w.store.Admin(), "/api/refs"); !strings.Contains(refs, webRef) {
		t.Errorf("the references of the page do not name %s: %s", webRef, refs)
	}
}

func TestOpenRefusesWhatCannotBeAStore(t *testing.T) {
	bucket := buckettest.New(t)
	allow := authz.NewAllowlist(nil)
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"no directory", Config{Objects: bucket, Authz: allow}, "no directory"},
		{"no authorizer", Config{Dir: t.TempDir(), Objects: bucket}, "nobody to authorize"},
		{"no bucket", Config{Dir: t.TempDir(), Authz: allow}, "no bucket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(context.Background(), tt.cfg)
			if err == nil {
				s.Close()
				t.Fatal("Open made a store")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Open: %v, want an error that says %q", err, tt.want)
			}
		})
	}
}

// The database of the store is store.sqlite in its directory, and the
// scratch space a directory beside it unless another is named: what a
// store was opened with, it is opened with again.
func TestTheStoreIsKeptInItsDirectory(t *testing.T) {
	bucket := buckettest.New(t)
	dir, scratch := t.TempDir(), filepath.Join(t.TempDir(), "elsewhere")
	for _, cfg := range []Config{
		{Dir: dir, Objects: bucket, Authz: authz.NewAllowlist(nil), Log: quiet},
		{Dir: dir, Objects: bucket, Authz: authz.NewAllowlist(nil), Scratch: scratch, Log: quiet},
	} {
		s, err := Open(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "store.sqlite"), filepath.Join(dir, "scratch"), scratch} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

// The bucket is not asked anything when the store is opened: one that
// cannot be reached shows when a client is sent to it, and the error is
// the client's, with the address it could not reach.
func TestABucketThatCannotBeReached(t *testing.T) {
	// An address nothing listens on: that of a server that is closed.
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.Listener.Addr().String()
	closed.Close()
	// The credentials are those of the environment, as in draal: none of
	// whoever runs the test.
	absent := filepath.Join(t.TempDir(), "absent")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", absent)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", absent)

	operator := newKey(t)
	store, err := Open(context.Background(), Config{
		Dir:    t.TempDir(),
		Bucket: bucket.Config{Bucket: "packs", Endpoint: "http://" + addr, Region: "us-east-1", PathStyle: true},
		Authz:  authz.NewAllowlist([]wire.ID{operator.ID()}),
		Log:    quiet,
	})
	if err != nil {
		t.Fatalf("Open with a bucket that cannot be reached: %v", err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	l, err := node.Listen(ctx, node.ListenConfig{Key: newKey(t), Local: true, Handlers: map[string]func(node.Conn){ALPN: store.ServeConn}, Log: quiet})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()
	go l.Accept(ctx)
	w := &world{t: t, store: store, listener: l, operator: operator}

	_, err = pushDir(t, w.dial(operator), storePath(t, "abc-web", "the program of web"), webRef)
	if err == nil || !strings.Contains(err.Error(), addr) {
		t.Fatalf("a push to a store whose bucket cannot be reached: %v, want an error that names %s", err, addr)
	}
	t.Logf("the push failed with: %v", err)
	// Nothing points at what was not uploaded.
	if _, err := pull(t, w.dial(newKey(t)), webRef); !errors.Is(err, client.ErrNotFound) {
		t.Errorf("a pull of what could not be pushed: %v, want client.ErrNotFound", err)
	}
}
