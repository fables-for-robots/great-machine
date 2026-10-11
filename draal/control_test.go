package draal

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/wire"
)

// The endpoint IDs of the tests.
var (
	draalID    = nodetest.ID(1)
	operatorID = nodetest.ID(2)
	strangerID = nodetest.ID(3)
)

// deadline is how long a test waits for something that is to happen.
const deadline = 10 * time.Second

// eventually polls cond until it holds, and fails the test with what when
// it does not within the deadline.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// openDB opens a database in a directory of the test.
func openDB(t *testing.T, path string) *db.DB {
	t.Helper()
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// testConfig returns a Config with a database of the test, the operator
// of the tests and short durations.
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DB:             openDB(t, filepath.Join(t.TempDir(), "draal.sqlite")),
		Authz:          authz.NewAllowlist([]wire.ID{operatorID}),
		Store:          wire.StoreInfo{Server: nodetest.ID(99), Prefix: "nix/"},
		Settle:         time.Millisecond,
		StableAfter:    50 * time.Millisecond,
		PrepareTimeout: time.Minute,
		RetryAfter:     50 * time.Millisecond,
		GoneAfter:      5 * time.Second,
		WatchEvery:     time.Millisecond,
		History:        time.Minute,
		Log:            slog.New(slog.DiscardHandler),
	}
}

// webRollout returns a rollout of the service web that draal accepts.
func webRollout(parallel int) wire.RolloutRequest {
	return wire.RolloutRequest{
		Service:  "web",
		Binary:   "/nix/store/abc-web/bin/web",
		Args:     []string{"--port", "80"},
		Closure:  []string{"abc-web", "def-libc"},
		Parallel: parallel,
	}
}

func TestStore(t *testing.T) {
	cfg := testConfig(t)
	d := New(cfg)
	// Every service has a prefix of its own, and one draal has not heard
	// of yet has one like any other: the first rollout of a service
	// pushes before draal knows the service.
	for _, service := range []string{"web", "never-rolled-out"} {
		got, err := d.Store(context.Background(), service)
		want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: "/services/" + service + "/nix/store/"}
		if err != nil || got != want {
			t.Errorf("Store(%q) = %+v, %v; want %+v", service, got, err, want)
		}
	}
	for _, service := range []string{"", "Web", "a/b", "..", "web/../api"} {
		if got, err := d.Store(context.Background(), service); wire.CodeOf(err) != wire.CodeBadRequest {
			t.Errorf("Store(%q) = %+v, %v; want a bad-request", service, got, err)
		}
	}
}

func TestRolloutIsRecorded(t *testing.T) {
	cfg := testConfig(t)
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cfg.Now = func() time.Time { return at }
	d := New(cfg)
	ctx := context.Background()

	info, err := d.Rollout(ctx, operatorID, webRollout(2))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if info.Service != "web" || info.Generation != 1 || info.Parallel != 2 ||
		info.State != wire.RolloutPreparing || info.CreatedBy != operatorID || !info.Created.Equal(at) {
		t.Errorf("Rollout = %+v", info)
	}
	gen, err := cfg.DB.Generation(ctx, "web", 1)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if gen.Binary != "/nix/store/abc-web/bin/web" || strings.Join(gen.Args, " ") != "--port 80" ||
		strings.Join(gen.Closure, " ") != "abc-web def-libc" {
		t.Errorf("generation = %+v", gen)
	}

	again, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("second Rollout: %v", err)
	}
	if again.Generation != 2 || again.ID == info.ID {
		t.Errorf("second Rollout = %+v, after %+v", again, info)
	}
}

func TestRolloutRefusals(t *testing.T) {
	change := func(f func(*wire.RolloutRequest)) wire.RolloutRequest {
		req := webRollout(1)
		f(&req)
		return req
	}
	cases := []struct {
		name string
		req  wire.RolloutRequest
	}{
		{"no service name", change(func(r *wire.RolloutRequest) { r.Service = "" })},
		{"a service name with a capital", change(func(r *wire.RolloutRequest) { r.Service = "Web" })},
		{"a service name with a slash", change(func(r *wire.RolloutRequest) { r.Service = "a/b" })},
		{"no binary", change(func(r *wire.RolloutRequest) { r.Binary = "" })},
		{"a relative binary", change(func(r *wire.RolloutRequest) { r.Binary = "nix/store/abc-web/bin/web" })},
		{"a binary outside the store", change(func(r *wire.RolloutRequest) { r.Binary = "/usr/bin/web" })},
		{"a binary that leaves the store", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web/../../../bin/sh" })},
		{"a binary that is the store", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/" })},
		{"a binary that is a store path", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web" })},
		{"a binary that is a store path and a slash", change(func(r *wire.RolloutRequest) { r.Binary = "/nix/store/abc-web/" })},
		{"no closure", change(func(r *wire.RolloutRequest) { r.Closure = nil })},
		{"a closure without the binary's store path", change(func(r *wire.RolloutRequest) { r.Closure = []string{"def-libc"} })},
		{"a path in the closure", change(func(r *wire.RolloutRequest) { r.Closure = []string{"abc-web", "/nix/store/def-libc"} })},
		{"an empty name in the closure", change(func(r *wire.RolloutRequest) { r.Closure = []string{"abc-web", ""} })},
		{"a dot in the closure", change(func(r *wire.RolloutRequest) { r.Closure = []string{"abc-web", "."} })},
		{"two dots in the closure", change(func(r *wire.RolloutRequest) { r.Closure = []string{"abc-web", ".."} })},
		{"parallel below 0", change(func(r *wire.RolloutRequest) { r.Parallel = -1 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			d := New(cfg)
			_, err := d.Rollout(context.Background(), operatorID, tc.req)
			if wire.CodeOf(err) != wire.CodeBadRequest {
				t.Fatalf("Rollout = %v, want a bad-request", err)
			}
			services, err := cfg.DB.Services(context.Background())
			if err != nil || len(services) != 0 {
				t.Fatalf("after a refusal the database has %v, %v; want nothing", services, err)
			}
		})
	}
}

func TestRolloutStoreDir(t *testing.T) {
	cfg := testConfig(t)
	cfg.StoreDir = "/tmp/store"
	d := New(cfg)
	req := webRollout(1)
	if _, err := d.Rollout(context.Background(), operatorID, req); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Fatalf("Rollout of %s with the store at /tmp/store = %v, want a bad-request", req.Binary, err)
	}
	req.Binary = "/tmp/store/abc-web/bin/web"
	if _, err := d.Rollout(context.Background(), operatorID, req); err != nil {
		t.Fatalf("Rollout of %s with the store at /tmp/store: %v", req.Binary, err)
	}
}

func TestRollback(t *testing.T) {
	cfg := testConfig(t)
	d := New(cfg)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		req  wire.RollbackRequest
		want wire.Code
	}{
		{"no service name", wire.RollbackRequest{Service: "", Parallel: 1}, wire.CodeBadRequest},
		{"parallel below 0", wire.RollbackRequest{Service: "web", Parallel: -1}, wire.CodeBadRequest},
		{"a service that is not there", wire.RollbackRequest{Service: "web", Parallel: 1}, wire.CodeNotFound},
	} {
		if _, err := d.Rollback(ctx, operatorID, tc.req); wire.CodeOf(err) != tc.want {
			t.Errorf("Rollback with %s = %v, want %s", tc.name, err, tc.want)
		}
	}

	first, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if _, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web"}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollback without a stable generation = %v, want a bad-request", err)
	}
	if ok, err := cfg.DB.SetRolloutState(ctx, first.ID, wire.RolloutCompleted, "", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}
	if _, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web"}); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollback after a rollout that completed = %v, want a bad-request", err)
	}

	second, err := d.Rollout(ctx, operatorID, webRollout(1))
	if err != nil {
		t.Fatalf("Rollout: %v", err)
	}
	if ok, err := cfg.DB.SetRolloutState(ctx, second.ID, wire.RolloutHalted, "web-1: no", time.Now()); err != nil || !ok {
		t.Fatalf("SetRolloutState: %v, %v", ok, err)
	}
	back, err := d.Rollback(ctx, operatorID, wire.RollbackRequest{Service: "web", Parallel: 3})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if back.Generation != first.Generation || back.Parallel != 3 || back.State != wire.RolloutPreparing || back.CreatedBy != operatorID {
		t.Errorf("Rollback = %+v, want a rollout to generation %d", back, first.Generation)
	}
}

func TestRollouts(t *testing.T) {
	d := New(testConfig(t))
	ctx := context.Background()

	if _, err := d.Rollouts(ctx, "No Name"); wire.CodeOf(err) != wire.CodeBadRequest {
		t.Errorf("Rollouts of a bad name = %v, want a bad-request", err)
	}
	if list, err := d.Rollouts(ctx, "web"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts of a service without any = %v, %v", list, err)
	}
	var last wire.RolloutInfo
	for range 22 {
		var err error
		if last, err = d.Rollout(ctx, operatorID, webRollout(1)); err != nil {
			t.Fatalf("Rollout: %v", err)
		}
	}
	list, err := d.Rollouts(ctx, "web")
	if err != nil {
		t.Fatalf("Rollouts: %v", err)
	}
	if len(list) != 20 {
		t.Fatalf("Rollouts returned %d, want the last 20", len(list))
	}
	if list[0].ID != last.ID || list[0].Generation != 22 || list[19].Generation != 3 {
		t.Errorf("Rollouts go from generation %d to %d, want from 22 to 3", list[0].Generation, list[19].Generation)
	}
}

func TestOfAZathrasThatIsNotConnected(t *testing.T) {
	d := New(testConfig(t))
	if service, ok := d.ServiceOf(nodetest.ID(10)); ok {
		t.Errorf("ServiceOf = %q, true for a zathras that is not connected", service)
	}
	if _, err := d.History(context.Background(), nodetest.ID(10)); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("History = %v, want unavailable", err)
	}
}
