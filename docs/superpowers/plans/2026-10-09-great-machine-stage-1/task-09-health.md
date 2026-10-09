### Task 9: health, the HTTP health check

**Files:**
- Create: `health/health.go`
- Test: `health/health_test.go`

**Interfaces:**
- Consumes: from `wire` (Task 2):

  ```go
  type Phase string

  const (
  	PhaseIdle     Phase = "idle"
  	PhaseRunning  Phase = "running"
  	PhaseStopping Phase = "stopping"
  	PhaseBackoff  Phase = "backoff"
  )
  ```

- Produces, exactly as the plan's Interfaces section has it:

  ```go
  package health

  // Handler answers GET and HEAD of /healthz with 200 and "running" while
  // phase returns wire.PhaseRunning, and with 503 and the phase otherwise.
  // Every other path is 404, every other method 405.
  func Handler(phase func() wire.Phase) http.Handler
  ```

  The body is the phase and nothing else: no line feed behind it. A path that is not `/healthz` is 404 whatever the method. A 405 has the header `Allow: GET, HEAD`. `phase` is called once for every request that is answered with 200 or 503, and not for the others.

This is Spec section 7.7. The handler knows nothing of the supervisor: whoever serves it (the `zathras` command) gives it a function that returns the phase of the moment.

- [ ] **Step 1: Write the failing test**

Create `health/health_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./health/`
Expected: FAIL — the package does not build: `undefined: Handler`

- [ ] **Step 3: Write the implementation**

Create `health/health.go`:

```go
// Package health is the HTTP health check of a zathras: it passes while
// the service runs and at no other time.
package health

import (
	"io"
	"net/http"

	"github.com/fables-for-robots/great-machine/wire"
)

// Handler answers GET and HEAD of /healthz with 200 and "running" while
// phase returns wire.PhaseRunning, and with 503 and the phase otherwise.
// Every other path is 404, every other method 405.
func Handler(phase func() wire.Phase) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := phase()
		status := http.StatusServiceUnavailable
		if p == wire.PhaseRunning {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			io.WriteString(w, string(p))
		}
	})
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./health/ && go test -race ./health/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add health/
git commit -m "Health: the health check answers by the phase of the service"
```
