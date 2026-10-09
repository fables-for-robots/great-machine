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
