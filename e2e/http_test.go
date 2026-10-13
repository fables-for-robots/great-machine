package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// absent is an endpoint ID that no zathras of a harness has.
const absent = wire.ID("00000000000000000000000000000000000000000000000000000000000000aa")

// page is the web page's side of a harness: the HTTP API in front of the
// operator's client of its draal, as `great-machine web` serves it, and a
// browser of it.
type page struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
}

// pageClient is the operator's client of the harness as the handler of
// the page asks for one. The two differ in the type of a shell.
type pageClient struct{ *operator.Client }

// A pageClient is a web.Client.
var _ web.Client = pageClient{}

func (c pageClient) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (web.Shell, error) {
	sh, err := c.Client.Shell(ctx, zathras, req)
	if err != nil {
		return nil, err
	}
	return sh, nil
}

// web serves the HTTP API in front of the operator of the harness, which
// asks the draal that runs now. The server and the connections of its
// client are closed when the test ends.
func (h *harness) web() *page {
	h.t.Helper()
	return h.webWith(web.Config{})
}

// webWith is web with the times of cfg, for a test that needs another
// than the handler's own, and with its guard, for a test that gives it a
// ticket; its client and its logger are the harness's. A handler with a
// ticket is told the port it listens on, as the command tells it.
func (h *harness) webWith(cfg web.Config) *page {
	h.t.Helper()
	cfg.Client = pageClient{h.op}
	cfg.Log = quiet
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		h.t.Fatalf("listening for the page: %v", err)
	}
	cfg.Port = l.Addr().(*net.TCPAddr).Port
	srv := httptest.NewUnstartedServer(web.New(cfg))
	srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	p := &page{
		t:      h.t,
		srv:    srv,
		client: &http.Client{Transport: &http.Transport{}, Timeout: deadline},
	}
	h.t.Cleanup(func() {
		p.client.CloseIdleConnections()
		p.srv.Close()
	})
	return p
}

// get asks for a path, with the Host given if it is not empty, and
// returns the status and the body.
func (p *page) get(path, host string) (int, []byte) {
	p.t.Helper()
	req, err := http.NewRequest(http.MethodGet, p.srv.URL+path, nil)
	if err != nil {
		p.t.Fatalf("NewRequest %s: %v", path, err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatalf("GET %s: reading the answer: %v", path, err)
	}
	return resp.StatusCode, body
}

// socket opens a WebSocket as the page does: with the origin of the page.
// It is closed when the test ends.
func (p *page) socket(path string) *websocket.Conn {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.srv.URL, "http")+path, &websocket.DialOptions{
		HTTPClient: p.client,
		HTTPHeader: http.Header{"Origin": {p.srv.URL}},
	})
	if err != nil {
		p.t.Fatalf("opening the socket %s: %v", path, err)
	}
	c.SetReadLimit(-1)
	p.t.Cleanup(func() { c.CloseNow() })
	return c
}

// message is a message of a socket, or how the socket ended.
type message struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

// read reads one message of a socket.
func read(c *websocket.Conn) message {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	typ, data, err := c.Read(ctx)
	return message{typ, data, err}
}

// refused fails the test unless the socket sends the error message with
// the code and is closed normally after it.
func refused(t *testing.T, what string, c *websocket.Conn, code wire.Code) {
	t.Helper()
	m := read(c)
	var got struct {
		Error *wire.Error `json:"error"`
	}
	if m.err != nil || m.typ != websocket.MessageText || json.Unmarshal(m.data, &got) != nil || got.Error == nil || got.Error.Code != code {
		t.Fatalf("%s: the socket sent %q (%v), want the error message with the code %s", what, m.data, m.err, code)
	}
	if m = read(c); websocket.CloseStatus(m.err) != websocket.StatusNormalClosure {
		t.Fatalf("%s: after its error message the socket sent %q (%v), want a normal close", what, m.data, m.err)
	}
}

func TestHTTPWatchFollowsARollout(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 3})
	p := h.web()
	c := p.socket("/api/watch")

	// The first fleet at once: every zathras, idle.
	fleet := func() wire.Fleet {
		m := read(c)
		var f wire.Fleet
		if m.err != nil || m.typ != websocket.MessageText || json.Unmarshal(m.data, &f) != nil {
			t.Fatalf("the socket sent %q (%v), want a fleet as text", m.data, m.err)
		}
		return f
	}
	zs := zathrasesOf(fleet())
	if len(zs) != 3 {
		t.Fatalf("the fleet has %d zathrases, want 3", len(zs))
	}
	for i, z := range zs {
		if z.Name != h.pods[i].name || z.ID != h.pods[i].id || z.State.Phase != wire.PhaseIdle || z.Connected.IsZero() {
			t.Errorf("zathras %d of the fleet is %+v, want %s (%s), idle", i, z, h.pods[i].name, h.pods[i].id)
		}
	}

	// A rollout is followed to its end.
	info := h.rollout("--stdout", "hello")
	for {
		f := fleet()
		if r := rolloutOf(f); r != nil && r.ID == info.ID && r.State == wire.RolloutCompleted && runs(f, info.Generation, 3) {
			if svc := serviceOf(f); svc.Stable != info.Generation {
				t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
			}
			break
		}
	}

	// The rollouts of the service, newest first.
	second := h.rollout("--stdout", "again")
	h.await(second, wire.RolloutCompleted)
	status, body := p.get("/api/services/"+service+"/rollouts", "")
	var list struct {
		Rollouts []wire.RolloutInfo `json:"rollouts"`
	}
	if err := json.Unmarshal(body, &list); status != http.StatusOK || err != nil || len(list.Rollouts) != 2 {
		t.Fatalf("the rollouts are %d %s (%v), want two of them", status, body, err)
	}
	if a, b := list.Rollouts[0], list.Rollouts[1]; a.ID != second.ID || a.State != wire.RolloutCompleted || a.Finished.IsZero() ||
		b.ID != info.ID || b.State != wire.RolloutCompleted || b.CreatedBy != h.opKey.ID() || b.Service != service {
		t.Errorf("the rollouts are %+v and %+v, want %d and then %d, completed", a, b, second.ID, info.ID)
	}
	// A service nobody rolled out has none, which is an empty list.
	if status, body := p.get("/api/services/other/rollouts", ""); status != http.StatusOK || strings.TrimSpace(string(body)) != `{"rollouts":[]}` {
		t.Errorf("the rollouts of a service without one are %d %s, want an empty list", status, body)
	}
}

func TestHTTPHistory(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	h.await(h.rollout(), wire.RolloutCompleted)
	status, body := p.get("/api/zathras/"+string(h.pods[0].id)+"/history", "")
	// Where the machine gives no figures, as a Mac, there are no samples:
	// the list is there all the same, and is a list.
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(body, &answer); status != http.StatusOK || err != nil || len(answer) != 1 || !strings.HasPrefix(string(answer["samples"]), "[") {
		t.Fatalf("the history is %d %s (%v), want a list of samples", status, body, err)
	}
	var samples []wire.Sample
	if err := json.Unmarshal(answer["samples"], &samples); err != nil {
		t.Fatalf("the samples are %s: %v", answer["samples"], err)
	}
	for i, s := range samples {
		if s.At.IsZero() || i > 0 && s.At.Before(samples[i-1].At) {
			t.Errorf("sample %d is of %v, behind one of %v: not oldest first", i, s.At, samples[i-1].At)
		}
	}
}

func TestHTTPLogs(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	path := "/api/zathras/" + string(h.pods[0].id) + "/logs"
	h.await(h.rollout("--stdout", "hello"), wire.RolloutCompleted)

	// Without follow: the ring, and a normal close.
	has := func(lines []wire.LogLine, stream, text string) bool {
		for _, line := range lines {
			if line.Stream == stream && strings.Contains(line.Text, text) {
				return true
			}
		}
		return false
	}
	var ring []wire.LogLine
	eventually(t, "the line of the service in the ring", func() bool {
		c := p.socket(path)
		ring = nil
		for {
			m := read(c)
			if m.err != nil {
				if websocket.CloseStatus(m.err) != websocket.StatusNormalClosure {
					t.Fatalf("the log ended with %v, want a normal close", m.err)
				}
				return has(ring, "stdout", "hello")
			}
			var line wire.LogLine
			if err := json.Unmarshal(m.data, &line); err != nil || m.typ != websocket.MessageText {
				t.Fatalf("the socket sent %q (%v), want a line as text", m.data, err)
			}
			ring = append(ring, line)
		}
	})
	if !has(ring, "zathras", "started generation 1") {
		t.Errorf("the ring lacks the supervisor's line about the start: %+v", ring)
	}

	// With follow, what is written later arrives.
	c := p.socket(path + "?follow=1")
	h.rollout("--stdout", "again")
	for {
		m := read(c)
		if m.err != nil {
			t.Fatalf("the log that is followed ended with %v before the line of the second rollout", m.err)
		}
		var line wire.LogLine
		if err := json.Unmarshal(m.data, &line); err != nil {
			t.Fatalf("the socket sent %q: %v", m.data, err)
		}
		if line.Stream == "stdout" && line.Text == "again" {
			return
		}
	}
}

func TestHTTPShell(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	c := p.socket("/api/zathras/" + string(h.pods[0].id) + "/shell?cols=80&rows=24&term=dumb")
	// The terminal echoes the command, quotes and all; only its output
	// has the word in one piece.
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("echo mar''ker; exit 3\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var output []byte
	for {
		m := read(c)
		if m.err != nil {
			t.Fatalf("the shell ended with %v before its exit status; its output was %q", m.err, output)
		}
		if m.typ == websocket.MessageBinary {
			output = append(output, m.data...)
			continue
		}
		if string(m.data) != `{"exit":3}` {
			t.Errorf("the text message is %s, want {\"exit\":3}", m.data)
		}
		break
	}
	if !strings.Contains(string(output), "marker") {
		t.Errorf("the output is %q, want marker in it", output)
	}
	if m := read(c); websocket.CloseStatus(m.err) != websocket.StatusNormalClosure {
		t.Errorf("after the exit status the socket sent %q (%v), want a normal close", m.data, m.err)
	}
}

func TestHTTPOfAZathrasThatIsNotConnected(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	status, body := p.get("/api/zathras/"+string(absent)+"/history", "")
	var got struct {
		Error *wire.Error `json:"error"`
	}
	if err := json.Unmarshal(body, &got); status != http.StatusServiceUnavailable || err != nil || got.Error == nil || got.Error.Code != wire.CodeUnavailable {
		t.Errorf("the history is %d %s, want 503 and unavailable", status, body)
	}
	refused(t, "the log", p.socket("/api/zathras/"+string(absent)+"/logs"), wire.CodeUnavailable)
	refused(t, "the log, followed", p.socket("/api/zathras/"+string(absent)+"/logs?follow=1"), wire.CodeUnavailable)
	refused(t, "the shell", p.socket("/api/zathras/"+string(absent)+"/shell"), wire.CodeUnavailable)
}

// The page as `great-machine web` serves it, with a ticket. Without the
// cookie that the ticket is traded for nothing is answered. With it the
// page reads the fleet and has a shell. Under a host name that is not
// this machine's, as a site has it that points a name of its own at the
// loopback address, nothing is answered though the cookie is there. And
// a page of another origin that has the cookie, as one of another
// program on this machine has, gets no socket.
func TestHTTPWithATicket(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	const ticket, secret = "the-ticket-of-the-test", "the-secret-of-the-test"
	p := h.webWith(web.Config{Ticket: ticket, Secret: secret})
	port := strconv.Itoa(p.srv.Listener.Addr().(*net.TCPAddr).Port)
	cookie := &http.Cookie{Name: "great-machine-" + port, Value: secret}
	history := "/api/zathras/" + string(h.pods[0].id) + "/history"
	shell := "/api/zathras/" + string(h.pods[0].id) + "/shell?term=dumb"
	const name = "attacker.example"

	// ask asks for a path as a browser that follows no redirect, with
	// the cookie or without, under the host given if it is not empty.
	ask := func(path, host string, with bool) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, p.srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if with {
			req.AddCookie(cookie)
		}
		client := &http.Client{Transport: p.client.Transport, Timeout: deadline,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	dial := func(path, host, origin string, with bool) (*websocket.Conn, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		header := http.Header{"Origin": {origin}}
		if with {
			header.Set("Cookie", cookie.String())
		}
		c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.srv.URL, "http")+path, &websocket.DialOptions{
			HTTPClient: p.client,
			Host:       host,
			HTTPHeader: header,
		})
		if err != nil {
			if resp == nil {
				t.Fatalf("opening the socket %s: %v", path, err)
			}
			return nil, resp.StatusCode
		}
		c.SetReadLimit(-1)
		t.Cleanup(func() { c.CloseNow() })
		return c, http.StatusSwitchingProtocols
	}

	// Without the cookie: nothing, and no socket.
	for _, path := range []string{"/", history} {
		if resp := ask(path, "", false); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without the cookie = %d, want 401", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/watch", shell} {
		if c, status := dial(path, "", p.srv.URL, false); c != nil || status != http.StatusUnauthorized {
			t.Errorf("%s without the cookie was answered %d, want 401", path, status)
		}
	}
	// The link of the page gives the cookie, and leads to the page.
	resp := ask("/?ticket="+ticket, "", false)
	if set := resp.Cookies(); resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" ||
		len(set) != 1 || set[0].Name != cookie.Name || set[0].Value != secret {
		t.Fatalf("the link of the page was answered %d to %q with the cookies %v, want 303 to / with the cookie of the page",
			resp.StatusCode, resp.Header.Get("Location"), resp.Cookies())
	}

	// With it: the API, the fleet and a shell.
	if resp := ask(history, "", true); resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s with the cookie = %d, want 200", history, resp.StatusCode)
	}
	watch, status := dial("/api/watch", "", p.srv.URL, true)
	if watch == nil {
		t.Fatalf("the fleet of the page was refused with %d", status)
	}
	var f wire.Fleet
	if m := read(watch); m.err != nil || json.Unmarshal(m.data, &f) != nil || len(zathrasesOf(f)) != 1 {
		t.Errorf("the page was sent %q (%v), want the fleet", m.data, m.err)
	}
	sh, status := dial(shell, "", p.srv.URL, true)
	if sh == nil {
		t.Fatalf("a shell of the page was refused with %d", status)
	}
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	if err := sh.Write(ctx, websocket.MessageBinary, []byte("exit 4\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for {
		m := read(sh)
		if m.err != nil {
			t.Fatalf("the shell ended with %v before its exit status", m.err)
		}
		if m.typ == websocket.MessageText {
			if string(m.data) != `{"exit":4}` {
				t.Errorf("the text message is %s, want {\"exit\":4}", m.data)
			}
			break
		}
	}

	// Under a name that is not this machine's: nothing, with the cookie
	// and as the page of that name.
	rebound := name + ":" + port
	for _, path := range []string{"/", history} {
		if resp := ask(path, rebound, true); resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s as %s = %d, want 403", path, rebound, resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/watch", shell} {
		if c, status := dial(path, rebound, "http://"+rebound, true); c != nil || status != http.StatusForbidden {
			t.Errorf("%s as the page of %s was answered %d, want 403", path, rebound, status)
		}
	}
	// A page of another program on this machine has the cookie, which is
	// sent to every port of a host, and gets no socket.
	for _, path := range []string{"/api/watch", shell} {
		if c, status := dial(path, "", "http://127.0.0.1:1", true); c != nil || status != http.StatusForbidden {
			t.Errorf("%s as a page of another origin was answered %d, want 403", path, status)
		}
	}
}

// shellPID starts a shell through a socket, has it run the command
// behind the printing of its process ID, and returns the socket and the
// ID once the shell has printed it.
func shellPID(t *testing.T, p *page, id wire.ID, then string) (*websocket.Conn, int) {
	t.Helper()
	c := p.socket("/api/zathras/" + string(id) + "/shell?term=dumb")
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	// The terminal echoes the command, in which the ID is not a number.
	if err := c.Write(ctx, websocket.MessageBinary, []byte("echo pid:$$:; "+then+"\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	printed := regexp.MustCompile(`pid:(\d+):`)
	var output []byte
	for {
		m := read(c)
		if m.err != nil || m.typ != websocket.MessageBinary {
			t.Fatalf("the shell sent %q (%v) before its process ID; its output was %q", m.data, m.err, output)
		}
		output = append(output, m.data...)
		if found := printed.FindSubmatch(output); found != nil {
			pid, _ := strconv.Atoi(string(found[1]))
			if !alive(pid) {
				t.Fatalf("the shell says it is process %d, and there is none", pid)
			}
			return c, pid
		}
	}
}

// endsWithError reads a socket to its end, and fails the test unless the
// end is the error message with the code and a normal close.
func endsWithError(t *testing.T, what string, c *websocket.Conn, code wire.Code) {
	t.Helper()
	for {
		m := read(c)
		if m.err != nil {
			t.Fatalf("%s: the socket ended with %v, want the error message with the code %s first", what, m.err, code)
		}
		var got struct {
			Error *wire.Error `json:"error"`
		}
		if m.typ != websocket.MessageText || json.Unmarshal(m.data, &got) != nil || got.Error == nil {
			continue // a line of the log, or output of the shell
		}
		if got.Error.Code != code {
			t.Fatalf("%s: the error message is %s, want the code %s", what, m.data, code)
		}
		if m = read(c); websocket.CloseStatus(m.err) != websocket.StatusNormalClosure {
			t.Fatalf("%s: after its error message the socket sent %q (%v), want a normal close", what, m.data, m.err)
		}
		return
	}
}

// Review Focus 2, through the real relay: a zathras that goes while its
// log and its shell are open ends both sockets with the reason.
func TestHTTPOfAZathrasThatGoesWhileItsLogAndItsShellAreOpen(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	z := h.pods[0]
	h.await(h.rollout("--stdout", "hello"), wire.RolloutCompleted)

	logs := p.socket("/api/zathras/" + string(z.id) + "/logs?follow=1")
	if m := read(logs); m.err != nil || m.typ != websocket.MessageText {
		t.Fatalf("the log sent %q (%v), want a line", m.data, m.err)
	}
	shell, pid := shellPID(t, p, z.id, "sleep 300")

	// The connection of the zathras to draal is cut. It dials again, as
	// the same zathras; what was open on the old connection is over.
	z.cut()
	endsWithError(t, "the log", logs, wire.CodeUnavailable)
	endsWithError(t, "the shell", shell, wire.CodeUnavailable)
	eventually(t, "the shell of the zathras that went to end", func() bool { return !alive(pid) })
}

// A socket of a shell that the page closes ends the session and the
// shell: the process in the container, which is found by the ID the
// shell printed, is gone.
func TestHTTPShellThatThePageClosesEndsItsProcess(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	for how, leave := range map[string]func(c *websocket.Conn){
		"with a close": func(c *websocket.Conn) { c.Close(websocket.StatusNormalClosure, "") },
		"by going":     func(c *websocket.Conn) { c.CloseNow() },
	} {
		// The shell waits in a command, as one that is in use does.
		c, pid := shellPID(t, p, h.pods[0].id, "sleep 300")
		leave(c)
		eventually(t, "the shell to end when the page left "+how, func() bool { return !alive(pid) })
	}
}

// The same for a shell whose program takes no input while the page
// sends more than the terminal and the streams to the zathras hold. This
// is the case in which the relay's Write to the zathras waits, the
// handler holds what it may hold of the input and reads no more, and
// what the page sends backs up in the page: its writes are held up.
//
// It ends in one of two ways. The page goes while its input waits; or
// the handler gives the input up after WriteWait and closes the socket
// with 1009 and its reason. In both the process must be gone. This test
// holds the first: it sees the typing come to a halt with most of it
// not taken, sees that the socket is open still, and goes then. The
// handler is given a WriteWait of 15 seconds for that, between the
// second or so the test needs to see the halt and the deadline within
// which the session has to have ended after the page went: a handler
// whose reader waits for room reads nothing, the going of the page
// neither, and ends the session when that wait is over.
//
// The handler does not wait for the relay's Write; that the process ends
// all the same is the zathras's doing, which ends the shell when draal's
// side of the stream is closed.
func TestHTTPShellThatTakesNoInputEndsItsProcessWhenThePageGoes(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.webWith(web.Config{WriteWait: 15 * time.Second})
	// exec: the process that takes no input has the ID the shell printed.
	c, pid := shellPID(t, p, h.pods[0].id, "exec sleep 300")
	// What the terminal echoes is read, as a page reads it, to the end of
	// the socket; how it ended is kept.
	ended := make(chan error, 1)
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				ended <- err
				return
			}
		}
	}()

	// 16 MiB are typed, in lines, in messages of 16 KiB. A terminal in
	// its usual mode drops input that has no line end and so never backs
	// up; of whole lines that nobody reads it takes a few kilobytes. The
	// streams between draal and the zathras hold about a megabyte, the
	// handler 64 KiB, and the connection of the page what the system
	// gives it: most of the 16 MiB can never be taken.
	const size = 16 << 20
	chunk := bytes.Repeat([]byte("0123456789abcde\n"), 1<<10)
	typing, stop := context.WithCancel(context.Background())
	defer stop()
	var typed atomic.Int64
	flooded := make(chan error, 1)
	go func() {
		for typed.Load() < size {
			if err := c.Write(typing, websocket.MessageBinary, chunk); err != nil {
				flooded <- err
				return
			}
			typed.Add(int64(len(chunk)))
		}
		flooded <- nil
	}()
	// open fails the test if the session has ended in the other way, or
	// in any, before the page went.
	open := func(when string) {
		t.Helper()
		select {
		case err := <-ended:
			t.Fatalf("%s the socket ended with %v after %d bytes were typed; want it open, and the typing held up, until the page goes", when, err, typed.Load())
		case err := <-flooded:
			t.Fatalf("%s the typing ended with %v after %d bytes; want it to wait for a program that reads nothing", when, err, typed.Load())
		default:
		}
	}
	// The typing is held up: nothing more was taken for a second, though
	// not all is typed.
	last, since := int64(-1), time.Now()
	eventually(t, "the typing to come to a halt", func() bool {
		open("while the typing was awaited to halt,")
		if now := typed.Load(); now != last {
			last, since = now, time.Now()
		}
		return last > 0 && time.Since(since) > time.Second
	})
	if last >= size {
		t.Fatalf("all %d bytes were taken: the typing was never held up", last)
	}
	if !alive(pid) {
		t.Fatalf("the process %d has ended before the page went", pid)
	}
	open("when the page was about to go,")
	t.Logf("the typing came to a halt after %d of %d bytes; the page goes while the rest waits", last, size)

	// The page goes while its input waits.
	c.CloseNow()
	select {
	case err := <-flooded:
		if err == nil {
			t.Error("the typing that was held up ended without an error when the page went")
		}
	case <-time.After(deadline):
		t.Fatalf("the typing still waits %v after the page went", deadline)
	}
	if now := typed.Load(); now >= size {
		t.Errorf("%d bytes were taken when the page had gone, want fewer than the %d it meant to type", now, size)
	}
	eventually(t, "the process that took no input to end", func() bool { return !alive(pid) })
}
