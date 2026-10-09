### Task 7: preload, the client of the sidecar's preload endpoint

**Files:**
- Create: `preload/preload.go`
- Test: `preload/preload_test.go`

**Interfaces:**
- Consumes: nothing of the other tasks. The endpoint is that of the
  jaccard-nfs-nix-store sidecar, `POST /v1/preload`: the body is a list of
  lines, each a path under `/nix/store` or the name of a store path; it
  asks for no content type; it answers `200` with `fetched N paths`, `404`
  with the path when the server has no such path, `502` with the path and
  the reason when it could not be fetched, `400` for a line that is no
  path; and it ends its fetches when the client goes away.
- Produces:

  ```go
  package preload

  type Client struct {
  	URL  string       // the preload endpoint of the sidecar
  	HTTP *http.Client // nil: http.DefaultClient
  }

  // Error is an answer of the endpoint that is not 200.
  type Error struct {
  	Status int
  	Body   string // its first 4 KiB
  }

  func (e *Error) Error() string // "the preload endpoint answered 404: <body, trimmed>"

  // Preload posts /nix/store/<name> for every name, one on a line, and
  // returns nil for an answer of 200.
  func (c Client) Preload(ctx context.Context, names []string) error
  ```

  A request that could not be made, or that its context ended, is an error
  that is no `*Error`, begins with `preload:` and names the URL; a context
  that was cancelled is in its chain. `Error.Body` is valid UTF-8, so that
  it can travel in a message.

- [ ] **Step 1: Write the failing test**

`preload/preload_test.go`:

```go
package preload_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fables-for-robots/great-machine/preload"
)

// posted is what an endpoint of a test was sent.
type posted struct {
	method      string
	path        string
	contentType string
	body        string
}

// endpoint returns a server that answers every request with status and
// answer, and a channel of what it was sent.
func endpoint(t *testing.T, status int, answer string) (*httptest.Server, <-chan posted) {
	t.Helper()
	got := make(chan posted, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request: %v", err)
		}
		got <- posted{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
		w.WriteHeader(status)
		io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestPreloadPostsTheStorePaths(t *testing.T) {
	srv, got := endpoint(t, http.StatusOK, "fetched 3 paths\n")
	names := []string{
		"6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0",
		"wb6rhpznjfczwlwx23zmdrrw74bayxw4-glibc-2.42-47",
		"2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0",
	}
	c := preload.Client{URL: srv.URL + "/v1/preload"}
	if err := c.Preload(context.Background(), names); err != nil {
		t.Fatalf("Preload() = %v", err)
	}
	p := <-got
	want := "/nix/store/6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0\n" +
		"/nix/store/wb6rhpznjfczwlwx23zmdrrw74bayxw4-glibc-2.42-47\n" +
		"/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0\n"
	if p.body != want {
		t.Errorf("body = %q, want %q", p.body, want)
	}
	if p.method != http.MethodPost || p.path != "/v1/preload" {
		t.Errorf("request = %s %s, want POST /v1/preload", p.method, p.path)
	}
	if !strings.HasPrefix(p.contentType, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", p.contentType)
	}
}

func TestPreloadOfNothing(t *testing.T) {
	srv, got := endpoint(t, http.StatusOK, "fetched 0 paths\n")
	c := preload.Client{URL: srv.URL, HTTP: srv.Client()}
	if err := c.Preload(context.Background(), nil); err != nil {
		t.Fatalf("Preload() = %v", err)
	}
	if p := <-got; p.body != "" || p.method != http.MethodPost {
		t.Errorf("request = %s with body %q, want POST with an empty body", p.method, p.body)
	}
}

func TestPreloadRefused(t *testing.T) {
	cases := []struct {
		status int
		answer string
	}{
		{http.StatusNotFound, "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0: the server has no such path\n"},
		{http.StatusBadGateway, "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0: could not be fetched: the bucket is gone\n"},
		{http.StatusBadRequest, "line 1: \"a/b\" is neither a path under /nix/store nor the name of a store path\n"},
		{http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			srv, _ := endpoint(t, c.status, c.answer)
			err := preload.Client{URL: srv.URL}.Preload(context.Background(), []string{"2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"})
			var refused *preload.Error
			if !errors.As(err, &refused) {
				t.Fatalf("Preload() = %v, want a *preload.Error", err)
			}
			if refused.Status != c.status || refused.Body != c.answer {
				t.Errorf("error = %d %q, want %d %q", refused.Status, refused.Body, c.status, c.answer)
			}
			text := err.Error()
			if !strings.Contains(text, strconv.Itoa(c.status)) || !strings.Contains(text, strings.TrimSpace(c.answer)) {
				t.Errorf("Error() = %q, which does not hold the status and what the endpoint said", text)
			}
			if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, ": ") {
				t.Errorf("Error() = %q, which ends badly", text)
			}
		})
	}
}

func TestPreloadCutsALongAnswer(t *testing.T) {
	// 1 byte and then characters of 2 bytes: the cut at 4096 bytes goes
	// through one of them.
	long := "x" + strings.Repeat("ä", 4000)
	srv, _ := endpoint(t, http.StatusBadGateway, long)
	err := preload.Client{URL: srv.URL}.Preload(context.Background(), []string{"a"})
	var refused *preload.Error
	if !errors.As(err, &refused) {
		t.Fatalf("Preload() = %v, want a *preload.Error", err)
	}
	if len(refused.Body) > 4<<10 || len(refused.Body) < 4<<10-4 {
		t.Errorf("the body is %d bytes, want the first 4096 or a few less", len(refused.Body))
	}
	if !strings.HasPrefix(refused.Body, "xääää") {
		t.Errorf("the body begins with %q", refused.Body[:9])
	}
	if !utf8.ValidString(refused.Body) {
		t.Error("the body is not valid UTF-8")
	}
}

func TestPreloadNobodyThere(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL + "/v1/preload"
	srv.Close() // nothing listens there any more

	err := preload.Client{URL: url}.Preload(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Preload() = nil, want an error")
	}
	var refused *preload.Error
	if errors.As(err, &refused) {
		t.Fatalf("Preload() = %v, a *preload.Error, though nothing answered", err)
	}
	if !strings.Contains(err.Error(), "preload") || !strings.Contains(err.Error(), url) {
		t.Errorf("Preload() = %q, which does not say what failed", err)
	}
}

func TestPreloadBadURL(t *testing.T) {
	err := preload.Client{URL: "://nowhere"}.Preload(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "preload") {
		t.Fatalf("Preload() = %v, want an error that says preload", err)
	}
}

func TestPreloadEndsWithItsContext(t *testing.T) {
	entered := make(chan struct{})
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done() // a fetch that hangs, until the client is gone
		close(gone)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- preload.Client{URL: srv.URL}.Preload(ctx, []string{"a"})
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the request did not arrive")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Preload() = %v, want context.Canceled in its chain", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Preload() did not return after its context was cancelled")
	}
	// The endpoint is told, which is how the sidecar ends its fetches.
	select {
	case <-gone:
	case <-time.After(10 * time.Second):
		t.Fatal("the endpoint was not told that the client is gone")
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./preload/`
Expected: FAIL (the build fails) — the package `preload` has no Go files yet (`no non-test Go files`)

- [ ] **Step 3: Write the implementation**

`preload/preload.go`:

```go
// Package preload is the client of the preload endpoint of the
// jaccard-nfs-nix-store sidecar: it sends the store paths of a closure,
// which the sidecar fetches before anything looks them up.
package preload

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// storeDir is where a Nix store is: what the name of a store path is put
// behind to make a line of the list.
const storeDir = "/nix/store"

// maxBody is how much of an answer that is not 200 is kept.
const maxBody = 4 << 10

// Client posts lists of store paths to a preload endpoint.
type Client struct {
	// URL is the preload endpoint of the sidecar, such as
	// http://127.0.0.1:9889/v1/preload.
	URL string
	// HTTP is the client the request is made with. nil: http.DefaultClient.
	HTTP *http.Client
}

// Error is an answer of the endpoint that is not 200: 404 for a store path
// the server does not have, 502 for one that could not be fetched, 400 for
// a list the endpoint could not read.
type Error struct {
	// Status is the HTTP status of the answer.
	Status int
	// Body is the first 4 KiB of the answer, in which the endpoint names
	// the store path and says what is wrong with it. It is valid UTF-8.
	Body string
}

// Error returns the status and what the endpoint said.
func (e *Error) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("the preload endpoint answered %d", e.Status)
	}
	return fmt.Sprintf("the preload endpoint answered %d: %s", e.Status, body)
}

// Preload posts /nix/store/<name> for every name, one on a line, and
// returns nil for an answer of 200. Any other answer is returned as an
// *Error. A request that could not be made, or that ctx ended, is an error
// of another kind, which names the endpoint.
func (c Client) Preload(ctx context.Context, names []string) error {
	var list strings.Builder
	for _, name := range names {
		list.WriteString(storeDir + "/" + name + "\n")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(list.String()))
	if err != nil {
		return fmt.Errorf("preload: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The error of net/http names the method and the URL.
		return fmt.Errorf("preload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil && len(body) == 0 {
		return fmt.Errorf("preload: reading the answer of %s, which is %d: %w", c.URL, resp.StatusCode, err)
	}
	// The cut may have gone through a character. "?" is no longer than
	// what it stands for, so the body stays within its 4 KiB.
	return &Error{Status: resp.StatusCode, Body: strings.ToValidUTF8(string(body), "?")}
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./preload/ && go test -race ./preload/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add preload/
git commit -m "Preload: the client of the sidecar's preload endpoint"
```
