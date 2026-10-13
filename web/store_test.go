package web

import (
	"net/http"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

// echo is the page of a store for the tests: it answers with the path it
// was asked for.
func echo(path string) (wire.StorePage, error) {
	return wire.StorePage{Status: http.StatusOK, Type: "text/plain; charset=utf-8", Body: []byte("GET " + path)}, nil
}

// The store's page is what draal answers for it, under /store/, asked
// with that part of the path taken off and with its query:
// jaccard-store's admin page asks for what it shows by paths beside its
// own.
func TestTheStoresPageIsAskedOfDraal(t *testing.T) {
	srv := serve(t, Config{Client: &fake{storePage: echo}})
	for path, want := range map[string]string{
		"/store/":                           "GET /",
		"/store/app.js":                     "GET /app.js",
		"/store/api/stats":                  "GET /api/stats",
		"/store/api/packs/ab":               "GET /api/packs/ab",
		"/store/api/refs?prefix=%2Fs&limit": "GET /api/refs?prefix=%2Fs&limit",
	} {
		a := ask(t, srv, http.MethodGet, path, "")
		if a.status != http.StatusOK || a.body != want || a.header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("GET %s = %d %q as %q, want what the store's page answers to %q", path, a.status, a.body, a.header.Get("Content-Type"), want)
		}
	}
	// Without the slash the paths beside the page would be beside /store.
	a := ask(t, srv, http.MethodGet, "/store", "")
	if a.status != http.StatusMovedPermanently || a.header.Get("Location") != "/store/" {
		t.Errorf("GET /store = %d to %q, want a redirect to /store/", a.status, a.header.Get("Location"))
	}
	// A path that begins like it is a path of the page.
	if a := ask(t, srv, http.MethodGet, "/stores", ""); a.status != http.StatusOK || a.body == "GET /s" {
		t.Errorf("GET /stores = %d %q, want the page", a.status, a.body)
	}
}

// The page of the store is read-only, and draal is asked for a GET of
// it and nothing else.
func TestTheStoresPageIsAskedForWithGET(t *testing.T) {
	c := &fake{storePage: func(path string) (wire.StorePage, error) {
		t.Errorf("draal was asked for %s", path)
		return echo(path)
	}}
	srv := serve(t, Config{Client: c})
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		a := ask(t, srv, method, "/store/api/stats", "")
		wantRefusal(t, a, http.StatusMethodNotAllowed, wire.CodeBadRequest)
	}
}

// What the store's page answers is passed on with its status and its
// type, an answer that is no 200 too; one with no type is not left for
// the browser to guess.
func TestWhatTheStoresPageAnswersIsPassedOn(t *testing.T) {
	c := &fake{storePage: func(path string) (wire.StorePage, error) {
		switch path {
		case "/api/nothing":
			return wire.StorePage{Status: http.StatusNotFound, Type: "application/json", Body: []byte(`{"error":"no such resource"}`)}, nil
		case "/blob":
			return wire.StorePage{Status: http.StatusOK, Body: []byte("<script>")}, nil
		case "/empty":
			return wire.StorePage{Status: http.StatusOK, Type: "text/css; charset=utf-8"}, nil
		}
		return wire.StorePage{}, nil // a status that is none
	}}
	srv := serve(t, Config{Client: c})
	a := ask(t, srv, http.MethodGet, "/store/api/nothing", "")
	if a.status != http.StatusNotFound || a.body != `{"error":"no such resource"}` || a.header.Get("Content-Type") != "application/json" {
		t.Errorf("an answer of the store's page that is 404 came as %d %q as %q", a.status, a.body, a.header.Get("Content-Type"))
	}
	if a = ask(t, srv, http.MethodGet, "/store/blob", ""); a.status != http.StatusOK || a.header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("an answer with no type came as %d as %q, want application/octet-stream", a.status, a.header.Get("Content-Type"))
	}
	if a = ask(t, srv, http.MethodGet, "/store/empty", ""); a.status != http.StatusOK || a.body != "" {
		t.Errorf("an answer with no body came as %d %q", a.status, a.body)
	}
	// An answer with a status that is none is draal's failure, and not
	// passed on as it is.
	a = ask(t, srv, http.MethodGet, "/store/whatever", "")
	wantRefusal(t, a, http.StatusInternalServerError, wire.CodeInternal)
}

// The answers of the store's page have the headers every answer has,
// the policy of this page among them: of the store's page itself the
// handler has the status, the type and the body alone.
func TestTheStoresPageHasTheSecurityHeaders(t *testing.T) {
	srv := serve(t, Config{Client: &fake{storePage: echo}})
	for _, path := range []string{"/store/", "/store/api/stats", "/store"} {
		a := ask(t, srv, http.MethodGet, path, "")
		for header, want := range map[string]string{
			"Content-Security-Policy": contentSecurityPolicy,
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := a.header.Values(header); len(got) != 1 || got[0] != want {
				t.Errorf("GET %s: %s is %q, want %q once", path, header, got, want)
			}
		}
	}
	// What the store holds changes: nothing of its page is kept.
	if a := ask(t, srv, http.MethodGet, "/store/api/stats", ""); a.header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control of the store's page is %q, want no-store", a.header.Get("Cache-Control"))
	}
}

// What draal refuses of the store's page is the refusal: the page is an
// admin's, and a draal that cannot be reached answers nothing.
func TestWhatDraalRefusesOfTheStoresPage(t *testing.T) {
	for code, status := range map[wire.Code]int{
		wire.CodeUnauthorized: http.StatusForbidden,
		wire.CodeUnavailable:  http.StatusServiceUnavailable,
		wire.CodeNotFound:     http.StatusNotFound,
	} {
		refusal := wire.Errorf(code, "draal says %s", code)
		srv := serve(t, Config{Client: &fake{storePage: func(string) (wire.StorePage, error) { return wire.StorePage{}, refusal }}})
		a := ask(t, srv, http.MethodGet, "/store/", "")
		if msg := wantRefusal(t, a, status, code); msg != refusal.Message {
			t.Errorf("the refusal says %q, want what draal said, %q", msg, refusal.Message)
		}
	}
}
