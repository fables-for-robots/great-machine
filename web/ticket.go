package web

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/fables-for-robots/great-machine/wire"
)

// The guard of the page, which a handler has when it was given a ticket.
//
// `great-machine web` acts with the operator's key for whoever asks it,
// and listens where every program of the machine, and every page that is
// open in a browser on it, can ask. So it makes two secrets when it
// starts. The ticket is in the link it prints: a request for / with the
// ticket is answered with a cookie and a redirect to /, so that the
// ticket does not stay in the address. The cookie holds the other
// secret, and every other request needs it. Both are good for as long as
// the command runs.
//
// Besides, a request under a host name that is not this machine's is
// refused whatever it carries: a site that points a name of its own at
// the loopback address asks under that name. And a request that changes
// something, like every WebSocket, has to come from the page itself
// (sameOrigin), since a cookie is sent to every port of a host.

// cookiePrefix begins the name of the cookie, which ends with the port
// the page listens on: two commands on two ports do not take each
// other's.
const cookiePrefix = "great-machine-"

// noCookie is what is said to whoever asks without the cookie.
const noCookie = "this page is opened with the link that `great-machine web` printed, which has its ticket. " +
	"Without that link, or after the command was started again, it answers nothing: open the link the command printed."

// admit says whether a request may be answered, and has answered it when
// not: with 403 under a host name that is not this machine's, with the
// cookie and a redirect for the ticket, and with 401 without the cookie.
func (h *handler) admit(w http.ResponseWriter, r *http.Request) bool {
	if !allowedHost(r.Host, h.cfg.Port) {
		h.log.Warn("a request under a host name that is not this machine's was refused", "host", r.Host, "path", r.URL.Path)
		h.turnAway(w, r, http.StatusForbidden,
			fmt.Sprintf("the page answers on this machine as localhost:%d, and not under the host name %q", h.cfg.Port, r.Host))
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" && same(r.URL.Query().Get("ticket"), h.cfg.Ticket) {
		// No Max-Age: the cookie is the browser's for as long as it is
		// open, and good for as long as the command runs.
		http.SetCookie(w, &http.Cookie{
			Name:     cookiePrefix + strconv.Itoa(h.cfg.Port),
			Value:    h.cfg.Secret,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return false
	}
	for _, c := range r.CookiesNamed(cookiePrefix + strconv.Itoa(h.cfg.Port)) {
		if same(c.Value, h.cfg.Secret) {
			return true
		}
	}
	h.turnAway(w, r, http.StatusUnauthorized, noCookie)
	return false
}

// turnAway answers a request that the guard refuses: a path of the API
// with the refusal as JSON, which the page reads, and everything else
// with one line of text, which a person reads.
func (h *handler) turnAway(w http.ResponseWriter, r *http.Request, status int, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.writeJSON(w, status, refusal{Error: wire.Errorf(wire.CodeUnauthorized, "%s", message)})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, "great-machine: %s\n", message)
}

// same says whether got is the secret, in a time that does not depend on
// where the two differ. Nothing is the same as a secret that is empty.
func same(got, secret string) bool {
	return secret != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}

// allowedHost says whether host, the Host of a request, names this
// machine with the port the page listens on: localhost, or a loopback
// address as it is written, 127.0.0.1 and [::1] among them. A name is
// never looked up: one that is not localhost is somebody else's, wherever
// it points. On the port of HTTP a browser names no port.
func allowedHost(host string, port int) bool {
	name, p, err := net.SplitHostPort(host)
	if err != nil {
		if port != 80 {
			return false
		}
		switch {
		case strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]"):
			name = host[1 : len(host)-1]
		case strings.Contains(host, ":"):
			return false
		default:
			name = host
		}
		p = "80"
	}
	if p != strconv.Itoa(port) {
		return false
	}
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}
