### Task 1: web, the handler without sockets

**Files:**
- Create: `web/web.go`, `web/loopback.go`, `web/page.go`, `web/api.go`
- Create: `web/dist/.gitkeep` (empty; the placeholder that keeps the embedded directory there)
- Modify: `.gitignore` (the build of the page is ignored but for the placeholder)
- Test: `web/fake_test.go`, `web/page_test.go`, `web/loopback_test.go`, `web/api_test.go`

**Interfaces:**
- Consumes (code that exists):
  - `draal.Controller` (`draal/draal.go`): `Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error)` and `History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error)`. `Store`, `Rollout` and `Rollback` are called nowhere.
  - `wire.ValidService(name string) error`, `wire.Errorf(code wire.Code, format string, args ...any) *wire.Error`, `wire.Error{Code, Message}` with its `json` tags, and the codes `wire.CodeBadRequest`, `CodeUnauthorized`, `CodeNotFound`, `CodeUnavailable`, `CodeInternal`.
- Produces:
  - `web.Config`, `web.New(cfg Config) http.Handler`, `web.Built() bool`, `web.IsLoopback(addr string) bool`, exactly as the plan's Interfaces have them.
  - `GET /api/services/{service}/rollouts` answers `{"rollouts": [...]}` and `GET /api/zathras/{id}/history` answers `{"samples": [...]}`; a list that is empty is `[]`, never `null`. Both carry `Cache-Control: no-store`.
  - A refusal is `{"error": {"code": "...", "message": "..."}}` with the status 400 (`bad-request`), 403 (`unauthorized`), 404 (`not-found`), 503 (`unavailable`), 500 (`internal`), and 405 with the code `bad-request` and `Allow: GET` for a method that is not `GET`. An `internal` refusal, whoever made it (an error of the controller that is no `*wire.Error`, and a `*wire.Error` with the code `internal`), has the fixed message `draal failed to answer; its log says why`; its text is logged and not sent.
  - An endpoint ID in a path is 64 characters of `0-9a-f`. Capitals are refused with 400 and not folded: `wire.ID` is lower case, and the page has its IDs from draal.
  - Every answer has `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` and `Referrer-Policy: no-referrer`. `'unsafe-inline'` is in `style-src` alone, because xterm.js makes `<style>` elements from script (four places in `@xterm/xterm` 6.0.0) and sets `style` attributes, and has no nonce: without it Chrome reports eight `style-src-elem` violations when a terminal is opened. `connect-src 'self'` covers the page's WebSockets (`ws:` from a page over `http:`, `wss:` from one over `https:`).
  - The page and the API are answered under every host name, on every address: nothing looks at the `Host` of a request to decide whether to answer it (Review Focus 1: the page reached under another name, through a tunnel, a forward or a proxy that keeps the outside host name, works as under localhost). `Config` has no `Loopback` and no `Hosts`. `web.IsLoopback` is in `web/loopback.go`; the handler does not use it, `cli` does for its warning at start.
  - A file of the page is sent by `http.ServeContent` from the file as the `fs.FS` gives it (nothing is copied when it can be sought, as an embedded file can); an extension that is not in the handler's table is `application/octet-stream`.
  - Every answer has 2 minutes to be written (`http.ResponseController.SetWriteDeadline`; the unexported field `handler.answerWait`, which a test makes short). Task 2 takes the deadline off a connection that becomes a WebSocket.
  - `Config.MaxSockets` is 256 when zero; the handler's count of open sockets (`handler.sockets`) is Task 2's to keep.
  - Not built (no `index.html` in the build): every path outside `/api/` is 501 with one plain line.
  - A path under `/assets/` that is no file of the build (`/assets` and `/assets/` themselves too) is 404 with one plain line, and never `index.html`: a page that is open since before a new build asks for scripts whose names are gone. Every other path that is no file and is not under `/api/` gets `index.html`.
  - For the tests of this package (later tasks use them): `fake` (a `draal.Controller` with the fields `rollouts`, `history`, `watch`, `logs`, `shell`), `serve(t, cfg)`, `serveUnder(t, base, cfg)`, `serveHandler(t, base, handler)`, `ask(t, srv, method, path, host) answer`, `wantRefusal(t, a, status, code) string`, `testDist()`, the IDs `zathrasA` and `zathrasB`, `deadline` and `quiet`.

The mux of Go 1.26 answers a path with `..` or `//` in it with a redirect (307) to the clean path before any handler sees it; the handler cleans the path again, and an `fs.FS` refuses a name that would leave it.

- [ ] **Step 1: The placeholder of the build, and what git ignores**

```bash
mkdir -p web/dist
touch web/dist/.gitkeep
```

Add to the end of `.gitignore`:

```gitignore

# the build of the page, which web embeds; the placeholder keeps the
# directory there for go:embed
/web/dist/*
!/web/dist/.gitkeep
```

- [ ] **Step 2: Write the failing tests**

The fake controller and the helpers every test of the package uses, `web/fake_test.go`:

```go
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

func (f *fake) Store(ctx context.Context) (wire.StoreInfo, error) {
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
```

The page (`TestPageIsAnsweredUnderEveryHostName` is the test of Review Focus 1 for this task), `web/page_test.go`:

```go
package web

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestPageServesItsFilesAndIndexForEveryOtherPath(t *testing.T) {
	srv := serve(t, Config{})
	const index = "<!doctype html><title>the page</title>"
	tests := []struct {
		path, body, contentType, cache string
	}{
		{"/", index, "text/html; charset=utf-8", "no-cache"},
		{"/index.html", index, "text/html; charset=utf-8", "no-cache"},
		// The page routes by path: a refresh on a view loads the page.
		{"/s/web", index, "text/html; charset=utf-8", "no-cache"},
		{"/s/web/z/" + string(zathrasA), index, "text/html; charset=utf-8", "no-cache"},
		{"/favicon.svg", "<svg xmlns='http://www.w3.org/2000/svg'/>", "image/svg+xml", "no-cache"},
		{"/assets/index-1a2b.js", "console.log('page')", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/index-3c4d.css", "body{margin:0}", "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/mono-5e6f.woff2", "wOF2", "font/woff2", "public, max-age=31536000, immutable"},
		// An extension the page has no type for is sent as bytes, whatever
		// the machine's tables make of it.
		{"/notes.md", "# notes", "application/octet-stream", "no-cache"},
		// Only assets/ is the place of the files: a name that begins like
		// it is a path of the page.
		{"/assetsx", index, "text/html; charset=utf-8", "no-cache"},
		{"/s/assets/index-1a2b.js", index, "text/html; charset=utf-8", "no-cache"},
	}
	for _, tt := range tests {
		a := ask(t, srv, http.MethodGet, tt.path, "")
		if a.status != http.StatusOK || a.body != tt.body {
			t.Errorf("GET %s = %d %q, want 200 %q", tt.path, a.status, a.body, tt.body)
		}
		if got := a.header.Get("Content-Type"); got != tt.contentType {
			t.Errorf("GET %s has the content type %q, want %q", tt.path, got, tt.contentType)
		}
		if got := a.header.Get("Cache-Control"); got != tt.cache {
			t.Errorf("GET %s has Cache-Control %q, want %q", tt.path, got, tt.cache)
		}
	}
}

// Review Focus 1: the page reached under another name, through a tunnel,
// a forward or a proxy that keeps the outside host name. It works as
// under localhost: nothing looks at the Host of a request to decide
// whether to answer it.
func TestPageIsAnsweredUnderEveryHostName(t *testing.T) {
	srv := serve(t, Config{Controller: &fake{rollouts: func(string) ([]wire.RolloutInfo, error) { return nil, nil }}})
	for _, host := range []string{
		"localhost:8080", "127.0.0.1", "[::1]:8080",
		"192.168.1.5:8080", "10.0.0.7",
		"draal.example", "DRAAL.Example.:8443", "tunnel-3f9a.trycloudflare.example",
		"localhost.example.com", "attacker.example:8080",
	} {
		for path, want := range map[string]string{
			"/":                          "<!doctype html><title>the page</title>",
			"/s/web":                     "<!doctype html><title>the page</title>",
			"/assets/index-1a2b.js":      "console.log('page')",
			"/api/services/web/rollouts": `{"rollouts":[]}` + "\n",
		} {
			if a := ask(t, srv, http.MethodGet, path, host); a.status != http.StatusOK || a.body != want {
				t.Errorf("GET %s as %s = %d %q, want 200 %q", path, host, a.status, a.body, want)
			}
		}
	}
}

// A page that was loaded before a new build asks for scripts whose names
// are gone. They are not there, and the answer says so: index.html with
// 200 for a script is refused by the browser with an error that explains
// nothing. A directory is no file either, so nothing is ever listed.
func TestPathUnderAssetsThatIsNoFileIsNotFound(t *testing.T) {
	srv := serve(t, Config{})
	for _, path := range []string{"/assets/gone-0000.js", "/assets/index-1a2b.js.map", "/assets", "/assets/", "/assets/sub/x.css"} {
		a := ask(t, srv, http.MethodGet, path, "")
		if a.status != http.StatusNotFound || !strings.HasPrefix(a.header.Get("Content-Type"), "text/plain") {
			t.Errorf("GET %s = %d as %q, want 404 as plain text", path, a.status, a.header.Get("Content-Type"))
		}
		if strings.Contains(a.body, "<!doctype") || !strings.Contains(a.body, "great-machine") || strings.Count(strings.TrimSpace(a.body), "\n") != 0 {
			t.Errorf("GET %s = %q, want one plain line and not the page", path, a.body)
		}
		if a.header.Get("Cache-Control") != "" {
			t.Errorf("GET %s has Cache-Control %q, want none: what is not there is not kept", path, a.header.Get("Cache-Control"))
		}
	}
}

// A file is sent as the file it is: a part of it when a part is asked
// for, from a build whose files can be sought and from one whose files
// cannot.
func TestPartOfAFileOfThePage(t *testing.T) {
	for name, dist := range map[string]fs.FS{"files that can be sought": testDist(), "files that cannot": unsought{testDist()}} {
		srv := serve(t, Config{Dist: dist})
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/assets/index-1a2b.js", nil)
		req.Header.Set("Range", "bytes=8-10")
		client := &http.Client{Timeout: deadline}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		client.CloseIdleConnections()
		if resp.StatusCode != http.StatusPartialContent || string(body) != "log" {
			t.Errorf("%s: bytes 8 to 10 = %d %q, want 206 and log", name, resp.StatusCode, body)
		}
		if a := ask(t, srv, http.MethodGet, "/", ""); a.status != http.StatusOK || a.body != "<!doctype html><title>the page</title>" {
			t.Errorf("%s: GET / = %d %q", name, a.status, a.body)
		}
	}
}

// unsought is an fs.FS whose files have Read, Stat and Close and no more.
type unsought struct{ fs.FS }

func (u unsought) Open(name string) (fs.File, error) {
	f, err := u.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return struct{ fs.File }{f}, nil
}

// An answer that is no WebSocket has a time to be written in. A client
// that asks for a file and reads nothing does not hold its handler, and
// with it a connection and the file, for as long as it likes.
func TestAnswerThatIsNotTakenLetsGoOfItsHandler(t *testing.T) {
	dist := testDist()
	dist["assets/big-0000.js"] = &fstest.MapFile{Data: make([]byte, 64<<20)}
	inner := New(Config{Controller: &fake{}, Dist: dist, Log: quiet})
	inner.(*handler).answerWait = 200 * time.Millisecond
	returned := make(chan struct{})
	srv := serveHandler(t, context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		close(returned)
	}))
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /assets/big-0000.js HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-returned:
	case <-time.After(deadline):
		t.Fatal("the handler has not returned from an answer nobody reads")
	}
}

func TestPageIsReadWithGetAndHeadAlone(t *testing.T) {
	srv := serve(t, Config{})
	if a := ask(t, srv, http.MethodHead, "/", ""); a.status != http.StatusOK || a.body != "" || a.header.Get("Content-Length") == "0" {
		t.Errorf("HEAD / = %d %q with the length %q, want 200 without a body and the length of the page", a.status, a.body, a.header.Get("Content-Length"))
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		a := ask(t, srv, method, "/", "")
		if a.status != http.StatusMethodNotAllowed || a.header.Get("Allow") != "GET, HEAD" {
			t.Errorf("%s / = %d with Allow %q, want 405 with GET, HEAD", method, a.status, a.header.Get("Allow"))
		}
	}
}

func TestPageCannotBeLeftThroughThePath(t *testing.T) {
	// A build in a directory of the machine, with a file beside it that
	// is not of the build.
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.Mkdir(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("the page"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("the secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(Config{Controller: &fake{}, Dist: os.DirFS(dist), Log: quiet})
	// The paths are given to the handler as they are: a client would
	// clean them before it sent them.
	for _, path := range []string{"/../secret.txt", "/a/../../secret.txt", "/..%2fsecret.txt", "/%2e%2e/secret.txt", "//secret.txt/../../secret.txt"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "the secret") {
			t.Errorf("GET %s = %d with the file beside the build", path, rec.Code)
		}
		// Whatever the path, the answer is the page, or the mux's
		// redirect to the path without its dots.
		redirect := rec.Code >= 300 && rec.Code < 400 && !strings.Contains(rec.Header().Get("Location"), "..")
		if rec.Code != http.StatusOK && !redirect {
			t.Errorf("GET %s = %d to %q, want the page or a redirect to a clean path", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestPageThatWasNotBuilt(t *testing.T) {
	srv := serve(t, Config{Dist: fstest.MapFS{".gitkeep": {}}})
	for _, path := range []string{"/", "/index.html", "/s/web", "/assets/index-1a2b.js"} {
		a := ask(t, srv, http.MethodGet, path, "")
		if a.status != http.StatusNotImplemented {
			t.Errorf("GET %s = %d, want 501", path, a.status)
		}
		if !strings.HasPrefix(a.header.Get("Content-Type"), "text/plain") {
			t.Errorf("GET %s has the content type %q, want plain text", path, a.header.Get("Content-Type"))
		}
		for _, want := range []string{"was not built into this program", "nix build", "npm run build", "ui/"} {
			if !strings.Contains(a.body, want) {
				t.Errorf("GET %s = %q, want %q in it", path, a.body, want)
			}
		}
		if strings.Count(strings.TrimSpace(a.body), "\n") != 0 {
			t.Errorf("GET %s = %q, want one line", path, a.body)
		}
	}
	// The API is there without the page.
	wantRefusal(t, ask(t, srv, http.MethodGet, "/api/nothing", ""), http.StatusNotFound, "not-found")
}

func TestBuiltSaysWhatTheEmbeddedBuildAnswers(t *testing.T) {
	// The tests pass with a build of the page in web/dist and without.
	srv := httptest.NewServer(New(Config{Controller: &fake{}, Log: quiet}))
	defer srv.Close()
	want := http.StatusNotImplemented
	if Built() {
		want = http.StatusOK
	}
	if a := ask(t, srv, http.MethodGet, "/", ""); a.status != want {
		t.Errorf("Built() = %v, and GET / = %d, want %d", Built(), a.status, want)
	}
}

func TestEveryAnswerHasTheSecurityHeaders(t *testing.T) {
	built := serve(t, Config{})
	unbuilt := serve(t, Config{Dist: fstest.MapFS{}})
	tests := []struct {
		name   string
		a      answer
		status int
	}{
		{"the page", ask(t, built, http.MethodGet, "/", ""), http.StatusOK},
		{"a file of the page", ask(t, built, http.MethodGet, "/assets/index-1a2b.js", ""), http.StatusOK},
		{"a method the page has not", ask(t, built, http.MethodPost, "/", ""), http.StatusMethodNotAllowed},
		{"a path the API has not", ask(t, built, http.MethodGet, "/api/nothing", ""), http.StatusNotFound},
		{"a refusal of the API", ask(t, built, http.MethodGet, "/api/zathras/x/history", ""), http.StatusBadRequest},
		{"a page that was not built", ask(t, unbuilt, http.MethodGet, "/", ""), http.StatusNotImplemented},
	}
	const policy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; " +
		"object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	for _, tt := range tests {
		if tt.a.status != tt.status {
			t.Errorf("%s: the status is %d, want %d", tt.name, tt.a.status, tt.status)
		}
		for header, want := range map[string]string{
			"Content-Security-Policy": policy,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := tt.a.header.Values(header); len(got) != 1 || got[0] != want {
				t.Errorf("%s: %s is %q, want %q once", tt.name, header, got, want)
			}
		}
	}
}
```

`IsLoopback`, `web/loopback_test.go`:

```go
package web

import "testing"

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.1:0", true},
		{"127.9.9.9:8080", true},
		{"localhost:8080", true},
		{"LOCALHOST:8080", true},
		{"[::1]:8080", true},
		{":8080", false}, // every address
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.168.1.5:8080", false},
		{"example.com:80", false},
		{"localhost.example.com:80", false},
		{"127.0.0.1", false}, // no port: not as --web-addr has it
		{"localhost", false},
		{"::1", false},
		{"", false},
		{"garbage", false},
		{"a:b:c", false},
		{"[::1%lo0]:8080", false},
	}
	for _, tt := range tests {
		if got := IsLoopback(tt.addr); got != tt.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
```

The JSON of the API, `web/api_test.go`:

```go
package web

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestRollouts(t *testing.T) {
	created := time.Date(2026, 10, 9, 12, 0, 0, 123456789, time.UTC)
	var asked []string
	c := &fake{rollouts: func(service string) ([]wire.RolloutInfo, error) {
		asked = append(asked, service)
		return []wire.RolloutInfo{
			{ID: 7, Service: "web", Generation: 4, Parallel: 1, State: wire.RolloutActivating, Created: created, CreatedBy: zathrasB},
			{ID: 6, Service: "web", Generation: 3, State: wire.RolloutHalted, Reason: "web-0: exited with status 3 after 10ms", Created: created.Add(-time.Hour), CreatedBy: zathrasB, Finished: created.Add(-time.Minute)},
		}, nil
	}}
	srv := serve(t, Config{Controller: c})
	a := ask(t, srv, http.MethodGet, "/api/services/web/rollouts", "")
	if a.status != http.StatusOK || a.header.Get("Content-Type") != "application/json" || a.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the answer is %d as %q with Cache-Control %q", a.status, a.header.Get("Content-Type"), a.header.Get("Cache-Control"))
	}
	// The JSON is the contract of the page: it is compared as text.
	const want = `{"rollouts":[` +
		`{"id":7,"service":"web","generation":4,"parallel":1,"state":"activating","created":"2026-10-09T12:00:00.123456789Z",` +
		`"created_by":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","finished":"0001-01-01T00:00:00Z","prepared":0,"activated":0,"awaited":0},` +
		`{"id":6,"service":"web","generation":3,"parallel":0,"state":"halted","reason":"web-0: exited with status 3 after 10ms","created":"2026-10-09T11:00:00.123456789Z",` +
		`"created_by":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","finished":"2026-10-09T11:59:00.123456789Z","prepared":0,"activated":0,"awaited":0}` +
		`]}` + "\n"
	if a.body != want {
		t.Errorf("the answer is\n%s\nwant\n%s", a.body, want)
	}
	if len(asked) != 1 || asked[0] != "web" {
		t.Errorf("the controller was asked for %q, want web once", asked)
	}
}

func TestRolloutsOfAServiceWithoutOneIsAnEmptyList(t *testing.T) {
	srv := serve(t, Config{Controller: &fake{rollouts: func(string) ([]wire.RolloutInfo, error) { return nil, nil }}})
	if a := ask(t, srv, http.MethodGet, "/api/services/new/rollouts", ""); a.status != http.StatusOK || a.body != `{"rollouts":[]}`+"\n" {
		t.Errorf("the answer is %d %q, want an empty list and not null", a.status, a.body)
	}
}

func TestRolloutsOfWhatIsNoNameOfAService(t *testing.T) {
	c := &fake{rollouts: func(service string) ([]wire.RolloutInfo, error) {
		t.Errorf("the controller was asked for %q", service)
		return nil, nil
	}}
	srv := serve(t, Config{Controller: c})
	for _, name := range []string{"Web", "-web", "web-", "we_b", "a%2Fb", "%20", strings.Repeat("a", 64)} {
		a := ask(t, srv, http.MethodGet, "/api/services/"+name+"/rollouts", "")
		wantRefusal(t, a, http.StatusBadRequest, wire.CodeBadRequest)
	}
}

func TestHistory(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var asked []wire.ID
	c := &fake{history: func(id wire.ID) ([]wire.Sample, error) {
		asked = append(asked, id)
		return []wire.Sample{
			{At: at, CPU: 0.25, Memory: 1 << 20, MemoryLimit: 1 << 30, DiskRead: 1, DiskWrite: 2.5, NetRx: 3, NetTx: 4},
			{At: at.Add(2 * time.Second), CPU: 1.5, Memory: 2 << 20},
		}, nil
	}}
	srv := serve(t, Config{Controller: c})
	a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", "")
	const want = `{"samples":[` +
		`{"at":"2026-10-09T12:00:00Z","cpu":0.25,"memory":1048576,"memory_limit":1073741824,"disk_read":1,"disk_write":2.5,"net_rx":3,"net_tx":4},` +
		`{"at":"2026-10-09T12:00:02Z","cpu":1.5,"memory":2097152,"memory_limit":0,"disk_read":0,"disk_write":0,"net_rx":0,"net_tx":0}` +
		`]}` + "\n"
	if a.status != http.StatusOK || a.body != want {
		t.Errorf("the answer is %d\n%s\nwant 200\n%s", a.status, a.body, want)
	}
	if len(asked) != 1 || asked[0] != zathrasA {
		t.Errorf("the controller was asked for %q, want %s once", asked, zathrasA)
	}
}

func TestHistoryOfAZathrasWithoutSamplesIsAnEmptyList(t *testing.T) {
	srv := serve(t, Config{Controller: &fake{history: func(wire.ID) ([]wire.Sample, error) { return nil, nil }}})
	if a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", ""); a.status != http.StatusOK || a.body != `{"samples":[]}`+"\n" {
		t.Errorf("the answer is %d %q, want an empty list and not null", a.status, a.body)
	}
}

func TestWhatIsNoEndpointIDIsABadRequest(t *testing.T) {
	c := &fake{history: func(id wire.ID) ([]wire.Sample, error) {
		t.Errorf("the controller was asked for %q", id)
		return nil, nil
	}}
	srv := serve(t, Config{Controller: c})
	for _, id := range []string{
		"x",
		"web-0",
		string(zathrasA)[:63],
		string(zathrasA) + "a",
		strings.ToUpper(string(zathrasB)), // an ID is in lower case
		string(zathrasA)[:63] + "g",
		string(zathrasA)[:63] + "%20",
	} {
		a := ask(t, srv, http.MethodGet, "/api/zathras/"+id+"/history", "")
		wantRefusal(t, a, http.StatusBadRequest, wire.CodeBadRequest)
	}
}

func TestErrorsOfTheControllerAsRefusals(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   wire.Code
	}{
		{wire.Errorf(wire.CodeBadRequest, "bad"), http.StatusBadRequest, wire.CodeBadRequest},
		{wire.Errorf(wire.CodeNotFound, "none"), http.StatusNotFound, wire.CodeNotFound},
		{wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", zathrasA), http.StatusServiceUnavailable, wire.CodeUnavailable},
		{wire.Errorf(wire.CodeUnauthorized, "no"), http.StatusForbidden, wire.CodeUnauthorized},
		// A *wire.Error keeps its code and its message under what wraps it.
		{errors.Join(errors.New("around"), wire.Errorf(wire.CodeUnavailable, "inside")), http.StatusServiceUnavailable, wire.CodeUnavailable},
	}
	for _, tt := range tests {
		srv := serve(t, Config{Controller: &fake{history: func(wire.ID) ([]wire.Sample, error) { return nil, tt.err }}})
		a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", "")
		message := wantRefusal(t, a, tt.status, tt.code)
		var e *wire.Error
		errors.As(tt.err, &e)
		if message != e.Message {
			t.Errorf("%v: the message is %q, want %q", tt.err, message, e.Message)
		}
	}
}

// An internal refusal says nothing of what went wrong, whoever made it:
// an error that is no refusal, and a refusal with the code internal, as
// draal makes of what its database said and as a zathras can answer
// with any text. The text is in the log.
func TestAnInternalErrorSaysNothingAndItsTextStaysInTheLog(t *testing.T) {
	var logged bytes.Buffer
	c := &fake{
		history: func(wire.ID) ([]wire.Sample, error) {
			return nil, errors.New("open /var/lib/draal/secret: permission denied")
		},
		rollouts: func(service string) ([]wire.RolloutInfo, error) {
			return nil, wire.Errorf(wire.CodeInternal, "reading the rollouts of %s: open /var/lib/draal/secret: permission denied", service)
		},
	}
	srv := serve(t, Config{Controller: c, Log: slog.New(slog.NewTextHandler(&logged, nil))})
	for _, path := range []string{"/api/zathras/" + string(zathrasA) + "/history", "/api/services/web/rollouts"} {
		logged.Reset()
		a := ask(t, srv, http.MethodGet, path, "")
		if msg := wantRefusal(t, a, http.StatusInternalServerError, wire.CodeInternal); msg != "draal failed to answer; its log says why" {
			t.Errorf("GET %s: the message is %q", path, msg)
		}
		if strings.Contains(a.body, "secret") || strings.Contains(a.body, "permission") {
			t.Errorf("GET %s = %q, want nothing of the error's text in it", path, a.body)
		}
		if !strings.Contains(logged.String(), "permission denied") || !strings.Contains(logged.String(), "level=ERROR") {
			t.Errorf("GET %s: the log is %q, want the error in it", path, logged.String())
		}
	}
}

func TestPathsTheAPIHasNotAndMethodsItHasNot(t *testing.T) {
	srv := serve(t, Config{})
	for _, path := range []string{"/api/", "/api/nothing", "/api/services", "/api/services/web", "/api/services/web/rollouts/7", "/api/zathras/" + string(zathrasA), "/api/index.html"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			wantRefusal(t, ask(t, srv, method, path, ""), http.StatusNotFound, wire.CodeNotFound)
		}
	}
	for _, path := range []string{"/api/services/web/rollouts", "/api/zathras/" + string(zathrasA) + "/history"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			a := ask(t, srv, method, path, "")
			wantRefusal(t, a, http.StatusMethodNotAllowed, wire.CodeBadRequest)
			if a.header.Get("Allow") != "GET" {
				t.Errorf("%s %s has Allow %q, want GET", method, path, a.header.Get("Allow"))
			}
		}
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./web/`
Expected: FAIL: the package does not build, with `undefined: Config` (and `undefined: New`, `undefined: IsLoopback`, `undefined: handler`, `undefined: refusal`).

- [ ] **Step 4: Write the implementation**

The handler, its configuration and the headers of every answer, `web/web.go`:

```go
// Package web is the HTTP API in front of a draal.Controller, and the web
// page that is built into the program. It is the second way in beside the
// iroh protocol, and can show what that one can show; it can roll nothing
// out and nothing back.
//
// The page has no authentication, and the handler answers under every
// host name, on every address: whoever can reach its port has a shell in
// every container. What there is: draal listens on loopback unless it is
// told otherwise, and a WebSocket is refused to a page that was not
// loaded from the host it opens the socket on, which keeps a page of
// another site in the same browser from opening one directly. That does
// not hold against a site that points a name of its own at draal's
// address (DNS rebinding): its page is then of the origin it asks. This
// is known and was decided so; the spec's section 4 records it.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
)

// embedded is the build of the page, as it was in web/dist when the
// program was built. Without a build, the placeholder git tracks is all
// there is in it.
//
//go:embed all:dist
var embedded embed.FS

// contentSecurityPolicy lets the page ask its own origin alone.
//
//   - connect-src 'self' covers the WebSockets of the page: 'self' matches
//     ws: on a page that came over http:, and wss: on one over https:.
//   - style-src has 'unsafe-inline' because xterm.js makes <style> elements
//     from script and has no other way; a style can run nothing.
//   - Scripts are files of the build alone: no inline script, no eval.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'"

// Config configures the handler.
type Config struct {
	// Controller is what the API answers from: a *draal.Draal.
	Controller draal.Controller
	// Dist is the build of the page. nil: the one that is embedded.
	Dist fs.FS
	// Each of these has a default when it is zero:
	PingEvery time.Duration // how often an idle socket is pinged; 30 seconds
	WriteWait time.Duration // how long one message to a browser may take; 10 seconds
	// MaxSockets is how many WebSockets are open at a time; 256.
	MaxSockets int
	Log        *slog.Logger
}

// withDefaults returns cfg with every zero value replaced by its default.
func (cfg Config) withDefaults() Config {
	if cfg.Dist == nil {
		// The directory is in the program, so Sub cannot fail.
		cfg.Dist, _ = fs.Sub(embedded, "dist")
	}
	if cfg.PingEvery == 0 {
		cfg.PingEvery = 30 * time.Second
	}
	if cfg.WriteWait == 0 {
		cfg.WriteWait = 10 * time.Second
	}
	if cfg.MaxSockets == 0 {
		cfg.MaxSockets = 256
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return cfg
}

// Built says whether the page is embedded in this program.
func Built() bool {
	return hasIndex(embedded, "dist/"+indexFile)
}

// hasIndex says whether name is a file of dist.
func hasIndex(dist fs.FS, name string) bool {
	info, err := fs.Stat(dist, name)
	return err == nil && info.Mode().IsRegular()
}

// answerWait is how long the writing of an answer that is no WebSocket
// may take: a build of the page over a slow line has to arrive still,
// and a client that reads nothing has to let go of its handler.
const answerWait = 2 * time.Minute

// handler is the API and the page.
type handler struct {
	cfg   Config
	log   *slog.Logger
	built bool // the build has an index.html
	mux   *http.ServeMux
	// answerWait is answerWait; the tests make it short.
	answerWait time.Duration

	sockets atomic.Int64 // how many WebSockets are open
	// inputWaits is how many readers of sockets wait for a shell to take
	// its input. The tests look at it.
	inputWaits atomic.Int64
}

// New returns the handler of the API and of the page.
func New(cfg Config) http.Handler {
	cfg = cfg.withDefaults()
	h := &handler{
		cfg:   cfg,
		log:   cfg.Log,
		built: hasIndex(cfg.Dist, indexFile),
		mux:   http.NewServeMux(),

		answerWait: answerWait,
	}
	h.mux.HandleFunc("/api/services/{service}/rollouts", h.get(h.rollouts))
	h.mux.HandleFunc("/api/zathras/{id}/history", h.get(h.history))
	h.mux.HandleFunc("/api/", h.noSuchPath)
	h.mux.HandleFunc("/", h.page)
	return h
}

// ServeHTTP answers a request, under whatever host name it was asked:
// with the headers every answer has, and within the time an answer has.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A connection that becomes a WebSocket has this deadline taken off.
	// A ResponseWriter that has no deadlines is one of a test.
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(h.answerWait))
	header := w.Header()
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	h.mux.ServeHTTP(w, r)
}
```

`IsLoopback`, `web/loopback.go`:

```go
package web

import (
	"net"
	"net/netip"
	"strings"
)

// IsLoopback says whether addr, as --web-addr has it (host:port), names
// a loopback address: an IP that is one, or localhost. An empty host
// (":8080") is every address and is not loopback.
//
// The handler asks this of nothing: it answers under every host name.
// It is for the command, which warns at start about an address that is
// not loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}
```

The files of the page, `web/page.go`:

```go
package web

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// indexFile is the page: what every path that is no file is answered with.
const indexFile = "index.html"

// notBuilt is what is answered in place of the page when the program was
// built without it.
const notBuilt = "great-machine: the web page was not built into this program. " +
	"Build the program with `nix build`, or the page with `npm run build` in ui/ and then the program."

// contentTypes are the types of the files a build of the page has, by
// extension. A file of any other extension is application/octet-stream:
// the tables of the machine are not asked, as they differ, and the
// browser is told not to guess (nosniff).
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".wasm":        "application/wasm",
}

// page answers with the file of the build that the path names, and with
// index.html for a path that names none: the page routes by path, and a
// refresh on /s/web/z/<id> has to load it. Under assets/ alone a path
// that names no file is 404.
func (h *handler) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "great-machine: the page is asked for with GET", http.StatusMethodNotAllowed)
		return
	}
	if !h.built {
		http.Error(w, notBuilt, http.StatusNotImplemented)
		return
	}
	// Clean leaves no "..", so the name stays inside the build; an fs.FS
	// refuses a name that would not.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	f, ok := h.file(name)
	if !ok && strings.HasPrefix(name+"/", "assets/") {
		// What is under assets/ is a file of a build or nothing. A page
		// that was loaded before a new build asks for scripts that are
		// gone; index.html in their place would be refused by the browser
		// with an error that says nothing.
		http.Error(w, "great-machine: the page has no file "+r.URL.Path+
			"; it may be of an older build of the page: load the page again.", http.StatusNotFound)
		return
	}
	if !ok {
		name = indexFile
		if f, ok = h.file(name); !ok {
			http.Error(w, notBuilt, http.StatusNotImplemented)
			return
		}
	}
	defer f.Close()
	header := w.Header()
	if strings.HasPrefix(name, "assets/") {
		// The name of a file under assets/ carries a hash of its content.
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		header.Set("Cache-Control", "no-cache")
	}
	ctype := contentTypes[strings.ToLower(path.Ext(name))]
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	header.Set("Content-Type", ctype)
	http.ServeContent(w, r, name, f.modified, f.content)
}

// pageFile is a file of the build, open to be sent.
type pageFile struct {
	fs.File
	content  io.ReadSeeker
	modified time.Time
}

// file opens a file of the build, and returns false for a name that is
// no file there: a directory is none, so nothing is ever listed. Who has
// a file closes it.
func (h *handler) file(name string) (*pageFile, bool) {
	if name == "" || !fs.ValidPath(name) {
		return nil, false
	}
	f, err := h.cfg.Dist.Open(name)
	if err != nil {
		return nil, false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, false
	}
	// A file that is embedded, and one of a directory, can be sought: it
	// is sent as it is, and nothing of it is copied for a request.
	if content, ok := f.(io.ReadSeeker); ok {
		return &pageFile{File: f, content: content, modified: info.ModTime()}, true
	}
	// An fs.FS need not give such files. One that does not is read whole.
	data, err := io.ReadAll(f)
	if err != nil {
		f.Close()
		h.log.Error("reading a file of the page", "file", name, "error", err)
		return nil, false
	}
	return &pageFile{File: f, content: bytes.NewReader(data), modified: info.ModTime()}, true
}
```

The JSON of the API, `web/api.go`:

```go
package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fables-for-robots/great-machine/wire"
)

// refusal is the JSON of an answer that refuses.
type refusal struct {
	Error *wire.Error `json:"error"`
}

// rolloutList is the answer of the rollouts of a service.
type rolloutList struct {
	Rollouts []wire.RolloutInfo `json:"rollouts"` // newest first
}

// sampleList is the answer of the history of a zathras.
type sampleList struct {
	Samples []wire.Sample `json:"samples"` // oldest first
}

// statusOf returns the HTTP status of a refusal with the code.
func statusOf(code wire.Code) int {
	switch code {
	case wire.CodeBadRequest:
		return http.StatusBadRequest
	case wire.CodeUnauthorized:
		return http.StatusForbidden
	case wire.CodeNotFound:
		return http.StatusNotFound
	case wire.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// writeJSON answers with v as JSON. Nothing of the API is to be kept by
// a browser: it is the state of the fleet at one time.
func (h *handler) writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.log.Error("encoding an answer", "error", err)
		status, data = http.StatusInternalServerError, []byte(`{"error":{"code":"internal","message":"draal could not encode its answer"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}

// refusalOf makes of an error of the controller what the browser is told.
// A *wire.Error that is not internal is passed on as it is. An internal
// one, whoever made it, and every other error is logged, and the page is
// told that draal failed and no more: draal makes such errors of what
// its database said, a zathras can answer one with any text, and neither
// is the business of a page.
func (h *handler) refusalOf(r *http.Request, err error) *wire.Error {
	var e *wire.Error
	if errors.As(err, &e) && e.Code != wire.CodeInternal {
		return e
	}
	if r.Context().Err() != nil {
		// Who asked has gone, which is why it failed.
		h.log.Debug("a request ended with who asked", "path", r.URL.Path, "error", err)
	} else {
		h.log.Error("a request failed", "path", r.URL.Path, "error", err)
	}
	return wire.Errorf(wire.CodeInternal, "draal failed to answer; its log says why")
}

// refuse answers with err as a refusal.
func (h *handler) refuse(w http.ResponseWriter, r *http.Request, err error) {
	e := h.refusalOf(r, err)
	h.writeJSON(w, statusOf(e.Code), refusal{Error: e})
}

// get returns next for GET alone: every path of the API is read, and
// none changes anything.
func (h *handler) get(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			h.writeJSON(w, http.StatusMethodNotAllowed, refusal{Error: wire.Errorf(wire.CodeBadRequest,
				"%s is asked for with GET, not with %s", r.URL.Path, r.Method)})
			return
		}
		next(w, r)
	}
}

// noSuchPath answers every path under /api/ that is none of the API.
func (h *handler) noSuchPath(w http.ResponseWriter, r *http.Request) {
	h.refuse(w, r, wire.Errorf(wire.CodeNotFound, "the API has no %s", r.URL.Path))
}

// zathrasID returns the endpoint ID in the path of a request, or a
// bad-request for what is not 64 hexadecimal characters in lower case.
// Capitals are refused and not folded: an ID is compared exactly
// everywhere, and the page has it from draal, which writes lower case.
func zathrasID(r *http.Request) (wire.ID, error) {
	id := r.PathValue("id")
	valid := len(id) == 64
	for i := 0; valid && i < len(id); i++ {
		c := id[i]
		valid = c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
	}
	if !valid {
		return "", wire.Errorf(wire.CodeBadRequest,
			"%q is no endpoint ID: one is 64 hexadecimal characters in lower case", id)
	}
	return wire.ID(id), nil
}

// rollouts answers with the last rollouts of a service.
func (h *handler) rollouts(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	if err := wire.ValidService(service); err != nil {
		h.refuse(w, r, err)
		return
	}
	list, err := h.cfg.Controller.Rollouts(r.Context(), service)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if list == nil {
		list = []wire.RolloutInfo{} // [] and not null
	}
	h.writeJSON(w, http.StatusOK, rolloutList{Rollouts: list})
}

// history answers with the samples kept of a zathras.
func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	samples, err := h.cfg.Controller.History(r.Context(), id)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if samples == nil {
		samples = []wire.Sample{}
	}
	h.writeJSON(w, http.StatusOK, sampleList{Samples: samples})
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./web/`
Expected: `ok  	github.com/fables-for-robots/great-machine/web`

Run: `nix develop .#bare --command sh -c 'gofmt -l web; go vet ./web/'`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add .gitignore web/
git commit -m "Web: the page and the JSON of the API, answered under any host name"
```
