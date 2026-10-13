package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/node/nodetest"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// fakeDraal answers an operator from a script, on a network in memory.
type fakeDraal struct {
	store  wire.StoreInfo
	fleets []wire.Fleet     // what every watch is answered, in this order
	gone   bool             // the watch ends after the last fleet, as when draal goes away
	lines  []wire.LogLine   // what logs is answered
	answer wire.RolloutInfo // what rollout and rollback are answered
	refuse error            // or this, if it is set; a grant and a revocation are refused with it too
	grants []wire.Grant     // what grants is answered

	mu       sync.Mutex
	requests []wire.Request
}

// start makes the fake reachable and returns a client that is connected
// to it. Both end with the test.
func (f *fakeDraal) start(t *testing.T) *operator.Client {
	t.Helper()
	network := nodetest.New()
	listener := network.Listen(nodetest.ID(1))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			conn, err := listener.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				for {
					s, err := conn.AcceptStream(ctx)
					if err != nil {
						return
					}
					go f.serve(s)
				}
			}()
		}
	}()
	conn, err := network.Dial(ctx, nodetest.ID(2), nodetest.ID(1))
	if err != nil {
		t.Fatal(err)
	}
	client := operator.New(conn)
	t.Cleanup(func() { client.Close() })
	return client
}

// serve answers the request of one stream. It ends what it sends with
// CloseWrite and then waits for the asking side to end the stream, so
// that nothing it wrote is lost.
func (f *fakeDraal) serve(s node.Stream) {
	defer s.Close()
	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	hold := func() {
		var nothing wire.Request
		wire.Read(s, &nothing)
	}
	switch {
	case req.Store != nil:
		wire.Write(s, &wire.Response{Store: &f.store})
	case req.Rollout != nil || req.Rollback != nil:
		if f.refuse != nil {
			wire.WriteError(s, f.refuse)
		} else {
			wire.Write(s, &wire.Response{Rollout: &f.answer})
		}
	case req.Watch != nil:
		for i := range f.fleets {
			wire.Write(s, &wire.Response{Fleet: &f.fleets[i]})
		}
		if !f.gone {
			hold()
			return
		}
	case req.Grants != nil:
		wire.Write(s, &wire.Response{Grants: &wire.GrantList{Grants: f.grants}})
	case req.Grant != nil || req.Revoke != nil:
		switch {
		case f.refuse != nil:
			wire.WriteError(s, f.refuse)
		case req.Grant != nil:
			wire.Write(s, &wire.Response{Granted: &wire.Granted{}})
		default:
			wire.Write(s, &wire.Response{Revoked: &wire.Revoked{}})
		}
	case req.Logs != nil:
		for i := range f.lines {
			wire.Write(s, &wire.Response{Line: &f.lines[i]})
		}
		if req.Logs.Follow {
			hold()
			return
		}
	}
	s.CloseWrite()
	hold()
}

// asked returns the requests the fake has read so far.
func (f *fakeDraal) asked() []wire.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wire.Request(nil), f.requests...)
}

// buffer is a bytes.Buffer that a test may read while the code under test
// writes.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// waitFor polls cond until it holds, and fails the test when it does not
// within five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// plainProgress returns a progress that writes plain lines to out, with a
// clock that stands still: every step takes 0.0s.
func plainProgress(out *buffer) *progress {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return &progress{out: out, now: func() time.Time { return at }, width: func() int { return 0 }}
}

// testFleet is a fleet with two services: api, which nobody has rolled
// anything out to, and web, in the middle of its rollout #13.
func testFleet(now time.Time) wire.Fleet {
	return wire.Fleet{Services: []wire.Service{
		{Name: "api"},
		{
			Name:   "web",
			Stable: 7,
			Rollout: &wire.RolloutInfo{ID: 13, Service: "web", Generation: 8, Parallel: 1, State: wire.RolloutActivating,
				Created: now.Add(-95 * time.Second), Prepared: 4, Activated: 2, Awaited: 2},
			Zathrases: []wire.Zathras{
				{ID: wire.ID(strings.Repeat("a1", 32)), Name: "web-a", State: wire.State{Generation: 8, Phase: wire.PhaseRunning, RunningFor: 192 * time.Second,
					Sample: &wire.Sample{CPU: 0.254, Memory: 126353408, MemoryLimit: 536870912}}},
				{ID: wire.ID(strings.Repeat("b2", 32)), Name: "web-b", State: wire.State{Generation: 8, Phase: wire.PhaseRunning, RunningFor: 31 * time.Second,
					Sample: &wire.Sample{CPU: 1.5, Memory: 2048}}},
				{ID: wire.ID(strings.Repeat("c3", 32)), Name: "web-c", State: wire.State{Generation: 7, Phase: wire.PhaseBackoff, RestartAt: now.Add(4 * time.Second), Restarts: 3}},
				{ID: wire.ID(strings.Repeat("d4", 32)), Name: "web-d", State: wire.State{Phase: wire.PhaseIdle}},
			},
		},
	}}
}

// Endpoint IDs of the tests.
var (
	idA = strings.Repeat("a1", 32)
	idB = strings.Repeat("b2", 32)
	idC = strings.Repeat("c3", 32)
)

// variables are the environment variables the commands read.
var variables = []string{
	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "GREAT_MACHINE_BIND", "GREAT_MACHINE_WEB_ADDR",
	"GREAT_MACHINE_S3_BUCKET", "GREAT_MACHINE_S3_PREFIX", "GREAT_MACHINE_S3_ENDPOINT", "GREAT_MACHINE_S3_REGION", "GREAT_MACHINE_S3_PATH_STYLE",
	"GREAT_MACHINE_STORE_SCRATCH", "GREAT_MACHINE_STORE_VERIFY_JOBS", "GREAT_MACHINE_STORE_MAX_PACK_SIZE", "GREAT_MACHINE_STORE_NO_VERIFY",
	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME",
	"GREAT_MACHINE_SHELL", "GREAT_MACHINE_STOP_TIMEOUT", "GREAT_MACHINE_HEALTH_ADDR",
	"GREAT_MACHINE_KEY", "GREAT_MACHINE_PARALLEL", "GREAT_MACHINE_DETACH", "GREAT_MACHINE_JOBS",
	"GREAT_MACHINE_TEMP_DIR", "GREAT_MACHINE_FOLLOW", "GREAT_MACHINE_ROLE_KEY", "GREAT_MACHINE_ADMINS",
	"GREAT_MACHINE_STORE_CACHE", "GREAT_MACHINE_NO_STORE", "GREAT_MACHINE_USER",
}

// cleanEnvironment takes the variables of the commands out of the
// environment for the time of the test, whatever the machine has set.
func cleanEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range variables {
		if value, ok := os.LookupEnv(name); ok {
			os.Unsetenv(name)
			t.Cleanup(func() { os.Setenv(name, value) })
		}
	}
}

// read runs the command line of cmd as far as reading it: the Action of
// cmd is replaced by read, so that nothing is started.
func read(t *testing.T, cmd *cli.Command, read func(c *cli.Context) error, args ...string) {
	t.Helper()
	cmd.Action = read
	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr)
	app.Commands = []*cli.Command{cmd}
	if got := run(context.Background(), app, append([]string{"great-machine", cmd.Name}, args...)); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
}
