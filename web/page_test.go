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
	srv := serve(t, Config{Client: &fake{rollouts: func(string) ([]wire.RolloutInfo, error) { return nil, nil }}})
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
	inner := New(Config{Client: &fake{}, Dist: dist, Log: quiet})
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
	h := New(Config{Client: &fake{}, Dist: os.DirFS(dist), Log: quiet})
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
	srv := httptest.NewServer(New(Config{Client: &fake{}, Log: quiet}))
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
