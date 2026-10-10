package web

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

func TestRollouts(t *testing.T) {
	created := time.Date(2026, 10, 9, 12, 0, 0, 123456789, time.UTC)
	var asked []string
	c := &fake{rollouts: func(service string) ([]wire.RolloutInfo, error) {
		asked = append(asked, service)
		return []wire.RolloutInfo{
			{ID: 7, Service: "web", Generation: 4, Parallel: 1, State: wire.RolloutActivating, Created: created, CreatedBy: zathrasB},
			{ID: 6, Service: "web", Generation: 3, State: wire.RolloutHalted, Reason: "web-0: exited with status 3 after 10ms", Created: created.Add(-time.Hour), CreatedBy: zathrasB, Finished: created.Add(-time.Minute)},
		}, nil
	}}
	srv := serve(t, Config{Controller: c})
	a := ask(t, srv, http.MethodGet, "/api/services/web/rollouts", "")
	if a.status != http.StatusOK || a.header.Get("Content-Type") != "application/json" || a.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the answer is %d as %q with Cache-Control %q", a.status, a.header.Get("Content-Type"), a.header.Get("Cache-Control"))
	}
	// The JSON is the contract of the page: it is compared as text.
	const want = `{"rollouts":[` +
		`{"id":7,"service":"web","generation":4,"parallel":1,"state":"activating","created":"2026-10-09T12:00:00.123456789Z",` +
		`"created_by":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","finished":"0001-01-01T00:00:00Z","prepared":0,"activated":0,"awaited":0},` +
		`{"id":6,"service":"web","generation":3,"parallel":0,"state":"halted","reason":"web-0: exited with status 3 after 10ms","created":"2026-10-09T11:00:00.123456789Z",` +
		`"created_by":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","finished":"2026-10-09T11:59:00.123456789Z","prepared":0,"activated":0,"awaited":0}` +
		`]}` + "\n"
	if a.body != want {
		t.Errorf("the answer is\n%s\nwant\n%s", a.body, want)
	}
	if len(asked) != 1 || asked[0] != "web" {
		t.Errorf("the controller was asked for %q, want web once", asked)
	}
}

func TestRolloutsOfAServiceWithoutOneIsAnEmptyList(t *testing.T) {
	srv := serve(t, Config{Controller: &fake{rollouts: func(string) ([]wire.RolloutInfo, error) { return nil, nil }}})
	if a := ask(t, srv, http.MethodGet, "/api/services/new/rollouts", ""); a.status != http.StatusOK || a.body != `{"rollouts":[]}`+"\n" {
		t.Errorf("the answer is %d %q, want an empty list and not null", a.status, a.body)
	}
}

func TestRolloutsOfWhatIsNoNameOfAService(t *testing.T) {
	c := &fake{rollouts: func(service string) ([]wire.RolloutInfo, error) {
		t.Errorf("the controller was asked for %q", service)
		return nil, nil
	}}
	srv := serve(t, Config{Controller: c})
	for _, name := range []string{"Web", "-web", "web-", "we_b", "a%2Fb", "%20", strings.Repeat("a", 64)} {
		a := ask(t, srv, http.MethodGet, "/api/services/"+name+"/rollouts", "")
		wantRefusal(t, a, http.StatusBadRequest, wire.CodeBadRequest)
	}
}

func TestHistory(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var asked []wire.ID
	c := &fake{history: func(id wire.ID) ([]wire.Sample, error) {
		asked = append(asked, id)
		return []wire.Sample{
			{At: at, CPU: 0.25, Memory: 1 << 20, MemoryLimit: 1 << 30, DiskRead: 1, DiskWrite: 2.5, NetRx: 3, NetTx: 4},
			{At: at.Add(2 * time.Second), CPU: 1.5, Memory: 2 << 20},
		}, nil
	}}
	srv := serve(t, Config{Controller: c})
	a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", "")
	const want = `{"samples":[` +
		`{"at":"2026-10-09T12:00:00Z","cpu":0.25,"memory":1048576,"memory_limit":1073741824,"disk_read":1,"disk_write":2.5,"net_rx":3,"net_tx":4},` +
		`{"at":"2026-10-09T12:00:02Z","cpu":1.5,"memory":2097152,"memory_limit":0,"disk_read":0,"disk_write":0,"net_rx":0,"net_tx":0}` +
		`]}` + "\n"
	if a.status != http.StatusOK || a.body != want {
		t.Errorf("the answer is %d\n%s\nwant 200\n%s", a.status, a.body, want)
	}
	if len(asked) != 1 || asked[0] != zathrasA {
		t.Errorf("the controller was asked for %q, want %s once", asked, zathrasA)
	}
}

func TestHistoryOfAZathrasWithoutSamplesIsAnEmptyList(t *testing.T) {
	srv := serve(t, Config{Controller: &fake{history: func(wire.ID) ([]wire.Sample, error) { return nil, nil }}})
	if a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", ""); a.status != http.StatusOK || a.body != `{"samples":[]}`+"\n" {
		t.Errorf("the answer is %d %q, want an empty list and not null", a.status, a.body)
	}
}

func TestWhatIsNoEndpointIDIsABadRequest(t *testing.T) {
	c := &fake{history: func(id wire.ID) ([]wire.Sample, error) {
		t.Errorf("the controller was asked for %q", id)
		return nil, nil
	}}
	srv := serve(t, Config{Controller: c})
	for _, id := range []string{
		"x",
		"web-0",
		string(zathrasA)[:63],
		string(zathrasA) + "a",
		strings.ToUpper(string(zathrasB)), // an ID is in lower case
		string(zathrasA)[:63] + "g",
		string(zathrasA)[:63] + "%20",
	} {
		a := ask(t, srv, http.MethodGet, "/api/zathras/"+id+"/history", "")
		wantRefusal(t, a, http.StatusBadRequest, wire.CodeBadRequest)
	}
}

func TestErrorsOfTheControllerAsRefusals(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   wire.Code
	}{
		{wire.Errorf(wire.CodeBadRequest, "bad"), http.StatusBadRequest, wire.CodeBadRequest},
		{wire.Errorf(wire.CodeNotFound, "none"), http.StatusNotFound, wire.CodeNotFound},
		{wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", zathrasA), http.StatusServiceUnavailable, wire.CodeUnavailable},
		{wire.Errorf(wire.CodeUnauthorized, "no"), http.StatusForbidden, wire.CodeUnauthorized},
		// A *wire.Error keeps its code and its message under what wraps it.
		{errors.Join(errors.New("around"), wire.Errorf(wire.CodeUnavailable, "inside")), http.StatusServiceUnavailable, wire.CodeUnavailable},
	}
	for _, tt := range tests {
		srv := serve(t, Config{Controller: &fake{history: func(wire.ID) ([]wire.Sample, error) { return nil, tt.err }}})
		a := ask(t, srv, http.MethodGet, "/api/zathras/"+string(zathrasA)+"/history", "")
		message := wantRefusal(t, a, tt.status, tt.code)
		var e *wire.Error
		errors.As(tt.err, &e)
		if message != e.Message {
			t.Errorf("%v: the message is %q, want %q", tt.err, message, e.Message)
		}
	}
}

// An internal refusal says nothing of what went wrong, whoever made it:
// an error that is no refusal, and a refusal with the code internal, as
// draal makes of what its database said and as a zathras can answer
// with any text. The text is in the log.
func TestAnInternalErrorSaysNothingAndItsTextStaysInTheLog(t *testing.T) {
	var logged bytes.Buffer
	c := &fake{
		history: func(wire.ID) ([]wire.Sample, error) {
			return nil, errors.New("open /var/lib/draal/secret: permission denied")
		},
		rollouts: func(service string) ([]wire.RolloutInfo, error) {
			return nil, wire.Errorf(wire.CodeInternal, "reading the rollouts of %s: open /var/lib/draal/secret: permission denied", service)
		},
	}
	srv := serve(t, Config{Controller: c, Log: slog.New(slog.NewTextHandler(&logged, nil))})
	for _, path := range []string{"/api/zathras/" + string(zathrasA) + "/history", "/api/services/web/rollouts"} {
		logged.Reset()
		a := ask(t, srv, http.MethodGet, path, "")
		if msg := wantRefusal(t, a, http.StatusInternalServerError, wire.CodeInternal); msg != "draal failed to answer; its log says why" {
			t.Errorf("GET %s: the message is %q", path, msg)
		}
		if strings.Contains(a.body, "secret") || strings.Contains(a.body, "permission") {
			t.Errorf("GET %s = %q, want nothing of the error's text in it", path, a.body)
		}
		if !strings.Contains(logged.String(), "permission denied") || !strings.Contains(logged.String(), "level=ERROR") {
			t.Errorf("GET %s: the log is %q, want the error in it", path, logged.String())
		}
	}
}

func TestARefusalWithACodeDraalDoesNotKnowSaysNothing(t *testing.T) {
	var logged bytes.Buffer
	c := &fake{
		rollouts: func(string) ([]wire.RolloutInfo, error) {
			return nil, wire.Errorf(wire.Code("weird"), "the secret of a zathras")
		},
		history: func(wire.ID) ([]wire.Sample, error) {
			return nil, fmt.Errorf("asking the zathras: %w", wire.Errorf(wire.Code("teapot"), "the secret of a zathras"))
		},
	}
	srv := serve(t, Config{Controller: c, Log: slog.New(slog.NewTextHandler(&logged, nil))})
	for _, path := range []string{"/api/zathras/" + string(zathrasA) + "/history", "/api/services/web/rollouts"} {
		logged.Reset()
		a := ask(t, srv, http.MethodGet, path, "")
		if msg := wantRefusal(t, a, http.StatusInternalServerError, wire.CodeInternal); msg != "draal failed to answer; its log says why" {
			t.Errorf("GET %s: the message is %q", path, msg)
		}
		if strings.Contains(a.body, "secret") {
			t.Errorf("GET %s = %q, want nothing of the error's text in it", path, a.body)
		}
		if !strings.Contains(logged.String(), "the secret of a zathras") || !strings.Contains(logged.String(), "level=ERROR") {
			t.Errorf("GET %s: the log is %q, want the error in it", path, logged.String())
		}
	}
}

func TestPathsTheAPIHasNotAndMethodsItHasNot(t *testing.T) {
	srv := serve(t, Config{})
	for _, path := range []string{"/api/", "/api/nothing", "/api/services", "/api/services/web", "/api/services/web/rollouts/7", "/api/zathras/" + string(zathrasA), "/api/index.html"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			wantRefusal(t, ask(t, srv, method, path, ""), http.StatusNotFound, wire.CodeNotFound)
		}
	}
	for _, path := range []string{"/api/services/web/rollouts", "/api/zathras/" + string(zathrasA) + "/history"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			a := ask(t, srv, method, path, "")
			wantRefusal(t, a, http.StatusMethodNotAllowed, wire.CodeBadRequest)
			if a.header.Get("Allow") != "GET" {
				t.Errorf("%s %s has Allow %q, want GET", method, path, a.header.Get("Allow"))
			}
		}
	}
}
