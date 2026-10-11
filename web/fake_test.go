package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// quiet is a logger that writes nothing.
var quiet = slog.New(slog.DiscardHandler)

// zathrasA and zathrasB are endpoint IDs as they are written.
const (
	zathrasA = wire.ID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	zathrasB = wire.ID("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
)

// fake is a draal.Controller that answers with what a test put into its
// fields. A field that is nil answers unavailable. Store, Rollout and
// Rollback panic: the API calls them nowhere.
type fake struct {
	rollouts func(service string) ([]wire.RolloutInfo, error)
	history  func(zathras wire.ID) ([]wire.Sample, error)
	watch    func(ctx context.Context) (<-chan wire.Fleet, error)
	logs     func(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error)
	shell    func(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (draal.Shell, error)
}

// A fake is a Controller.
var _ draal.Controller = (*fake)(nil)

// unset is what a fake answers when the test gave it nothing to answer.
var unset = wire.Errorf(wire.CodeUnavailable, "the fake has no answer")

func (f *fake) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
	panic("the API asked for the store")
}

func (f *fake) Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	panic("the API rolled out")
}

func (f *fake) Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	panic("the API rolled back")
}

func (f *fake) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	if f.watch == nil {
		return nil, unset
	}
	return f.watch(ctx)
}

func (f *fake) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	if f.rollouts == nil {
		return nil, unset
	}
	return f.rollouts(service)
}

func (f *fake) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	if f.history == nil {
		return nil, unset
	}
	return f.history(zathras)
}

func (f *fake) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	if f.logs == nil {
		return nil, unset
	}
	return f.logs(ctx, zathras, follow)
}

func (f *fake) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (draal.Shell, error) {
	if f.shell == nil {
		return nil, unset
	}
	return f.shell(ctx, zathras, req)
}

func (f *fake) ServiceOf(zathras wire.ID) (string, bool) {
	return "web", true
}

// testDist is a build of the page, as small as one can be.
func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><title>the page</title>")},
		"favicon.svg":            {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"assets/index-1a2b.js":   {Data: []byte("console.log('page')")},
		"assets/index-3c4d.css":  {Data: []byte("body{margin:0}")},
		"assets/mono-5e6f.woff2": {Data: []byte("wOF2")},
		"notes.md":               {Data: []byte("# notes")},
	}
}

// serve starts a server of the handler of cfg, which is given the test's
// build of the page, a quiet logger and a fake that answers nothing where
// it has none. The server is closed when the test ends.
func serve(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	return serveUnder(t, context.Background(), cfg)
}

// serveUnder is serve with the context every request of the server has:
// a server that ends cancels it.
func serveUnder(t *testing.T, base context.Context, cfg Config) *httptest.Server {
	t.Helper()
	if cfg.Dist == nil {
		cfg.Dist = testDist()
	}
	if cfg.Log == nil {
		cfg.Log = quiet
	}
	if cfg.Controller == nil {
		cfg.Controller = &fake{}
	}
	return serveHandler(t, base, New(cfg))
}

// serveHandler serves a handler, for a test that wraps the one of New
// or changes it first.
func serveHandler(t *testing.T, base context.Context, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.BaseContext = func(net.Listener) context.Context { return base }
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// answer is what a request was answered with.
type answer struct {
	status int
	header http.Header
	body   string
}

// ask sends a request and returns the answer. host, if not empty, is the
// Host the request carries in place of the server's address. Redirects
// are not followed.
func ask(t *testing.T, srv *httptest.Server, method, path, host string) answer {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	if host != "" {
		req.Host = host
	}
	client := &http.Client{
		Timeout:       deadline,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the answer: %v", method, path, err)
	}
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// wantRefusal fails the test unless a is a refusal as JSON with the
// status and the code, and returns its message.
func wantRefusal(t *testing.T, a answer, status int, code wire.Code) string {
	t.Helper()
	var got refusal
	if err := jsonOf(a, &got); err != nil || got.Error == nil {
		t.Fatalf("the answer is %d %q, want a refusal as JSON (%v)", a.status, a.body, err)
	}
	if a.status != status || got.Error.Code != code {
		t.Fatalf("the answer is %d with the code %q (%s), want %d with %q", a.status, got.Error.Code, got.Error.Message, status, code)
	}
	return got.Error.Message
}

// jsonOf decodes the body of an answer, which has to say that it is JSON
// and to have no field that v has not.
func jsonOf(a answer, v any) error {
	if ct := a.header.Get("Content-Type"); ct != "application/json" {
		return fmt.Errorf("the content type is %q", ct)
	}
	dec := json.NewDecoder(strings.NewReader(a.body))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// rawGet writes a GET with the Host given, exactly as it is, on a
// connection of its own, and returns the status and the body. It returns
// false when nothing that is an answer came back. host "" sends an
// HTTP/1.0 request without a Host.
func rawGet(t *testing.T, addr, path, host string) (int, string, bool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, deadline)
	if err != nil {
		t.Fatalf("dialing %s: %v", addr, err)
	}
	defer conn.Close()
	request := "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	if host == "" {
		request = "GET " + path + " HTTP/1.0\r\n\r\n"
	}
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("writing the request: %v", err)
	}
	return readAnswer(conn)
}

// readAnswer reads one answer from a connection, to its end.
func readAnswer(conn net.Conn) (int, string, bool) {
	conn.SetReadDeadline(time.Now().Add(deadline))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), true
}
