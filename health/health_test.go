package health

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestHandler(t *testing.T) {
	var phase atomic.Value // the wire.Phase the handler is told
	var asked atomic.Int64 // how often it asked
	srv := httptest.NewServer(Handler(func() wire.Phase {
		asked.Add(1)
		return phase.Load().(wire.Phase)
	}))
	defer srv.Close()

	tests := []struct {
		name   string
		phase  wire.Phase
		method string
		path   string
		status int
		body   string
		asks   bool // whether the phase is asked for
	}{
		{"running", wire.PhaseRunning, "GET", "/healthz", 200, "running", true},
		{"idle", wire.PhaseIdle, "GET", "/healthz", 503, "idle", true},
		{"stopping", wire.PhaseStopping, "GET", "/healthz", 503, "stopping", true},
		{"backoff", wire.PhaseBackoff, "GET", "/healthz", 503, "backoff", true},
		{"running again: the phase is asked every time", wire.PhaseRunning, "GET", "/healthz", 200, "running", true},
		{"a query changes nothing", wire.PhaseRunning, "GET", "/healthz?verbose=1", 200, "running", true},
		{"HEAD while running", wire.PhaseRunning, "HEAD", "/healthz", 200, "", true},
		{"HEAD while in backoff", wire.PhaseBackoff, "HEAD", "/healthz", 503, "", true},
		{"POST", wire.PhaseRunning, "POST", "/healthz", 405, "method not allowed\n", false},
		{"PUT", wire.PhaseRunning, "PUT", "/healthz", 405, "method not allowed\n", false},
		{"DELETE", wire.PhaseRunning, "DELETE", "/healthz", 405, "method not allowed\n", false},
		{"the root", wire.PhaseRunning, "GET", "/", 404, "404 page not found\n", false},
		{"another path", wire.PhaseRunning, "GET", "/health", 404, "404 page not found\n", false},
		{"a path below", wire.PhaseRunning, "GET", "/healthz/", 404, "404 page not found\n", false},
		{"POST to another path", wire.PhaseRunning, "POST", "/other", 404, "404 page not found\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase.Store(tt.phase)
			before := asked.Load()
			req, err := http.NewRequest(tt.method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tt.status {
				t.Errorf("status %d, want %d", res.StatusCode, tt.status)
			}
			if string(body) != tt.body {
				t.Errorf("body %q, want %q", body, tt.body)
			}
			if tt.status == 405 {
				if got := res.Header.Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow is %q, want GET, HEAD", got)
				}
			}
			want := before
			if tt.asks {
				want++
			}
			if got := asked.Load(); got != want {
				t.Errorf("the phase was asked for %d times, want %d", got-before, want-before)
			}
		})
	}
}
