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
