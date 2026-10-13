// Package web is the HTTP API in front of an operator's client, and the
// web page that is built into the program. `great-machine web` serves it
// on the operator's machine, and the client asks draal with the
// operator's key: the page can do what that key may, which draal decides,
// and can roll nothing out and nothing back.
//
// Who may ask the handler is a matter of its ticket (ticket.go): the
// command prints a link with a ticket in it, the page that is opened
// with it is given a cookie, and without the cookie nothing is answered.
// A request under a host name that is not this machine's is refused, so
// that a site which points a name of its own at the loopback address is
// not answered, and a request that changes something, like every
// WebSocket, has to come from the page itself: a cookie is sent to every
// port of a host, and a page of another program on the same machine
// would otherwise act with it.
//
// A handler that is given no ticket has no guard, and answers under
// every host name: the demo, and the tests of the API.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
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
	// Client is what the API answers from: the client of an operator.
	Client Client
	// Dist is the build of the page. nil: the one that is embedded.
	Dist fs.FS
	// Ticket, Secret and Port are the guard of the page (ticket.go): the
	// ticket that is in the link of the page, what the cookie holds that
	// the ticket is traded for, and the port the page listens on, which
	// is in the name of the cookie and in the hosts that are answered.
	// With no ticket there is no guard.
	Ticket string
	Secret string
	Port   int
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
	h.mux.HandleFunc("/api/watch", h.get(h.watch))
	h.mux.HandleFunc("/api/zathras/{id}/logs", h.get(h.logs))
	h.mux.HandleFunc("/api/zathras/{id}/shell", h.get(h.shell))
	h.mux.HandleFunc("/api/me", h.get(h.me))
	h.mux.HandleFunc("/api/grants", h.grants)
	h.mux.HandleFunc("/api/", h.noSuchPath)
	// The page of the store asks for what it shows by paths beside its
	// own, so it is asked for with the slash: without it those paths
	// would be beside /store.
	h.mux.Handle("/store", http.RedirectHandler("/store/", http.StatusMovedPermanently))
	h.mux.HandleFunc("/store/", h.get(h.store))
	h.mux.HandleFunc("/", h.page)
	return h
}

// ServeHTTP answers a request with the headers every answer has, and
// within the time an answer has. With a ticket it answers who has the
// cookie alone, and under no host name but this machine's (ticket.go);
// without one, under whatever host name it was asked.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A connection that becomes a WebSocket has this deadline taken off.
	// A ResponseWriter that has no deadlines is one of a test.
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(h.answerWait))
	header := w.Header()
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	if h.cfg.Ticket != "" && !h.admit(w, r) {
		return
	}
	h.mux.ServeHTTP(w, r)
}
