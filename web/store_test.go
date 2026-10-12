package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

// echo is a store's page for the tests: it answers with the method and
// the path it was asked with, under a policy of its own, as the page of
// jaccard-store has one.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", echoPolicy)
	fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
})

const echoPolicy = "default-src 'none'"

// The store's page is the handler it is given, under /store/, asked with
// that part of the path taken off: jaccard-store's admin page asks for
// what it shows by paths beside its own.
func TestTheStoresPageIsServedUnderStore(t *testing.T) {
	srv := serve(t, Config{Store: echo})
	for path, want := range map[string]string{
		"/store/":             "GET /",
		"/store/app.js":       "GET /app.js",
		"/store/api/stats":    "GET /api/stats",
		"/store/api/packs/ab": "GET /api/packs/ab",
	} {
		if a := ask(t, srv, http.MethodGet, path, ""); a.status != http.StatusOK || a.body != want {
			t.Errorf("GET %s = %d %q, want what the store's page answers to %q", path, a.status, a.body, want)
		}
	}
	// What the page makes of a method is its own business.
	if a := ask(t, srv, http.MethodPost, "/store/api/stats", ""); a.body != "POST /api/stats" {
		t.Errorf("POST /store/api/stats = %d %q, want it handed to the store's page", a.status, a.body)
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

// The answers of the store's page have the headers every answer has.
func TestTheStoresPageHasTheSecurityHeaders(t *testing.T) {
	srv := serve(t, Config{Store: echo})
	for _, path := range []string{"/store/", "/store/api/stats", "/store"} {
		a := ask(t, srv, http.MethodGet, path, "")
		for header, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := a.header.Values(header); len(got) != 1 || got[0] != want {
				t.Errorf("GET %s: %s is %q, want %q once", path, header, got, want)
			}
		}
		// The answer is the store's page's, not that of the page of
		// the fleet, which has these headers too; and it has one
		// policy, the store's own. The redirect is draal's.
		got := a.header.Values("Content-Security-Policy")
		if path == "/store" {
			if len(got) != 1 {
				t.Errorf("GET %s: there are %d policies, want one: %q", path, len(got), got)
			}
			continue
		}
		if !strings.HasPrefix(a.body, "GET /") {
			t.Errorf("GET %s = %d %q, want the answer of the store's page", path, a.status, a.body)
		}
		if len(got) != 1 || got[0] != echoPolicy {
			t.Errorf("GET %s: the policies are %q, want the store's own, %q, alone", path, got, echoPolicy)
		}
	}
}

// A draal without a store's page has nothing under /store: not the page
// of the fleet either, which answers every other path.
func TestWithoutAStoreThereIsNoStoresPage(t *testing.T) {
	srv := serve(t, Config{})
	for _, path := range []string{"/store/", "/store", "/store/api/stats"} {
		a := ask(t, srv, http.MethodGet, path, "")
		if msg := wantRefusal(t, a, http.StatusNotFound, wire.CodeNotFound); msg == "" {
			t.Errorf("GET %s: the refusal says nothing", path)
		}
	}
}
