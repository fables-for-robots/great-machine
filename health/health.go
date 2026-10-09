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
