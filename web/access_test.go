package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// request is a request as a test makes one: what ask cannot say.
type request struct {
	method, path string
	host         string // in place of the server's address, if not empty
	origin       string // the Origin, if not empty
	body         string
	cookies      []*http.Cookie
}

// do sends a request and returns the answer. Redirects are not followed.
func do(t *testing.T, srv *httptest.Server, rq request) answer {
	t.Helper()
	req, err := http.NewRequest(rq.method, srv.URL+rq.path, strings.NewReader(rq.body))
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", rq.method, rq.path, err)
	}
	if rq.host != "" {
		req.Host = rq.host
	}
	if rq.origin != "" {
		req.Header.Set("Origin", rq.origin)
	}
	for _, c := range rq.cookies {
		req.AddCookie(c)
	}
	client := &http.Client{
		Timeout:       deadline,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", rq.method, rq.path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the answer: %v", rq.method, rq.path, err)
	}
	return answer{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

func TestMe(t *testing.T) {
	c := &fake{me: func() (wire.Identity, error) {
		return wire.Identity{ID: zathrasB, Grants: []wire.Grant{
			{ID: zathrasB, Role: wire.RoleAdmin, Fixed: true},
			{ID: zathrasB, Role: wire.RoleDeployer, Service: "web", Note: "ci", By: zathrasA, At: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)},
		}}, nil
	}}
	srv := serve(t, Config{Client: c})
	a := ask(t, srv, http.MethodGet, "/api/me", "")
	if a.status != http.StatusOK || a.header.Get("Content-Type") != "application/json" || a.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the answer is %d as %q with Cache-Control %q", a.status, a.header.Get("Content-Type"), a.header.Get("Cache-Control"))
	}
	// The JSON is the contract of the page: it is compared as text.
	const want = `{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","grants":[` +
		`{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","role":"admin","at":"0001-01-01T00:00:00Z","fixed":true},` +
		`{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","role":"deployer","service":"web","note":"ci",` +
		`"by":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","at":"2026-10-13T09:00:00Z"}` +
		`]}` + "\n"
	if a.body != want {
		t.Errorf("the answer is\n%s\nwant\n%s", a.body, want)
	}
}

// A key with no grant has an identity all the same: its ID, which is
// what an admin needs to grant it one, and a list that is empty and not
// null.
func TestMeOfAKeyWithNoGrant(t *testing.T) {
	srv := serve(t, Config{Client: &fake{me: func() (wire.Identity, error) { return wire.Identity{ID: zathrasA}, nil }}})
	a := ask(t, srv, http.MethodGet, "/api/me", "")
	if want := `{"id":"` + string(zathrasA) + `","grants":[]}` + "\n"; a.status != http.StatusOK || a.body != want {
		t.Errorf("the answer is %d %q, want %q", a.status, a.body, want)
	}
}

func TestGrants(t *testing.T) {
	c := &fake{grants: func() ([]wire.Grant, error) {
		return []wire.Grant{
			{ID: zathrasA, Role: wire.RoleAdmin, Fixed: true},
			{ID: zathrasB, Role: wire.RoleZathras, Service: "web", By: zathrasA, At: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)},
		}, nil
	}}
	srv := serve(t, Config{Client: c})
	a := ask(t, srv, http.MethodGet, "/api/grants", "")
	const want = `{"grants":[` +
		`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","role":"admin","at":"0001-01-01T00:00:00Z","fixed":true},` +
		`{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","role":"zathras","service":"web",` +
		`"by":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","at":"2026-10-13T09:00:00Z"}` +
		`]}` + "\n"
	if a.status != http.StatusOK || a.body != want {
		t.Errorf("the answer is %d\n%s\nwant\n%s", a.status, a.body, want)
	}
	if a.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", a.header.Get("Cache-Control"))
	}

	srv = serve(t, Config{Client: &fake{grants: func() ([]wire.Grant, error) { return nil, nil }}})
	if a := ask(t, srv, http.MethodGet, "/api/grants", ""); a.status != http.StatusOK || a.body != `{"grants":[]}`+"\n" {
		t.Errorf("no grants are %d %q, want an empty list and not null", a.status, a.body)
	}
}

func TestGrant(t *testing.T) {
	var asked []wire.GrantRequest
	c := &fake{grant: func(req wire.GrantRequest) error {
		asked = append(asked, req)
		return nil
	}}
	srv := serve(t, Config{Client: c})
	a := do(t, srv, request{method: http.MethodPost, path: "/api/grants",
		body: `{"id":"` + string(zathrasB) + `","role":"deployer","service":"web","note":"ci"}`})
	if a.status != http.StatusNoContent || a.body != "" {
		t.Fatalf("the answer is %d %q, want 204 and nothing", a.status, a.body)
	}
	want := wire.GrantRequest{ID: zathrasB, Role: wire.RoleDeployer, Service: "web", Note: "ci"}
	if len(asked) != 1 || asked[0] != want {
		t.Errorf("the client was asked to grant %+v, want %+v once", asked, want)
	}
	// Without a service and a note: every service.
	a = do(t, srv, request{method: http.MethodPost, path: "/api/grants", body: `{"id":"` + string(zathrasB) + `","role":"admin"}`})
	if a.status != http.StatusNoContent || len(asked) != 2 || asked[1] != (wire.GrantRequest{ID: zathrasB, Role: wire.RoleAdmin}) {
		t.Errorf("the answer is %d, and the client was asked %+v", a.status, asked)
	}
}

// What is no grant as JSON is refused before the client is asked: no
// JSON, something behind it, a field a grant has not, and more than a
// grant can be.
func TestGrantOfWhatIsNoGrant(t *testing.T) {
	c := &fake{grant: func(req wire.GrantRequest) error {
		t.Errorf("the client was asked to grant %+v", req)
		return nil
	}}
	srv := serve(t, Config{Client: c})
	for what, body := range map[string]string{
		"nothing":        ``,
		"no JSON":        `id=abc&role=admin`,
		"a list":         `[{"id":"a","role":"admin"}]`,
		"two of them":    `{"id":"a","role":"admin"}{"id":"b","role":"admin"}`,
		"another field":  `{"id":"a","role":"admin","fixed":true}`,
		"a role of 7":    `{"id":"a","role":7}`,
		"more than fits": `{"id":"a","role":"admin","note":"` + strings.Repeat("n", 70<<10) + `"}`,
	} {
		a := do(t, srv, request{method: http.MethodPost, path: "/api/grants", body: body})
		if msg := wantRefusal(t, a, http.StatusBadRequest, wire.CodeBadRequest); msg == "" {
			t.Errorf("%s: the refusal says nothing", what)
		}
	}
}

func TestRevoke(t *testing.T) {
	var asked []wire.RevokeRequest
	c := &fake{revoke: func(req wire.RevokeRequest) error {
		asked = append(asked, req)
		return nil
	}}
	srv := serve(t, Config{Client: c})
	a := do(t, srv, request{method: http.MethodDelete, path: "/api/grants?id=" + string(zathrasB) + "&role=deployer&service=web"})
	if a.status != http.StatusNoContent || a.body != "" {
		t.Fatalf("the answer is %d %q, want 204 and nothing", a.status, a.body)
	}
	if want := (wire.RevokeRequest{ID: zathrasB, Role: wire.RoleDeployer, Service: "web"}); len(asked) != 1 || asked[0] != want {
		t.Errorf("the client was asked to revoke %+v, want %+v once", asked, want)
	}
	// A grant for every service has no service to name.
	a = do(t, srv, request{method: http.MethodDelete, path: "/api/grants?id=" + string(zathrasB) + "&role=admin"})
	if a.status != http.StatusNoContent || len(asked) != 2 || asked[1] != (wire.RevokeRequest{ID: zathrasB, Role: wire.RoleAdmin}) {
		t.Errorf("the answer is %d, and the client was asked %+v", a.status, asked)
	}
}

// What draal refuses is passed on as it is, with the status of its code.
func TestWhatDraalRefusesOfTheGrants(t *testing.T) {
	for code, status := range map[wire.Code]int{
		wire.CodeUnauthorized: http.StatusForbidden,
		wire.CodeBadRequest:   http.StatusBadRequest,
		wire.CodeNotFound:     http.StatusNotFound,
		wire.CodeUnavailable:  http.StatusServiceUnavailable,
	} {
		refusal := wire.Errorf(code, "draal says %s", code)
		c := &fake{
			me:     func() (wire.Identity, error) { return wire.Identity{}, refusal },
			grants: func() ([]wire.Grant, error) { return nil, refusal },
			grant:  func(wire.GrantRequest) error { return refusal },
			revoke: func(wire.RevokeRequest) error { return refusal },
		}
		srv := serve(t, Config{Client: c})
		for _, rq := range []request{
			{method: http.MethodGet, path: "/api/me"},
			{method: http.MethodGet, path: "/api/grants"},
			{method: http.MethodPost, path: "/api/grants", body: `{"id":"a","role":"admin"}`},
			{method: http.MethodDelete, path: "/api/grants?id=a&role=admin"},
		} {
			a := do(t, srv, rq)
			if msg := wantRefusal(t, a, status, code); msg != refusal.Message {
				t.Errorf("%s %s: the refusal says %q, want what draal said, %q", rq.method, rq.path, msg, refusal.Message)
			}
		}
	}
}

func TestTheGrantsAreAskedForWithTheirMethods(t *testing.T) {
	srv := serve(t, Config{Client: &fake{}})
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		a := do(t, srv, request{method: method, path: "/api/grants"})
		wantRefusal(t, a, http.StatusMethodNotAllowed, wire.CodeBadRequest)
		if got := a.header.Get("Allow"); got != "GET, POST, DELETE" {
			t.Errorf("%s /api/grants: Allow is %q, want GET, POST, DELETE", method, got)
		}
	}
	a := do(t, srv, request{method: http.MethodPost, path: "/api/me"})
	wantRefusal(t, a, http.StatusMethodNotAllowed, wire.CodeBadRequest)
}

// A request that changes something has to come from the page: one whose
// Origin is another host is refused, and the client is not asked. A
// program that is no browser sends no Origin, and is answered.
func TestAChangeFromAnotherOriginIsRefused(t *testing.T) {
	asked := 0
	c := &fake{
		grant:  func(wire.GrantRequest) error { asked++; return nil },
		revoke: func(wire.RevokeRequest) error { asked++; return nil },
	}
	srv := serve(t, Config{Client: c})
	host := strings.TrimPrefix(srv.URL, "http://")
	_, port, _ := strings.Cut(host, ":")
	changes := []request{
		{method: http.MethodPost, path: "/api/grants", body: `{"id":"a","role":"admin"}`},
		{method: http.MethodDelete, path: "/api/grants?id=a&role=admin"},
	}
	for _, origin := range []string{"https://evil.example", "http://127.0.0.1:1", "http://localhost:" + port, "null"} {
		for _, rq := range changes {
			rq.origin = origin
			a := do(t, srv, rq)
			wantRefusal(t, a, http.StatusForbidden, wire.CodeUnauthorized)
		}
	}
	if asked != 0 {
		t.Fatalf("the client was asked %d times by requests of another origin", asked)
	}
	for _, origin := range []string{"", srv.URL, "http://" + strings.ToUpper(host)} {
		for _, rq := range changes {
			rq.origin = origin
			if a := do(t, srv, rq); a.status != http.StatusNoContent {
				t.Errorf("%s %s with the origin %q = %d %q, want 204", rq.method, rq.path, origin, a.status, a.body)
			}
		}
	}
	if asked != 6 {
		t.Errorf("the client was asked %d times, want 6", asked)
	}
}
