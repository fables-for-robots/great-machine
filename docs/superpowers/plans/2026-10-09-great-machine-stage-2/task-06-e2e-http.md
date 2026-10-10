### Task 6: e2e through HTTP

**Files:**
- Modify: `e2e/harness_test.go` (the harness keeps the draal that runs)
- Test: `e2e/http_test.go`

**Interfaces:**
- Consumes:
  - Tasks 1 and 2: `web.New(web.Config{Controller: <the harness's *draal.Draal>, Log: quiet})` and the HTTP API as the plan's Interfaces have it.
  - `(*pod).cut()` of the harness, which closes the connection a zathras has to draal, and `alive(pid int) bool`.
  - The harness of `e2e/harness_test.go`: `start(t, options{zathrases: n}) *harness`, `h.rollout(args ...string) wire.RolloutInfo`, `h.await(info, state) wire.Fleet`, `h.pods[i].id`, `h.pods[i].name`, `h.opKey`, `serviceOf`, `rolloutOf`, `zathrasesOf`, `runs`, `eventually`, and the constants `deadline`, `service` and `quiet`.
  - `github.com/coder/websocket` as the client.
- Produces: the "through HTTP" half of the spec's section 14. `(*harness).web() *page` serves the HTTP API of the harness's draal on an `httptest.Server`; `(*page).get(path, host)` and `(*page).socket(path)` ask it, the socket with the `Origin` of the page as a browser sends it. `TestHTTPUnderAnotherHostName` (Review Focus 1): under another host name the API is answered, and the fleet and a shell are given to a page whose `Origin` is that name; a page of another origin is refused both sockets with 403. Beyond the happy paths: a zathras whose connection is cut while its log and its shell are open ends both sockets with `unavailable` (Review Focus 2 through the real relay); a shell socket that the page closes, or leaves, ends the process in the container, which the test finds by the process ID the shell printed and watches with `alive`; the same for a shell whose program takes no input while the page types megabytes.

This task does not depend on Task 5.

- [ ] **Step 1: Write the failing tests**

`e2e/http_test.go`:

```go
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// absent is an endpoint ID that no zathras of a harness has.
const absent = wire.ID("00000000000000000000000000000000000000000000000000000000000000aa")

// page is the web page's side of a harness: the HTTP API in front of
// its draal, as draal serves it, and a client of it.
type page struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
}

// web serves the HTTP API of the draal that runs now. The server and the
// connections of its client are closed when the test ends.
func (h *harness) web() *page {
	h.t.Helper()
	h.mu.Lock()
	d := h.draal
	h.mu.Unlock()
	p := &page{
		t:      h.t,
		srv:    httptest.NewServer(web.New(web.Config{Controller: d, Log: quiet})),
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

// Review Focus 1: the page reached under another name, as through a
// tunnel or a proxy that keeps the outside host name. It works as under
// localhost, its sockets included, when the page was loaded from the
// host it asks; a page of another origin still gets no socket.
func TestHTTPUnderAnotherHostName(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	const name = "draal.example.com"
	for _, path := range []string{"/api/services/" + service + "/rollouts", "/api/zathras/" + string(h.pods[0].id) + "/history"} {
		if status, body := p.get(path, name); status != http.StatusOK {
			t.Errorf("GET %s as %s = %d %q, want 200", path, name, status, body)
		}
	}
	// The page itself: built into the program or not, it is not refused.
	if status, body := p.get("/", name); status != http.StatusOK && status != http.StatusNotImplemented {
		t.Errorf("GET / as %s = %d %q, want the page, or that it was not built", name, status, body)
	}

	dial := func(path, origin string) (*websocket.Conn, int) {
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		defer cancel()
		c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.srv.URL, "http")+path, &websocket.DialOptions{
			HTTPClient: p.client,
			Host:       name,
			HTTPHeader: http.Header{"Origin": {origin}},
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
	// The page of that name reads the fleet and has a shell.
	watch, status := dial("/api/watch", "https://"+name)
	if watch == nil {
		t.Fatalf("the fleet as the page of %s was refused with %d", name, status)
	}
	var f wire.Fleet
	if m := read(watch); m.err != nil || json.Unmarshal(m.data, &f) != nil || len(zathrasesOf(f)) != 1 {
		t.Errorf("the page of %s was sent %q (%v), want the fleet", name, m.data, m.err)
	}
	shell := "/api/zathras/" + string(h.pods[0].id) + "/shell?term=dumb"
	sh, status := dial(shell, "https://"+name)
	if sh == nil {
		t.Fatalf("a shell as the page of %s was refused with %d", name, status)
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
	// A page of another site, in the same browser, gets neither.
	for _, path := range []string{"/api/watch", shell} {
		if c, status := dial(path, "https://evil.example"); c != nil || status != http.StatusForbidden {
			t.Errorf("%s as a page of another site was answered %d, want 403", path, status)
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
// sends more than the terminal and the stream to the zathras hold: the
// session ends, at the latest when the page goes, and the process with
// it. This is the case in which the relay's Write to the zathras waits.
// The handler does not wait for that Write; that the process ends all
// the same is the zathras's doing, which ends the shell when draal's
// side of the stream is closed.
func TestHTTPShellThatTakesNoInputEndsItsProcessWhenThePageGoes(t *testing.T) {
	t.Parallel()
	h := start(t, options{zathrases: 1})
	p := h.web()
	// exec: the process that takes no input has the ID the shell printed.
	c, pid := shellPID(t, p, h.pods[0].id, "exec sleep 300")
	// What the terminal echoes is read, as a page reads it.
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	// Up to 16 MiB are typed, in messages of 32 KiB. If the socket is
	// closed for it (1009: the shell does not take its input), the
	// session has ended already.
	chunk := []byte(strings.Repeat("x", 32<<10-1) + "\n")
	typed := 0
	for ; typed < 16<<20; typed += len(chunk) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.Write(ctx, websocket.MessageBinary, chunk)
		cancel()
		if err != nil {
			break
		}
	}
	t.Logf("%d bytes were typed into a shell that takes none", typed)
	c.CloseNow()
	eventually(t, "the process that took no input to end", func() bool { return !alive(pid) })
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./e2e/ -run HTTP`
Expected: FAIL: the package does not build, with `h.draal undefined (type *harness has no field or method draal)`.

- [ ] **Step 3: The harness keeps its draal**

In `e2e/harness_test.go`, the struct `harness` gets a field beside `addr`:

```go
	mu        sync.Mutex
	addr      netip.AddrPort // where draal listens now
	draal     *draal.Draal   // the draal that runs now
	stopDraal func()
}
```

and `startDraal` sets it where it sets `addr`:

```go
	var once sync.Once
	h.mu.Lock()
	h.addr = l.LocalAddr()
	h.draal = d
	h.stopDraal = func() {
		once.Do(func() {
			cancel()
			if err := <-served; err != nil {
				h.t.Errorf("Serve: %v", err)
			}
			l.Close()
			database.Close()
		})
	}
	h.mu.Unlock()
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./e2e/ -run HTTP -v`
Expected: `--- PASS` for `TestHTTPWatchFollowsARollout`, `TestHTTPHistory`, `TestHTTPLogs`, `TestHTTPShell`, `TestHTTPOfAZathrasThatIsNotConnected`, `TestHTTPUnderAnotherHostName`, `TestHTTPOfAZathrasThatGoesWhileItsLogAndItsShellAreOpen`, `TestHTTPShellThatThePageClosesEndsItsProcess` and `TestHTTPShellThatTakesNoInputEndsItsProcessWhenThePageGoes`, and `ok  	github.com/fables-for-robots/great-machine/e2e`.

Run: `nix develop .#bare --command go test ./...`
Expected: `ok` for every package that has tests, `web` and `web/demo` among them.

Run: `nix develop .#bare --command sh -c 'gofmt -l .; go vet ./...'`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add e2e/
git commit -m "E2E: the fleet, a rollout, the log and a shell through HTTP"
```
