package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

// The ticket and the secret of the guarded servers of the tests.
const (
	testTicket = "the-ticket-of-this-run"
	testSecret = "what-the-cookie-holds"
)

// guarded serves the handler of cfg with its guard, as `great-machine
// web` has it: a ticket, a secret, and the port it listens on, which is
// known once it listens. It returns the server, the port, and the cookie
// a page that was opened with the ticket has.
func guarded(t *testing.T, cfg Config) (*httptest.Server, string, *http.Cookie) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	cfg.Ticket, cfg.Secret, cfg.Port = testTicket, testSecret, port
	if cfg.Dist == nil {
		cfg.Dist = testDist()
	}
	if cfg.Log == nil {
		cfg.Log = quiet
	}
	if cfg.Client == nil {
		cfg.Client = &fake{}
	}
	srv := httptest.NewUnstartedServer(New(cfg))
	srv.Listener.Close()
	srv.Listener = l
	srv.Config.BaseContext = func(net.Listener) context.Context { return context.Background() }
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, strconv.Itoa(port), &http.Cookie{Name: "great-machine-" + strconv.Itoa(port), Value: testSecret}
}

// me is a client that says who it is, and counts how often it was asked.
func me(asked *int) *fake {
	return &fake{me: func() (wire.Identity, error) {
		*asked++
		return wire.Identity{ID: zathrasA}, nil
	}}
}

// Without the cookie nothing is answered: not the page, not a file of
// it, not the API and not the page of the store. A path of the API is
// refused as JSON, everything else in a line of text, and both say what
// to do. The client is asked nothing.
func TestWithoutTheCookieNothingIsAnswered(t *testing.T) {
	asked := 0
	srv, _, _ := guarded(t, Config{Client: me(&asked)})
	for _, path := range []string{"/", "/s/web", "/index.html", "/assets/index-1a2b.js", "/favicon.svg", "/store/", "/store"} {
		a := ask(t, srv, http.MethodGet, path, "")
		if a.status != http.StatusUnauthorized || !strings.HasPrefix(a.header.Get("Content-Type"), "text/plain") {
			t.Errorf("GET %s = %d as %q, want 401 as text", path, a.status, a.header.Get("Content-Type"))
		}
		if !strings.Contains(a.body, "great-machine web") || strings.Contains(a.body, "<") {
			t.Errorf("GET %s says %q, want a line that says to open the link `great-machine web` printed", path, a.body)
		}
		if a.header.Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s: Cache-Control is %q, want no-store", path, a.header.Get("Cache-Control"))
		}
	}
	for _, path := range []string{"/api/me", "/api/grants", "/api/watch", "/api/nothing", "/api/services/web/rollouts"} {
		a := ask(t, srv, http.MethodGet, path, "")
		if msg := wantRefusal(t, a, http.StatusUnauthorized, wire.CodeUnauthorized); !strings.Contains(msg, "great-machine web") {
			t.Errorf("GET %s is refused with %q, want it to say to open the link `great-machine web` printed", path, msg)
		}
	}
	a := do(t, srv, request{method: http.MethodPost, path: "/api/grants", body: `{"id":"a","role":"admin"}`})
	wantRefusal(t, a, http.StatusUnauthorized, wire.CodeUnauthorized)
	if asked != 0 {
		t.Errorf("the client was asked %d times by requests without the cookie", asked)
	}
	// The answers that refuse have the headers every answer has.
	a = ask(t, srv, http.MethodGet, "/", "")
	if a.header.Get("X-Content-Type-Options") != "nosniff" || a.header.Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Errorf("the 401 lacks the headers every answer has: %v", a.header)
	}
}

// The link of the page has the ticket: a request for / with it is
// answered with the cookie and a redirect to /, so that the ticket does
// not stay in the address.
func TestTheTicketIsTradedForTheCookie(t *testing.T) {
	asked := 0
	srv, port, cookie := guarded(t, Config{Client: me(&asked)})
	a := ask(t, srv, http.MethodGet, "/?ticket="+testTicket, "")
	if a.status != http.StatusSeeOther || a.header.Get("Location") != "/" {
		t.Fatalf("GET / with the ticket = %d to %q, want 303 to /", a.status, a.header.Get("Location"))
	}
	if a.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", a.header.Get("Cache-Control"))
	}
	set := (&http.Response{Header: a.header}).Cookies()
	if len(set) != 1 {
		t.Fatalf("the answer sets %d cookies, want one: %q", len(set), a.header.Values("Set-Cookie"))
	}
	c := set[0]
	if c.Name != "great-machine-"+port || c.Value != testSecret {
		t.Errorf("the cookie is %s=%s, want great-machine-%s with the secret", c.Name, c.Value, port)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 0 || !c.Expires.IsZero() || c.Domain != "" {
		t.Errorf("the cookie is %q, want HttpOnly, SameSite=Strict, Path=/, of this host, and for as long as the browser is open", a.header.Get("Set-Cookie"))
	}

	// With the cookie the page is the page, and the API answers.
	a = do(t, srv, request{method: http.MethodGet, path: "/", cookies: []*http.Cookie{cookie}})
	if a.status != http.StatusOK || !strings.Contains(a.body, "the page") {
		t.Errorf("GET / with the cookie = %d %q, want the page", a.status, a.body)
	}
	a = do(t, srv, request{method: http.MethodGet, path: "/assets/index-1a2b.js", cookies: []*http.Cookie{cookie}})
	if a.status != http.StatusOK {
		t.Errorf("a file of the page with the cookie = %d", a.status)
	}
	a = do(t, srv, request{method: http.MethodGet, path: "/api/me", cookies: []*http.Cookie{cookie}})
	if a.status != http.StatusOK || asked != 1 {
		t.Errorf("GET /api/me with the cookie = %d %q, the client asked %d times", a.status, a.body, asked)
	}
	// The link is good again, in another browser too, and for a browser
	// that has the cookie.
	for _, cookies := range [][]*http.Cookie{nil, {cookie}} {
		a = do(t, srv, request{method: http.MethodGet, path: "/?ticket=" + testTicket, cookies: cookies})
		if a.status != http.StatusSeeOther {
			t.Errorf("GET / with the ticket once more = %d, want 303", a.status)
		}
	}
	// A ticket that is none does not take the page from who has the
	// cookie.
	a = do(t, srv, request{method: http.MethodGet, path: "/?ticket=stale", cookies: []*http.Cookie{cookie}})
	if a.status != http.StatusOK {
		t.Errorf("GET / with the cookie and a ticket that is none = %d, want the page", a.status)
	}
}

func TestATicketThatIsNotTheCommands(t *testing.T) {
	srv, _, _ := guarded(t, Config{})
	for _, path := range []string{
		"/?ticket=",
		"/?ticket=another",
		"/?ticket=" + testTicket + "x",
		"/?ticket=" + testTicket[:len(testTicket)-1],
		"/?ticket=" + strings.ToUpper(testTicket),
		"/?Ticket=" + testTicket,
		// The ticket is taken at / and nowhere else.
		"/index.html?ticket=" + testTicket,
		"/s/web?ticket=" + testTicket,
	} {
		a := ask(t, srv, http.MethodGet, path, "")
		if a.status != http.StatusUnauthorized || len(a.header.Values("Set-Cookie")) != 0 {
			t.Errorf("GET %s = %d with the cookies %q, want 401 and none", path, a.status, a.header.Values("Set-Cookie"))
		}
	}
	a := ask(t, srv, http.MethodGet, "/api/me?ticket="+testTicket, "")
	wantRefusal(t, a, http.StatusUnauthorized, wire.CodeUnauthorized)
	// And it is taken with GET.
	if a := ask(t, srv, http.MethodPost, "/?ticket="+testTicket, ""); a.status != http.StatusUnauthorized {
		t.Errorf("POST / with the ticket = %d, want 401", a.status)
	}
}

func TestACookieThatIsNotTheCommands(t *testing.T) {
	asked := 0
	srv, port, _ := guarded(t, Config{Client: me(&asked)})
	for what, cookies := range map[string][]*http.Cookie{
		"another secret":           {{Name: "great-machine-" + port, Value: "stale"}},
		"no secret":                {{Name: "great-machine-" + port, Value: ""}},
		"a longer secret":          {{Name: "great-machine-" + port, Value: testSecret + "x"}},
		"the cookie of a port":     {{Name: "great-machine-1", Value: testSecret}},
		"a cookie without a port":  {{Name: "great-machine", Value: testSecret}},
		"the ticket as the cookie": {{Name: "great-machine-" + port, Value: testTicket}},
	} {
		a := do(t, srv, request{method: http.MethodGet, path: "/api/me", cookies: cookies})
		if a.status != http.StatusUnauthorized {
			t.Errorf("%s: GET /api/me = %d, want 401", what, a.status)
		}
		if a = do(t, srv, request{method: http.MethodGet, path: "/", cookies: cookies}); a.status != http.StatusUnauthorized {
			t.Errorf("%s: GET / = %d, want 401", what, a.status)
		}
	}
	if asked != 0 {
		t.Errorf("the client was asked %d times", asked)
	}
	// The right one among others is the right one.
	a := do(t, srv, request{method: http.MethodGet, path: "/api/me", cookies: []*http.Cookie{
		{Name: "great-machine-1", Value: "of another command"}, {Name: "great-machine-" + port, Value: testSecret}, {Name: "other", Value: "x"},
	}})
	if a.status != http.StatusOK {
		t.Errorf("the cookie among others: GET /api/me = %d, want 200", a.status)
	}
}

// A request under a host name that is not this machine's is refused,
// with the cookie and with the ticket: a site that points a name of its
// own at the loopback address is not answered.
func TestUnderAnotherHostNameNothingIsAnswered(t *testing.T) {
	asked := 0
	srv, port, cookie := guarded(t, Config{Client: me(&asked)})
	for _, host := range []string{
		"attacker.example", "attacker.example:" + port, "localhost.attacker.example:" + port,
		"localhost", "127.0.0.1", "localhost:1", "127.0.0.1:1", "[::1]:1",
		"10.1.2.3:" + port, "[::2]:" + port, "0.0.0.0:" + port, "localhost.:" + port, "::1",
	} {
		a := do(t, srv, request{method: http.MethodGet, path: "/", host: host, cookies: []*http.Cookie{cookie}})
		if a.status != http.StatusForbidden || !strings.HasPrefix(a.header.Get("Content-Type"), "text/plain") {
			t.Errorf("GET / as %s with the cookie = %d as %q, want 403 as text", host, a.status, a.header.Get("Content-Type"))
		}
		a = do(t, srv, request{method: http.MethodGet, path: "/?ticket=" + testTicket, host: host})
		if a.status != http.StatusForbidden || len(a.header.Values("Set-Cookie")) != 0 {
			t.Errorf("GET / as %s with the ticket = %d with the cookies %q, want 403 and none", host, a.status, a.header.Values("Set-Cookie"))
		}
		a = do(t, srv, request{method: http.MethodGet, path: "/api/me", host: host, cookies: []*http.Cookie{cookie}})
		wantRefusal(t, a, http.StatusForbidden, wire.CodeUnauthorized)
	}
	if asked != 0 {
		t.Fatalf("the client was asked %d times under a host name that is not this machine's", asked)
	}
	for _, host := range []string{"localhost:" + port, "LocalHost:" + port, "127.0.0.1:" + port, "[::1]:" + port, "127.0.0.2:" + port} {
		a := do(t, srv, request{method: http.MethodGet, path: "/api/me", host: host, cookies: []*http.Cookie{cookie}})
		if a.status != http.StatusOK {
			t.Errorf("GET /api/me as %s with the cookie = %d %q, want 200", host, a.status, a.body)
		}
	}
	// A request of HTTP/1.0 names no host at all.
	if status, _, ok := rawGet(t, strings.TrimPrefix(srv.URL, "http://"), "/", ""); !ok || status != http.StatusForbidden {
		t.Errorf("GET / without a Host = %d (%v), want 403", status, ok)
	}
}

// A socket needs the cookie as everything does, and the page's own
// origin as it always did: a page of another program on this machine has
// the cookie, which is sent to every port of a host, and no socket.
func TestASocketNeedsTheCookieAndThePagesOrigin(t *testing.T) {
	w := newWatcher()
	w.fleets <- fleetOf("web")
	srv, port, cookie := guarded(t, Config{Client: &fake{watch: w.watch}})
	with := func(origin string, cookies ...*http.Cookie) *websocket.DialOptions {
		opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
		if origin != "" {
			opts.HTTPHeader.Set("Origin", origin)
		}
		for _, c := range cookies {
			opts.HTTPHeader.Add("Cookie", c.String())
		}
		return opts
	}
	if c, status := dialWith(t, srv, "/api/watch", with(srv.URL)); c != nil || status != http.StatusUnauthorized {
		t.Errorf("a socket without the cookie was answered %d, want 401", status)
	}
	for _, origin := range []string{"http://localhost:1", "http://127.0.0.1:1", "https://attacker.example", "http://localhost:" + port} {
		if c, status := dialWith(t, srv, "/api/watch", with(origin, cookie)); c != nil || status != http.StatusForbidden {
			t.Errorf("a socket of the origin %s with the cookie was answered %d, want 403", origin, status)
		}
	}
	if got := len(w.calls); got != 0 {
		t.Fatalf("the client was asked to watch %d times by sockets that were refused", got)
	}
	c, status := dialWith(t, srv, "/api/watch", with(srv.URL, cookie))
	if c == nil {
		t.Fatalf("the socket of the page with the cookie was refused with %d", status)
	}
	await(t, "the client to be asked to watch", w.calls)
	var f wire.Fleet
	nextText(t, c, &f)
	if len(f.Services) != 1 {
		t.Errorf("the page read the fleet %+v", f)
	}
}

// A page of a name that was pointed at this machine gets nothing: not
// the page, not the fleet, not a shell. A web site can point a name of
// its own at the loopback address while its page is open in the
// operator's browser (DNS rebinding); the browser then asks under that
// name, with that name as the page's origin, which the rule of the
// Origin alone lets in. The browser sends it no cookie of localhost
// either; this test gives it the cookie all the same, and it is refused
// by its name.
func TestAPageOfANameThatWasPointedAtThisMachineGetsNothing(t *testing.T) {
	w := newWatcher()
	asked := make(chan wire.ShellRequest, 1)
	srv, port, cookie := guarded(t, Config{Client: &fake{watch: w.watch, shell: shells(newFakeShell(), asked)}})
	rebound := "attacker.example:" + port

	a := do(t, srv, request{method: http.MethodGet, path: "/", host: rebound, cookies: []*http.Cookie{cookie}})
	if a.status != http.StatusForbidden {
		t.Errorf("GET / as %s = %d, want 403", rebound, a.status)
	}
	for _, path := range []string{"/api/watch", shellPath} {
		opts := &websocket.DialOptions{Host: rebound, HTTPHeader: http.Header{"Origin": {"http://" + rebound}, "Cookie": {cookie.String()}}}
		if c, status := dialWith(t, srv, path, opts); c != nil || status != http.StatusForbidden {
			t.Errorf("the socket %s of the page of %s was answered %d, want 403", path, rebound, status)
		}
	}
	if len(w.calls) != 0 || len(asked) != 0 {
		t.Errorf("the client was asked by the page of %s", rebound)
	}
}

// A change needs the cookie and the page's origin: the two are checked
// each by itself.
func TestAChangeNeedsTheCookieAndThePagesOrigin(t *testing.T) {
	granted := 0
	srv, _, cookie := guarded(t, Config{Client: &fake{grant: func(wire.GrantRequest) error { granted++; return nil }}})
	grant := request{method: http.MethodPost, path: "/api/grants", body: `{"id":"a","role":"admin"}`}

	grant.origin = srv.URL
	wantRefusal(t, do(t, srv, grant), http.StatusUnauthorized, wire.CodeUnauthorized)

	grant.cookies, grant.origin = []*http.Cookie{cookie}, "http://127.0.0.1:1"
	wantRefusal(t, do(t, srv, grant), http.StatusForbidden, wire.CodeUnauthorized)
	if granted != 0 {
		t.Fatalf("%d grants were made by requests that were to be refused", granted)
	}

	grant.origin = srv.URL
	if a := do(t, srv, grant); a.status != http.StatusNoContent || granted != 1 {
		t.Errorf("the grant of the page = %d %q, %d grants made", a.status, a.body, granted)
	}
}

func TestAllowedHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost:8080": true, "LOCALHOST:8080": true, "127.0.0.1:8080": true, "[::1]:8080": true,
		// Every loopback address, as it is written, is this machine.
		"127.0.0.2:8080": true, "127.255.255.254:8080": true,
		"localhost": false, "localhost:80": false, "localhost:08080": false, "127.0.0.1:8081": false,
		"127.1:8080": false, "[::1]": false, "::1:8080": false, "example.com:8080": false,
		"10.0.0.1:8080": false, "0.0.0.0:8080": false, "[::]:8080": false, "localhost.example.com:8080": false,
		"localhost:8080.": false, " localhost:8080": false, "": false, ":8080": false,
	} {
		if got := allowedHost(host, 8080); got != want {
			t.Errorf("allowedHost(%q, 8080) = %v, want %v", host, got, want)
		}
	}
	// On the port of HTTP a browser names no port.
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "[::1]": true, "localhost:80": true, "example.com": false, "localhost:8080": false} {
		if got := allowedHost(host, 80); got != want {
			t.Errorf("allowedHost(%q, 80) = %v, want %v", host, got, want)
		}
	}
}
