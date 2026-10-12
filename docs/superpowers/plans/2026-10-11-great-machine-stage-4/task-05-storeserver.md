### Task 5: `storeserver`: the store in draal

The store is jaccard-store's server, with its database and its bucket, put together as `jaccard-stored` puts them together, and answered on connections that somebody else accepted. The server's own `Serve` takes a whole endpoint, so it is not used: `ServeConn` reads one request from every stream, asks `authz`, and has the server's `Handle` answer. What draal accepts on its own protocol in the way of streams (a request within 5 seconds, 64 at a time) holds here too.

Four things are this package's own, and each was found by a test:

- **A stream that was answered is kept until the peer ends it**, 2 seconds at most (`AnswerWait`). jaccard-store's client closes its stream when it has read the answer. Closing a connection discards what its streams have not delivered, so without this a request that had its grace when the store is stopped could lose its answer.
- **`Stop` has an order**: no new request; the requests under way have `StopGrace`; then their context ends and the connections are closed; and a request that does not end with its context has `StopGrace` once more and is left behind, so that draal is not held by a verification in the middle of a large pack.
- **The bucket is asked nothing when the store is opened.** One that is not there shows in the first push.
- **The scratch directory is one the store makes for itself**, `great-machine-scratch`, inside the directory it is given. jaccard-store's server empties its scratch directory whenever it is made; given the directory of the store itself, it would empty that, the database and draal's key with it.

**Files:**
- Create: `storeserver/storeserver.go`
- Test: `storeserver/storeserver_test.go`

**Interfaces:**
- Consumes: `node.Conn`, `node.Stream` and `node.ListenConfig.Handlers` (Task 4); `authz.Authorizer`, `authz.OpStorePull`, `authz.OpStoreWrite` (Task 3); of jaccard-store v0.7.0: `server.New`, `(*server.Server).Handle` and `RunSweeper`, `server.Bucket`, `db.Open`, `bucket.New` and `bucket.Config`, `admin.Handler`, `wire.ReadRequest`, `wire.WriteFrame`, `wire.Errorf`; in the tests `bucket/buckettest`, an S3 in memory, which sets the environment of the test (`t.Setenv`): **a test that uses it cannot be parallel.**
- Produces, in package `storeserver`:
  - `const ALPN = "amber/jaccard-store/1"`, held equal to jaccard-store's `wire.ALPN` by a test.
  - `type Config struct { Dir string; Bucket bucket.Config; Objects server.Bucket; Scratch string; VerifyJobs int; MaxPackBytes int64; NoVerify bool; Authz authz.Authorizer; RequestWait, AnswerWait time.Duration; MaxStreams int; StopGrace, SweepEvery time.Duration; Log *slog.Logger }`. `Dir` and `Authz` are required, and `Bucket.Bucket` unless `Objects` is set, which is for tests: the bucket itself.
  - `func Open(ctx context.Context, cfg Config) (*Store, error)`: the database `store.sqlite` in `Dir`, the bucket, the server, and the sweeper, which it starts.
  - `func (s *Store) ServeConn(conn node.Conn)`: what `node.ListenConfig.Handlers` has for `ALPN`.
  - `func (s *Store) Admin() http.Handler`: jaccard-store's admin page, asked with the path it is mounted under taken off.
  - `func (s *Store) Stop()` and `func (s *Store) Close() error`. `Close` stops, waits for the sweeper and closes the database. Both may be called more than once.
  - What a peer reads: for a push, a list or a delete by someone who is no operator, an error of jaccard-store's protocol, `bad_request`, `unauthorized: <operation> is for operators`; for an operation the store does not know, `the store answers no such request`, whoever asks.

- [ ] **Step 1: Write the failing test: the store in draal: an operator pushes, anyone pulls, and what it accepts is bounded**

Create `storeserver/storeserver_test.go`:

```go
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
	hold := g.hold
	g.mu.Unlock()
	if hold != nil {
		g.waiting <- struct{}{}
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
		resp, err := ask(t, conn, jswire.Request{Op: op, Name: webRef, Root: root[:], UploadID: "1"})
		if err != nil {
			t.Errorf("%s of a stranger: %v, want a refusal", op, err)
			continue
		}
		want := "unauthorized: " + string(op) + " is for operators"
		if resp.Error == nil || resp.Error.Code != jswire.CodeBadRequest || resp.Error.Message != want {
			t.Errorf("%s of a stranger: %+v, want the refusal %q", op, resp.Error, want)
		}
		if !strings.Contains(w.log.String(), "refused peer="+string(stranger.ID())+" operation="+string(op)) {
			t.Errorf("the refusal of %s is not logged with the peer and the operation:\n%s", op, w.log.String())
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
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./storeserver/`

Expected: it fails, with

```text
storeserver/storeserver_test.go:125:12: undefined: Store
```

- [ ] **Step 3: Write the code: package storeserver**

Create `storeserver/storeserver.go`:

```go
// Package storeserver is the store in draal: the jaccard-store server,
// answered on the connections that draal's own endpoint accepts for
// jaccard-store's protocol. The endpoint ID of draal is so all that an
// operator or a pod needs, for the protocol of great-machine and for the
// store.
//
// The server is jaccard-store's package server over its db and its
// bucket, put together as jaccard-stored puts them together. Its own
// Serve is not used, because it takes a whole endpoint: a connection is
// given to ServeConn instead, and every stream of it is one request, which
// is authorized before the server sees it. A pull is every peer's, since
// the mount of a zathras pulls with a key of its own that draal does not
// know, and since what the store holds is what the pods run. Everything
// else, pushing, listing and deleting, is an operator's.
//
// Nothing of a pack passes through here: a client moves the bytes between
// itself and the bucket through URLs the server signs.
package storeserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amber-store/jaccard-store/admin"
	"github.com/amber-store/jaccard-store/bucket"
	jsdb "github.com/amber-store/jaccard-store/db"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// ALPN is the protocol the store answers: jaccard-store's.
const ALPN = "amber/jaccard-store/1"

// The store in its directory.
const (
	dbFile     = "store.sqlite"
	scratchDir = "scratch"
)

// Config is what a Store is made of. The zero value of a number or of a
// duration stands for its default.
type Config struct {
	// Dir holds the database of the store, store.sqlite. It is the only
	// record of the references and of which pack leans on which.
	Dir string
	// Bucket names the S3 bucket the packs are kept in. The credentials
	// are those the AWS SDK finds.
	Bucket bucket.Config
	// Objects is for tests: the bucket itself. When it is set, Bucket is
	// not looked at.
	Objects jsserver.Bucket
	// Scratch is where packs are verified: the uncompressed data of each
	// is written there, MaxPackBytes at most for each of VerifyJobs. It
	// is the store's alone and is emptied when the store is opened. "":
	// the directory scratch in Dir.
	Scratch      string
	VerifyJobs   int   // 0: 2 packs verified at once
	MaxPackBytes int64 // 0: 16 GiB, the largest pack, uncompressed
	// NoVerify records a pack without reading its data, as
	// jaccard-store's --no-verify does.
	NoVerify bool
	// Authz decides who may do what: a pull is asked as
	// authz.OpStorePull, everything else as authz.OpStoreWrite.
	Authz       authz.Authorizer
	RequestWait time.Duration // 0: 5 seconds, for the request of a stream
	AnswerWait  time.Duration // 0: 2 seconds, for the peer to end a stream that was answered
	MaxStreams  int           // 0: 64 streams of a connection answered at a time
	StopGrace   time.Duration // 0: 10 seconds, for the requests under way when Stop is called
	SweepEvery  time.Duration // 0: 30 seconds
	Log         *slog.Logger  // nil: slog.Default()
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.Scratch == "" {
		cfg.Scratch = filepath.Join(cfg.Dir, scratchDir)
	}
	if cfg.RequestWait == 0 {
		cfg.RequestWait = 5 * time.Second
	}
	if cfg.AnswerWait == 0 {
		cfg.AnswerWait = 2 * time.Second
	}
	if cfg.MaxStreams == 0 {
		cfg.MaxStreams = 64
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = 10 * time.Second
	}
	if cfg.SweepEvery == 0 {
		cfg.SweepEvery = 30 * time.Second
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// Store is the store of a draal. Its methods may be called from any
// goroutine.
type Store struct {
	cfg    Config
	log    *slog.Logger
	db     *jsdb.DB
	server *jsserver.Server

	// accepting is done when Stop begins: no stream is accepted after
	// that, and a stream whose request has not come is closed.
	accepting context.Context
	refuse    context.CancelFunc
	// requests is what every request is answered under. It is done when
	// the grace of Stop is over.
	requests context.Context
	end      context.CancelFunc

	mu       sync.Mutex
	stopping bool
	conns    map[node.Conn]struct{}
	// serving are the calls of ServeConn, and under the requests under
	// way. Both are added to under mu and only while stopping is false,
	// so that Stop waits for all there are.
	serving sync.WaitGroup
	under   sync.WaitGroup

	// streams is how many streams are being served. The tests look at
	// it.
	streams atomic.Int64

	stop    sync.Once
	stopped chan struct{}

	sweeping context.CancelFunc
	swept    chan struct{}

	close    sync.Once
	closeErr error
}

// Open opens the database and the bucket, makes the server and starts its
// sweeper. ctx bounds the opening alone.
//
// The bucket is not asked anything here: one that is not there, or cannot
// be reached, shows in the first request that needs it.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	switch {
	case cfg.Dir == "":
		return nil, errors.New("storeserver: no directory to keep the store in")
	case cfg.Authz == nil:
		return nil, errors.New("storeserver: nobody to authorize a request")
	case cfg.Objects == nil && cfg.Bucket.Bucket == "":
		return nil, errors.New("storeserver: no bucket")
	}
	cfg = cfg.withDefaults()
	objects := cfg.Objects
	if objects == nil {
		b, err := bucket.New(ctx, cfg.Bucket)
		if err != nil {
			return nil, fmt.Errorf("storeserver: %w", err)
		}
		objects = b
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("storeserver: the directory of the store: %w", err)
	}
	database, err := jsdb.Open(filepath.Join(cfg.Dir, dbFile))
	if err != nil {
		return nil, fmt.Errorf("storeserver: the database: %w", err)
	}
	srv, err := jsserver.New(jsserver.Config{
		DB:           database,
		Bucket:       objects,
		Scratch:      cfg.Scratch,
		VerifyJobs:   cfg.VerifyJobs,
		MaxPackBytes: cfg.MaxPackBytes,
		NoVerify:     cfg.NoVerify,
		Log:          cfg.Log,
	})
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("storeserver: %w", err)
	}
	s := &Store{
		cfg:     cfg,
		log:     cfg.Log,
		db:      database,
		server:  srv,
		conns:   make(map[node.Conn]struct{}),
		stopped: make(chan struct{}),
		swept:   make(chan struct{}),
	}
	s.accepting, s.refuse = context.WithCancel(context.Background())
	s.requests, s.end = context.WithCancel(context.Background())
	sweeping, stop := context.WithCancel(context.Background())
	s.sweeping = stop
	go func() {
		defer close(s.swept)
		srv.RunSweeper(sweeping, cfg.SweepEvery)
	}()
	return s, nil
}

// ServeConn answers the requests of one connection, each a stream, until
// the connection ends or the store is stopped, and closes the connection.
// It is what node.ListenConfig.Handlers has for ALPN.
//
// At most MaxStreams streams of the connection are answered at a time;
// one beyond them is closed at once, without an answer.
func (s *Store) ServeConn(conn node.Conn) {
	defer conn.Close()
	if !s.enter(conn) {
		return
	}
	defer s.leave(conn)

	var wg sync.WaitGroup
	places := make(chan struct{}, s.cfg.MaxStreams) // one for every stream served
	warned := false
	for {
		st, err := conn.AcceptStream(s.accepting)
		if err != nil {
			break
		}
		select {
		case places <- struct{}{}:
		default:
			if !warned {
				warned = true
				s.log.Warn("a peer opens more streams than the store answers at a time: closing those beyond", "peer", conn.RemoteID(), "most", s.cfg.MaxStreams)
			}
			st.Close()
			continue
		}
		s.streams.Add(1)
		wg.Go(func() {
			defer func() {
				s.streams.Add(-1)
				<-places
			}()
			s.serveStream(conn, st)
		})
	}
	// What is under way ends by itself, or with the grace of Stop.
	wg.Wait()
}

// enter notes a connection that is being served. It returns false when
// the store is being stopped: the connection is then not served.
func (s *Store) enter(conn node.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.conns[conn] = struct{}{}
	s.serving.Add(1)
	return true
}

func (s *Store) leave(conn node.Conn) {
	s.mu.Lock()
	delete(s.conns, conn)
	s.mu.Unlock()
	s.serving.Done()
}

// begin notes a request that is under way. It returns false when the
// store is being stopped: no request is begun then.
func (s *Store) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	s.under.Add(1)
	return true
}

// serveStream reads the request of a stream and answers it. The request
// has RequestWait to arrive, or the stream is closed.
func (s *Store) serveStream(conn node.Conn, st node.Stream) {
	defer st.Close()
	peer := conn.RemoteID()
	// A stream has no deadline: a timer closes it, which ends the Read.
	// So does the stop of the store: a request that has not come is not
	// under way.
	timer := time.AfterFunc(s.cfg.RequestWait, func() { st.Close() })
	unhook := context.AfterFunc(s.accepting, func() { st.Close() })
	var req jswire.Request
	err := jswire.ReadRequest(st, &req)
	late := !timer.Stop()
	if !unhook() {
		return
	}
	if late {
		s.log.Debug("no request within RequestWait", "peer", peer, "after", s.cfg.RequestWait)
		return
	}
	if err != nil {
		s.log.Debug("reading a request of the store", "peer", peer, "error", err)
		return
	}
	if !s.begin() {
		return
	}
	defer s.under.Done()

	resp := s.answer(peer, req)
	if err := jswire.WriteFrame(st, resp); err != nil {
		s.log.Debug("writing an answer of the store", "peer", peer, "operation", string(req.Op), "error", err)
		return
	}
	// The answer is the peer's to read. Closing a connection discards
	// what its streams have not delivered, and Stop closes the
	// connections when the last request has ended: so a request has not
	// ended before the peer has closed its stream, which jaccard-store's
	// client does when it has read the answer, or AnswerWait is over.
	wait := time.NewTimer(s.cfg.AnswerWait)
	defer wait.Stop()
	select {
	case <-st.Done():
	case <-wait.C:
	case <-s.requests.Done():
	}
}

// operation returns what Authorize is asked for an operation of
// jaccard-store's protocol, and false for one the store does not answer.
func operation(op jswire.Op) (authz.Operation, bool) {
	switch op {
	case jswire.OpPull:
		return authz.OpStorePull, true
	case jswire.OpPushStart, jswire.OpPushUpload, jswire.OpPushCommit, jswire.OpList, jswire.OpDelete:
		return authz.OpStoreWrite, true
	default:
		return "", false
	}
}

// answer authorizes a request and has the server answer it. A refusal is
// an answer of jaccard-store's protocol, which has no code for one: a
// bad_request whose message begins with "unauthorized".
func (s *Store) answer(peer wire.ID, req jswire.Request) jswire.Response {
	op, known := operation(req.Op)
	if !known {
		return jswire.Errorf(jswire.CodeBadRequest, "the store answers no such request")
	}
	if err := s.cfg.Authz.Authorize(peer, op, ""); err != nil {
		s.log.Warn("refused", "peer", peer, "operation", string(req.Op))
		return jswire.Errorf(jswire.CodeBadRequest, "unauthorized: %s is for operators", req.Op)
	}
	started := time.Now()
	resp := s.server.Handle(s.requests, string(peer), req)
	code := ""
	if resp.Error != nil {
		code = resp.Error.Code
	}
	s.log.Debug("a request of the store", "operation", string(req.Op), "peer", peer, "name", req.Name, "error", code, "took", time.Since(started).Round(time.Millisecond))
	return resp
}

// Admin returns jaccard-store's admin page over the store's database: the
// page, and under /api/ what it shows. It is read-only. It expects to be
// asked with the path it is mounted under stripped.
func (s *Store) Admin() http.Handler {
	return admin.Handler(s.db)
}

// Stop ends the answering: no new request is begun, the requests under way
// have StopGrace, and then every connection of the store is closed. It
// returns when that is done. It may be called more than once.
func (s *Store) Stop() {
	s.stop.Do(func() {
		defer close(s.stopped)
		s.mu.Lock()
		s.stopping = true
		s.mu.Unlock()
		// No stream is accepted any more, and a connection with nothing
		// under way is closed as its ServeConn returns.
		s.refuse()
		ended := make(chan struct{})
		go func() {
			s.under.Wait()
			close(ended)
		}()
		grace := time.NewTimer(s.cfg.StopGrace)
		defer grace.Stop()
		select {
		case <-ended:
		case <-grace.C:
			s.log.Warn("stopping the store with requests under way", "after", s.cfg.StopGrace)
		}
		// What is still under way ends with its context, and whoever
		// waits for a peer waits no longer.
		s.end()
		s.mu.Lock()
		conns := s.conns
		s.conns = make(map[node.Conn]struct{})
		s.mu.Unlock()
		for conn := range conns {
			conn.Close()
		}
		<-ended
		s.serving.Wait()
	})
	<-s.stopped
}

// Close stops the store (Stop), waits for the sweeper, and closes the
// database. It may be called more than once.
func (s *Store) Close() error {
	s.close.Do(func() {
		s.Stop()
		s.sweeping()
		<-s.swept
		if err := s.db.Close(); err != nil {
			s.closeErr = fmt.Errorf("storeserver: closing the database: %w", err)
		}
	})
	return s.closeErr
}
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./storeserver/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add storeserver/storeserver_test.go storeserver/storeserver.go
git commit -m 'Store: the store in draal: an operator pushes, anyone pulls, and what it accepts is bounded'
```

- [ ] **Step 6: A test that holds as it is written: a bucket that cannot be reached shows in the push, not when the store is opened**

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -20,6 +20,7 @@ import (
 	"github.com/amber-store/core/ingest"
 	"github.com/amber-store/core/key"
 	"github.com/amber-store/core/packstore"
+	"github.com/amber-store/jaccard-store/bucket"
 	"github.com/amber-store/jaccard-store/bucket/buckettest"
 	"github.com/amber-store/jaccard-store/client"
 	jsnode "github.com/amber-store/jaccard-store/node"
@@ -776,3 +777,53 @@ func TestTheStoreIsKeptInItsDirectory(t *testing.T) {
 		}
 	}
 }
+
+// The bucket is not asked anything when the store is opened: one that
+// cannot be reached shows when a client is sent to it, and the error is
+// the client's, with the address it could not reach.
+func TestABucketThatCannotBeReached(t *testing.T) {
+	// An address nothing listens on: that of a server that is closed.
+	closed := httptest.NewServer(http.NotFoundHandler())
+	addr := closed.Listener.Addr().String()
+	closed.Close()
+	// The credentials are those of the environment, as in draal: none of
+	// whoever runs the test.
+	absent := filepath.Join(t.TempDir(), "absent")
+	t.Setenv("AWS_ACCESS_KEY_ID", "test")
+	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
+	t.Setenv("AWS_SESSION_TOKEN", "")
+	t.Setenv("AWS_PROFILE", "")
+	t.Setenv("AWS_CONFIG_FILE", absent)
+	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", absent)
+
+	operator := newKey(t)
+	store, err := Open(context.Background(), Config{
+		Dir:    t.TempDir(),
+		Bucket: bucket.Config{Bucket: "packs", Endpoint: "http://" + addr, Region: "us-east-1", PathStyle: true},
+		Authz:  authz.NewAllowlist([]wire.ID{operator.ID()}),
+		Log:    quiet,
+	})
+	if err != nil {
+		t.Fatalf("Open with a bucket that cannot be reached: %v", err)
+	}
+	defer store.Close()
+	ctx, cancel := context.WithTimeout(context.Background(), patience)
+	defer cancel()
+	l, err := node.Listen(ctx, node.ListenConfig{Key: newKey(t), Local: true, Handlers: map[string]func(node.Conn){ALPN: store.ServeConn}, Log: quiet})
+	if err != nil {
+		t.Fatalf("Listen: %v", err)
+	}
+	defer l.Close()
+	go l.Accept(ctx)
+	w := &world{t: t, store: store, listener: l, operator: operator}
+
+	_, err = pushDir(t, w.dial(operator), storePath(t, "abc-web", "the program of web"), webRef)
+	if err == nil || !strings.Contains(err.Error(), addr) {
+		t.Fatalf("a push to a store whose bucket cannot be reached: %v, want an error that names %s", err, addr)
+	}
+	t.Logf("the push failed with: %v", err)
+	// Nothing points at what was not uploaded.
+	if _, err := pull(t, w.dial(newKey(t)), webRef); !errors.Is(err, client.ErrNotFound) {
+		t.Errorf("a pull of what could not be pushed: %v, want client.ErrNotFound", err)
+	}
+}
```

- [ ] **Step 7: Run it, and see it pass**

Run: `go test ./storeserver/ -run TestABucketThatCannotBeReached`

Expected: it passes: it holds what Open and jaccard-store's client do already.

- [ ] **Step 8: Commit**

```bash
git add storeserver/storeserver_test.go
git commit -m 'Store: a bucket that cannot be reached shows in the push, not when the store is opened'
```

- [ ] **Step 9: Write the failing test: Stop returns though a request does not end with its context**

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -66,6 +66,7 @@ type gate struct {
 
 	mu     sync.Mutex
 	hold   chan struct{} // while set and open, PresignGet waits for it, or for its context
+	deaf   bool          // PresignGet waits for hold alone, whatever its context says
 	putErr error         // while set, what PresignPut fails with
 	// waiting gets a value for every PresignGet that begins to wait.
 	waiting chan struct{}
@@ -97,10 +98,14 @@ func (g *gate) failUploads(err error) {
 
 func (g *gate) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
 	g.mu.Lock()
-	hold := g.hold
+	hold, deaf := g.hold, g.deaf
 	g.mu.Unlock()
 	if hold != nil {
 		g.waiting <- struct{}{}
+		if deaf {
+			<-hold
+			return "", errors.New("the bucket of the test was held")
+		}
 		select {
 		case <-hold:
 		case <-ctx.Done():
@@ -667,6 +672,54 @@ func TestStopEndsARequestThatOutlivesTheGrace(t *testing.T) {
 	}
 }
 
+// A request that does not end with its context, as a verification that
+// walks a large pack does not until it is through: Stop gives it the
+// grace a second time, says so, and returns. draal is not held by it.
+func TestStopReturnsThoughARequestDoesNotEnd(t *testing.T) {
+	w := start(t, func(cfg *Config) { cfg.StopGrace = 50 * time.Millisecond })
+	if _, err := pushDir(t, w.dial(w.operator), storePath(t, "abc-web", "the program of web"), webRef); err != nil {
+		t.Fatalf("the push: %v", err)
+	}
+	conn := w.dial(newKey(t))
+	w.bucket.mu.Lock()
+	w.bucket.deaf = true
+	w.bucket.mu.Unlock()
+	release := w.bucket.holdPulls()
+	// The request is let go before the store is closed, which the
+	// cleanup of start does.
+	t.Cleanup(func() {
+		release()
+		eventually(t, "the request that was held to end", func() bool { return w.store.streams.Load() == 0 })
+	})
+	go ask(t, conn, jswire.Request{Op: jswire.OpPull, Name: webRef})
+	select {
+	case <-w.bucket.waiting:
+	case <-time.After(patience):
+		t.Fatal("timed out waiting for the pull to be under way")
+	}
+	stopped := make(chan struct{})
+	began := time.Now()
+	go func() {
+		w.store.Stop()
+		close(stopped)
+	}()
+	select {
+	case <-stopped:
+	case <-time.After(5 * time.Second):
+		t.Fatal("Stop waits for a request that does not end with its context")
+	}
+	if took := time.Since(began); took < 100*time.Millisecond {
+		t.Errorf("Stop returned after %s: the request had no second grace", took)
+	}
+	if n := w.store.streams.Load(); n != 1 {
+		t.Errorf("%d streams are being served, want the one that was held", n)
+	}
+	if log := w.log.String(); !strings.Contains(log, "have not ended") {
+		t.Errorf("the store did not say that it stopped with a request that has not ended:\n%s", log)
+	}
+	gone(t, "the store is stopped", conn)
+}
+
 // A bucket that cannot do its part: the client is told which step
 // failed, and why is in the log of the store, as jaccard-store has it.
 func TestABucketThatFails(t *testing.T) {
```

- [ ] **Step 10: Run it, and see it fail**

Run: `go test -run TestStopReturnsThoughARequestDoesNotEnd ./storeserver/`

Expected: it fails, with

```text
storeserver_test.go:723: Stop waits for a request that does not end with its context
```

- [ ] **Step 11: Write the code: a request that does not end with its context has the grace twice, and no more**

Change `storeserver/storeserver.go`:

```diff
--- a/storeserver/storeserver.go
+++ b/storeserver/storeserver.go
@@ -380,6 +380,13 @@ func (s *Store) Admin() http.Handler {
 // Stop ends the answering: no new request is begun, the requests under way
 // have StopGrace, and then every connection of the store is closed. It
 // returns when that is done. It may be called more than once.
+//
+// A request ends with its context, which is done when the grace is over.
+// One that does not, as a verification in the middle of a large pack does
+// not until it is through, has the grace a second time and is then left
+// behind: Stop says so and returns, and what the request still asks of the
+// database after Close fails, which leaves nothing that the next start of
+// the store does not find.
 func (s *Store) Stop() {
 	s.stop.Do(func() {
 		defer close(s.stopped)
@@ -411,8 +418,19 @@ func (s *Store) Stop() {
 		for conn := range conns {
 			conn.Close()
 		}
-		<-ended
-		s.serving.Wait()
+		gone := make(chan struct{})
+		go func() {
+			<-ended
+			s.serving.Wait()
+			close(gone)
+		}()
+		last := time.NewTimer(s.cfg.StopGrace)
+		defer last.Stop()
+		select {
+		case <-gone:
+		case <-last.C:
+			s.log.Warn("the store is stopped, with requests that have not ended with their context", "streams", s.streams.Load(), "after", 2*s.cfg.StopGrace)
+		}
 	})
 	<-s.stopped
 }
```

- [ ] **Step 12: Run it, and see it pass**

Run: `go test -run TestStopReturnsThoughARequestDoesNotEnd ./storeserver/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 13: Commit**

```bash
git add storeserver/storeserver_test.go storeserver/storeserver.go
git commit -m 'Store: Stop returns though a request does not end with its context'
```

- [ ] **Step 14: A test that holds as it is written: the refusal of every operation of a stranger leaves a line of its own in the log**

A review found that the lines of three of the five were in the log before the test asked: its assertion could not fail for them.

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -403,6 +403,10 @@ func TestAStrangerMayNotWrite(t *testing.T) {
 	// Every operation of a push by itself, as a client of its own making
 	// would send them.
 	for _, op := range []jswire.Op{jswire.OpPushStart, jswire.OpPushUpload, jswire.OpPushCommit, jswire.OpList, jswire.OpDelete} {
+		// Three of the five were refused above already: what is held is
+		// that this one leaves a line of its own.
+		line := "refused peer=" + string(stranger.ID()) + " operation=" + string(op)
+		before := strings.Count(w.log.String(), line)
 		resp, err := ask(t, conn, jswire.Request{Op: op, Name: webRef, Root: root[:], UploadID: "1"})
 		if err != nil {
 			t.Errorf("%s of a stranger: %v, want a refusal", op, err)
@@ -412,8 +416,8 @@ func TestAStrangerMayNotWrite(t *testing.T) {
 		if resp.Error == nil || resp.Error.Code != jswire.CodeBadRequest || resp.Error.Message != want {
 			t.Errorf("%s of a stranger: %+v, want the refusal %q", op, resp.Error, want)
 		}
-		if !strings.Contains(w.log.String(), "refused peer="+string(stranger.ID())+" operation="+string(op)) {
-			t.Errorf("the refusal of %s is not logged with the peer and the operation:\n%s", op, w.log.String())
+		if logged := strings.Count(w.log.String(), line) - before; logged != 1 {
+			t.Errorf("the refusal of %s left %d lines with the peer and the operation, want one:\n%s", op, logged, w.log.String())
 		}
 	}
 
```

- [ ] **Step 15: Run it, and see it pass**

Run: `go test -run TestAStrangerMayNotWrite ./storeserver/`

Expected: it passes: the store logs every refusal; the test now counts the lines.

- [ ] **Step 16: Commit**

```bash
git add storeserver/storeserver_test.go
git commit -m 'Store: the refusal of every operation of a stranger leaves a line of its own in the log'
```

- [ ] **Step 17: Write the failing test: the scratch directory is one the store makes for itself, so that emptying it loses nothing**

A review found it: a scratch directory that was the directory of the store, or one above it, was emptied as it was, with the store's database and draal's key in it.

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -810,9 +810,9 @@ func TestOpenRefusesWhatCannotBeAStore(t *testing.T) {
 	}
 }
 
-// The database of the store is store.sqlite in its directory, and the
-// scratch space a directory beside it unless another is named: what a
-// store was opened with, it is opened with again.
+// The database of the store is store.sqlite in its directory, and its
+// scratch directory is made there too unless another directory is named
+// to make it in: what a store was opened with, it is opened with again.
 func TestTheStoreIsKeptInItsDirectory(t *testing.T) {
 	bucket := buckettest.New(t)
 	dir, scratch := t.TempDir(), filepath.Join(t.TempDir(), "elsewhere")
@@ -828,13 +828,69 @@ func TestTheStoreIsKeptInItsDirectory(t *testing.T) {
 			t.Fatalf("Close: %v", err)
 		}
 	}
-	for _, path := range []string{filepath.Join(dir, "store.sqlite"), filepath.Join(dir, "scratch"), scratch} {
+	for _, path := range []string{filepath.Join(dir, "store.sqlite"), filepath.Join(dir, "great-machine-scratch"), filepath.Join(scratch, "great-machine-scratch")} {
 		if _, err := os.Stat(path); err != nil {
 			t.Errorf("%s: %v", path, err)
 		}
 	}
 }
 
+// The store empties its scratch directory whenever it is opened. That
+// directory is one it makes for itself, by a name of its own, inside the
+// directory it was given. So nothing that was there is lost, whatever was
+// given: the directory of the store itself, where its database and
+// draal's key are, or a directory that holds other things.
+func TestTheScratchIsADirectoryOfTheStoresOwn(t *testing.T) {
+	bucket := buckettest.New(t)
+	for _, c := range []struct {
+		name    string
+		scratch func(dir string) string // what Config.Scratch is, for a store in dir
+		in      func(dir string) string // where the scratch directory then is
+	}{
+		{"none is named", func(string) string { return "" }, func(dir string) string { return dir }},
+		{"the directory of the store", func(dir string) string { return dir }, func(dir string) string { return dir }},
+		{"the directory above it", filepath.Dir, filepath.Dir},
+	} {
+		t.Run(c.name, func(t *testing.T) {
+			dir := filepath.Join(t.TempDir(), "data")
+			if err := os.MkdirAll(dir, 0o700); err != nil {
+				t.Fatal(err)
+			}
+			key, beside := filepath.Join(dir, "draal.key"), filepath.Join(filepath.Dir(dir), "something else")
+			own := filepath.Join(c.in(dir), "great-machine-scratch")
+			left := filepath.Join(own, "of a verification that was cut short")
+			if err := os.MkdirAll(own, 0o700); err != nil {
+				t.Fatal(err)
+			}
+			for _, path := range []string{key, beside, left} {
+				if err := os.WriteFile(path, []byte("kept"), 0o600); err != nil {
+					t.Fatal(err)
+				}
+			}
+			cfg := Config{Dir: dir, Scratch: c.scratch(dir), Objects: bucket, Authz: authz.NewAllowlist(nil), Log: quiet}
+			// Twice: the database of the first time is among what the
+			// second finds.
+			for range 2 {
+				s, err := Open(context.Background(), cfg)
+				if err != nil {
+					t.Fatalf("Open: %v", err)
+				}
+				if err := s.Close(); err != nil {
+					t.Fatalf("Close: %v", err)
+				}
+			}
+			for _, path := range []string{key, beside, filepath.Join(dir, "store.sqlite"), own} {
+				if _, err := os.Stat(path); err != nil {
+					t.Errorf("after the store was opened: %v", err)
+				}
+			}
+			if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
+				t.Errorf("what a run before left in the scratch directory is still there (%v): the directory was not emptied", err)
+			}
+		})
+	}
+}
+
 // The bucket is not asked anything when the store is opened: one that
 // cannot be reached shows when a client is sent to it, and the error is
 // the client's, with the address it could not reach.
```

- [ ] **Step 18: Run it, and see it fail**

Run: `go test -run 'TestTheScratchIsADirectoryOfTheStoresOwn|TestTheStoreIsKeptInItsDirectory' ./storeserver/`

Expected: it fails, with

```text
storeserver_test.go:898: after the store was opened: stat .../data/draal.key: no such file or directory
```

- [ ] **Step 19: Write the code: the store verifies in a directory of its own making, great-machine-scratch, inside the one it is given**

Change `storeserver/storeserver.go`:

```diff
--- a/storeserver/storeserver.go
+++ b/storeserver/storeserver.go
@@ -45,8 +45,14 @@ const ALPN = "amber/jaccard-store/1"
 
 // The store in its directory.
 const (
-	dbFile     = "store.sqlite"
-	scratchDir = "scratch"
+	dbFile = "store.sqlite"
+	// scratchDir is the directory the store makes for what it verifies,
+	// in the directory it is given for that. It is emptied whenever the
+	// store is opened, which is why it is the store's own, by a name
+	// nothing else has: what it is made in may be the directory of the
+	// store itself, with the database and draal's key in it, or any
+	// other, and loses nothing.
+	scratchDir = "great-machine-scratch"
 )
 
 // Config is what a Store is made of. The zero value of a number or of a
@@ -61,10 +67,11 @@ type Config struct {
 	// Objects is for tests: the bucket itself. When it is set, Bucket is
 	// not looked at.
 	Objects jsserver.Bucket
-	// Scratch is where packs are verified: the uncompressed data of each
-	// is written there, MaxPackBytes at most for each of VerifyJobs. It
-	// is the store's alone and is emptied when the store is opened. "":
-	// the directory scratch in Dir.
+	// Scratch is the directory in which packs are verified: the
+	// uncompressed data of each is written there, MaxPackBytes at most
+	// for each of VerifyJobs. The store makes a directory of its own in
+	// it, great-machine-scratch, and empties that one whenever it is
+	// opened: nothing else in Scratch is touched. "": Dir.
 	Scratch      string
 	VerifyJobs   int   // 0: 2 packs verified at once
 	MaxPackBytes int64 // 0: 16 GiB, the largest pack, uncompressed
@@ -85,7 +92,7 @@ type Config struct {
 // withDefaults returns cfg with every zero value replaced by its default.
 func (cfg Config) withDefaults() Config {
 	if cfg.Scratch == "" {
-		cfg.Scratch = filepath.Join(cfg.Dir, scratchDir)
+		cfg.Scratch = cfg.Dir
 	}
 	if cfg.RequestWait == 0 {
 		cfg.RequestWait = 5 * time.Second
@@ -181,7 +188,7 @@ func Open(ctx context.Context, cfg Config) (*Store, error) {
 	srv, err := jsserver.New(jsserver.Config{
 		DB:           database,
 		Bucket:       objects,
-		Scratch:      cfg.Scratch,
+		Scratch:      filepath.Join(cfg.Scratch, scratchDir),
 		VerifyJobs:   cfg.VerifyJobs,
 		MaxPackBytes: cfg.MaxPackBytes,
 		NoVerify:     cfg.NoVerify,
```

- [ ] **Step 20: Run it, and see it pass**

Run: `go test -run 'TestTheScratchIsADirectoryOfTheStoresOwn|TestTheStoreIsKeptInItsDirectory' ./storeserver/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 21: Commit**

```bash
git add storeserver/storeserver_test.go storeserver/storeserver.go
git commit -m 'Store: the scratch directory is one the store makes for itself, so that emptying it loses nothing'
```

- [ ] **Step 22: The whole**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 31 packages that have tests; and nothing from `git status`: every step of this task was committed, and nothing was built into the tree.
