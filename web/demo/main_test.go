package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// serviceOf returns a service of a fleet.
func serviceOf(t *testing.T, f wire.Fleet, name string) wire.Service {
	t.Helper()
	for _, svc := range f.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("the fleet has no service %s", name)
	return wire.Service{}
}

// The smoke test: the program is started on a free port, asked for one
// fleet and for the history of one zathras, and ended.
func TestDemoServes(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	urls := make(chan string, 1)
	ended := make(chan error, 1)
	go func() { ended <- run(ctx, "127.0.0.1:0", "", false, io.Discard, func(url string) { urls <- url }) }()
	var url string
	select {
	case url = <-urls:
	case err := <-ended:
		t.Fatalf("run: %v", err)
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the demo to listen")
	}

	asking, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	c, _, err := websocket.Dial(asking, "ws"+strings.TrimPrefix(url, "http")+"api/watch", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("dialing /api/watch: %v", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	_, data, err := c.Read(asking)
	if err != nil {
		t.Fatalf("reading the fleet: %v", err)
	}
	var f wire.Fleet
	if err := json.Unmarshal(data, &f); err != nil || len(f.Services) != 4 {
		t.Fatalf("the fleet is %s (%v), want four services", data, err)
	}

	z := serviceOf(t, f, "web").Zathrases[0]
	resp, err := client.Get(url + "api/zathras/" + string(z.ID) + "/history")
	if err != nil {
		t.Fatalf("GET history: %v", err)
	}
	defer resp.Body.Close()
	var answer struct {
		Samples []wire.Sample `json:"samples"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil || resp.StatusCode != http.StatusOK || len(answer.Samples) < 400 {
		t.Fatalf("the history is %d with %d samples (%v), want 200 with fifteen minutes of them", resp.StatusCode, len(answer.Samples), err)
	}

	// Ended as an interrupt ends it: the socket is closed as going away.
	stop()
	select {
	case err := <-ended:
		if err != nil {
			t.Errorf("run = %v, want nil", err)
		}
	case <-time.After(deadline):
		t.Fatal("timed out waiting for the demo to end")
	}
	if _, _, err := c.Read(context.Background()); websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Errorf("the socket ended with %v, want it closed as going away", err)
	}
}
