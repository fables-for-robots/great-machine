package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// linkOfThePage is the one line `great-machine web` prints: the link of
// the page, with the port it listens on and its ticket, 32 random bytes
// as they are written in a URL.
var linkOfThePage = regexp.MustCompile(`^http://localhost:([1-9][0-9]*)/\?ticket=([A-Za-z0-9_-]{43})\n$`)

// aWeb is the configuration of a `great-machine web` on a free port of
// the loopback address, whose draal is the one of the test.
func aWeb(t *testing.T, d *pageDraal) webConfig {
	t.Helper()
	return webConfig{
		operator: operatorSettings{draal: wire.ID(idA), key: filepath.Join(t.TempDir(), "operator.key")},
		addr:     "127.0.0.1:0",
		dial:     d.dial,
	}
}

// thePage is a `great-machine web` that runs, and a browser of it.
type thePage struct {
	t      *testing.T
	link   string // what the command printed
	base   string // http://localhost:PORT
	ticket string
	logged *buffer
	done   <-chan error
	// browser keeps the cookies it is given, and follows no redirect.
	browser *http.Client
}

// startPage runs the command in a goroutine and waits for the link it
// prints.
func startPage(t *testing.T, ctx context.Context, cfg webConfig) *thePage {
	t.Helper()
	var out, logged buffer
	done := make(chan error, 1)
	go func() { done <- runWeb(ctx, cfg, &out, quietLog(&logged)) }()
	waitFor(t, "the command to print the link of the page", func() bool {
		select {
		case err := <-done:
			t.Fatalf("runWeb returned early: %v\n%s", err, logged.String())
		default:
		}
		return strings.HasSuffix(out.String(), "\n")
	})
	m := linkOfThePage.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("the command printed %q, want one line: the link of the page with its ticket", out.String())
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	p := &thePage{
		t: t, link: strings.TrimSpace(out.String()), base: "http://localhost:" + m[1], ticket: m[2], logged: &logged, done: done,
		browser: &http.Client{
			Jar:           jar,
			Timeout:       20 * time.Second,
			Transport:     &http.Transport{},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	t.Cleanup(p.browser.CloseIdleConnections)
	return p
}

// get asks for a URL as the browser of the page, and returns the status
// and the body.
func (p *thePage) get(link string) (int, string) {
	p.t.Helper()
	resp, err := p.browser.Get(link)
	if err != nil {
		p.t.Fatalf("GET %s: %v\n%s", link, err, p.logged.String())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// open opens the link the command printed, which gives the browser its
// cookie.
func (p *thePage) open() {
	p.t.Helper()
	if status, body := p.get(p.link); status != http.StatusSeeOther {
		p.t.Fatalf("the link of the page was answered %d %q, want 303\n%s", status, body, p.logged.String())
	}
}

// ended waits for the command to return, and returns what it returned.
func (p *thePage) ended() error {
	p.t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(30 * time.Second):
		p.t.Fatalf("runWeb did not return\n%s", p.logged.String())
		return nil
	}
}

// The command prints the link of the page and nothing else, answers
// nothing without the ticket of that link, and with it the page and the
// API, which asks draal with the operator's key. It serves before draal
// is reached, and dials when the page first asks for something. When its
// context is done it stops with no error and leaves nothing listening.
func TestRunWebPrintsItsLinkAndAnswersWhoOpensIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aPageDraal(t)
	p := startPage(t, ctx, aWeb(t, d))

	// Without the ticket: nothing.
	for _, path := range []string{"/", "/api/me", "/assets/index.js", "/store/"} {
		if status, body := p.get(p.base + path); status != http.StatusUnauthorized || !strings.Contains(body, "great-machine web") {
			t.Errorf("GET %s without the ticket = %d %q, want 401 and what to do", path, status, body)
		}
	}
	if status, _ := p.get(p.base + "/?ticket=" + strings.Repeat("A", 43)); status != http.StatusUnauthorized {
		t.Errorf("GET / with a ticket that is not the command's = %d, want 401", status)
	}

	// With it: the page, built into the program or not.
	p.open()
	want := http.StatusNotImplemented
	if web.Built() {
		want = http.StatusOK
	}
	if status, body := p.get(p.base + "/"); status != want {
		t.Errorf("GET / with the cookie = %d %q, want %d", status, body, want)
	}
	if !web.Built() && !strings.Contains(p.logged.String(), "the web page was not built into this program") {
		t.Errorf("the command does not warn that the page was not built:\n%s", p.logged.String())
	}
	if n := d.dialed(); n != 0 {
		t.Errorf("draal was dialed %d times before the page asked for anything", n)
	}
	// The cookie is not the ticket: what is in the address of a browser
	// is not what it is let in by afterwards.
	for _, c := range p.browser.Jar.Cookies(mustURL(t, p.base)) {
		if c.Value == p.ticket || len(c.Value) != 43 {
			t.Errorf("the cookie %s holds %q, want a secret of its own", c.Name, c.Value)
		}
	}

	// And the API, which asks draal.
	status, body := p.get(p.base + "/api/me")
	var me wire.Identity
	if err := json.Unmarshal([]byte(body), &me); status != http.StatusOK || err != nil || me.ID != nodetest.ID(2) {
		t.Errorf("GET /api/me = %d %q, want who the key is to draal", status, body)
	}
	if status, body := p.get(p.base + "/api/services/api/rollouts"); status != http.StatusForbidden || !strings.Contains(body, "no deployer of api") {
		t.Errorf("what draal refuses came as %d %q, want 403 with what draal said", status, body)
	}
	if n := d.dialed(); n != 1 {
		t.Errorf("draal was dialed %d times, want once", n)
	}

	cancel()
	if err := p.ended(); err != nil {
		t.Errorf("runWeb returned %v, want nil", err)
	}
	if conn, err := net.DialTimeout("tcp", strings.TrimPrefix(p.base, "http://"), time.Second); err == nil {
		conn.Close()
		t.Error("the page still listens after the command has ended")
	}
}

func mustURL(t *testing.T, text string) *url.URL {
	t.Helper()
	u, err := url.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Two commands have two tickets: one that is started again is opened
// with its own link.
func TestEveryRunOfWebHasATicketOfItsOwn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aPageDraal(t)
	first := startPage(t, ctx, aWeb(t, d))
	second := startPage(t, ctx, aWeb(t, d))
	if first.ticket == second.ticket {
		t.Fatalf("two commands printed the same ticket, %s", first.ticket)
	}
	if status, _ := second.get(second.base + "/?ticket=" + first.ticket); status != http.StatusUnauthorized {
		t.Errorf("the ticket of another command was answered %d, want 401", status)
	}
	// And the cookie of the one is none of the other, though both are of
	// localhost: its name has the port.
	first.open()
	if status, _ := first.get(second.base + "/api/me"); status != http.StatusUnauthorized {
		t.Errorf("the cookie of another command was answered %d, want 401", status)
	}
}

// While draal cannot be reached the page loads all the same, and what it
// asks is answered unavailable with what the dial said; when draal is
// there, the next request reaches it.
func TestRunWebWhileDraalCannotBeReached(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aPageDraal(t)
	d.fail = errors.New("no address found for draal")
	p := startPage(t, ctx, aWeb(t, d))
	p.open()
	if status, _ := p.get(p.base + "/"); status != http.StatusOK && status != http.StatusNotImplemented {
		t.Errorf("GET / while draal cannot be reached = %d, want the page", status)
	}
	status, body := p.get(p.base + "/api/me")
	var refusal struct {
		Error *wire.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &refusal); status != http.StatusServiceUnavailable || err != nil || refusal.Error == nil ||
		refusal.Error.Code != wire.CodeUnavailable || !strings.Contains(refusal.Error.Message, "no address found for draal") {
		t.Errorf("GET /api/me while draal cannot be reached = %d %q, want 503, unavailable, and what the dial said", status, body)
	}
	d.mu.Lock()
	d.fail = nil
	d.mu.Unlock()
	if status, body := p.get(p.base + "/api/me"); status != http.StatusOK {
		t.Errorf("GET /api/me when draal is there = %d %q, want 200", status, body)
	}
}

// When the connection to draal is lost, the fleet of the page ends with
// the reason, and what the page asks next is asked over a connection
// that was dialed anew.
func TestRunWebDialsAgainAfterTheConnectionWasLost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aPageDraal(t)
	p := startPage(t, ctx, aWeb(t, d))
	p.open()
	watch := p.socket("/api/watch")
	if _, data := readSocket(t, watch); !strings.Contains(string(data), `"services"`) {
		t.Fatalf("the socket sent %s, want the fleet", data)
	}
	d.cut()
	_, data := readSocket(t, watch)
	var refusal struct {
		Error *wire.Error `json:"error"`
	}
	if err := json.Unmarshal(data, &refusal); err != nil || refusal.Error == nil || refusal.Error.Code != wire.CodeUnavailable {
		t.Errorf("when the connection to draal was lost the socket sent %s, want an error that is unavailable", data)
	}
	if status, body := p.get(p.base + "/api/me"); status != http.StatusOK {
		t.Errorf("GET /api/me after the connection was lost = %d %q, want 200", status, body)
	}
	if n := d.dialed(); n != 2 {
		t.Errorf("draal was dialed %d times, want twice", n)
	}
}

// socket opens a WebSocket as the page does: with the cookie and the
// origin of the page.
func (p *thePage) socket(path string) *websocket.Conn {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.base, "http")+path, &websocket.DialOptions{
		HTTPClient: p.browser,
		HTTPHeader: http.Header{"Origin": {p.base}},
	})
	if err != nil {
		p.t.Fatalf("opening the socket %s: %v\n%s", path, err, p.logged.String())
	}
	c.SetReadLimit(-1)
	p.t.Cleanup(func() { c.CloseNow() })
	return c
}

// readSocket reads one message of a socket.
func readSocket(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("reading the socket: %v", err)
	}
	return typ, data
}

// An address that is taken ends the command with an error that names the
// page and the address, and nothing was printed.
func TestRunWebAddrTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := aWeb(t, aPageDraal(t))
	cfg.addr = taken.Addr().String()
	var out, logged buffer
	err = runWeb(context.Background(), cfg, &out, quietLog(&logged))
	if err == nil || !strings.Contains(err.Error(), "the web page: listen tcp "+taken.Addr().String()) {
		t.Errorf("runWeb returned %v, want an error that names the web page and the address", err)
	}
	if out.String() != "" {
		t.Errorf("the command printed %q, and has no page", out.String())
	}
}

// A key file that cannot be read ends the command before it listens:
// what it would print is the link of a page that can ask nothing.
func TestRunWebWithAKeyThatCannotBeRead(t *testing.T) {
	cfg := aWeb(t, aPageDraal(t))
	cfg.dial = nil
	cfg.operator.key = t.TempDir() // a directory is no key file
	var out, logged buffer
	err := runWeb(context.Background(), cfg, &out, quietLog(&logged))
	if err == nil || !strings.Contains(err.Error(), "the operator's key") {
		t.Errorf("runWeb returned %v, want the error of the operator's key", err)
	}
	if out.String() != "" {
		t.Errorf("the command printed %q, and has no page", out.String())
	}
}

// Pages that hold a socket and read no more, as a laptop asleep or a tab
// frozen in the background, neither answer a ping nor the close of their
// socket. The command stops all the same, well within the time it gives
// the handlers, and with every handler ended.
func TestRunWebStopsWithPagesThatDoNotAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := aPageDraal(t)
	p := startPage(t, ctx, aWeb(t, d))
	p.open()
	for range 3 {
		c := p.socket("/api/watch")
		// The first fleet: the handler runs. From here on the page reads
		// nothing, and so answers nothing.
		readSocket(t, c)
	}
	waitFor(t, "draal to be watched three times", func() bool { return d.watching() == 3 })

	begun := time.Now()
	cancel()
	if err := p.ended(); err != nil {
		t.Errorf("runWeb returned %v, want nil", err)
	}
	if took := time.Since(begun); took > webStopWait-time.Second {
		t.Errorf("the command took %s to stop, and gives its handlers %s", took, webStopWait)
	}
	if strings.Contains(p.logged.String(), "still being answered") {
		t.Errorf("the command gave up on handlers that still ran:\n%s", p.logged.String())
	}
	// The connection to draal went with the command: nobody watches.
	waitFor(t, "the watches of draal to end", func() bool { return d.watching() == 0 })
}

// When the server of the page ends by itself, the command ends with its
// error.
func TestRunWebFails(t *testing.T) {
	cfg := aWeb(t, aPageDraal(t))
	bound := make(chan net.Listener, 1)
	cfg.listen = func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err == nil {
			bound <- l
		}
		return l, err
	}
	p := startPage(t, context.Background(), cfg)
	// The listener is closed under the server, which is how its Accept
	// fails for good.
	(<-bound).Close()
	if err := p.ended(); err == nil || !strings.Contains(err.Error(), "the web page: ") || !errors.Is(err, net.ErrClosed) {
		t.Errorf("runWeb returned %v, want the error of the web page", err)
	}
}

// The command as it is typed: its link on standard output and nothing
// else there, and the status 0 when it is interrupted.
func TestTheWebCommand(t *testing.T) {
	cleanEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr buffer
	status := make(chan int, 1)
	go func() {
		status <- run(ctx, newApp(&stdout, &stderr), []string{"great-machine", "web",
			"--draal", idA, "--key", filepath.Join(t.TempDir(), "operator.key"), "--addr", "127.0.0.1:0"})
	}()
	waitFor(t, "the command to print the link of the page", func() bool {
		select {
		case got := <-status:
			t.Fatalf("the command ended with %d: %s", got, stderr.String())
		default:
		}
		return strings.HasSuffix(stdout.String(), "\n")
	})
	if !linkOfThePage.MatchString(stdout.String()) {
		t.Errorf("standard output is %q, want the link of the page and nothing else", stdout.String())
	}
	cancel()
	select {
	case got := <-status:
		if got != 0 {
			t.Errorf("the exit status is %d, want 0: %s", got, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the command did not end when it was interrupted")
	}
	if !bytes.Contains([]byte(stderr.String()), []byte("draal")) {
		t.Errorf("standard error does not say whose page it is: %q", stderr.String())
	}
}
