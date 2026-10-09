### Task 3: web/demo

**Files:**
- Create: `web/demo/main.go`, `web/demo/fleet.go`, `web/demo/figures.go`, `web/demo/lines.go`, `web/demo/shell.go`
- Test: `web/demo/demo_test.go`

**Interfaces:**
- Consumes:
  - Tasks 1 and 2: `web.New(web.Config{Controller, Dist})`, `web.IsLoopback`, `web.Built`.
  - `draal.Controller` and `draal.Shell`, which the demo implements.
  - `logring.New(size, maxLine int) *Ring`, `(*Ring).SetClock`, `Add(stream, text string)`, `Lines() []wire.LogLine`, `Follow(ctx, buffer int) ([]wire.LogLine, <-chan wire.LogLine)`; the streams `logring.Stdout`, `Stderr`, `Zathras`.
  - `ptyshell.Start(ptyshell.Config{Term, Cols, Rows}) (*Session, error)`; `(*Session).Read`, `Write`, `Resize(cols, rows uint16) error`, `Wait() int`, `Close() error`.
- Produces: `go run ./web/demo [--addr 127.0.0.1:8080] [--dist DIR] [--empty]`, which prints the URL it serves and ends on SIGINT or SIGTERM. With `--empty` it serves a fleet without a service, `{"services":[]}`, as a draal that was started for the first time, and nothing happens to it. Without, it serves four services:
  - `fresh`: the state before the first rollout. Two zathrases, `fresh-0` (with samples) and `fresh-1` (without), idle, generation 0, no binary; the service has `"stable":0` and no key `rollout`, as draal sends it; their logs have no line (the supervisor of a zathras says nothing before its first start); `Rollouts` returns none (`{"rollouts":[]}`).
  - `empty`: stable generation 2, a completed rollout, no zathras.
  - `web`: five zathrases, two of them with the name `web-7d9f8b6c4-8hjwz`. Every minute a rollout: preparing for 9 seconds, activating two at a time for 25, completed. In every second minute the first rollout does not get there (superseded after 6 seconds, or halted after 5 with a reason) and another follows it. In every second minute `web-7d9f8b6c4-mn4tc` is in backoff for 9 seconds. `web-7d9f8b6c4-q5rvd` goes for 6 seconds in every minute and comes back with its ID: its log and its shell end with `unavailable`. One of the two of one name writes 1000 lines a second for 10 seconds after each of its starts, and then 3000 at once, of which one who follows loses about 2000 (`lost` markers). `web-7d9f8b6c4-2xkqp` writes about three lines a second; among them lines with tabs, with ANSI colours, with wide characters, on `stderr`, and one of 16 KiB. Twenty rollouts of every state.
  - `worker`: halted (`worker-1: exited with status 2 after 1.3s`); `worker-1` on generation 4 and in backoff nearly all the time; `worker-0` (no limit of memory), `worker-2` (no samples) and one with a name of 253 characters (stopping for 6 seconds in every 40) on generation 3; `worker-3` idle, with an empty log.
  - Samples every 2 seconds, with 15 minutes of them from the start. A zathras that came back has only those since.
  - The endpoint IDs are the same in every run.
  - The times of the JSON carry the offset of the machine's time zone (`+02:00`) and up to nine digits of a second, as draal's may.
  - The counts of a rollout (`prepared`, `activated`) are those of its last decision: after one of five has gone they are still 5.
  - **The shell is a real shell on the machine that runs the demo.**

The fleet moves by tasks that are due at a time, and `advance(now)` does those whose time has come. The program calls it twenty times a second; the test calls it for four minutes without waiting for them.

- [ ] **Step 1: Write the failing tests**

`web/demo/demo_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

// deadline is how long a test waits for something that is to happen.
const deadline = 30 * time.Second

// fleetAt moves the fleet to a time and returns it.
func fleetAt(d *demo, at time.Time) wire.Fleet {
	d.advance(at)
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fleet(at)
}

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

// The demo is what the page is developed against: this is what it has
// to show for that, at its start and in the minutes after.
func TestFleetHasWhatThePageIsToShow(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, false)
	f := fleetAt(d, start)

	if len(f.Services) != 4 || f.Services[0].Name != "empty" || f.Services[1].Name != "fresh" || f.Services[2].Name != "web" || f.Services[3].Name != "worker" {
		t.Fatalf("the services are %+v, want empty, fresh, web and worker", f.Services)
	}
	empty, fresh, web, worker := f.Services[0], f.Services[1], f.Services[2], f.Services[3]
	// fresh is the state before the first rollout, in the JSON draal
	// sends for it: no rollout at all.
	if fresh.Stable != 0 || fresh.Rollout != nil || len(fresh.Zathrases) != 2 {
		t.Errorf("fresh = %+v, want no stable generation, no rollout and two zathrases", fresh)
	}
	for _, z := range fresh.Zathrases {
		if s := z.State; s.Phase != wire.PhaseIdle || s.Generation != 0 || s.Binary != "" || s.Args != nil || s.LastExit != nil || s.Restarts != 0 || s.Prepared != nil {
			t.Errorf("%s of fresh has the state %+v, want it idle and without a generation", z.Name, s)
		}
		lines, err := d.Logs(t.Context(), z.ID, false)
		if err != nil {
			t.Fatalf("Logs of %s: %v", z.Name, err)
		}
		for l := range lines {
			t.Errorf("the log of %s has the line %+v, want none before the first rollout", z.Name, l)
		}
	}
	if data, _ := json.Marshal(fresh); !strings.HasPrefix(string(data), `{"name":"fresh","stable":0,"zathrases":[{"id":"`) {
		t.Errorf("fresh is sent as %s, want it without the key rollout", data)
	}
	if list, err := d.Rollouts(t.Context(), "fresh"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts of fresh = %v, %v; want none", list, err)
	}
	if empty.Stable == 0 || empty.Rollout == nil || empty.Zathrases == nil || len(empty.Zathrases) != 0 {
		t.Errorf("empty = %+v, want a stable generation, a rollout and no zathras", empty)
	}
	if r := worker.Rollout; r == nil || r.State != wire.RolloutHalted || !strings.Contains(r.Reason, "worker-1") || !strings.Contains(r.Reason, "status 2") {
		t.Errorf("the rollout of worker is %+v, want it halted with a zathras and an exit status in the reason", r)
	}
	if len(web.Zathrases) != 5 || web.Rollout == nil || web.Rollout.State != wire.RolloutPreparing {
		t.Errorf("web has %d zathrases and the rollout %+v, want five and one that is preparing", len(web.Zathrases), web.Rollout)
	}

	phases, generations := map[wire.Phase]bool{}, map[uint64]bool{}
	var bare, longest, pair, restarted, exited bool
	for _, svc := range f.Services {
		for i, z := range svc.Zathrases {
			if len(z.ID) != 64 || z.Connected.IsZero() {
				t.Errorf("%s has the ID %q and connected at %v", z.Name, z.ID, z.Connected)
			}
			phases[z.State.Phase] = true
			bare = bare || z.State.Sample == nil && z.State.Phase == wire.PhaseRunning
			longest = longest || len(z.Name) == longestName
			pair = pair || i > 0 && svc.Zathrases[i-1].Name == z.Name && svc.Zathrases[i-1].ID < z.ID
			restarted = restarted || z.State.Restarts > 0
			exited = exited || z.State.LastExit != nil
			if svc.Name == "worker" && z.State.Generation != 0 {
				generations[z.State.Generation] = true
			}
			if z.State.Phase == wire.PhaseRunning && (z.State.PID == 0 || z.State.RunningFor <= 0 || !strings.HasPrefix(z.State.Binary, "/nix/store/") || len(z.State.Args) == 0) {
				t.Errorf("%s runs with the state %+v", z.Name, z.State)
			}
			if z.State.Phase == wire.PhaseBackoff && z.State.RestartAt.IsZero() {
				t.Errorf("%s is in backoff without a time of its restart", z.Name)
			}
		}
	}
	for _, phase := range []wire.Phase{wire.PhaseIdle, wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff} {
		if !phases[phase] {
			t.Errorf("no zathras is %s at the start", phase)
		}
	}
	if !bare || !longest || !pair || !restarted || !exited || len(generations) != 2 {
		t.Errorf("without samples: %v; a name of %d characters: %v; two of one name: %v; restarts: %v; a last exit: %v; generations of worker: %v",
			bare, longestName, longest, pair, restarted, exited, generations)
	}

	// Twenty rollouts of web, newest first.
	rollouts, err := d.Rollouts(t.Context(), "web")
	if err != nil || len(rollouts) != 20 {
		t.Fatalf("Rollouts = %d of them, %v; want 20", len(rollouts), err)
	}
	states := map[wire.RolloutState]bool{}
	for i, r := range rollouts {
		states[r.State] = true
		if i > 0 && (r.ID >= rollouts[i-1].ID || r.Created.After(rollouts[i-1].Created)) {
			t.Errorf("rollout %d comes behind %d: not newest first", r.ID, rollouts[i-1].ID)
		}
		if r.State.UnderWay() != r.Finished.IsZero() || (r.State == wire.RolloutHalted) != (r.Reason != "") {
			t.Errorf("rollout %+v: its end or its reason does not go with its state", r)
		}
	}

	// Fifteen minutes of samples, two seconds apart, oldest first.
	samples, err := d.History(t.Context(), web.Zathrases[0].ID)
	if err != nil || len(samples) != int(history/sampleEvery) {
		t.Fatalf("History = %d samples, %v; want %d", len(samples), err, history/sampleEvery)
	}
	for i, s := range samples[1:] {
		if s.At.Sub(samples[i].At) != sampleEvery || s.CPU <= 0 || s.Memory == 0 {
			t.Fatalf("sample %d is %+v, behind %+v", i+1, s, samples[i])
		}
	}

	// A ring that is full, with every kind of line.
	lines, err := d.Logs(t.Context(), web.Zathrases[0].ID, false)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	streams, count := map[string]bool{}, 0
	var long, tabs, colours, wide bool
	for l := range lines {
		count++
		streams[l.Stream] = true
		long = long || len(l.Text) == 16<<10
		tabs = tabs || strings.Contains(l.Text, "\t")
		colours = colours || strings.Contains(l.Text, "\x1b[")
		wide = wide || strings.Contains(l.Text, "注文")
		if strings.Contains(l.Text, "\n") {
			t.Errorf("the line %q has a line feed in it", l.Text)
		}
	}
	if count != 400 || !long || !tabs || !colours || !wide || !streams["stdout"] || !streams["stderr"] {
		t.Errorf("%d lines; one of 16 KiB: %v; tabs: %v; colours: %v; wide characters: %v; streams: %v", count, long, tabs, colours, wide, streams)
	}

	// Four minutes, a twentieth of a second at a time.
	goner := d.services["web"].zathrases[4].id
	var quiet time.Duration // how long no rollout of web has been under way
	var gone, back, backoff bool
	for at := start; at.Before(start.Add(4 * time.Minute)); at = at.Add(50 * time.Millisecond) {
		f := fleetAt(d, at)
		if _, err := json.Marshal(f); err != nil {
			t.Fatalf("the fleet at %s: %v", at.Sub(start), err)
		}
		web := serviceOf(t, f, "web")
		states[web.Rollout.State] = true
		if web.Rollout.State.UnderWay() {
			quiet = 0
		} else if quiet += 50 * time.Millisecond; quiet > time.Minute {
			t.Fatalf("at %s no rollout of web has been under way for a minute", at.Sub(start))
		}
		// The counts are those of the last decision about the rollout:
		// of the five there were, also when one has gone since.
		if r := web.Rollout; r.Prepared > 5 || r.Activated > r.Prepared || r.Awaited > r.Parallel {
			t.Fatalf("at %s the rollout counts %d prepared, %d activated and %d awaited", at.Sub(start), r.Prepared, r.Activated, r.Awaited)
		}
		here := slices.ContainsFunc(web.Zathrases, func(z wire.Zathras) bool { return z.ID == goner })
		gone = gone || !here
		back = back || gone && here
		if !here {
			if _, err := d.History(t.Context(), goner); wire.CodeOf(err) != wire.CodeUnavailable {
				t.Fatalf("History of the zathras that has gone = %v, want unavailable", err)
			}
		}
		for _, z := range web.Zathrases {
			backoff = backoff || z.State.Phase == wire.PhaseBackoff
		}
		if !slices.IsSortedFunc(web.Zathrases, func(a, b wire.Zathras) int {
			return strings.Compare(a.Name+" "+string(a.ID), b.Name+" "+string(b.ID))
		}) {
			t.Fatalf("at %s the zathrases of web are not by name and then by ID", at.Sub(start))
		}
	}
	for _, state := range []wire.RolloutState{wire.RolloutPreparing, wire.RolloutActivating, wire.RolloutCompleted, wire.RolloutHalted, wire.RolloutSuperseded} {
		if !states[state] {
			t.Errorf("no rollout of web was %s", state)
		}
	}
	if !gone || !back || !backoff {
		t.Errorf("a zathras of web went: %v, and came back: %v; one was in backoff: %v", gone, back, backoff)
	}
	if svc := serviceOf(t, fleetAt(d, start.Add(4*time.Minute)), "web"); svc.Stable < web.Stable+3 {
		t.Errorf("the stable generation of web is %d after four minutes, and was %d", svc.Stable, web.Stable)
	}
}

// With --empty there is no service, and the fleet says so as draal's
// does: with a list that is empty, not with null.
func TestEmptyFleetHasNoService(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, true)
	for _, at := range []time.Time{start, start.Add(5 * time.Minute)} {
		data, err := json.Marshal(fleetAt(d, at))
		if err != nil || string(data) != `{"services":[]}` {
			t.Errorf("the fleet is %s (%v), want {\"services\":[]}", data, err)
		}
	}
	if list, err := d.Rollouts(t.Context(), "web"); err != nil || len(list) != 0 {
		t.Errorf("Rollouts = %v, %v; want none", list, err)
	}
	fleets, err := d.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := <-fleets; f.Services == nil || len(f.Services) != 0 {
		t.Errorf("the first fleet of a watcher is %+v, want an empty list of services", f)
	}
}

// A zathras that goes ends the log that is followed and the shell that
// is open, as draal's do: the channel is closed, and Read answers
// unavailable.
func TestZathrasThatGoesEndsItsLogAndItsShell(t *testing.T) {
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := newDemo(start, false)
	z := d.services["web"].zathrases[4]
	lines, err := d.Logs(t.Context(), z.id, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	sh, err := d.Shell(t.Context(), z.id, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	defer sh.Close()
	for range 400 {
		<-lines
	}

	d.mu.Lock()
	d.leave(z)
	d.mu.Unlock()
	timeout := time.After(deadline)
	for open := true; open; {
		select {
		case _, open = <-lines:
		case <-timeout:
			t.Fatal("timed out waiting for the log to end")
		}
	}
	if _, err := io.ReadAll(sh); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("the shell ended with %v, want unavailable", err)
	}
	if _, err := d.Logs(t.Context(), z.id, false); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Logs of a zathras that has gone = %v, want unavailable", err)
	}
	if _, err := d.Shell(t.Context(), z.id, wire.ShellRequest{}); wire.CodeOf(err) != wire.CodeUnavailable {
		t.Errorf("Shell of a zathras that has gone = %v, want unavailable", err)
	}
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
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./web/demo/`
Expected: FAIL: the package does not build, with `undefined: demo`, `undefined: newDemo` and `undefined: run`.

- [ ] **Step 3: Write the implementation**

The program, `web/demo/main.go`:

```go
// Command demo serves the web page of great-machine over a made-up fleet,
// for developing the page: the real handler of the package web, and
// behind it a controller that invents four services, their zathrases,
// their figures and their logs, and moves them along.
//
//	go run ./web/demo [--addr 127.0.0.1:8080] [--dist DIR] [--empty]
//
// With --empty there is no service at all, as draal has none when it was
// started for the first time.
//
// Everything it shows is made up but the shell: THE SHELL OF THE PAGE IS A
// REAL SHELL ON THE MACHINE THAT RUNS THE DEMO, as the user who runs it.
// The page has no authentication and answers under every host name.
// Leave the demo on a loopback address.
//
// It is not part of the program, and nothing of great-machine uses it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fables-for-robots/great-machine/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "where to listen, as `HOST:PORT`")
	dist := flag.String("dist", "", "serve the build of the page in `DIR` instead of the embedded one")
	empty := flag.Bool("empty", false, "serve a fleet without a service, as a draal that was just started")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "demo: %q is no argument of the demo\n", flag.Arg(0))
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *dist, *empty, os.Stderr, nil); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

// run serves the demo on addr until ctx is done: the made-up fleet, or
// with empty a fleet without a service. It writes to out what it serves,
// and calls ready, if set, with the URL once it listens.
func run(ctx context.Context, addr, dist string, empty bool, out io.Writer, ready func(url string)) error {
	cfg := web.Config{}
	if dist != "" {
		if info, err := os.Stat(dist); err != nil || !info.IsDir() {
			return fmt.Errorf("--dist %s is no directory", dist)
		}
		cfg.Dist = os.DirFS(dist)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	defer l.Close()

	fleet := newDemo(time.Now(), empty)
	moving, stopMoving := context.WithCancel(context.Background())
	defer stopMoving()
	go fleet.run(moving)
	cfg.Controller = fleet

	// The context of every request. A socket lives on when a server is
	// shut down, and ends when this is cancelled.
	requests, endRequests := context.WithCancel(context.Background())
	defer endRequests()
	srv := &http.Server{
		Handler:           web.New(cfg),
		BaseContext:       func(net.Listener) context.Context { return requests },
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + l.Addr().String() + "/"
	what := "a made-up fleet"
	if empty {
		what = "a fleet without a service"
	}
	fmt.Fprintf(out, "demo: %s behind the real API of great-machine, at %s\n", what, url)
	fmt.Fprintf(out, "demo: the shell of the page is a REAL SHELL ON THIS MACHINE, as the user who runs the demo\n")
	if !web.IsLoopback(addr) {
		fmt.Fprintf(out, "demo: %s is not loopback: whoever reaches it has that shell\n", addr)
	}
	if dist == "" && !web.Built() {
		fmt.Fprintf(out, "demo: the page is not built into this program, and %s answers 501. The API is there:\n", url)
		fmt.Fprintf(out, "demo: run `npm run dev` in ui/, or build the page and give its directory with --dist\n")
	}
	if ready != nil {
		ready(url)
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	select {
	case err := <-served:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}
	endRequests()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}
```

The fleet and what happens to it, `web/demo/fleet.go`:

```go
package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// watchEvery is the least time between two fleets for one watcher, as
	// draal has it.
	watchEvery = time.Second
	// cycleEvery is how often the service web is rolled out.
	cycleEvery = time.Minute
	// rolloutsKept is how many rollouts of a service are listed.
	rolloutsKept = 20
	// followBuffer is how many lines may wait for one who follows a log,
	// as a zathras has it.
	followBuffer = 1024
	// longestName is how long the name of a zathras may be.
	longestName = 253
)

// demo is the made-up fleet. It is a draal.Controller.
//
// All of it moves by tasks that are to be done at a time: advance does
// those whose time has come. So the fleet of any time can be had without
// waiting for it, which is how its test looks at three minutes of it.
type demo struct {
	start time.Time

	mu       sync.Mutex
	at       time.Time           // the time of the task that is being done
	last     time.Time           // when advance was called last
	rng      *rand.Rand          // for what is made up as it goes
	services map[string]*service // by name
	all      []*zathras          // in the order they were made up
	byID     map[wire.ID]*zathras
	rollouts uint64 // how many there were, of all services
	todo     []task
	dirty    bool                       // the fleet has changed since the watchers were told
	subs     map[chan struct{}]struct{} // who is told that the fleet changed
}

// A demo is a Controller.
var _ draal.Controller = (*demo)(nil)

// task is something that is to happen at a time. It is given that time,
// which is the present for it, however late it is done.
type task struct {
	at time.Time
	do func(at time.Time)
}

// service is a made-up service.
type service struct {
	name       string
	generation uint64             // the newest there is
	stable     uint64             // 0 for none
	rollouts   []wire.RolloutInfo // newest first
	zathrases  []*zathras
	args       []string
}

// zathras is a made-up zathras.
type zathras struct {
	id      wire.ID
	name    string
	service *service

	present   bool          // connected: one that is not is in no fleet and answers nothing
	left      chan struct{} // closed when it goes; a new one when it is back
	connected time.Time
	state     wire.State // without RunningFor and Sample, which the fleet adds
	since     time.Time  // when its service was started
	pid       int        // of its service, when that runs

	ring  *logring.Ring
	talks time.Duration // about how long between two lines of its service; 0: it says nothing
	lines int           // how many its service has written

	figures *figures // nil: a machine without cgroup v2, and no samples
	samples []wire.Sample
}

// operator is who made the rollouts.
var operator = idOf("an operator")

// idOf returns an endpoint ID for a text: always the same one, so that a
// link to a zathras of the demo is good for the next run of it.
func idOf(text string) wire.ID {
	sum := sha256.Sum256([]byte(text))
	return wire.ID(hex.EncodeToString(sum[:]))
}

// binaryOf returns the path a generation of a service has in the store.
func binaryOf(service string, generation uint64) string {
	const alphabet = "0123456789abcdfghijklmnpqrsvwxyz" // what Nix writes a hash with
	sum := sha256.Sum256(fmt.Appendf(nil, "%s %d", service, generation))
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = alphabet[sum[i]%32]
	}
	minor := map[string]int{"web": 4, "worker": 9}[service]
	return fmt.Sprintf("/nix/store/%s-%s-1.%d.%d/bin/%s", hash, service, minor, generation, service)
}

// newDemo returns the fleet as it is at start, with what is to happen to
// it from then on. With empty it has no service and nothing happens to
// it: a draal that was started for the first time.
func newDemo(start time.Time, empty bool) *demo {
	d := &demo{
		start:    start,
		at:       start,
		last:     start,
		rng:      rand.New(rand.NewPCG(2026, 10)),
		services: map[string]*service{},
		byID:     map[wire.ID]*zathras{},
		subs:     map[chan struct{}]struct{}{},
	}
	if !empty {
		d.seed()
	}
	return d
}

// after has do done when delay has passed since at.
func (d *demo) after(at time.Time, delay time.Duration, do func(at time.Time)) {
	d.todo = append(d.todo, task{at: at.Add(delay), do: do})
}

// every has do done now and then again whenever pause has passed.
func (d *demo) every(at time.Time, pause time.Duration, do func(at time.Time)) {
	do(at)
	d.after(at, pause, func(at time.Time) { d.every(at, pause, do) })
}

// run moves the fleet along with the clock until ctx is done.
func (d *demo) run(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.advance(now)
		}
	}
}

// advance does every task whose time has come by now, oldest first, and
// tells the watchers if the fleet has changed.
func (d *demo) advance(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// A machine that slept does not make up for the time: what was to
	// happen is put off by as long as it slept.
	if gap := now.Sub(d.last); gap > 5*time.Second {
		for i := range d.todo {
			d.todo[i].at = d.todo[i].at.Add(gap)
		}
	}
	d.last = now
	for {
		next := -1
		for i, t := range d.todo {
			if !t.at.After(now) && (next < 0 || t.at.Before(d.todo[next].at)) {
				next = i
			}
		}
		if next < 0 {
			break
		}
		t := d.todo[next]
		d.todo = slices.Delete(d.todo, next, next+1)
		d.at = t.at
		t.do(t.at)
	}
	d.at = now
	if d.dirty {
		d.dirty = false
		for ch := range d.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// seed makes the fleet of the start: four services, their rollouts so
// far and their zathrases, and what is to happen to them.
func (d *demo) seed() {
	// fresh: what a new user sees first. Two zathrases have registered,
	// and nothing was rolled out: no rollout, no generation, and logs
	// without a line, as the supervisor of a zathras says nothing before
	// its first start.
	fresh := d.addService("fresh")
	d.addZathras(fresh, "fresh-0", 0, 3*time.Minute).figures = newFigures(31, 0.1, 256*mib)
	d.addZathras(fresh, "fresh-1", 0, 2*time.Minute)

	// empty: rolled out twice, and nobody is there to run it.
	empty := d.addService("empty")
	d.record(empty, wire.RolloutSuperseded, "", d.start.Add(-30*time.Hour), 40*time.Second)
	d.record(empty, wire.RolloutCompleted, "", d.start.Add(-30*time.Hour+40*time.Second), 3*time.Second)

	// worker: its last rollout is halted, and its zathrases are on two
	// generations: the one that was tried first, and the others.
	worker := d.addService("worker", "--queue", "jobs", "--concurrency", "4")
	for i := range 3 {
		d.record(worker, wire.RolloutCompleted, "", d.start.Add(time.Duration(i-80)*time.Hour), 95*time.Second)
	}
	d.record(worker, wire.RolloutHalted, "worker-1: exited with status 2 after 1.3s", d.start.Add(-26*time.Minute), 21*time.Second)

	steady := d.addZathras(worker, "worker-0", 0, 71*time.Hour)
	steady.figures = newFigures(11, 0.6, 0) // no limit of memory
	d.runs(steady, worker.stable, d.start.Add(-26*time.Hour))
	steady.talks = 4 * time.Second

	crashing := d.addZathras(worker, "worker-1", 0, 71*time.Hour)
	crashing.figures = newFigures(12, 0.3, 512*mib)
	crashing.state.Generation = worker.generation
	crashing.state.Binary, crashing.state.Args = binaryOf("worker", worker.generation), worker.args
	crashing.state.Prepared = []uint64{worker.generation}
	crashing.state.Restarts = 187

	bare := d.addZathras(worker, "worker-2", 0, 9*time.Hour) // a machine without cgroup v2
	d.runs(bare, worker.stable, d.start.Add(-9*time.Hour))
	bare.talks = 6 * time.Second

	d.addZathras(worker, "worker-3", 0, 4*time.Minute) // has just come, and runs nothing

	long := "worker-batch-eu-central-1a." + strings.Repeat("node-0123456789abcdef.", 12)
	long = long[:longestName-len("internal")] + "internal"
	slow := d.addZathras(worker, long, 0, 50*time.Hour)
	slow.figures = newFigures(13, 0.9, 1024*mib)
	d.runs(slow, worker.stable, d.start.Add(-50*time.Hour))
	slow.talks = 5 * time.Second

	// web: twenty rollouts of every state, and one under way for most of
	// every minute.
	web := d.addService("web", "--listen", ":8080", "--db", "postgres://db.internal/web", "--workers", "8")
	reasons := []string{
		"web-7d9f8b6c4-mn4tc: exited with status 1 after 2.3s",
		"still preparing after 30m0s: web-7d9f8b6c4-q5rvd",
		"web-7d9f8b6c4-2xkqp: was ended by SIGKILL after 14.2s",
		"web-7d9f8b6c4-8hjwz: could not be started: fork/exec " + binaryOf("web", 6) + ": exec format error",
	}
	for i := 19; i >= 1; i-- {
		created := d.start.Add(-time.Duration(i) * 47 * time.Minute)
		switch {
		case i%6 == 3:
			d.record(web, wire.RolloutHalted, reasons[(i/6)%len(reasons)], created, time.Duration(20+i)*time.Second)
		case i%5 == 2:
			d.record(web, wire.RolloutSuperseded, "", created, time.Duration(3+i)*time.Second)
		default:
			d.record(web, wire.RolloutCompleted, "", created, time.Duration(60+7*i)*time.Second)
		}
	}
	names := []string{
		"web-7d9f8b6c4-2xkqp", // talks all the time
		"web-7d9f8b6c4-8hjwz", // floods its log after every start
		"web-7d9f8b6c4-8hjwz", // has the name of the one before
		"web-7d9f8b6c4-mn4tc", // exits now and then
		"web-7d9f8b6c4-q5rvd", // goes now and then, and comes back
	}
	for i, name := range names {
		z := d.addZathras(web, name, i, time.Duration(5+3*i)*time.Hour)
		z.figures = newFigures(uint64(20+i), 0.25+0.15*float64(i), 512*mib)
		d.runs(z, web.stable, d.start.Add(-47*time.Minute+time.Duration(i)*8*time.Second))
		z.talks = 5 * time.Second
	}
	web.zathrases[0].talks = 300 * time.Millisecond
	web.zathrases[3].state.Restarts = 4
	web.zathrases[3].state.LastExit = &wire.Exit{
		Generation: web.stable - 1, Status: -1, Signal: "SIGKILL", At: d.start.Add(-3 * time.Hour), Ran: 41 * time.Minute,
	}

	// What every zathras has from before the start: its samples and the
	// lines of its ring.
	for _, z := range d.all {
		d.past(z)
	}

	// And what happens from the start on.
	d.every(d.start, sampleEvery, d.sample)
	for _, z := range d.all {
		if z.talks > 0 {
			d.after(d.start, d.around(z.talks), func(at time.Time) { d.talk(z, at) })
		}
	}
	d.crash(crashing, d.start)
	d.every(d.start, 40*time.Second, func(at time.Time) { d.restart(slow, at) })
	d.cycle(d.start, 0)
}

// around returns a time about as long as pause: half of it to half more.
func (d *demo) around(pause time.Duration) time.Duration {
	return pause/2 + time.Duration(d.rng.Int64N(int64(pause)))
}

// addService adds a service without a rollout.
func (d *demo) addService(name string, args ...string) *service {
	svc := &service{name: name, args: args}
	d.services[name] = svc
	return svc
}

// addZathras adds a zathras that is connected and idle. salt tells two of
// one name apart.
func (d *demo) addZathras(svc *service, name string, salt int, connectedFor time.Duration) *zathras {
	z := &zathras{
		id:        idOf(fmt.Sprintf("%s/%s/%d", svc.name, name, salt)),
		name:      name,
		service:   svc,
		present:   true,
		left:      make(chan struct{}),
		connected: d.start.Add(-connectedFor),
		state:     wire.State{Phase: wire.PhaseIdle},
		ring:      logring.New(400, 16<<10),
	}
	z.ring.SetClock(func() time.Time { return d.at })
	svc.zathrases = append(svc.zathrases, z)
	d.all = append(d.all, z)
	d.byID[z.id] = z
	return z
}

// runs puts a zathras on a generation that it has run since the time
// given, without a word in its log.
func (d *demo) runs(z *zathras, generation uint64, since time.Time) {
	z.state.Generation = generation
	z.state.Binary, z.state.Args = binaryOf(z.service.name, generation), z.service.args
	z.state.Phase = wire.PhaseRunning
	z.state.RestartAt = time.Time{}
	z.state.Prepared = []uint64{generation}
	z.since = since
	z.pid = 2 + d.rng.IntN(30000)
	d.dirty = true
}

// started is runs for a service that is started now: the supervisor of
// the zathras says so in the log.
func (d *demo) started(z *zathras, generation uint64, at time.Time) {
	prepared := z.state.Prepared
	d.runs(z, generation, at)
	if !slices.Contains(prepared, generation) {
		prepared = append(prepared, generation)
	}
	z.state.Prepared = prepared
	z.ring.Add(logring.Zathras, fmt.Sprintf("started generation %d: %s (pid %d)", generation, z.state.Binary, z.pid))
}

// exited records how the service of a zathras ended, as the supervisor
// does, and returns the words for it.
func (d *demo) exited(z *zathras, status int, signal string, at time.Time) string {
	ran := at.Sub(z.since)
	z.state.LastExit = &wire.Exit{Generation: z.state.Generation, Status: status, Signal: signal, At: at, Ran: ran}
	z.pid = 0
	d.dirty = true
	if ran >= time.Second {
		ran = ran.Round(100 * time.Millisecond)
	}
	if signal != "" {
		return fmt.Sprintf("generation %d was ended by %s after %s", z.state.Generation, signal, ran)
	}
	return fmt.Sprintf("generation %d exited with status %d after %s", z.state.Generation, status, ran)
}

// backoff has the service of a zathras end by itself and be started
// again after the pause.
func (d *demo) backoff(z *zathras, status int, signal string, pause time.Duration, at time.Time, then func(at time.Time)) {
	how := d.exited(z, status, signal, at)
	z.state.Phase = wire.PhaseBackoff
	z.state.RestartAt = at.Add(pause)
	z.state.Restarts++
	z.ring.Add(logring.Zathras, fmt.Sprintf("%s; starting it again in %s", how, pause))
	d.after(at, pause, func(at time.Time) {
		d.started(z, z.state.Generation, at)
		if then != nil {
			then(at)
		}
	})
}

// crash is the life of a zathras whose service exits a moment after
// every start: it is in backoff nearly all the time.
func (d *demo) crash(z *zathras, at time.Time) {
	z.since = at.Add(-1300 * time.Millisecond)
	for _, line := range []string{
		"panic: runtime error: invalid memory address or nil pointer dereference",
		"[signal SIGSEGV: segmentation violation code=0x1 addr=0x18 pc=0x4a51c2]",
		"",
		"goroutine 1 [running]:",
		"main.(*queue).next(0x0)",
		"\t/build/worker/queue.go:88 +0x22",
		"main.main()",
		"\t/build/worker/main.go:41 +0x1b4",
	} {
		z.ring.Add(logring.Stderr, line)
	}
	d.backoff(z, 2, "", 8*time.Second, at, func(at time.Time) {
		z.ring.Add(logring.Stdout, "worker: connecting to the queue jobs")
		d.after(at, 1300*time.Millisecond, func(at time.Time) { d.crash(z, at) })
	})
}

// restart has a zathras stop its service, which takes its time over it,
// and start it again.
func (d *demo) restart(z *zathras, at time.Time) {
	z.state.Phase = wire.PhaseStopping
	d.dirty = true
	d.after(at, 6*time.Second, func(at time.Time) {
		z.ring.Add(logring.Zathras, d.exited(z, 0, "", at))
		d.started(z, z.state.Generation, at)
	})
}

// record adds a rollout of a new generation that ended as it is given.
func (d *demo) record(svc *service, state wire.RolloutState, reason string, created time.Time, took time.Duration) {
	r := d.rollOut(svc, created)
	r.State, r.Reason, r.Finished = state, reason, created.Add(took)
	if state == wire.RolloutCompleted {
		svc.stable = r.Generation
	}
}

// rollOut records a new generation of a service and the rollout to it,
// and returns the rollout, which is the last one of the service until
// the next.
func (d *demo) rollOut(svc *service, at time.Time) *wire.RolloutInfo {
	svc.generation++
	d.rollouts++
	svc.rollouts = slices.Insert(svc.rollouts, 0, wire.RolloutInfo{
		ID:         d.rollouts,
		Service:    svc.name,
		Generation: svc.generation,
		Parallel:   2,
		State:      wire.RolloutPreparing,
		Created:    at,
		CreatedBy:  operator,
	})
	if len(svc.rollouts) > rolloutsKept {
		svc.rollouts = svc.rollouts[:rolloutsKept]
	}
	for _, z := range svc.zathrases {
		z.state.Preparing = svc.generation
	}
	d.dirty = true
	return &svc.rollouts[0]
}

// cycle is a minute of the service web: a rollout that is prepared, then
// activated two zathrases at a time, and completed, and after it a
// zathras whose service exits and one that goes. Every other time the
// first rollout does not get there: it is superseded, or it halts.
func (d *demo) cycle(at time.Time, n int) {
	web := d.services["web"]
	zs := web.zathrases
	last := func() *wire.RolloutInfo { return &web.rollouts[0] }
	begin := time.Duration(0)

	d.rollOut(web, at)
	switch n % 4 {
	case 1:
		// The operator rolls out again before the first is prepared.
		begin = 6 * time.Second
		d.after(at, 2*time.Second, func(time.Time) { d.prepared(zs[0]) })
		d.after(at, begin, func(at time.Time) {
			d.finish(last(), wire.RolloutSuperseded, "", at)
			d.rollOut(web, at)
		})
	case 3:
		// A zathras cannot fetch the closure; the operator pushes it and
		// rolls out again.
		begin = 9 * time.Second
		d.after(at, 2*time.Second, func(time.Time) { d.prepared(zs[0]) })
		d.after(at, 5*time.Second, func(at time.Time) {
			d.finish(last(), wire.RolloutHalted, zs[2].name+": preload: the store has no "+
				strings.TrimSuffix(strings.TrimPrefix(binaryOf("web", web.generation), "/nix/store/"), "/bin/web"), at)
			for _, z := range zs {
				z.state.Preparing = 0
			}
		})
		d.after(at, begin, func(at time.Time) { d.rollOut(web, at) })
	}

	// Preparing: one zathras after the other has the closure.
	for i, z := range zs {
		d.after(at, begin+time.Duration(i+1)*1500*time.Millisecond, func(time.Time) { d.prepared(z) })
	}
	d.after(at, begin+9*time.Second, func(time.Time) {
		last().State = wire.RolloutActivating
		d.dirty = true
	})
	// Activating: two at a time are stopped, started on the generation,
	// and counted when they have run for a while.
	for b := 0; b*2 < len(zs); b++ {
		batch := zs[b*2 : min(b*2+2, len(zs))]
		t := begin + 10*time.Second + time.Duration(b)*8*time.Second
		d.after(at, t, func(time.Time) {
			for _, z := range batch {
				z.state.Phase = wire.PhaseStopping
			}
			last().Awaited = len(batch)
			d.dirty = true
		})
		d.after(at, t+2*time.Second, func(at time.Time) {
			for _, z := range batch {
				z.ring.Add(logring.Zathras, d.exited(z, 0, "", at))
				d.started(z, web.generation, at)
			}
		})
		d.after(at, t+7*time.Second, func(time.Time) {
			last().Activated += len(batch)
			last().Awaited = 0
			d.dirty = true
		})
	}
	d.after(at, begin+34*time.Second, func(at time.Time) {
		d.finish(last(), wire.RolloutCompleted, "", at)
		web.stable = web.generation
	})

	// The second zathras indexes after every start, and says so a
	// thousand times a second for ten seconds.
	d.after(at, begin+14*time.Second, func(at time.Time) { d.flood(zs[1], at, 0) })
	// Every other minute the service of the fourth exits by itself.
	if n%2 == 0 {
		d.after(at, begin+37*time.Second, func(at time.Time) {
			if n%4 == 0 {
				zs[3].ring.Add(logring.Stderr, "\x1b[31mFATAL\x1b[0m db: too many connections; giving up")
				d.backoff(zs[3], 1, "", 9*time.Second, at, nil)
			} else {
				d.backoff(zs[3], -1, "SIGKILL", 9*time.Second, at, nil) // out of memory
			}
		})
	}
	// The fifth goes for some seconds, and is back with the ID it had.
	d.after(at, begin+43*time.Second, func(time.Time) { d.leave(zs[4]) })
	d.after(at, begin+49*time.Second, func(at time.Time) { d.join(zs[4], at) })

	d.after(at, cycleEvery, func(at time.Time) { d.cycle(at, n+1) })
}

// prepared has a zathras have the closure of the newest generation of
// its service.
func (d *demo) prepared(z *zathras) {
	r := &z.service.rollouts[0]
	if z.state.Preparing != r.Generation {
		return
	}
	z.state.Preparing = 0
	z.state.Prepared = append(z.state.Prepared, r.Generation)
	if len(z.state.Prepared) > 3 {
		z.state.Prepared = z.state.Prepared[len(z.state.Prepared)-3:]
	}
	r.Prepared++
	d.dirty = true
}

// finish ends a rollout.
func (d *demo) finish(r *wire.RolloutInfo, state wire.RolloutState, reason string, at time.Time) {
	r.State, r.Reason, r.Finished = state, reason, at
	d.dirty = true
}

// leave has a zathras go: it is in no fleet, and who followed its log or
// had a shell in it is told.
func (d *demo) leave(z *zathras) {
	z.present = false
	close(z.left)
	z.samples = nil // draal forgets one that has gone
	d.dirty = true
}

// join has a zathras that went come back.
func (d *demo) join(z *zathras, at time.Time) {
	z.present = true
	z.left = make(chan struct{})
	z.connected = at
	d.dirty = true
}

// sample gives every zathras that has figures the sample of the time.
func (d *demo) sample(at time.Time) {
	for _, z := range d.all {
		if z.figures == nil {
			continue
		}
		s := z.figures.next(at, z.state.Phase == wire.PhaseRunning || z.state.Phase == wire.PhaseStopping)
		if !z.present {
			continue
		}
		z.samples = append(z.samples, s)
		if most := int(history / sampleEvery); len(z.samples) > most {
			z.samples = slices.Delete(z.samples, 0, len(z.samples)-most)
		}
	}
	d.dirty = true
}

// past makes up what a zathras has from before the start: the samples
// of the last fifteen minutes and a ring that is full.
func (d *demo) past(z *zathras) {
	if z.figures != nil {
		for at := d.start.Add(-history); at.Before(d.start); at = at.Add(sampleEvery) {
			z.samples = append(z.samples, z.figures.next(at, z.state.Phase == wire.PhaseRunning))
		}
	}
	if z.talks == 0 {
		return // its ring is empty
	}
	// As many lines as the ring holds and some more, the last of them
	// just before the start.
	const lines = 420
	d.at = d.start.Add(-time.Duration(lines) * z.talks)
	z.ring.Add(logring.Zathras, fmt.Sprintf("started generation %d: %s (pid %d)", z.state.Generation, z.state.Binary, z.pid))
	for range lines {
		d.at = d.at.Add(z.talks)
		z.ring.Add(d.line(z, d.at))
	}
	d.at = d.start
}

// talk has the service of a zathras write a line, and another one later.
func (d *demo) talk(z *zathras, at time.Time) {
	if z.state.Phase == wire.PhaseRunning {
		z.ring.Add(d.line(z, at))
	}
	d.after(at, d.around(z.talks), func(at time.Time) { d.talk(z, at) })
}

// flood has the service of a zathras write a thousand lines a second for
// ten seconds, and then three thousand at once: more than waits for one
// who follows, who is told how many were lost.
func (d *demo) flood(z *zathras, at time.Time, written int) {
	const each, all, burst = 10, 10000, 3000
	if z.state.Phase != wire.PhaseRunning {
		return
	}
	if written >= all {
		for i := range burst {
			z.ring.Add(logring.Stdout, fmt.Sprintf("INFO  reindex: flushed segment %04d of %d", i+1, burst))
		}
		z.ring.Add(logring.Stdout, "\x1b[32mINFO\x1b[0m  reindex: done")
		return
	}
	for i := range each {
		z.ring.Add(logring.Stdout, fmt.Sprintf("INFO  reindex: document %05d of %d indexed (shard %d)", written+i+1, all, (written+i)%7))
	}
	d.after(at, 10*time.Millisecond, func(at time.Time) { d.flood(z, at, written+each) })
}

// fleet returns the fleet as it is at now. The caller holds d.mu.
func (d *demo) fleet(now time.Time) wire.Fleet {
	f := wire.Fleet{Services: make([]wire.Service, 0, len(d.services))}
	for _, svc := range d.services {
		out := wire.Service{Name: svc.name, Stable: svc.stable, Zathrases: []wire.Zathras{}}
		if len(svc.rollouts) > 0 {
			last := svc.rollouts[0]
			out.Rollout = &last
		}
		for _, z := range svc.zathrases {
			if !z.present {
				continue
			}
			state := z.state
			state.Prepared = slices.Clone(state.Prepared)
			if state.LastExit != nil {
				exit := *state.LastExit
				state.LastExit = &exit
			}
			if state.Phase == wire.PhaseRunning {
				state.PID, state.RunningFor = z.pid, now.Sub(z.since)
			}
			if len(z.samples) > 0 {
				s := z.samples[len(z.samples)-1]
				state.Sample = &s
			}
			out.Zathrases = append(out.Zathrases, wire.Zathras{ID: z.id, Name: z.name, Connected: z.connected, State: state})
		}
		slices.SortFunc(out.Zathrases, func(a, b wire.Zathras) int {
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		})
		f.Services = append(f.Services, out)
	}
	slices.SortFunc(f.Services, func(a, b wire.Service) int { return cmp.Compare(a.Name, b.Name) })
	return f
}

// connected returns a zathras that is connected, or unavailable, as draal
// answers for one that is not.
func (d *demo) connected(id wire.ID) (*zathras, error) {
	if z := d.byID[id]; z != nil && z.present {
		return z, nil
	}
	return nil, wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", id)
}

// Store is not of the demo: the page asks for no store.
func (d *demo) Store(ctx context.Context) (wire.StoreInfo, error) {
	return wire.StoreInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo has no store")
}

// Rollout is not of the demo: the page rolls nothing out.
func (d *demo) Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	return wire.RolloutInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo rolls out by itself")
}

// Rollback is not of the demo: the page rolls nothing back.
func (d *demo) Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	return wire.RolloutInfo{}, wire.Errorf(wire.CodeUnavailable, "the demo rolls back nothing")
}

// Watch sends the fleet at once and after every change, at most once in
// watchEvery, until ctx is done, as draal does: who does not read misses
// the fleets between and reads the newest next.
func (d *demo) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	changed := make(chan struct{}, 1)
	d.mu.Lock()
	d.subs[changed] = struct{}{}
	first := d.fleet(time.Now())
	d.mu.Unlock()

	out := make(chan wire.Fleet, 1)
	out <- first
	go func() {
		defer close(out)
		defer func() {
			d.mu.Lock()
			delete(d.subs, changed)
			d.mu.Unlock()
		}()
		pause := time.NewTimer(watchEvery)
		defer pause.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			select {
			case <-ctx.Done():
				return
			case <-pause.C:
			}
			d.mu.Lock()
			f := d.fleet(time.Now())
			d.mu.Unlock()
			select {
			case out <- f:
			default:
				select {
				case <-out:
				default:
				}
				out <- f
			}
			pause.Reset(watchEvery)
		}
	}()
	return out, nil
}

// Rollouts returns the last 20 rollouts of a service, newest first, as
// the database of draal has them: without the counts of a fleet.
func (d *demo) Rollouts(ctx context.Context, name string) ([]wire.RolloutInfo, error) {
	if err := wire.ValidService(name); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	svc := d.services[name]
	if svc == nil {
		return nil, nil
	}
	list := slices.Clone(svc.rollouts)
	for i := range list {
		list[i].Prepared, list[i].Activated, list[i].Awaited = 0, 0, 0
	}
	return list, nil
}

// History returns the samples of a connected zathras, oldest first.
func (d *demo) History(ctx context.Context, id wire.ID) ([]wire.Sample, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, err := d.connected(id)
	if err != nil {
		return nil, err
	}
	return slices.Clone(z.samples), nil
}

// Logs sends the ring of a connected zathras, and with follow what
// follows, until ctx is done or the zathras goes.
func (d *demo) Logs(ctx context.Context, id wire.ID, follow bool) (<-chan wire.LogLine, error) {
	d.mu.Lock()
	z, err := d.connected(id)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	left := z.left
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	ring, more := z.ring.Lines(), (<-chan wire.LogLine)(nil)
	if follow {
		ring, more = z.ring.Follow(ctx, followBuffer)
	}
	out := make(chan wire.LogLine)
	go func() {
		defer close(out)
		defer cancel()
		send := func(l wire.LogLine) bool {
			select {
			case out <- l:
				return true
			case <-ctx.Done():
			case <-left:
			}
			return false
		}
		for _, l := range ring {
			if !send(l) {
				return
			}
		}
		for follow {
			select {
			case l, ok := <-more:
				if !ok || !send(l) {
					return
				}
			case <-ctx.Done():
				return
			case <-left:
				return
			}
		}
	}()
	return out, nil
}

// Shell starts a shell on this machine: there is no container.
func (d *demo) Shell(ctx context.Context, id wire.ID, req wire.ShellRequest) (draal.Shell, error) {
	d.mu.Lock()
	z, err := d.connected(id)
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	left := z.left
	d.mu.Unlock()
	return startShell(id, req, left)
}

// ServiceOf returns the service of a connected zathras.
func (d *demo) ServiceOf(id wire.ID) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	z, err := d.connected(id)
	if err != nil {
		return "", false
	}
	return z.service.name, true
}
```

The samples, `web/demo/figures.go`:

```go
package main

import (
	"math/rand/v2"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// sampleEvery is how often a zathras has a new sample.
	sampleEvery = 2 * time.Second
	// history is how long samples are kept, and how far back they are
	// made up at the start.
	history = 15 * time.Minute

	kib = 1 << 10
	mib = 1 << 20
)

// figures makes up the samples of one container, each from the one
// before: a load that wanders, memory that creeps up and is collected,
// and a network that is quiet but for its bursts.
type figures struct {
	rng *rand.Rand

	load  float64 // cores the service uses when nothing happens
	limit uint64  // of memory, in bytes; 0 for none
	floor float64 // bytes the service holds after a collection
	ceil  float64 // bytes at which it collects

	cpu    float64
	memory float64
	burst  int     // samples the burst of the network still lasts
	rate   float64 // bytes per second of the burst
}

// newFigures returns the figures of a container. seed tells one container
// from another, and the same seed makes the same figures.
func newFigures(seed uint64, load float64, limit uint64) *figures {
	rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
	floor := (120 + 80*rng.Float64()) * mib
	return &figures{
		rng:    rng,
		load:   load,
		limit:  limit,
		floor:  floor,
		ceil:   floor * (1.8 + 0.6*rng.Float64()),
		cpu:    load,
		memory: floor * (1 + 0.5*rng.Float64()),
	}
}

// next returns the sample of the time at. A container whose service does
// not run has zathras alone in it.
func (f *figures) next(at time.Time, running bool) wire.Sample {
	if !running {
		f.cpu, f.memory, f.burst = 0.002, 14*mib, 0
		return wire.Sample{At: at, CPU: f.cpu, Memory: uint64(f.memory), MemoryLimit: f.limit}
	}
	if f.memory < f.floor/2 {
		f.memory = f.floor * 0.6 // just started
	}

	// The load is pulled back to where it belongs, and now and then
	// something asks a lot of it.
	f.cpu += 0.2*(f.load-f.cpu) + f.rng.NormFloat64()*f.load*0.12
	if f.rng.Float64() < 0.02 {
		f.cpu += f.load * (1 + 3*f.rng.Float64())
	}
	f.cpu = min(max(f.cpu, 0.003), 4)

	// Memory creeps up until the service collects.
	f.memory += (0.2 + 1.2*f.rng.Float64()) * mib
	if f.memory > f.ceil*(0.9+0.1*f.rng.Float64()) {
		f.memory = f.floor * (1 + 0.15*f.rng.Float64())
	}

	// The network: a murmur, and bursts of some seconds.
	if f.burst == 0 && f.rng.Float64() < 0.03 {
		f.burst = 3 + f.rng.IntN(12)
		f.rate = (1 + 7*f.rng.Float64()) * mib
	}
	rx := (20 + 40*f.rng.Float64()) * kib * (0.5 + f.cpu/f.load/2)
	if f.burst > 0 {
		f.burst--
		rx += f.rate * (0.7 + 0.6*f.rng.Float64())
	}

	// The disk: little is written all the time, and at times much is
	// read or written at once.
	read, write := 0.0, (4+30*f.rng.Float64())*kib
	if f.rng.Float64() < 0.04 {
		read = (0.5 + 20*f.rng.Float64()) * mib
	}
	if f.rng.Float64() < 0.03 {
		write += (1 + 6*f.rng.Float64()) * mib
	}

	return wire.Sample{
		At:          at,
		CPU:         f.cpu,
		Memory:      uint64(f.memory),
		MemoryLimit: f.limit,
		DiskRead:    read,
		DiskWrite:   write,
		NetRx:       rx,
		NetTx:       rx * (0.4 + 0.4*f.rng.Float64()),
	}
}
```

The lines of the logs, `web/demo/lines.go`:

```go
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
)

// The colours a service writes its levels in.
const (
	dim    = "\x1b[2m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[1;31m"
	cyan   = "\x1b[36m"
	reset  = "\x1b[0m"
)

// paths are what the service web is asked for.
var paths = []string{"/", "/api/orders", "/api/orders/8841", "/api/cart", "/api/checkout", "/healthz", "/static/app.css", "/api/search?q=caf%C3%A9"}

// line makes up the next line of the service of a zathras: the stream
// and the text. Most are what a service says of its requests; among them
// are lines with tabs, with colours, with characters that are wider than
// one column, lines on standard error, and one as long as a line may be.
func (d *demo) line(z *zathras, at time.Time) (stream, text string) {
	z.lines++
	n := z.lines
	clock := dim + at.UTC().Format("15:04:05.000") + reset
	took := fmt.Sprintf("%.1fms", 0.3+d.rng.ExpFloat64()*18)
	if z.service.name == "worker" {
		switch {
		case n%23 == 0:
			return logring.Stderr, fmt.Sprintf("%s %sWARN%s  job %d took %s, and its limit is 30s", clock, yellow, reset, 40000+n, time.Duration(31+n%20)*time.Second)
		case n%9 == 0:
			return logring.Stdout, fmt.Sprintf("%s %sINFO%s  queue jobs: %d waiting, %d in flight", clock, green, reset, d.rng.IntN(400), 1+d.rng.IntN(4))
		default:
			return logring.Stdout, fmt.Sprintf("%s %sINFO%s  job %d (resize-image) done in %s", clock, green, reset, 40000+n, took)
		}
	}
	switch {
	case n%211 == 0:
		// As long as a line may be: the ring cuts what is longer.
		head := clock + " " + cyan + "DEBUG" + reset + " request body: "
		return logring.Stdout, head + strings.Repeat("0123456789abcdef", (16<<10-len(head))/16+1)[:16<<10-len(head)]
	case n%61 == 0:
		return logring.Stderr, fmt.Sprintf("%s %sERROR%s upstream timed out after 5s: POST /api/checkout\tat checkout.submit (/build/web/checkout.go:214)\tat http.serve (/build/web/server.go:88)", clock, red, reset)
	case n%37 == 0:
		return logring.Stdout, fmt.Sprintf("%s %sINFO%s  注文 #%d を受け付けました ✓ — café Ünïcödé 🚀 — ｗｉｄｅ", clock, green, reset, 8000+n)
	case n%29 == 0:
		return logring.Stderr, fmt.Sprintf("%s %sWARN%s  pool: connection reset by peer, retrying (attempt %d)", clock, yellow, reset, 1+n%3)
	case n%17 == 0:
		return logring.Stdout, fmt.Sprintf(`{"level":"info","ts":%q,"msg":"cache refreshed","keys":%d,"took_ms":%d}`, at.UTC().Format(time.RFC3339Nano), 1000+d.rng.IntN(2000), 5+d.rng.IntN(60))
	case n%13 == 0:
		return logring.Stdout, fmt.Sprintf("GET\t%s\t200\t%s\t<b>not markup</b> &amp; \"quotes\"", paths[n%len(paths)], took)
	default:
		status, colour := 200, green
		switch {
		case n%19 == 0:
			status, colour = 404, yellow
		case n%43 == 0:
			status, colour = 502, red
		}
		method := "GET"
		if n%5 == 0 {
			method = "POST"
		}
		return logring.Stdout, fmt.Sprintf("%s %sINFO%s  http: %s %s %s%d%s %s", clock, green, reset, method, paths[n%len(paths)], colour, status, reset, took)
	}
}
```

The shell, `web/demo/shell.go`:

```go
package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/wire"
)

// shell is a real shell on this machine, as the draal.Shell of a made-up
// zathras. It ends as a session of draal does when its zathras goes:
// with unavailable.
type shell struct {
	zathras wire.ID
	session *ptyshell.Session
	stop    chan struct{} // closed by Close
	gone    atomic.Bool   // the zathras went while the shell ran
	once    sync.Once
}

// A shell is a draal.Shell.
var _ draal.Shell = (*shell)(nil)

// startShell starts a shell on this machine for the zathras. It is ended
// when left is closed: the zathras has gone.
func startShell(zathras wire.ID, req wire.ShellRequest, left <-chan struct{}) (*shell, error) {
	session, err := ptyshell.Start(ptyshell.Config{Term: req.Term, Cols: req.Cols, Rows: req.Rows})
	if err != nil {
		return nil, wire.Errorf(wire.CodeInternal, "starting a shell: %v", err)
	}
	sh := &shell{zathras: zathras, session: session, stop: make(chan struct{})}
	go func() {
		select {
		case <-left:
			sh.gone.Store(true)
			session.Close()
		case <-sh.stop:
		}
	}()
	return sh, nil
}

// Read returns what the shell printed, io.EOF when it has ended, and
// unavailable when the zathras went.
func (sh *shell) Read(p []byte) (int, error) {
	n, err := sh.session.Read(p)
	if errors.Is(err, io.EOF) && sh.gone.Load() {
		return n, wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: the zathras is gone", sh.zathras)
	}
	return n, err
}

// Write writes what was typed.
func (sh *shell) Write(p []byte) (int, error) {
	return sh.session.Write(p)
}

// Resize tells the shell the size of the terminal.
func (sh *shell) Resize(cols, rows uint16) error {
	return sh.session.Resize(cols, rows)
}

// Wait returns the exit status of the shell once it has ended.
func (sh *shell) Wait(ctx context.Context) (int, error) {
	status := make(chan int, 1)
	go func() { status <- sh.session.Wait() }()
	select {
	case s := <-status:
		return s, nil
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// Close ends the shell.
func (sh *shell) Close() error {
	sh.once.Do(func() { close(sh.stop) })
	return sh.session.Close()
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `nix develop .#bare --command go test -race ./web/demo/`
Expected: `ok  	github.com/fables-for-robots/great-machine/web/demo`

Run: `nix develop .#bare --command sh -c 'gofmt -l web; go vet ./web/...'`
Expected: no output.

- [ ] **Step 5: Run it and look**

Run, and leave running: `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:18080`
Expected, on standard error (the last two lines only while the page is not built):

```
demo: a made-up fleet behind the real API of great-machine, at http://127.0.0.1:18080/
demo: the shell of the page is a REAL SHELL ON THIS MACHINE, as the user who runs the demo
demo: the page is not built into this program, and http://127.0.0.1:18080/ answers 501. The API is there:
demo: run `npm run dev` in ui/, or build the page and give its directory with --dist
```

In another terminal:

Run: `curl -s http://127.0.0.1:18080/`
Expected: ``great-machine: the web page was not built into this program. Build the program with `nix build`, or the page with `npm run build` in ui/ and then the program.``

Run: `curl -s http://127.0.0.1:18080/api/services/web/rollouts | head -c 150`
Expected: `{"rollouts":[{"id":26,"service":"web","generation":20,"parallel":2,"state":"preparing","created":"` and a time (the state is `activating` or `completed` later in the minute).

Run: `curl -s -H 'Host: example.com' http://127.0.0.1:18080/api/services/fresh/rollouts`
Expected: `{"rollouts":[]}`: the demo answers under any host name, as draal does.

Run: `curl -s http://127.0.0.1:18080/api/watch`
Expected: `{"error":{"code":"bad-request","message":"/api/watch is a WebSocket"}}`

Run: `curl -s http://127.0.0.1:18080/api/services/fresh/rollouts`
Expected: `{"rollouts":[]}`

End the demo with Ctrl-C. It ends at once, and `go run` leaves nothing behind.

Run: `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:18080 --empty`
Expected: the first line is `demo: a fleet without a service behind the real API of great-machine, at http://127.0.0.1:18080/`. End it with Ctrl-C.

- [ ] **Step 6: Commit**

```bash
git add web/demo/
git commit -m "Web: a demo that serves the page over a made-up fleet"
```
