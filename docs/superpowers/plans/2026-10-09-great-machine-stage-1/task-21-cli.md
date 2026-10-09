### Task 21: cli

Every command of section 3 of the spec but `tui` (stage 3), and without
`--web-addr` of draal (stage 2): `draal`, `zathras`, `id`, `rollout`,
`rollback`, `status`, `logs`, `shell`. Spec sections 3, 6.1 and 13.

A command is a thin `Action` that reads the command line into a value and
calls a function `runX` that is given what it works with, so that the
tests run `runStatus`, `runLogs`, `runRollback` and `runRollout` against a
small scripted draal on the network in memory of `nodetest`. Nothing here
touches go-iroh in a test: the commands as a whole, against a real draal,
are the `e2e` package's.

**Files:**
- Create: `cli/human.go` (durations and sizes as text), `cli/progress.go`
- Create: `cli/flags.go` (the flags and argument checks the commands share), `cli/operator.go` (dialing draal as an operator), `cli/follow.go`
- Create: `cli/status.go`, `cli/logs.go`
- Create: `cli/rollout.go`, `cli/rollback.go`, `cli/shell.go`
- Create: `cli/draal.go`, `cli/zathras.go`, `cli/id.go`
- Modify: `cli/app.go` (Task 1's: `commands()` lists every command; `run` honors an exit status a command chooses; the whole file is in Step 23)
- Test: `cli/fake_test.go` (the scripted draal and the helpers of the tests), `cli/progress_test.go`, `cli/follow_test.go`, `cli/status_test.go`, `cli/logs_test.go`, `cli/rollout_test.go`, `cli/shell_test.go`, `cli/commands_test.go`

**Interfaces:**
- Consumes, from Task 1 (`cli/app.go`, `cli/version.go`):
  - `var Version`, `func versionCommand() *cli.Command`
  - `func usageError(format string, args ...any) error`: makes `Run` return 2.
  - `func newApp(stdout, stderr io.Writer) *cli.App`, `func run(ctx context.Context, app *cli.App, args []string) int`, `func commands() []*cli.Command`
  - `c.Context` of a command is done at the first SIGINT or SIGTERM. **No
    command of this task calls `signal.Notify`.** `c.App.Writer` and
    `c.App.ErrWriter` are the `stdout` and `stderr` of `Run`.
  - No flag has `Required: true`, and no command sets `OnUsageError`.
- Consumes, from the contract:
  - `wire`: `ID`, `Fleet`, `Service`, `Zathras`, `State`, `Phase` and its constants, `Sample`, `RolloutInfo`, `RolloutState` and its constants, `RolloutRequest`, `RollbackRequest`, `StoreInfo`, `LogLine`, `ShellRequest`, `ValidService(name string) error`; and in the tests `Request`, `Response`, `Read`, `Write`, `WriteError`, `Errorf`, `CodeOf`, `CodeBadRequest`.
  - `node`: `Key`, `NewKey() (Key, error)`, `LoadOrCreateKey(path string) (Key, error)`, `(Key).ID() wire.ID`, `ParseID(text string) (wire.ID, error)`, `Listen(ctx, ListenConfig{Key, Bind, Log}) (Listener, error)`, `Dial(ctx, DialConfig{Key, Server, Log}) (Conn, error)`, `Listener.ID()`, `Listener.LocalAddr()`, `Listener.Close()`; in the tests `Stream`.
  - `node/nodetest` (tests): `New() *Net`, `(*Net).Listen(id) node.Listener`, `(*Net).Dial(ctx, from, to) (node.Conn, error)`, `ID(n int) wire.ID`.
  - `operator`: `New(conn node.Conn) *Client`, `(*Client).Close`, `Store`, `Rollout`, `Rollback`, `Watch`, `Logs`, `Shell`, `(*Shell).Close`, `Resolve(f wire.Fleet, service, name string) (wire.Zathras, error)`.
  - `term`: `Size(f *os.File) (cols, rows uint16)`, `Attach(ctx, in *os.File, out io.Writer, s Session) (exit int, detached bool, err error)`.
  - `closure`: `Binary(path string) (binary, storePath string, err error)`, `CheckLinux(binary string) error`, `Of(ctx, storePath string) ([]string, error)`, `Names(storePaths []string) []string`.
  - `push`: `Config{Key, Server, Prefix, Jobs, TempDir, Progress}`, `Progress` (`Found(present, missing int)`, `Pushed(storePath string, bytes uint64)`), `Result{Pushed, Present}`, `Push(ctx, cfg, storePaths) (Result, error)`.
  - `db`: `Open(path string) (*DB, error)`, `(*DB).Close() error`. `authz`: `NewAllowlist(operators []wire.ID) Allowlist`. `draal`: `Config{DB, Authz, Store, Log}`, `New(cfg) *Draal`, `(*Draal).Serve(ctx, l node.Listener) error`.
  - `logring`: `New(size, maxLine int) *Ring`, `Stdout`, `Stderr`, `Zathras`. `supervisor`: `Config{Ring, Stdout, Stderr, StopTimeout, Log}`, `New`, `(*Supervisor).Run(ctx) error`, `(*Supervisor).State() wire.State`. `stats`: `NewReader() *Reader`. `preload`: `Client{URL}`. `ptyshell`: `Config{Shell}`. `health`: `Handler(phase func() wire.Phase) http.Handler`. `zathras`: `Config{Service, Name, Version, Dial, Supervisor, Ring, Stats, Preload, Shell, Log}`, `New`, `(*Agent).Run(ctx) error`.
- Produces:
  - The commands, through `cli.Run`. Exit status: 0; 1 for a failure; 2
    for a command line that is not valid; for `shell` the exit status of
    the remote shell; 130 for `rollout` and `rollback` interrupted while
    they follow, and for `shell` interrupted.
  - `run` returns the `ExitCode()` of a `cli.ExitCoder` a command returns
    (`cli.Exit(message, status)`), and prints its message unless it is
    empty.
  - draal logs `msg=serving endpoint=<id> …` at its start; zathras logs
    `msg=zathras endpoint=<id> …`.
  - The variables, besides those of the spec's tables:
    `GREAT_MACHINE_PARALLEL`, `GREAT_MACHINE_DETACH`, `GREAT_MACHINE_JOBS`,
    `GREAT_MACHINE_TEMP_DIR`, `GREAT_MACHINE_FOLLOW` (`logs -f`,
    `--follow`).

What the spec leaves open, and this task decides:

- **Flags come before the arguments**, as the library reads them. A word
  that begins with `-` behind SERVICE is refused with what to do about it
  (`logs web -f`), and is not taken for the name of a zathras.
- **`rollout SERVICE PATH -- ARG...`**: the library leaves the `--` in
  `c.Args()` when it follows an argument (it drops only one that comes
  before the first argument), so `rollout` cuts its arguments at the first
  `--` itself.
- **The counts of a rollout** are shown over the number of zathrases of
  the service that are connected: `preparing 2/5` is `Prepared` of
  `len(Zathrases)`, `activating 2/5` is `Activated` of it.
- **Progress off a terminal**: a step is one line when it ends. A step
  that counts (`pushing`, `preparing`, `activating`) also says where it is
  when the count has moved, every five seconds at most.
- **`logs` without `-f`** that is interrupted fails; with `-f` an
  interrupt is the way it ends, with 0. A log that ends by itself while
  it is followed (the zathras or draal has gone) is an error.

#### Progress, and text for people

- [ ] **Step 1: Write the failing test**

`cli/fake_test.go` holds the scripted draal and what the tests of this
task share. It is used from the next cycle on; it is written whole now.

```go
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
	refuse error            // or this, if it is set

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
	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "JACCARD_SERVER", "JACCARD_PREFIX", "GREAT_MACHINE_BIND",
	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME", "GREAT_MACHINE_PRELOAD_URL",
	"GREAT_MACHINE_SHELL", "GREAT_MACHINE_STOP_TIMEOUT", "GREAT_MACHINE_HEALTH_ADDR",
	"GREAT_MACHINE_KEY", "GREAT_MACHINE_PARALLEL", "GREAT_MACHINE_DETACH", "GREAT_MACHINE_JOBS",
	"GREAT_MACHINE_TEMP_DIR", "GREAT_MACHINE_FOLLOW",
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
```

`cli/progress_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a clock the test moves.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *clock { return &clock{at: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func TestProgressPlain(t *testing.T) {
	var out buffer
	c := newClock()
	p := &progress{out: &out, now: c.now, width: func() int { return 0 }}

	p.begin("reading the closure")
	c.advance(340 * time.Millisecond)
	p.done("42 paths")

	p.begin("connecting to draal")
	c.advance(1200 * time.Millisecond)
	p.done("")

	// A step that counts says where it is when it has moved, and every
	// five seconds at most.
	p.begin("pushing")
	p.count(0, 3)
	c.advance(6 * time.Second)
	p.add(1)
	c.advance(time.Second)
	p.add(1) // moved, but the last line is a second old
	c.advance(time.Second)
	p.count(2, 3) // and now two seconds
	c.advance(4 * time.Second)
	p.count(2, 3) // it has moved since 1/3 was said, five seconds ago: a line
	c.advance(time.Second)
	p.add(1) // moved, but the last line is a second old
	p.done("3 paths, 1.5 KiB")

	p.begin("preparing")
	c.advance(75 * time.Second)
	p.fail(errors.New("web-b: the store has no such path"))
	p.fail(errors.New("a second failure has no step to mark"))

	p.begin("activating")
	c.advance(2 * time.Second)
	p.fail(context.Canceled)

	// A step that is begun while another runs ends that one.
	p.begin("one")
	p.begin("two")
	p.finish("all of it")
	p.begin("nothing is shown after the end")
	p.close()

	want := strings.Join([]string{
		"reading the closure: 42 paths (0.3s)",
		"connecting to draal (1.2s)",
		"pushing: 1/3 after 6.0s",
		"pushing: 2/3 after 12.0s",
		"pushing: 3 paths, 1.5 KiB (13.0s)",
		"preparing: failed after 1m15s",
		"activating: interrupted after 2.0s",
		"one (0.0s)",
		"two (0.0s)",
		"total: all of it (1m31s)",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// screen returns what a terminal shows after it was written text: its
// lines, and whether the cursor is at the start of a line that is empty.
// It knows the three things progress writes besides text: a carriage
// return, "erase to the end of the line" and a line feed.
func screen(t *testing.T, text string) (lines []string, fresh bool) {
	t.Helper()
	line, col := []rune{}, 0
	for len(text) > 0 {
		switch {
		case strings.HasPrefix(text, "\033[K"):
			line = line[:col]
			text = text[3:]
			continue
		case text[0] == '\r':
			col = 0
		case text[0] == '\n':
			lines = append(lines, string(line))
			line, col = []rune{}, 0
		case text[0] == '\033':
			t.Fatalf("an escape sequence the test does not know: %q", text)
		default:
			r := []rune(text)[0]
			if col < len(line) {
				line[col] = r
			} else {
				line = append(line, r)
			}
			col++
			text = text[len(string(r)):]
			continue
		}
		text = text[1:]
	}
	return lines, len(line) == 0 && col == 0
}

func TestProgressOnATerminal(t *testing.T) {
	var out buffer
	c := newClock()
	// every is 0: the step is redrawn when it changes, and by no ticker.
	width := 80
	p := &progress{out: &out, live: true, now: c.now, width: func() int { return width }}

	p.begin("reading the closure")
	c.advance(340 * time.Millisecond)
	p.done("42 paths")

	p.begin("pushing")
	p.count(0, 4)
	c.advance(2 * time.Second)
	p.add(1)
	lines, _ := screen(t, out.String()+"\n")
	if got, want := lines[len(lines)-1], "⠼ pushing                    2.0s  ██████░░░░░░░░░░░░░░░░░░  1/4"; got != want {
		t.Errorf("the running step:\n%q\nwant:\n%q", got, want)
	}
	p.add(3)
	p.done("4 paths, 2.0 MiB")

	// A line that is longer than the terminal is wide is cut: one that
	// wraps cannot be drawn over.
	width = 60
	p.begin("a step with a name that is longer than its column")
	p.count(1, 2)
	lines, _ = screen(t, out.String()+"\n")
	if got := []rune(lines[len(lines)-1]); len(got) != 59 {
		t.Errorf("the running step is %d wide on a terminal of 60: %q", len(got), string(got))
	}
	p.fail(errors.New("it failed"))

	p.begin("activating")
	c.advance(time.Minute)
	p.finish("rollout #13 completed")

	want := []string{
		"✓ reading the closure        0.3s  42 paths",
		"✓ pushing                    2.0s  4 paths, 2.0 MiB",
		"✗ a step with a name that is longer than its column   0.0s  failed",
		"✓ activating                1m00s",
		"  total                     1m02s  rollout #13 completed",
	}
	lines, fresh := screen(t, out.String())
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("the screen:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !fresh {
		t.Error("the cursor is not at the start of a fresh line")
	}
}

// A show that is closed in the middle of a step, as after --detach or an
// error nobody marked, takes the line of that step off the terminal.
func TestProgressClosedInAStep(t *testing.T) {
	var out buffer
	p := &progress{out: &out, live: true, now: newClock().now, width: func() int { return 80 }, every: time.Millisecond}
	p.begin("connecting to draal")
	p.done("")
	p.begin("rolling out")
	// The ticker redraws the step by itself.
	before := len(out.String())
	waitFor(t, "the step to be redrawn", func() bool { return len(out.String()) > before })
	p.close()
	p.close()
	lines, fresh := screen(t, out.String())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "✓ connecting to draal") || !fresh {
		t.Errorf("the screen: %q, fresh line: %v", lines, fresh)
	}
}

func TestByteSize(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{{0, "0 B"}, {1023, "1023 B"}, {1536, "1.5 KiB"}, {126353408, "120.5 MiB"}, {3 << 30, "3.0 GiB"}}
	for _, tt := range tests {
		if got := byteSize(tt.n); got != tt.want {
			t.Errorf("byteSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./cli/`
Expected: FAIL, the test does not build:

```
cli/fake_test.go:154:34: undefined: progress
cli/progress_test.go:35:8: undefined: progress
cli/progress_test.go:209:13: undefined: byteSize
```

(among others of the same kind).

- [ ] **Step 3: Write the implementation**

`cli/human.go`:

```go
package cli

import (
	"fmt"
	"time"
)

// took writes how long a step took: tenths of a second up to a minute,
// which is where they matter, and whole seconds or minutes beyond.
func took(d time.Duration) string {
	switch {
	case d < 0:
		return "0.0s"
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Truncate(100*time.Millisecond).Seconds())
	}
	return age(d)
}

// age writes for how long something has been so, in two units at most:
// 42s, 3m12s, 5h03m, 2d04h.
func age(d time.Duration) string {
	s := int64(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 24*3600:
		return fmt.Sprintf("%dh%02dm", s/3600, s/60%60)
	}
	return fmt.Sprintf("%dd%02dh", s/(24*3600), s/3600%24)
}

// byteSize writes a number of bytes with a binary unit: 512 B, 1.5 KiB,
// 120.5 MiB.
func byteSize(n uint64) string {
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
```

`cli/progress.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	xterm "golang.org/x/term"
)

// progress shows on standard error what a rollout is doing, as
// jaccard-store shows a push. Every step leaves a line: its name, the
// time it took and what came of it. On a terminal the line of the running
// step is redrawn in place, with a bar when the step counts what it does.
// Elsewhere a step is one plain line when it ends, and a step that counts
// says where it is every few seconds, so that a log shows it moving.
//
// Its methods may be called from several goroutines.
type progress struct {
	out   io.Writer
	live  bool       // out is a terminal
	width func() int // of the terminal; 0 for unknown
	now   func() time.Time
	every time.Duration // between two redrawings on a terminal; 0 for none

	mu      sync.Mutex
	began   time.Time // when the first step began; zero before it
	step    *step     // the running one; nil between steps
	frame   int
	closed  bool
	stop    chan struct{} // closed to end the redrawing; nil when none runs
	stopped chan struct{} // closed when the redrawing has ended
}

// step is the running step.
type step struct {
	name        string
	start       time.Time
	counts      bool // it says how far it is
	done, total uint64
	said        time.Time // when a line was last written for it off a terminal
	saidDone    uint64
}

const (
	// liveEvery is how often the running step is redrawn on a terminal,
	// and plainEvery how often at most it gets a line elsewhere.
	liveEvery  = 100 * time.Millisecond
	plainEvery = 5 * time.Second
	// nameWidth and timeWidth are the columns a line on a terminal begins
	// with: what is done and for how long.
	nameWidth = 24
	timeWidth = 6
	barWidth  = 24
)

// newProgress returns the progress of a command that writes to out. It
// draws when out is a terminal.
func newProgress(out io.Writer) *progress {
	p := &progress{out: out, now: time.Now, width: func() int { return 0 }}
	f, ok := out.(*os.File)
	if !ok || !xterm.IsTerminal(int(f.Fd())) || os.Getenv("TERM") == "dumb" {
		return p
	}
	p.live, p.every = true, liveEvery
	p.width = func() int {
		w, _, err := xterm.GetSize(int(f.Fd()))
		if err != nil || w <= 0 {
			return 80
		}
		return w
	}
	return p
}

// begin starts a step. A step that is running ends first, with nothing to
// say for itself.
func (p *progress) begin(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.end("")
	now := p.now()
	if p.began.IsZero() {
		p.began = now
	}
	p.step = &step{name: name, start: now, said: now}
	p.draw(now)
	if p.live && p.every > 0 && p.stop == nil {
		p.stop, p.stopped = make(chan struct{}), make(chan struct{})
		go p.run(p.stop, p.stopped)
	}
}

// count says how far the running step is: done of total.
func (p *progress) count(done, total uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step == nil {
		return
	}
	p.step.counts, p.step.done, p.step.total = true, done, total
	p.draw(p.now())
}

// add says that the running step has done n more.
func (p *progress) add(n uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step == nil {
		return
	}
	p.step.done += n
	p.draw(p.now())
}

// done ends the running step and leaves its line: the time it took and
// what came of it.
func (p *progress) done(summary string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.end(summary)
}

// end is done with p.mu held.
func (p *progress) end(summary string) {
	s := p.step
	if s == nil {
		return
	}
	p.step = nil
	p.leave("✓", s.name, p.now().Sub(s.start), summary)
}

// fail marks the running step as the one err came in, and leaves its
// line. Without a running step it does nothing.
func (p *progress) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.step
	if s == nil {
		return
	}
	p.step = nil
	how := "failed"
	if errors.Is(err, context.Canceled) {
		how = "interrupted"
	}
	d := p.now().Sub(s.start)
	if p.live {
		fmt.Fprintf(p.out, "\r\033[K%s\n", liveLine("✗", s.name, took(d), how))
		return
	}
	fmt.Fprintf(p.out, "%s: %s after %s\n", s.name, how, took(d))
}

// finish ends the show with a last line: what came of it all, and the
// time of all steps together.
func (p *progress) finish(summary string) {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.end("")
	if p.began.IsZero() {
		return
	}
	p.leave(" ", "total", p.now().Sub(p.began), summary)
	p.began = time.Time{}
}

// close ends the show without a word. The line of a step nobody ended is
// taken off the terminal. It may be called more than once, and nothing is
// shown after it.
func (p *progress) close() {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.step != nil && p.live {
		fmt.Fprint(p.out, "\r\033[K")
	}
	p.step = nil
}

// halt ends the redrawing, and with it the taking of new steps.
func (p *progress) halt() {
	p.mu.Lock()
	stop, stopped := p.stop, p.stopped
	p.stop, p.closed = nil, true
	p.mu.Unlock()
	if stop != nil {
		close(stop)
		<-stopped
	}
}

// run redraws the running step now and then until stop is closed, so that
// its time moves while nothing else does.
func (p *progress) run(stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	t := time.NewTicker(p.every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.mu.Lock()
			if p.step != nil {
				p.draw(p.now())
			}
			p.mu.Unlock()
		}
	}
}

// leave writes the line a step leaves behind. p.mu is held.
func (p *progress) leave(mark, name string, d time.Duration, summary string) {
	if p.live {
		fmt.Fprintf(p.out, "\r\033[K%s\n", liveLine(mark, name, took(d), summary))
		return
	}
	if summary == "" {
		fmt.Fprintf(p.out, "%s (%s)\n", name, took(d))
		return
	}
	fmt.Fprintf(p.out, "%s: %s (%s)\n", name, summary, took(d))
}

// draw shows the running step: over its line on a terminal, and elsewhere
// as a line of its own when it counts, has moved, and the last line is
// plainEvery old. p.mu is held.
func (p *progress) draw(now time.Time) {
	s := p.step
	if s == nil {
		return
	}
	if p.live {
		p.frame++
		spinner := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		rest := ""
		if s.counts {
			rest = fmt.Sprintf("%s  %d/%d", bar(s.done, s.total), s.done, s.total)
		}
		line := liveLine(string(spinner[p.frame%len(spinner)]), s.name, took(now.Sub(s.start)), rest)
		fmt.Fprintf(p.out, "\r\033[K%s", fit(line, p.width()))
		return
	}
	if !s.counts || s.done == s.saidDone || now.Sub(s.said) < plainEvery {
		return
	}
	s.said, s.saidDone = now, s.done
	fmt.Fprintf(p.out, "%s: %d/%d after %s\n", s.name, s.done, s.total, took(now.Sub(s.start)))
}

// liveLine is a line on a terminal: a mark, what is done, for how long,
// and what there is to say of it.
func liveLine(mark, name, time, rest string) string {
	line := fmt.Sprintf("%s %-*s %*s", mark, nameWidth, name, timeWidth, time)
	if rest != "" {
		line += "  " + rest
	}
	return line
}

// bar draws how much of total is done.
func bar(done, total uint64) string {
	filled := barWidth
	if done < total {
		filled = int(done * barWidth / total)
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
}

// fit cuts a line that is redrawn in place to what fits a terminal of
// width columns: a line that wraps cannot be drawn over.
func fit(line string, width int) string {
	if width <= 1 || utf8.RuneCountInString(line) < width {
		return line
	}
	return string([]rune(line)[:width-1])
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go mod tidy && go vet ./cli/ && go test ./cli/ -race -run 'Progress|ByteSize' -v`
Expected: PASS for `TestProgressPlain`, `TestProgressOnATerminal`,
`TestProgressClosedInAStep` and `TestByteSize`. `golang.org/x/term` is in
`go.mod` since Task 20.

- [ ] **Step 5: Commit**

```bash
git add cli/ go.mod go.sum
git commit -m "CLI: the progress of a rollout"
```

#### Flags, dialing draal, following a rollout

- [ ] **Step 6: Write the failing test**

`cli/follow_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rolloutFleet is a fleet in which the last rollout of web, which has
// three zathrases, is r.
func rolloutFleet(r wire.RolloutInfo) wire.Fleet {
	r.Service = "web"
	return wire.Fleet{Services: []wire.Service{
		{Name: "api", Rollout: &wire.RolloutInfo{ID: 99, Service: "api", State: wire.RolloutHalted, Reason: "not ours"}},
		{Name: "web", Rollout: &r, Zathrases: make([]wire.Zathras, 3)},
	}}
}

// fleetsOf returns a channel that holds the fleets and, with closed, has
// ended behind them.
func fleetsOf(closed bool, fleets ...wire.Fleet) <-chan wire.Fleet {
	ch := make(chan wire.Fleet, len(fleets))
	for _, f := range fleets {
		ch <- f
	}
	if closed {
		close(ch)
	}
	return ch
}

func TestFollow(t *testing.T) {
	const hint = "rollout #13 of web goes on; look at it with: great-machine status web"
	before := rolloutFleet(wire.RolloutInfo{ID: 12, State: wire.RolloutCompleted})
	preparing := rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 1})
	activating := rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 1})

	tests := []struct {
		name   string
		fleets <-chan wire.Fleet
		err    string // "" for nil
		status int    // of a cli.ExitCoder; 0 for an error that is none
		output string
	}{
		{
			name: "completed",
			// The first fleet is older than the rollout, and is passed over.
			fleets: fleetsOf(false, before, wire.Fleet{}, preparing, activating,
				rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})),
			output: "preparing: 3 zathrases (0.0s)\nactivating: 3 zathrases (0.0s)\n",
		},
		{
			name:   "completed before it was looked at",
			fleets: fleetsOf(false, rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})),
		},
		{
			name: "halted",
			fleets: fleetsOf(false, preparing, activating,
				rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1 after 0.2s"})),
			err:    "rollout #13 of web halted: web-b: exited with status 1 after 0.2s",
			output: "preparing: 3 zathrases (0.0s)\n",
		},
		{
			name:   "superseded",
			fleets: fleetsOf(false, preparing, rolloutFleet(wire.RolloutInfo{ID: 14, State: wire.RolloutPreparing})),
			err:    "rollout #13 of web was superseded by rollout #14",
		},
		{
			name:   "draal went away",
			fleets: fleetsOf(true, preparing),
			err:    "the connection to draal ended: " + hint,
		},
		{
			name:   "interrupted",
			fleets: nil, // set below: nothing comes, and the context is done
			err:    "interrupted: " + hint,
			status: 130,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.fleets == nil {
				tt.fleets = fleetsOf(false)
				cancel()
			}
			var out buffer
			err := follow(ctx, tt.fleets, "web", 13, plainProgress(&out))
			switch {
			case tt.err == "" && err != nil:
				t.Fatalf("follow returned %v, want nil", err)
			case tt.err != "" && (err == nil || err.Error() != tt.err):
				t.Fatalf("follow returned %v, want %q", err, tt.err)
			}
			var coder cli.ExitCoder
			if got := errors.As(err, &coder); got != (tt.status != 0) || got && coder.ExitCode() != tt.status {
				t.Errorf("follow returned %#v, want an error with the exit status %d", err, tt.status)
			}
			if got := out.String(); got != tt.output {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.output)
			}
		})
	}
}

// An interrupt in the middle of a step: the step says so, and the error
// is the hint with the status 130. The same goes for a watch that ended
// because the context is done.
func TestFollowInterrupted(t *testing.T) {
	for _, closed := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		fleets := make(chan wire.Fleet)
		var out buffer
		done := make(chan error, 1)
		go func() { done <- follow(ctx, fleets, "web", 13, plainProgress(&out)) }()
		fleets <- rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Activated: 1})
		cancel()
		if closed {
			close(fleets)
		}
		err := <-done
		var coder cli.ExitCoder
		if !errors.As(err, &coder) || coder.ExitCode() != 130 || !strings.Contains(err.Error(), "great-machine status web") {
			t.Errorf("closed %v: follow returned %v, want the hint with the exit status 130", closed, err)
		}
		if got, want := out.String(), "activating: interrupted after 0.0s\n"; got != want {
			t.Errorf("closed %v: output %q, want %q", closed, got, want)
		}
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `go test ./cli/`
Expected: FAIL, the test does not build:

```
cli/follow_test.go:93:11: undefined: follow
cli/follow_test.go:120:23: undefined: follow
```

- [ ] **Step 8: Write the implementation**

`cli/flags.go`:

```go
package cli

import (
	"strings"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// The flags that more than one command has. Each is a function: a flag
// keeps the value it was given, and so cannot be shared by two commands.

// draalFlag is --draal, of zathras and of every operator command.
func draalFlag() cli.Flag {
	return &cli.StringFlag{Name: "draal", EnvVars: []string{"GREAT_MACHINE_DRAAL"},
		Usage: "endpoint `ID` of draal, as draal logs it at its start (required)"}
}

// keyFlag is --key, of every operator command.
func keyFlag() cli.Flag {
	return &cli.StringFlag{Name: "key", EnvVars: []string{"GREAT_MACHINE_KEY"},
		Usage: "`FILE` of the operator's key, created on first use (default: great-machine/operator.key in the user's configuration directory)"}
}

// parallelFlag is --parallel, of rollout and rollback.
func parallelFlag() cli.Flag {
	return &cli.IntFlag{Name: "parallel", EnvVars: []string{"GREAT_MACHINE_PARALLEL"}, Value: 1,
		Usage: "zathrases restarted at a time; 0 is all of them at once"}
}

// detachFlag is --detach, of rollout and rollback.
func detachFlag() cli.Flag {
	return &cli.BoolFlag{Name: "detach", EnvVars: []string{"GREAT_MACHINE_DETACH"},
		Usage: "start the rollout and do not wait for it"}
}

// operatorFlags are the flags of every command that talks to draal as an
// operator, with the command's own behind them.
func operatorFlags(own ...cli.Flag) []cli.Flag {
	return append([]cli.Flag{draalFlag(), keyFlag()}, own...)
}

// required returns the value of a flag that has to be set, by itself or
// by its variable.
func required(c *cli.Context, name, variable string) (string, error) {
	v := strings.TrimSpace(c.String(name))
	if v == "" {
		return "", usageError("--%s is required: give it, or set %s", name, variable)
	}
	return v, nil
}

// draalID returns the endpoint ID that --draal names.
func draalID(c *cli.Context) (wire.ID, error) {
	text, err := required(c, "draal", "GREAT_MACHINE_DRAAL")
	if err != nil {
		return "", err
	}
	id, err := node.ParseID(text)
	if err != nil {
		return "", usageError("--draal: %q is not an endpoint ID, as draal logs one at its start: %w", text, err)
	}
	return id, nil
}

// parallel returns the value of --parallel.
func parallel(c *cli.Context) (int, error) {
	n := c.Int("parallel")
	if n < 0 {
		return 0, usageError("--parallel: %d is not a number of zathrases: want 0 for all of them at once, or more", n)
	}
	return n, nil
}

// arguments returns the arguments of a command that takes those named, of
// which the first min have to be there. The library stops reading flags
// at the first argument, so a flag behind it would be taken for one: that
// is refused here, with what to do about it.
func arguments(c *cli.Context, min int, names ...string) ([]string, error) {
	args := c.Args().Slice()
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return nil, usageError("%s: the flag %s has to come before %s", c.Command.Name, arg, names[0])
		}
	}
	if len(args) > len(names) {
		return nil, usageError("%s: unexpected argument %q", c.Command.Name, args[len(names)])
	}
	if len(args) < min {
		return nil, usageError("%s: %s is missing; usage: great-machine %s %s", c.Command.Name, names[len(args)], c.Command.Name, c.Command.ArgsUsage)
	}
	return args, nil
}

// noArguments refuses any argument, for a command that takes none.
func noArguments(c *cli.Context) error {
	if c.Args().Present() {
		return usageError("%s takes no argument, got %q", c.Command.Name, c.Args().First())
	}
	return nil
}

// service returns name if it can be the name of a service.
func service(name string) (string, error) {
	if err := wire.ValidService(name); err != nil {
		return "", usageError("%q is not the name of a service: want a DNS label, such as web or api-2", name)
	}
	return name, nil
}
```

`cli/operator.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// dialTimeout is how long an operator command tries to reach draal.
const dialTimeout = 30 * time.Second

// operatorSettings is what the flags of every operator command say.
type operatorSettings struct {
	draal wire.ID
	key   string // the file of the operator's key
}

// readOperator reads --draal and --key. A command calls it before it does
// anything, so that a command line that is not valid is refused at once.
func readOperator(c *cli.Context) (operatorSettings, error) {
	id, err := draalID(c)
	if err != nil {
		return operatorSettings{}, err
	}
	path, err := keyPath(c)
	if err != nil {
		return operatorSettings{}, err
	}
	return operatorSettings{draal: id, key: path}, nil
}

// keyPath returns the file of the operator's key: what --key says, or
// great-machine/operator.key in the user's configuration directory.
func keyPath(c *cli.Context) (string, error) {
	if path := c.String("key"); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no place for the operator's key, give one with --key: %w", err)
	}
	return filepath.Join(dir, "great-machine", "operator.key"), nil
}

// connect dials draal as the operator the settings name, and returns the
// client and the operator's key. The caller closes the client.
func (s operatorSettings) connect(ctx context.Context) (*operator.Client, node.Key, error) {
	k, err := node.LoadOrCreateKey(s.key)
	if err != nil {
		return nil, node.Key{}, fmt.Errorf("the operator's key: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := node.Dial(ctx, node.DialConfig{Key: k, Server: s.draal})
	if err != nil {
		return nil, node.Key{}, fmt.Errorf("connecting to draal %s: %w", s.draal, err)
	}
	return operator.New(conn), k, nil
}

// connect reads the flags of an operator command and dials draal.
func connect(ctx context.Context, c *cli.Context) (*operator.Client, node.Key, error) {
	s, err := readOperator(c)
	if err != nil {
		return nil, node.Key{}, err
	}
	return s.connect(ctx)
}

// fleet asks draal for the fleet as it is now.
func fleet(ctx context.Context, client *operator.Client) (wire.Fleet, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fleets, err := client.Watch(ctx)
	if err != nil {
		return wire.Fleet{}, fmt.Errorf("asking draal for the fleet: %w", err)
	}
	select {
	case f, ok := <-fleets:
		if !ok {
			if err := ctx.Err(); err != nil {
				return wire.Fleet{}, err
			}
			return wire.Fleet{}, errors.New("asking draal for the fleet: the connection ended")
		}
		return f, nil
	case <-ctx.Done():
		return wire.Fleet{}, ctx.Err()
	}
}

// interrupted is what a command returns that was interrupted: the status
// 130, as a shell gives a program that SIGINT ended, and why.
func interrupted(format string, args ...any) error {
	return cli.Exit(fmt.Sprintf(format, args...), 130)
}
```

`cli/follow.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
)

// follow shows the rollout id of a service, out of the fleets draal
// sends, until the rollout has ended. It returns nil when the rollout
// completed, and an error that says what became of it otherwise: it
// halted, with the reason; it was superseded, with the rollout that did
// so; draal went away; or ctx is done, which is an interrupt and status
// 130. The last two do not end the rollout, and the error says how to
// look at it.
//
// A fleet that does not have the rollout yet is passed over: draal
// answers a rollout before it sends the fleet that shows it.
func follow(ctx context.Context, fleets <-chan wire.Fleet, service string, id uint64, p *progress) error {
	goesOn := fmt.Sprintf("rollout #%d of %s goes on; look at it with: great-machine status %s", id, service, service)
	interrupt := func() error {
		p.fail(context.Canceled)
		return interrupted("interrupted: %s", goesOn)
	}

	// phase is the step that is shown: "", preparing or activating.
	phase := ""
	zathrases := 0
	enter := func(next string) {
		if phase == next {
			return
		}
		if phase != "" {
			p.done(fmt.Sprintf("%d zathrases", zathrases))
		}
		if next != "" {
			p.begin(next)
		}
		phase = next
	}

	for {
		var f wire.Fleet
		select {
		case <-ctx.Done():
			return interrupt()
		case next, ok := <-fleets:
			if !ok {
				// The watch ends with ctx as well as with draal.
				if ctx.Err() != nil {
					return interrupt()
				}
				return fmt.Errorf("the connection to draal ended: %s", goesOn)
			}
			f = next
		}

		var r *wire.RolloutInfo
		for _, s := range f.Services {
			if s.Name == service {
				r, zathrases = s.Rollout, len(s.Zathrases)
			}
		}
		switch {
		case r == nil || r.ID < id:
			continue
		case r.ID > id:
			return fmt.Errorf("rollout #%d of %s was superseded by rollout #%d", id, service, r.ID)
		}
		switch r.State {
		case wire.RolloutPreparing:
			enter("preparing")
			p.count(uint64(r.Prepared), uint64(zathrases))
		case wire.RolloutActivating:
			enter("activating")
			p.count(uint64(r.Activated), uint64(zathrases))
		case wire.RolloutCompleted:
			enter("")
			return nil
		case wire.RolloutHalted:
			return fmt.Errorf("rollout #%d of %s halted: %s", id, service, r.Reason)
		case wire.RolloutSuperseded:
			return fmt.Errorf("rollout #%d of %s was superseded", id, service)
		default:
			return fmt.Errorf("rollout #%d of %s is in a state this program does not know: %q", id, service, r.State)
		}
	}
}

// watchRollout is what rollout and rollback do once draal has answered
// with the rollout: with detach they print its number and leave, and
// otherwise they follow it to its end.
func watchRollout(ctx context.Context, client *operator.Client, r wire.RolloutInfo, detach bool, stdout io.Writer, p *progress) error {
	if detach {
		p.close()
		_, err := fmt.Fprintf(stdout, "rollout #%d of %s to generation %d was started; look at it with: great-machine status %s\n",
			r.ID, r.Service, r.Generation, r.Service)
		return err
	}
	watching, cancel := context.WithCancel(ctx)
	defer cancel()
	fleets, err := client.Watch(watching)
	if err != nil {
		return fmt.Errorf("watching rollout #%d of %s, which goes on: %w", r.ID, r.Service, err)
	}
	if err := follow(ctx, fleets, r.Service, r.ID, p); err != nil {
		return err
	}
	p.finish(fmt.Sprintf("rollout #%d completed: %s runs generation %d", r.ID, r.Service, r.Generation))
	return nil
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go vet ./cli/ && go test ./cli/ -race -run Follow -v`
Expected: PASS for `TestFollow` (six cases: completed, completed before
it was looked at, halted, superseded, draal went away, interrupted) and
`TestFollowInterrupted`.

- [ ] **Step 10: Commit**

```bash
git add cli/
git commit -m "CLI: shared flags, the operator's connection, following a rollout"
```

#### status and logs

- [ ] **Step 11: Write the failing test**

`cli/status_test.go`:

```go
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

var statusNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestFormatFleet(t *testing.T) {
	want := strings.Join([]string{
		"SERVICE  STABLE  ZATHRASES                     LAST ROLLOUT",
		"api      -       none                          -",
		"web      7       2 running, 1 backoff, 1 idle  #13 activating 2/4",
		"",
	}, "\n")
	if got := formatFleet(testFleet(statusNow)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, want := formatFleet(wire.Fleet{}), "no services: no zathras is connected and nothing was rolled out\n"; got != want {
		t.Errorf("an empty fleet: got %q, want %q", got, want)
	}
}

func TestRolloutLine(t *testing.T) {
	zathrases := make([]wire.Zathras, 5)
	tests := []struct {
		rollout *wire.RolloutInfo
		want    string
	}{
		{nil, "-"},
		{&wire.RolloutInfo{ID: 12, State: wire.RolloutCompleted}, "#12 completed"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 1}, "#13 preparing 1/5"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 5, Activated: 2}, "#13 activating 2/5"},
		{&wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1"}, "#13 halted: web-b: exited with status 1"},
		{&wire.RolloutInfo{ID: 11, State: wire.RolloutSuperseded}, "#11 superseded"},
	}
	for _, tt := range tests {
		if got := rolloutLine(wire.Service{Rollout: tt.rollout, Zathrases: zathrases}); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestFormatService(t *testing.T) {
	want := strings.Join([]string{
		"web: stable generation 7, last rollout #13 activating 2/4",
		"rollout #13: to generation 8, 1 at a time, begun 1m35s ago",
		"NAME   ID        PHASE    GENERATION  SINCE          RESTARTS  CPU   MEMORY",
		"web-a  a1a1a1a1  running  8           up 3m12s       0         0.25  120.5 MiB of 512.0 MiB",
		"web-b  b2b2b2b2  running  8           up 31s         0         1.50  2.0 KiB",
		"web-c  c3c3c3c3  backoff  7           restart in 4s  3         -     -",
		"web-d  d4d4d4d4  idle     -           -              0         -     -",
		"",
	}, "\n")
	got, err := formatService(testFleet(statusNow), "web", statusNow)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	want = "api: stable generation -, last rollout -\nno zathras is connected\n"
	if got, err = formatService(testFleet(statusNow), "api", statusNow); err != nil || got != want {
		t.Errorf("a service without zathrases: got %q, %v; want %q", got, err, want)
	}

	_, err = formatService(testFleet(statusNow), "wbe", statusNow)
	if err == nil || err.Error() != `no service "wbe"; there are: api, web` {
		t.Errorf("an unknown service: got %v", err)
	}
}

func TestSince(t *testing.T) {
	tests := []struct {
		state wire.State
		want  string
	}{
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 42 * time.Second}, "up 42s"},
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 5*time.Hour + 3*time.Minute}, "up 5h03m"},
		{wire.State{Phase: wire.PhaseRunning, RunningFor: 52 * time.Hour}, "up 2d04h"},
		{wire.State{Phase: wire.PhaseBackoff, RestartAt: statusNow.Add(90 * time.Second)}, "restart in 1m30s"},
		{wire.State{Phase: wire.PhaseBackoff, RestartAt: statusNow.Add(-time.Second)}, "restart now"},
		{wire.State{Phase: wire.PhaseStopping}, "-"},
		{wire.State{Phase: wire.PhaseIdle}, "-"},
	}
	for _, tt := range tests {
		if got := since(tt.state, statusNow); got != tt.want {
			t.Errorf("%+v: got %q, want %q", tt.state, got, tt.want)
		}
	}
}

// TestRunStatus asks a draal for the fleet, over the protocol.
func TestRunStatus(t *testing.T) {
	fake := &fakeDraal{fleets: []wire.Fleet{testFleet(statusNow)}}
	client := fake.start(t)

	var out buffer
	if err := runStatus(context.Background(), client, "", &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != formatFleet(testFleet(statusNow)) {
		t.Errorf("without a service:\n%s", got)
	}

	out = buffer{}
	if err := runStatus(context.Background(), client, "web", &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "web-c  c3c3c3c3  backoff  7           restart in 4s  3") {
		t.Errorf("with a service:\n%s", got)
	}

	err := runStatus(context.Background(), client, "db", &out, statusNow)
	if err == nil || !strings.Contains(err.Error(), `no service "db"`) {
		t.Errorf("an unknown service: got %v", err)
	}
}
```

`cli/logs_test.go`:

```go
package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

func testLines() []wire.LogLine {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	return []wire.LogLine{
		{At: at, Stream: logring.Zathras, Text: "started generation 8: /nix/store/abc-web/bin/web (pid 41)"},
		{At: at.Add(120 * time.Millisecond), Stream: logring.Stdout, Text: "listening on :8080"},
		{At: at.Add(1500 * time.Millisecond), Stream: logring.Stderr, Text: "cannot reach the database"},
		{At: at.Add(2 * time.Second), Stream: logring.Zathras, Lost: 12},
		{At: at.Add(2 * time.Second), Stream: logring.Zathras, Lost: 1},
		{At: at.Add(3 * time.Second), Stream: logring.Stdout, Text: ""},
	}
}

const testLinesText = "" +
	"14:00:00.000 --  started generation 8: /nix/store/abc-web/bin/web (pid 41)\n" +
	"14:00:00.120 out listening on :8080\n" +
	"14:00:01.500 err cannot reach the database\n" +
	"14:00:02.000 --  … 12 lines lost\n" +
	"14:00:02.000 --  … 1 line lost\n" +
	"14:00:03.000 out \n"

func TestFormatLogLine(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	var b strings.Builder
	for _, line := range testLines() {
		b.WriteString(formatLogLine(line, zone) + "\n")
	}
	if got := b.String(); got != testLinesText {
		t.Errorf("got:\n%s\nwant:\n%s", got, testLinesText)
	}
}

// TestRunLogs asks a draal for the log of a zathras, over the protocol.
func TestRunLogs(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	fake := &fakeDraal{fleets: []wire.Fleet{testFleet(statusNow)}, lines: testLines()}
	client := fake.start(t)

	t.Run("the ring", func(t *testing.T) {
		var out buffer
		if err := runLogs(context.Background(), client, target{service: "web", zathras: "web-b"}, false, &out, zone); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != testLinesText {
			t.Errorf("got:\n%s\nwant:\n%s", got, testLinesText)
		}
		asked := fake.asked()
		last := asked[len(asked)-1].Logs
		if last == nil || last.Zathras != testFleet(statusNow).Services[1].Zathrases[1].ID || last.Follow {
			t.Errorf("draal was asked %+v, want the log of web-b by its ID, without follow", last)
		}
	})

	t.Run("following ends with an interrupt, and well", func(t *testing.T) {
		var out buffer
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- runLogs(ctx, client, target{service: "web", zathras: "web-b"}, true, &out, zone) }()
		waitFor(t, "the lines", func() bool { return out.String() == testLinesText })
		select {
		case err := <-done:
			t.Fatalf("runLogs returned %v while it was to follow", err)
		default:
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runLogs returned %v after the interrupt, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runLogs did not return after the interrupt")
		}
	})

	t.Run("a name that names no zathras", func(t *testing.T) {
		var out buffer
		err := runLogs(context.Background(), client, target{service: "web", zathras: "nobody"}, false, &out, zone)
		if err == nil {
			t.Fatal("runLogs returned nil")
		}
		if out.String() != "" {
			t.Errorf("something was printed: %q", out.String())
		}
	})
}
```

- [ ] **Step 12: Run the test to see it fail**

Run: `go test ./cli/`
Expected: FAIL, the test does not build:

```
cli/logs_test.go:37:17: undefined: formatLogLine
cli/logs_test.go:52:13: undefined: runLogs
cli/logs_test.go:52:51: undefined: target
cli/status_test.go:21:12: undefined: formatFleet
```

(among others of the same kind).

- [ ] **Step 13: Write the implementation**

`cli/status.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// statusCommand is `great-machine status [SERVICE]`.
func statusCommand() *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "list the services, or the zathrases of one",
		ArgsUsage: "[SERVICE]",
		Flags:     operatorFlags(),
		Action: func(c *cli.Context) error {
			args, err := arguments(c, 0, "SERVICE")
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				if name, err = service(args[0]); err != nil {
					return err
				}
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runStatus(c.Context, client, name, c.App.Writer, time.Now())
		},
	}
}

// runStatus prints the services of the fleet, or with a service its
// zathrases, as they are now.
func runStatus(ctx context.Context, client *operator.Client, service string, stdout io.Writer, now time.Time) error {
	f, err := fleet(ctx, client)
	if err != nil {
		return err
	}
	text := formatFleet(f)
	if service != "" {
		if text, err = formatService(f, service, now); err != nil {
			return err
		}
	}
	_, err = io.WriteString(stdout, text)
	return err
}

// table writes rows as columns two spaces apart. A row is its cells with
// a tab between them.
func table(rows []string) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
	for _, row := range rows {
		fmt.Fprintln(w, row)
	}
	w.Flush()
	return b.String()
}

// formatFleet is the table of services: the name, the stable generation,
// the zathrases counted by phase, and the last rollout.
func formatFleet(f wire.Fleet) string {
	if len(f.Services) == 0 {
		return "no services: no zathras is connected and nothing was rolled out\n"
	}
	rows := []string{"SERVICE\tSTABLE\tZATHRASES\tLAST ROLLOUT"}
	for _, s := range f.Services {
		rows = append(rows, strings.Join([]string{s.Name, generation(s.Stable), phases(s.Zathrases), rolloutLine(s)}, "\t"))
	}
	return table(rows)
}

// formatService is the last rollout of a service on a line and its
// zathrases as a table: name, the first 8 characters of the ID, phase,
// generation, for how long it has been running or when it is started
// again, restarts, and CPU and memory where there is a sample.
func formatService(f wire.Fleet, name string, now time.Time) (string, error) {
	var s *wire.Service
	var names []string
	for i := range f.Services {
		names = append(names, f.Services[i].Name)
		if f.Services[i].Name == name {
			s = &f.Services[i]
		}
	}
	switch {
	case s == nil && len(names) == 0:
		return "", fmt.Errorf("no service %q: draal has no services", name)
	case s == nil:
		return "", fmt.Errorf("no service %q; there are: %s", name, strings.Join(names, ", "))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: stable generation %s, last rollout %s\n", s.Name, generation(s.Stable), rolloutLine(*s))
	if r := s.Rollout; r != nil {
		fmt.Fprintf(&b, "rollout #%d: to generation %d, %s at a time, begun %s ago\n",
			r.ID, r.Generation, atATime(r.Parallel), age(now.Sub(r.Created)))
	}
	if len(s.Zathrases) == 0 {
		b.WriteString("no zathras is connected\n")
		return b.String(), nil
	}
	rows := []string{"NAME\tID\tPHASE\tGENERATION\tSINCE\tRESTARTS\tCPU\tMEMORY"}
	for _, z := range s.Zathrases {
		st := z.State
		cpu, memory := "-", "-"
		if st.Sample != nil {
			cpu = strconv.FormatFloat(st.Sample.CPU, 'f', 2, 64)
			memory = byteSize(st.Sample.Memory)
			if st.Sample.MemoryLimit > 0 {
				memory += " of " + byteSize(st.Sample.MemoryLimit)
			}
		}
		id := string(z.ID)
		if len(id) > 8 {
			id = id[:8]
		}
		rows = append(rows, strings.Join([]string{
			z.Name, id, string(st.Phase), generation(st.Generation), since(st, now),
			strconv.FormatUint(st.Restarts, 10), cpu, memory,
		}, "\t"))
	}
	b.WriteString(table(rows))
	return b.String(), nil
}

// generation writes the number of a generation, or - for none.
func generation(n uint64) string {
	if n == 0 {
		return "-"
	}
	return strconv.FormatUint(n, 10)
}

// atATime writes the parallel of a rollout.
func atATime(parallel int) string {
	if parallel == 0 {
		return "all"
	}
	return strconv.Itoa(parallel)
}

// phases counts zathrases by phase: "3 running, 1 backoff".
func phases(zs []wire.Zathras) string {
	if len(zs) == 0 {
		return "none"
	}
	count := map[wire.Phase]int{}
	for _, z := range zs {
		count[z.State.Phase]++
	}
	var parts []string
	for _, p := range []wire.Phase{wire.PhaseRunning, wire.PhaseStopping, wire.PhaseBackoff, wire.PhaseIdle} {
		if n := count[p]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, p))
		}
	}
	return strings.Join(parts, ", ")
}

// rolloutLine writes the last rollout of a service: "#12 completed",
// "#13 activating 2/5", "#13 halted: <reason>", or - for none. The counts
// are of the zathrases that are connected.
func rolloutLine(s wire.Service) string {
	r := s.Rollout
	if r == nil {
		return "-"
	}
	switch r.State {
	case wire.RolloutPreparing:
		return fmt.Sprintf("#%d preparing %d/%d", r.ID, r.Prepared, len(s.Zathrases))
	case wire.RolloutActivating:
		return fmt.Sprintf("#%d activating %d/%d", r.ID, r.Activated, len(s.Zathrases))
	case wire.RolloutHalted:
		return fmt.Sprintf("#%d halted: %s", r.ID, r.Reason)
	}
	return fmt.Sprintf("#%d %s", r.ID, r.State)
}

// since writes for how long the service of a zathras has been running,
// or when it is started again.
func since(st wire.State, now time.Time) string {
	switch st.Phase {
	case wire.PhaseRunning:
		return "up " + age(st.RunningFor)
	case wire.PhaseBackoff:
		if wait := st.RestartAt.Sub(now); !st.RestartAt.IsZero() && wait > 0 {
			return "restart in " + age(wait)
		}
		return "restart now"
	}
	return "-"
}
```

`cli/logs.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// logsCommand is `great-machine logs [-f] SERVICE [ZATHRAS]`.
func logsCommand() *cli.Command {
	return &cli.Command{
		Name:      "logs",
		Usage:     "print the last 400 lines of a zathras' service, and follow with -f",
		ArgsUsage: "SERVICE [ZATHRAS]",
		Description: "ZATHRAS is a name or the beginning of one, or of an endpoint ID. It may\n" +
			"be left out when the service has one zathras.\n\n" +
			"A line is its time, its stream (out, err, or -- for what zathras says\n" +
			"itself) and its text.",
		Flags: operatorFlags(&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, EnvVars: []string{"GREAT_MACHINE_FOLLOW"},
			Usage: "go on with every new line, until interrupted"}),
		Action: func(c *cli.Context) error {
			cfg, err := readTarget(c)
			if err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runLogs(c.Context, client, cfg, c.Bool("follow"), c.App.Writer, time.Local)
		},
	}
}

// target is the zathras a command is about: SERVICE [ZATHRAS].
type target struct {
	service string
	zathras string // a name, the beginning of one or of an ID, or ""
}

// readTarget reads SERVICE [ZATHRAS].
func readTarget(c *cli.Context) (target, error) {
	args, err := arguments(c, 1, "SERVICE", "ZATHRAS")
	if err != nil {
		return target{}, err
	}
	name, err := service(args[0])
	if err != nil {
		return target{}, err
	}
	t := target{service: name}
	if len(args) == 2 {
		t.zathras = args[1]
	}
	return t, nil
}

// resolve finds the zathras that t names in the fleet as it is now.
func (t target) resolve(ctx context.Context, client *operator.Client) (wire.Zathras, error) {
	f, err := fleet(ctx, client)
	if err != nil {
		return wire.Zathras{}, err
	}
	return operator.Resolve(f, t.service, t.zathras)
}

// runLogs prints the ring of a zathras, and with follow what follows,
// until ctx is done. An interrupt is how following ends, and no error.
func runLogs(ctx context.Context, client *operator.Client, t target, follow bool, stdout io.Writer, zone *time.Location) error {
	z, err := t.resolve(ctx, client)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, err := client.Logs(ctx, z.ID, follow)
	if err != nil {
		return fmt.Errorf("the log of %s: %w", z.Name, err)
	}
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if follow && ctx.Err() == nil {
					return fmt.Errorf("the log of %s ended: the zathras or draal has gone", z.Name)
				}
				return nil
			}
			if _, err := fmt.Fprintln(stdout, formatLogLine(line, zone)); err != nil {
				return err
			}
		case <-ctx.Done():
			if follow {
				return nil
			}
			return errors.New("interrupted")
		}
	}
}

// formatLogLine writes a line of the ring as its time in zone, its stream
// and its text. The stream is out or err, and -- for what zathras says
// itself, which so stands out and is found with `grep ' -- '`.
func formatLogLine(l wire.LogLine, zone *time.Location) string {
	stream := l.Stream
	switch l.Stream {
	case logring.Stdout:
		stream = "out"
	case logring.Stderr:
		stream = "err"
	case logring.Zathras:
		stream = "-- "
	}
	text := l.Text
	if l.Lost > 0 {
		noun := "lines"
		if l.Lost == 1 {
			noun = "line"
		}
		text = fmt.Sprintf("… %d %s lost", l.Lost, noun)
	}
	return fmt.Sprintf("%s %s %s", l.At.In(zone).Format("15:04:05.000"), stream, text)
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `go vet ./cli/ && go test ./cli/ -race -run 'Format|RolloutLine|Since|RunStatus|RunLogs' -v`
Expected: PASS for `TestFormatFleet`, `TestRolloutLine`,
`TestFormatService`, `TestSince`, `TestRunStatus`, `TestFormatLogLine` and
`TestRunLogs` (three cases).

- [ ] **Step 15: Commit**

```bash
git add cli/
git commit -m "CLI: status and logs"
```

#### rollout, rollback and shell

- [ ] **Step 16: Write the failing test**

`cli/rollout_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// What follows -- are the arguments of the service, verbatim, flags of
// rollout among them.
func TestReadRollout(t *testing.T) {
	cleanEnvironment(t)
	var got rolloutConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readRollout(c)
		return err
	}
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want rolloutConfig
	}{
		{"arguments of the service", nil,
			[]string{"web", "/nix/store/x/bin/web", "--", "--port", "80", "-v"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--port", "80", "-v"}, parallel: 1, jobs: 5}},
		{"no arguments of the service", nil,
			[]string{"web", "./result/bin/web"},
			rolloutConfig{service: "web", path: "./result/bin/web", parallel: 1, jobs: 5}},
		{"nothing behind --", nil,
			[]string{"web", "./result/bin/web", "--"},
			rolloutConfig{service: "web", path: "./result/bin/web", args: []string{}, parallel: 1, jobs: 5}},
		{"flags of rollout, and the same words behind --", nil,
			[]string{"--parallel", "0", "--detach", "--jobs", "2", "--temp-dir", "/var/tmp", "web", "/nix/store/x/bin/web", "--", "--detach", "--", "--parallel", "9"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", args: []string{"--detach", "--", "--parallel", "9"},
				parallel: 0, detach: true, jobs: 2, tempDir: "/var/tmp"}},
		{"the variables, and a flag that wins over its own",
			map[string]string{"GREAT_MACHINE_PARALLEL": "3", "GREAT_MACHINE_DETACH": "true", "GREAT_MACHINE_JOBS": "9", "GREAT_MACHINE_TEMP_DIR": "/scratch"},
			[]string{"--jobs", "1", "web", "/nix/store/x/bin/web"},
			rolloutConfig{service: "web", path: "/nix/store/x/bin/web", parallel: 3, detach: true, jobs: 1, tempDir: "/scratch"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			read(t, rolloutCommand(), reader, tt.args...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// fakeRollout is a draal that answers a rollout of web with #13 to
// generation 8, and the parts of a rollout that are not draal's.
type fakeRollout struct {
	fake    *fakeDraal
	client  *operator.Client
	key     node.Key
	pushed  push.Config
	paths   []string
	pushErr error
}

func newFakeRollout(t *testing.T, last wire.RolloutInfo) *fakeRollout {
	t.Helper()
	key, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDraal{
		store:  wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
		answer: wire.RolloutInfo{ID: 13, Service: "web", Generation: 8, State: wire.RolloutPreparing},
		fleets: []wire.Fleet{
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutPreparing, Prepared: 2}),
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 1}),
			rolloutFleet(last),
		},
	}
	return &fakeRollout{fake: fake, client: fake.start(t), key: key}
}

func (f *fakeRollout) deps() rolloutDeps {
	return rolloutDeps{
		binary: func(path string) (string, string, error) {
			if path != "./result/bin/web" {
				return "", "", errors.New(path + ": no such file")
			}
			return "/nix/store/abc-web/bin/web", "/nix/store/abc-web", nil
		},
		checkLinux: func(string) error { return nil },
		closure: func(_ context.Context, storePath string) ([]string, error) {
			return []string{"/nix/store/aaa-glibc", storePath, "/nix/store/zzz-openssl"}, nil
		},
		connect: func(context.Context) (*operator.Client, node.Key, error) { return f.client, f.key, nil },
		push: func(_ context.Context, cfg push.Config, storePaths []string) (push.Result, error) {
			f.pushed, f.paths = cfg, storePaths
			if f.pushErr != nil {
				cfg.Progress.Found(1, 2)
				return push.Result{}, f.pushErr
			}
			cfg.Progress.Found(1, 2)
			cfg.Progress.Pushed(storePaths[0], 1024)
			cfg.Progress.Pushed(storePaths[1], 2048)
			return push.Result{Pushed: 2, Present: 1}, nil
		},
	}
}

func TestRunRollout(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	var stdout, stderr buffer
	cfg := rolloutConfig{service: "web", path: "./result/bin/web", args: []string{"--port", "80"}, parallel: 2, jobs: 5, tempDir: "/scratch"}
	if err := runRollout(context.Background(), cfg, f.deps(), &stdout, plainProgress(&stderr)); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"resolving the binary: /nix/store/abc-web/bin/web (0.0s)",
		"reading the closure: 3 paths (0.0s)",
		"connecting to draal (0.0s)",
		"asking the store: 1 of 3 already there (0.0s)",
		"pushing: 2 paths, 3.0 KiB (0.0s)",
		"rolling out: generation 8, rollout #13 (0.0s)",
		"preparing: 3 zathrases (0.0s)",
		"activating: 3 zathrases (0.0s)",
		"total: rollout #13 completed: web runs generation 8 (0.0s)",
		"",
	}, "\n")
	if got := stderr.String(); got != want {
		t.Errorf("progress:\n%s\nwant:\n%s", got, want)
	}
	if stdout.String() != "" {
		t.Errorf("stdout is %q, want nothing", stdout.String())
	}

	// The push went to the store draal named, as the operator.
	if f.pushed.Server != wire.ID(idC) || f.pushed.Prefix != "nix/" || f.pushed.Jobs != 5 || f.pushed.TempDir != "/scratch" || f.pushed.Key.ID() != f.key.ID() {
		t.Errorf("the push was configured %+v", f.pushed)
	}
	if want := []string{"/nix/store/aaa-glibc", "/nix/store/abc-web", "/nix/store/zzz-openssl"}; !reflect.DeepEqual(f.paths, want) {
		t.Errorf("pushed %v, want %v", f.paths, want)
	}
	// draal was asked for the rollout with the names of the closure.
	var asked *wire.RolloutRequest
	for _, req := range f.fake.asked() {
		if req.Rollout != nil {
			asked = req.Rollout
		}
	}
	wantReq := &wire.RolloutRequest{Service: "web", Binary: "/nix/store/abc-web/bin/web", Args: []string{"--port", "80"},
		Closure: []string{"aaa-glibc", "abc-web", "zzz-openssl"}, Parallel: 2}
	if !reflect.DeepEqual(asked, wantReq) {
		t.Errorf("draal was asked %+v, want %+v", asked, wantReq)
	}
}

func TestRunRolloutHalted(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-b: exited with status 1 after 0.2s"})
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5}, f.deps(), &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "rollout #13 of web halted: web-b: exited with status 1 after 0.2s" {
		t.Fatalf("runRollout returned %v", err)
	}
	if !strings.HasSuffix(stderr.String(), "preparing: 3 zathrases (0.0s)\nactivating: failed after 0.0s\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
}

// A push that fails ends the command before draal has heard of a rollout.
func TestRunRolloutPushFails(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	f.pushErr = errors.New("/nix/store/aaa-glibc: the server is full")
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5}, f.deps(), &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "pushing the closure: /nix/store/aaa-glibc: the server is full" {
		t.Fatalf("runRollout returned %v", err)
	}
	if !strings.HasSuffix(stderr.String(), "asking the store: 1 of 3 already there (0.0s)\npushing: failed after 0.0s\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
	for _, req := range f.fake.asked() {
		if req.Rollout != nil {
			t.Error("draal was asked for a rollout after the push had failed")
		}
	}
}

// A PATH that is no binary ends the command before anyone is dialed.
func TestRunRolloutBadPath(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	deps := f.deps()
	deps.connect = func(context.Context) (*operator.Client, node.Key, error) {
		t.Error("draal was dialed for a PATH that is no binary")
		return nil, node.Key{}, errors.New("not to be dialed")
	}
	var stdout, stderr buffer
	err := runRollout(context.Background(), rolloutConfig{service: "web", path: "./nothing", jobs: 5}, deps, &stdout, plainProgress(&stderr))
	if err == nil || err.Error() != "./nothing: no such file" {
		t.Fatalf("runRollout returned %v", err)
	}
	if got, want := stderr.String(), "resolving the binary: failed after 0.0s\n"; got != want {
		t.Errorf("progress: %q, want %q", got, want)
	}
}

func TestRunRolloutDetach(t *testing.T) {
	f := newFakeRollout(t, wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})
	var stdout, stderr buffer
	cfg := rolloutConfig{service: "web", path: "./result/bin/web", jobs: 5, detach: true}
	if err := runRollout(context.Background(), cfg, f.deps(), &stdout, plainProgress(&stderr)); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "rollout #13 of web to generation 8 was started; look at it with: great-machine status web\n"; got != want {
		t.Errorf("stdout is %q, want %q", got, want)
	}
	if !strings.HasSuffix(stderr.String(), "rolling out: generation 8, rollout #13 (0.0s)\n") {
		t.Errorf("progress:\n%s", stderr.String())
	}
	for _, req := range f.fake.asked() {
		if req.Watch != nil {
			t.Error("the fleet was watched with --detach")
		}
	}
}

// TestRunRollback asks a draal for a rollback, over the protocol, and
// follows the rollout.
func TestRunRollback(t *testing.T) {
	answer := wire.RolloutInfo{ID: 13, Service: "web", Generation: 7, State: wire.RolloutPreparing}
	fleets := func(last wire.RolloutInfo) []wire.Fleet {
		return []wire.Fleet{
			rolloutFleet(wire.RolloutInfo{ID: 12, State: wire.RolloutHalted, Reason: "what is rolled back from"}),
			rolloutFleet(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Prepared: 3, Activated: 2}),
			rolloutFleet(last),
		}
	}
	req := wire.RollbackRequest{Service: "web", Parallel: 0}

	t.Run("completed", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutCompleted})}
		var stdout, stderr buffer
		if err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr)); err != nil {
			t.Fatal(err)
		}
		want := "rolling back: generation 7, rollout #13 (0.0s)\n" +
			"activating: 3 zathrases (0.0s)\n" +
			"total: rollout #13 completed: web runs generation 7 (0.0s)\n"
		if got := stderr.String(); got != want {
			t.Errorf("progress:\n%s\nwant:\n%s", got, want)
		}
		if asked := fake.asked()[0].Rollback; asked == nil || *asked != req {
			t.Errorf("draal was asked %+v, want %+v", asked, req)
		}
	})

	t.Run("halted", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutHalted, Reason: "web-a: could not be started"})}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || err.Error() != "rollout #13 of web halted: web-a: could not be started" {
			t.Fatalf("runRollback returned %v", err)
		}
	})

	t.Run("draal goes away", func(t *testing.T) {
		fake := &fakeDraal{answer: answer, fleets: fleets(wire.RolloutInfo{ID: 13, State: wire.RolloutActivating, Activated: 2})[:2], gone: true}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || !strings.Contains(err.Error(), "rollout #13 of web goes on; look at it with: great-machine status web") {
			t.Fatalf("runRollback returned %v", err)
		}
	})

	t.Run("refused", func(t *testing.T) {
		fake := &fakeDraal{refuse: wire.Errorf(wire.CodeBadRequest, "the last rollout completed")}
		var stdout, stderr buffer
		err := runRollback(context.Background(), fake.start(t), req, false, &stdout, plainProgress(&stderr))
		if err == nil || err.Error() != "rolling web back: bad-request: the last rollout completed" || wire.CodeOf(err) != wire.CodeBadRequest {
			t.Fatalf("runRollback returned %v", err)
		}
		if got, want := stderr.String(), "rolling back: failed after 0.0s\n"; got != want {
			t.Errorf("progress: %q, want %q", got, want)
		}
	})

	t.Run("detach", func(t *testing.T) {
		fake := &fakeDraal{answer: answer}
		var stdout, stderr buffer
		if err := runRollback(context.Background(), fake.start(t), req, true, &stdout, plainProgress(&stderr)); err != nil {
			t.Fatal(err)
		}
		if got, want := stdout.String(), "rollout #13 of web to generation 7 was started; look at it with: great-machine status web\n"; got != want {
			t.Errorf("stdout is %q, want %q", got, want)
		}
	})
}

// push.Progress is called from several goroutines.
func TestPushSteps(t *testing.T) {
	var out buffer
	p := plainProgress(&out)
	p.begin("asking the store")
	steps := &pushSteps{p: p}
	steps.Found(40, 50)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { steps.Pushed("/nix/store/abc-web", 1024) })
	}
	wg.Wait()
	p.done("done")
	want := "asking the store: 40 of 90 already there (0.0s)\npushing: done (0.0s)\n"
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := steps.bytes.Load(); got != 50*1024 {
		t.Errorf("%d bytes were counted, want %d", got, 50*1024)
	}
}
```

`cli/shell_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/urfave/cli/v2"
)

// TestShellResult: what the session came to decides the exit status of
// `great-machine shell`.
func TestShellResult(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		exit     int
		detached bool
		err      error
		status   int    // of what is returned; 0 for nil
		message  string // of what is returned
		stderr   string
	}{
		{"the shell ended well", context.Background(), 0, false, nil, 0, "", ""},
		{"the shell ended with a status", context.Background(), 3, false, nil, 3, "", ""},
		{"a signal ended the shell", context.Background(), -1, false, nil, 1, "the shell on web-a was ended by a signal", ""},
		{"detached", context.Background(), 0, true, nil, 0, "", "\ndetached\n"},
		{"the session failed", context.Background(), 0, false, errors.New("the stream ended"), 1, "the shell on web-a: the stream ended", ""},
		{"interrupted", done, 0, false, context.Canceled, 130, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr buffer
			err := shellResult(tt.ctx, "web-a", tt.exit, tt.detached, tt.err, &stderr)
			status, message := 0, ""
			var coder cli.ExitCoder
			switch {
			case errors.As(err, &coder):
				status, message = coder.ExitCode(), err.Error()
			case err != nil:
				status, message = 1, err.Error()
			}
			if status != tt.status || message != tt.message {
				t.Errorf("got the status %d and %q, want %d and %q", status, message, tt.status, tt.message)
			}
			if stderr.String() != tt.stderr {
				t.Errorf("stderr is %q, want %q", stderr.String(), tt.stderr)
			}
		})
	}
}
```

- [ ] **Step 17: Run the test to see it fail**

Run: `go test ./cli/`
Expected: FAIL, the test does not build:

```
cli/rollout_test.go:22:10: undefined: rolloutConfig
cli/rollout_test.go:24:14: undefined: readRollout
cli/rollout_test.go:56:12: undefined: rolloutCommand
cli/rollout_test.go:93:30: undefined: rolloutDeps
```

(among others of the same kind).

- [ ] **Step 18: Write the implementation**

`cli/rollout.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/fables-for-robots/great-machine/closure"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rolloutCommand is `great-machine rollout [--parallel N] [--detach]
// [--jobs N] [--temp-dir DIR] SERVICE PATH [-- ARG...]`.
func rolloutCommand() *cli.Command {
	return &cli.Command{
		Name:      "rollout",
		Usage:     "push the closure of a binary and roll it out to a service",
		ArgsUsage: "SERVICE PATH [-- ARG...]",
		Description: "PATH is the binary: a path inside /nix/store, or one that leads there\n" +
			"through symbolic links, such as ./result/bin/web. What follows -- are the\n" +
			"arguments the service is started with.\n\n" +
			"The command follows the rollout and ends with 0 when it completed and\n" +
			"with 1 when it halted or was superseded. An interrupt ends the following\n" +
			"and not the rollout.",
		Flags: operatorFlags(parallelFlag(), detachFlag(),
			&cli.IntFlag{Name: "jobs", EnvVars: []string{"GREAT_MACHINE_JOBS"}, Value: 5,
				Usage: "store paths pushed at a time"},
			&cli.StringFlag{Name: "temp-dir", EnvVars: []string{"GREAT_MACHINE_TEMP_DIR"},
				Usage: "`DIR` in which the temporary stores of a push are made (default: the system's)"},
		),
		Action: func(c *cli.Context) error {
			cfg, err := readRollout(c)
			if err != nil {
				return err
			}
			settings, err := readOperator(c)
			if err != nil {
				return err
			}
			p := newProgress(c.App.ErrWriter)
			defer p.close()
			return runRollout(c.Context, cfg, rolloutDeps{
				binary:     closure.Binary,
				checkLinux: closure.CheckLinux,
				closure:    closure.Of,
				connect:    settings.connect,
				push:       push.Push,
			}, c.App.Writer, p)
		},
	}
}

// rolloutConfig is what the command line of rollout says.
type rolloutConfig struct {
	service  string
	path     string
	args     []string // of the service
	parallel int
	detach   bool
	jobs     int
	tempDir  string
}

// rolloutDeps is what a rollout does through other packages, which a test
// replaces.
type rolloutDeps struct {
	binary     func(path string) (binary, storePath string, err error)
	checkLinux func(binary string) error
	closure    func(ctx context.Context, storePath string) ([]string, error)
	connect    func(ctx context.Context) (*operator.Client, node.Key, error)
	push       func(ctx context.Context, cfg push.Config, storePaths []string) (push.Result, error)
}

// splitArguments cuts the arguments of rollout at the first --: its own
// are before it, and those of the service behind it, verbatim. The
// library leaves a -- that follows an argument where it is.
func splitArguments(args []string) (own, service []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// readRollout reads the command line of rollout.
func readRollout(c *cli.Context) (rolloutConfig, error) {
	own, args := splitArguments(c.Args().Slice())
	usage := "usage: great-machine rollout " + c.Command.ArgsUsage
	switch {
	case len(own) == 0:
		return rolloutConfig{}, usageError("rollout: SERVICE is missing; %s", usage)
	case len(own) == 1:
		return rolloutConfig{}, usageError("rollout: PATH is missing; %s", usage)
	case len(own) > 2:
		return rolloutConfig{}, usageError("rollout: unexpected argument %q: the flags of rollout come before SERVICE, and the arguments of the service behind --", own[2])
	}
	cfg := rolloutConfig{path: own[1], args: args, detach: c.Bool("detach"), jobs: c.Int("jobs"), tempDir: c.String("temp-dir")}
	var err error
	if cfg.service, err = service(own[0]); err != nil {
		return rolloutConfig{}, err
	}
	if cfg.parallel, err = parallel(c); err != nil {
		return rolloutConfig{}, err
	}
	if cfg.jobs < 1 {
		return rolloutConfig{}, usageError("--jobs: %d is not a number of store paths to push at a time: want 1 or more", cfg.jobs)
	}
	return cfg, nil
}

// runRollout is section 6.1 of the design: the binary, its closure, the
// store, the push, the rollout, and the following of it.
func runRollout(ctx context.Context, cfg rolloutConfig, deps rolloutDeps, stdout io.Writer, p *progress) (err error) {
	defer func() {
		if err != nil {
			p.fail(err)
		}
	}()

	p.begin("resolving the binary")
	binary, storePath, err := deps.binary(cfg.path)
	if err != nil {
		return err
	}
	if err := deps.checkLinux(binary); err != nil {
		return err
	}
	p.done(binary)

	p.begin("reading the closure")
	paths, err := deps.closure(ctx, storePath)
	if err != nil {
		return err
	}
	p.done(fmt.Sprintf("%d paths", len(paths)))

	p.begin("connecting to draal")
	client, key, err := deps.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	p.done("")

	// The step lasts until the push says what the server has: asking
	// draal where the store is, and the store what it holds.
	p.begin("asking the store")
	store, err := client.Store(ctx)
	if err != nil {
		return fmt.Errorf("asking draal for the store: %w", err)
	}
	steps := &pushSteps{p: p}
	result, err := deps.push(ctx, push.Config{
		Key:      key,
		Server:   store.Server,
		Prefix:   store.Prefix,
		Jobs:     cfg.jobs,
		TempDir:  cfg.tempDir,
		Progress: steps,
	}, paths)
	if err != nil {
		// Nothing was rolled out: draal has not heard of it.
		return fmt.Errorf("pushing the closure: %w", err)
	}
	switch {
	case !steps.found.Load():
		p.done(fmt.Sprintf("%d of %d already there", result.Present, result.Present+result.Pushed))
	case result.Pushed == 0:
		p.done("nothing to push")
	default:
		p.done(fmt.Sprintf("%d paths, %s", result.Pushed, byteSize(steps.bytes.Load())))
	}

	p.begin("rolling out")
	r, err := client.Rollout(ctx, wire.RolloutRequest{
		Service:  cfg.service,
		Binary:   binary,
		Args:     cfg.args,
		Closure:  closure.Names(paths),
		Parallel: cfg.parallel,
	})
	if err != nil {
		return fmt.Errorf("rolling %s out: %w", cfg.service, err)
	}
	p.done(fmt.Sprintf("generation %d, rollout #%d", r.Generation, r.ID))
	return watchRollout(ctx, client, r, cfg.detach, stdout, p)
}

// pushSteps is the push.Progress of a rollout: it ends the step that asks
// the store when the push says what the server has, and counts the paths
// of the step that pushes. A push calls it from several goroutines.
type pushSteps struct {
	p     *progress
	found atomic.Bool
	bytes atomic.Uint64
}

// Found says how many of the store paths the server has already, and how
// many are to be pushed.
func (s *pushSteps) Found(present, missing int) {
	s.found.Store(true)
	s.p.done(fmt.Sprintf("%d of %d already there", present, present+missing))
	s.p.begin("pushing")
	s.p.count(0, uint64(missing))
}

// Pushed says that one store path was pushed, and how many bytes its pack
// came to.
func (s *pushSteps) Pushed(storePath string, bytes uint64) {
	s.bytes.Add(bytes)
	s.p.add(1)
}
```

`cli/rollback.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// rollbackCommand is `great-machine rollback [--parallel N] [--detach] SERVICE`.
func rollbackCommand() *cli.Command {
	return &cli.Command{
		Name:      "rollback",
		Usage:     "roll the stable generation of a service out again",
		ArgsUsage: "SERVICE",
		Description: "The stable generation is the target of the last rollout that completed.\n" +
			"draal refuses when there is none, and when the last rollout completed.\n\n" +
			"The command follows the rollout and ends with 0 when it completed and\n" +
			"with 1 when it halted or was superseded. An interrupt ends the following\n" +
			"and not the rollout.",
		Flags: operatorFlags(parallelFlag(), detachFlag()),
		Action: func(c *cli.Context) error {
			args, err := arguments(c, 1, "SERVICE")
			if err != nil {
				return err
			}
			req := wire.RollbackRequest{}
			if req.Service, err = service(args[0]); err != nil {
				return err
			}
			if req.Parallel, err = parallel(c); err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			p := newProgress(c.App.ErrWriter)
			defer p.close()
			return runRollback(c.Context, client, req, c.Bool("detach"), c.App.Writer, p)
		},
	}
}

// runRollback asks draal for the rollback and follows the rollout that
// comes of it, unless detach says not to.
func runRollback(ctx context.Context, client *operator.Client, req wire.RollbackRequest, detach bool, stdout io.Writer, p *progress) (err error) {
	defer func() {
		if err != nil {
			p.fail(err)
		}
	}()
	p.begin("rolling back")
	r, err := client.Rollback(ctx, req)
	if err != nil {
		return fmt.Errorf("rolling %s back: %w", req.Service, err)
	}
	p.done(fmt.Sprintf("generation %d, rollout #%d", r.Generation, r.ID))
	return watchRollout(ctx, client, r, detach, stdout, p)
}
```

`cli/shell.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/term"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// shellCommand is `great-machine shell SERVICE [ZATHRAS]`.
func shellCommand() *cli.Command {
	return &cli.Command{
		Name:      "shell",
		Usage:     "a shell in the container of a zathras",
		ArgsUsage: "SERVICE [ZATHRAS]",
		Description: "ZATHRAS is a name or the beginning of one, or of an endpoint ID. It may\n" +
			"be left out when the service has one zathras.\n\n" +
			"Ctrl-] leaves the shell without ending it. With a standard input that is\n" +
			"no terminal (echo uptime | great-machine shell web) the input is passed\n" +
			"on and the shell ends with it. The exit status is that of the shell.",
		Flags: operatorFlags(),
		Action: func(c *cli.Context) error {
			t, err := readTarget(c)
			if err != nil {
				return err
			}
			client, _, err := connect(c.Context, c)
			if err != nil {
				return err
			}
			defer client.Close()
			return runShell(c.Context, client, t, os.Stdin, os.Stdout, c.App.ErrWriter)
		},
	}
}

// runShell joins stdin and stdout to a shell of the zathras that t names.
// What it returns for a shell that ended with a status other than 0 makes
// that status the command's.
func runShell(ctx context.Context, client *operator.Client, t target, stdin *os.File, stdout, stderr io.Writer) error {
	z, err := t.resolve(ctx, client)
	if err != nil {
		return err
	}
	cols, rows := term.Size(stdin)
	sh, err := client.Shell(ctx, z.ID, wire.ShellRequest{Zathras: z.ID, Cols: cols, Rows: rows, Term: os.Getenv("TERM")})
	if err != nil {
		return fmt.Errorf("a shell on %s: %w", z.Name, err)
	}
	defer sh.Close()
	exit, detached, err := term.Attach(ctx, stdin, stdout, sh)
	return shellResult(ctx, z.Name, exit, detached, err, stderr)
}

// shellResult turns what term.Attach returned into what the command
// returns.
func shellResult(ctx context.Context, name string, exit int, detached bool, err error, stderr io.Writer) error {
	switch {
	case detached:
		// The line of the shell's prompt is left as it is.
		fmt.Fprintln(stderr, "\ndetached")
		return nil
	case err != nil && ctx.Err() != nil:
		return interrupted("")
	case err != nil:
		return fmt.Errorf("the shell on %s: %w", name, err)
	case exit < 0:
		return fmt.Errorf("the shell on %s was ended by a signal", name)
	case exit != 0:
		return cli.Exit("", exit)
	}
	return nil
}
```

- [ ] **Step 19: Run the tests to see them pass**

Run: `go vet ./cli/ && go test ./cli/ -race -run 'Rollout|Rollback|PushSteps|ShellResult' -v`
Expected: PASS for `TestReadRollout` (five cases, the first of which is
`rollout web /nix/store/x/bin/web -- --port 80 -v` giving
`["--port", "80", "-v"]`), `TestRunRollout`, `TestRunRolloutHalted`,
`TestRunRolloutPushFails`, `TestRunRolloutBadPath`, `TestRunRolloutDetach`,
`TestRunRollback` (five cases), `TestPushSteps` and `TestShellResult` (six
cases).

- [ ] **Step 20: Commit**

```bash
git add cli/
git commit -m "CLI: rollout, rollback and shell"
```

#### draal, zathras, id, and the program with every command

- [ ] **Step 21: Write the failing test**

`cli/commands_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// TestInvalidCommandLines holds every command to the exit status 2 and a
// message on stderr for a command line that is not valid. None of them
// gets as far as the network.
func TestInvalidCommandLines(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		args []string
		want string // in stderr
	}{
		// The operator commands.
		{"status: too many arguments", nil, []string{"status", "--draal", idA, "web", "api"}, `unexpected argument "api"`},
		{"status: a bad service name", nil, []string{"status", "--draal", idA, "Web"}, `"Web" is not the name of a service`},
		{"status: no --draal", nil, []string{"status"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"status: a bad --draal", nil, []string{"status", "--draal", "nonsense"}, `--draal: "nonsense"`},
		{"status: the variable stands in for --draal", map[string]string{"GREAT_MACHINE_DRAAL": "nonsense"},
			[]string{"status"}, `--draal: "nonsense"`},
		{"status: --draal wins over its variable", map[string]string{"GREAT_MACHINE_DRAAL": idA},
			[]string{"status", "--draal", "nonsense"}, `--draal: "nonsense"`},
		{"logs: no SERVICE", nil, []string{"logs", "--draal", idA}, "logs: SERVICE is missing; usage: great-machine logs SERVICE [ZATHRAS]"},
		{"logs: too many arguments", nil, []string{"logs", "--draal", idA, "web", "web-a", "web-b"}, `unexpected argument "web-b"`},
		{"logs: a flag behind SERVICE", nil, []string{"logs", "--draal", idA, "web", "-f"}, "logs: the flag -f has to come before SERVICE"},
		{"logs: no --draal", nil, []string{"logs", "-f", "web"}, "--draal is required"},
		{"shell: no SERVICE", nil, []string{"shell", "--draal", idA}, "shell: SERVICE is missing"},
		{"shell: a bad service name", nil, []string{"shell", "--draal", idA, "web_1"}, `"web_1" is not the name of a service`},
		{"shell: no --draal", nil, []string{"shell", "web"}, "--draal is required"},
		{"rollback: no SERVICE", nil, []string{"rollback", "--draal", idA}, "rollback: SERVICE is missing"},
		{"rollback: too many arguments", nil, []string{"rollback", "--draal", idA, "web", "api"}, `unexpected argument "api"`},
		{"rollback: a negative --parallel", nil, []string{"rollback", "--draal", idA, "--parallel", "-1", "web"}, "--parallel: -1 is not a number of zathrases"},
		{"rollback: a --parallel that is no number", nil, []string{"rollback", "--draal", idA, "--parallel", "many", "web"}, `"many"`},
		{"rollback: the variable stands in for --parallel", map[string]string{"GREAT_MACHINE_PARALLEL": "-3"},
			[]string{"rollback", "--draal", idA, "web"}, "--parallel: -3 is not a number of zathrases"},
		{"rollback: a variable that is no number", map[string]string{"GREAT_MACHINE_PARALLEL": "many"},
			[]string{"rollback", "--draal", idA, "web"}, `"many"`},
		{"rollback: --detach behind SERVICE", nil, []string{"rollback", "--draal", idA, "web", "--detach"}, "rollback: the flag --detach has to come before SERVICE"},
		{"rollback: no --draal", nil, []string{"rollback", "web"}, "--draal is required"},
		{"rollout: no SERVICE", nil, []string{"rollout", "--draal", idA}, "rollout: SERVICE is missing; usage: great-machine rollout SERVICE PATH [-- ARG...]"},
		{"rollout: no PATH", nil, []string{"rollout", "--draal", idA, "web"}, "rollout: PATH is missing"},
		{"rollout: no PATH before --", nil, []string{"rollout", "--draal", idA, "web", "--", "/nix/store/abc-web/bin/web"}, "rollout: PATH is missing"},
		{"rollout: an argument of the service without --", nil, []string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web", "--port", "80"},
			`unexpected argument "--port": the flags of rollout come before SERVICE, and the arguments of the service behind --`},
		{"rollout: a service name that begins like a flag", nil, []string{"rollout", "--draal", idA, "-web", "/nix/store/abc-web/bin/web"}, "flag provided but not defined: -web"},
		{"rollout: a service name with a dot", nil, []string{"rollout", "--draal", idA, "web.prod", "/nix/store/abc-web/bin/web"}, `"web.prod" is not the name of a service`},
		{"rollout: a negative --parallel", nil, []string{"rollout", "--draal", idA, "--parallel", "-1", "web", "/nix/store/abc-web/bin/web"}, "--parallel: -1"},
		{"rollout: --jobs 0", nil, []string{"rollout", "--draal", idA, "--jobs", "0", "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
		{"rollout: the variable stands in for --jobs", map[string]string{"GREAT_MACHINE_JOBS": "0"},
			[]string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
		{"rollout: no --draal", nil, []string{"rollout", "web", "/nix/store/abc-web/bin/web"}, "--draal is required"},
		{"rollout: a bad --draal", nil, []string{"rollout", "--draal", "nonsense", "web", "/nix/store/abc-web/bin/web"}, `--draal: "nonsense"`},
		{"id: an argument", nil, []string{"id", "now"}, `id takes no argument, got "now"`},

		// draal.
		{"draal: no --data", nil, []string{"draal", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, "--data is required: give it, or set GREAT_MACHINE_DATA"},
		{"draal: no --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-prefix", "nix/"}, "--jaccard-server is required: give it, or set JACCARD_SERVER"},
		{"draal: no --jaccard-prefix", nil, []string{"draal", "--data", "d", "--jaccard-server", idB}, "--jaccard-prefix is required: give it, or set JACCARD_PREFIX"},
		{"draal: a bad --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-server", "nonsense", "--jaccard-prefix", "nix/"}, `--jaccard-server: "nonsense" is not an endpoint ID`},
		{"draal: an operator that is no endpoint ID", nil,
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--operator", idA, "--operator", "alice"},
			`--operator: "alice" is not an endpoint ID`},
		{"draal: the variable stands in for --operator", map[string]string{"GREAT_MACHINE_OPERATORS": idA + ",bob"},
			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--operator: "bob" is not an endpoint ID`},
		{"draal: a bad --bind", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--bind", "4433"}, `--bind: "4433" is not an address to bind`},
		{"draal: an argument", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "now"}, `draal takes no argument, got "now"`},

		// zathras.
		{"zathras: no --draal", nil, []string{"zathras", "--service", "web"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
		{"zathras: a bad --draal", nil, []string{"zathras", "--draal", "nonsense", "--service", "web"}, `--draal: "nonsense"`},
		{"zathras: no --service", nil, []string{"zathras", "--draal", idA}, "--service is required: give it, or set GREAT_MACHINE_SERVICE"},
		{"zathras: a bad --service", nil, []string{"zathras", "--draal", idA, "--service", "Web"}, `"Web" is not the name of a service`},
		{"zathras: the variable stands in for --service", map[string]string{"GREAT_MACHINE_SERVICE": "Web"},
			[]string{"zathras", "--draal", idA}, `"Web" is not the name of a service`},
		{"zathras: a --stop-timeout that is no duration", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "soon"}, `"soon"`},
		{"zathras: a --stop-timeout of nothing", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "0s"}, "--stop-timeout: 0s is not a time"},
		{"zathras: an argument", nil, []string{"zathras", "--draal", idA, "--service", "web", "now"}, `zathras takes no argument, got "now"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanEnvironment(t)
			for name, value := range tt.env {
				t.Setenv(name, value)
			}
			var stdout, stderr bytes.Buffer
			got := run(context.Background(), newApp(&stdout, &stderr), append([]string{"great-machine"}, tt.args...))
			if got != 2 {
				t.Errorf("the exit status is %d, want 2", got)
			}
			if !strings.HasPrefix(stderr.String(), "great-machine: ") || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr is %q, want it to say %q", stderr.String(), tt.want)
			}
			if strings.Count(stderr.String(), "\n") != 1 {
				t.Errorf("stderr is more than one line: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is %q, want nothing", stdout.String())
			}
		})
	}
}

func TestReadDraal(t *testing.T) {
	cleanEnvironment(t)
	var got draalConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readDraal(c)
		return err
	}

	// The variables alone. The operators have commas between them, and
	// what a comma too many leaves empty is passed over.
	t.Setenv("GREAT_MACHINE_DATA", "/var/lib/draal")
	t.Setenv("GREAT_MACHINE_OPERATORS", idA+", "+idB+",")
	t.Setenv("JACCARD_SERVER", idC)
	t.Setenv("JACCARD_PREFIX", "nix/")
	read(t, draalCommand(), reader)
	want := draalConfig{
		data:      "/var/lib/draal",
		operators: []wire.ID{wire.ID(idA), wire.ID(idB)},
		store:     wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the variables: got %+v, want %+v", got, want)
	}

	// A flag wins over its variable, and --operator may be repeated.
	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA,
		"--jaccard-server", idA, "--jaccard-prefix", "store/", "--bind", "0.0.0.0:4433")
	want = draalConfig{
		data:      "data",
		operators: []wire.ID{wire.ID(idC), wire.ID(idA)},
		store:     wire.StoreInfo{Server: wire.ID(idA), Prefix: "store/"},
		bind:      netip.MustParseAddrPort("0.0.0.0:4433"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the flags: got %+v, want %+v", got, want)
	}
}

func TestReadZathras(t *testing.T) {
	cleanEnvironment(t)
	var got zathrasConfig
	reader := func(c *cli.Context) (err error) {
		got, err = readZathras(c)
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}

	// The defaults of the design.
	read(t, zathrasCommand(), reader, "--draal", idA, "--service", "web")
	want := zathrasConfig{
		draal:       wire.ID(idA),
		service:     "web",
		name:        host,
		preloadURL:  "http://127.0.0.1:9889/v1/preload",
		shell:       "/bin/sh",
		stopTimeout: 2 * time.Minute,
		healthAddr:  ":9890",
	}
	if got != want {
		t.Errorf("the defaults: got %+v, want %+v", got, want)
	}

	// The variables, and two flags that win over theirs. An empty
	// --health-addr is no health check.
	t.Setenv("GREAT_MACHINE_DRAAL", idB)
	t.Setenv("GREAT_MACHINE_SERVICE", "api")
	t.Setenv("GREAT_MACHINE_NAME", "api-0")
	t.Setenv("GREAT_MACHINE_PRELOAD_URL", "http://127.0.0.1:1/preload")
	t.Setenv("GREAT_MACHINE_SHELL", "/bin/bash")
	t.Setenv("GREAT_MACHINE_STOP_TIMEOUT", "30s")
	t.Setenv("GREAT_MACHINE_HEALTH_ADDR", "127.0.0.1:9999")
	read(t, zathrasCommand(), reader, "--name", "api-1", "--health-addr", "")
	want = zathrasConfig{
		draal:       wire.ID(idB),
		service:     "api",
		name:        "api-1",
		preloadURL:  "http://127.0.0.1:1/preload",
		shell:       "/bin/bash",
		stopTimeout: 30 * time.Second,
		healthAddr:  "",
	}
	if got != want {
		t.Errorf("the variables: got %+v, want %+v", got, want)
	}
}

// A health check that cannot listen ends zathras at once, before it has
// made a key or dialed anyone.
func TestZathrasHealthAddressTaken(t *testing.T) {
	cleanEnvironment(t)
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), newApp(&stdout, &stderr),
		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--health-addr", taken.Addr().String()})
	if got != 1 || !strings.Contains(stderr.String(), "great-machine: the health check: listen tcp") {
		t.Errorf("the exit status is %d and stderr %q, want 1 and the failure to listen", got, stderr.String())
	}
}

func TestID(t *testing.T) {
	cleanEnvironment(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "operator.key")
	ids := map[string]bool{}
	for range 2 {
		var stdout, stderr bytes.Buffer
		if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id", "--key", path}); got != 0 {
			t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
		}
		id, ok := strings.CutSuffix(stdout.String(), "\n")
		if !ok || len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
			t.Fatalf("stdout is %q, want 64 hexadecimal characters and a line feed", stdout.String())
		}
		ids[id] = true
	}
	if len(ids) != 1 {
		t.Errorf("the ID changed between two runs: %v", ids)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the key file was not created: %v", err)
	}

	// The variable stands in for the flag, and the flag wins over it.
	other := filepath.Join(dir, "other.key")
	t.Setenv("GREAT_MACHINE_KEY", other)
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id"}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("the key file the variable names was not created: %v", err)
	}
	if ids[strings.TrimSuffix(stdout.String(), "\n")] {
		t.Error("the variable was not read: the ID is that of the first key")
	}
	stdout.Reset()
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id", "--key", path}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if !ids[strings.TrimSuffix(stdout.String(), "\n")] {
		t.Error("the flag did not win over the variable")
	}
}

// The default place of the operator's key is in the user's configuration
// directory.
func TestIDDefaultKey(t *testing.T) {
	cleanEnvironment(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "id"}); got != 0 {
		t.Fatalf("the exit status is %d, stderr: %s", got, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "great-machine", "operator.key")); err != nil {
		t.Errorf("the key file was not created in %s: %v", dir, err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("the configuration directory %s is outside the test's home", dir)
	}
}

// TestRunExitStatus: a command that returns a cli.ExitCoder chooses the
// exit status, and a message that is empty is not printed.
func TestRunExitStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		stderr string
	}{
		{"the status of the remote shell", cli.Exit("", 7), 7, ""},
		{"an interrupt", interrupted("interrupted: rollout #%d goes on", 13), 130, "great-machine: interrupted: rollout #13 goes on\n"},
		{"an interrupt with nothing to say", interrupted(""), 130, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newApp(&stdout, &stderr)
			app.Commands = append(app.Commands, &cli.Command{
				Name:   "fail",
				Action: func(*cli.Context) error { return tt.err },
			})
			if got := run(context.Background(), app, []string{"great-machine", "fail"}); got != tt.want {
				t.Errorf("run returned %d, want %d", got, tt.want)
			}
			if got := stderr.String(); got != tt.stderr {
				t.Errorf("stderr is %q, want %q", got, tt.stderr)
			}
		})
	}
}

func TestHelpNamesEveryCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(context.Background(), newApp(&stdout, &stderr), []string{"great-machine", "--help"}); got != 0 {
		t.Fatalf("the exit status is %d", got)
	}
	for _, name := range []string{"draal", "zathras", "id", "rollout", "rollback", "status", "logs", "shell", "version"} {
		if !strings.Contains(stdout.String(), "\n   "+name+" ") {
			t.Errorf("the help does not name %s:\n%s", name, stdout.String())
		}
	}
}
```

- [ ] **Step 22: Run the test to see it fail**

Run: `go test ./cli/`
Expected: FAIL, the test does not build:

```
cli/commands_test.go:120:10: undefined: draalConfig
cli/commands_test.go:122:14: undefined: readDraal
cli/commands_test.go:132:10: undefined: draalCommand
cli/commands_test.go:158:10: undefined: zathrasConfig
```

(among others of the same kind).

- [ ] **Step 23: Write the implementation**

`cli/draal.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/urfave/cli/v2"
)

// The files of draal in its data directory.
const (
	draalKeyFile = "draal.key"
	draalDBFile  = "draal.sqlite"
)

// draalCommand is `great-machine draal`, the controller.
func draalCommand() *cli.Command {
	return &cli.Command{
		Name:  "draal",
		Usage: "the controller: zathrases report to it, operators roll out through it",
		Description: "The endpoint ID draal logs at its start is all that a zathras or an\n" +
			"operator needs to reach it.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "data", EnvVars: []string{"GREAT_MACHINE_DATA"},
				Usage: "`DIR`ectory of draal: its key (created on first run) and its database (required)"},
			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"},
				Usage: "endpoint `ID` that may act as an operator, as `great-machine id` prints it; may be repeated, and the variable takes several with commas between them"},
			&cli.StringFlag{Name: "jaccard-server", EnvVars: []string{"JACCARD_SERVER"},
				Usage: "endpoint `ID` of the jaccard-store server that closures are pushed to (required)"},
			&cli.StringFlag{Name: "jaccard-prefix", EnvVars: []string{"JACCARD_PREFIX"},
				Usage: "`PREFIX` put before the name of a store path to make its reference; the same value the sidecars are given (required)"},
			&cli.StringFlag{Name: "bind", EnvVars: []string{"GREAT_MACHINE_BIND"},
				Usage: "UDP `IP:PORT` the iroh endpoint binds (default: every address, a port the system picks)"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := readDraal(c)
			if err != nil {
				return err
			}
			return runDraal(c.Context, cfg, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// draalConfig is what the command line of draal says.
type draalConfig struct {
	data      string
	operators []wire.ID
	store     wire.StoreInfo
	bind      netip.AddrPort // the zero value: every address, a port the system picks
}

// readDraal reads the command line of draal.
func readDraal(c *cli.Context) (draalConfig, error) {
	if err := noArguments(c); err != nil {
		return draalConfig{}, err
	}
	var cfg draalConfig
	var err error
	if cfg.data, err = required(c, "data", "GREAT_MACHINE_DATA"); err != nil {
		return draalConfig{}, err
	}
	// The library has cut the values at their commas already. What is
	// left empty by a comma too many, or by a space, is not an ID.
	for _, text := range c.StringSlice("operator") {
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		id, err := node.ParseID(text)
		if err != nil {
			return draalConfig{}, usageError("--operator: %q is not an endpoint ID, as `great-machine id` prints one: %w", text, err)
		}
		cfg.operators = append(cfg.operators, id)
	}
	server, err := required(c, "jaccard-server", "JACCARD_SERVER")
	if err != nil {
		return draalConfig{}, err
	}
	if cfg.store.Server, err = node.ParseID(server); err != nil {
		return draalConfig{}, usageError("--jaccard-server: %q is not an endpoint ID: %w", server, err)
	}
	if cfg.store.Prefix, err = required(c, "jaccard-prefix", "JACCARD_PREFIX"); err != nil {
		return draalConfig{}, err
	}
	if bind := c.String("bind"); bind != "" {
		if cfg.bind, err = netip.ParseAddrPort(bind); err != nil {
			return draalConfig{}, usageError("--bind: %q is not an address to bind: want IP:PORT, such as 0.0.0.0:4433 or [::]:4433", bind)
		}
	}
	return cfg, nil
}

// runDraal is draal until ctx is done, which SIGINT and SIGTERM do. That
// is how draal is meant to end, and no error.
func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error) {
	if err := os.MkdirAll(cfg.data, 0o700); err != nil {
		return fmt.Errorf("the data directory: %w", err)
	}
	key, err := node.LoadOrCreateKey(filepath.Join(cfg.data, draalKeyFile))
	if err != nil {
		return fmt.Errorf("the key of draal: %w", err)
	}
	database, err := db.Open(filepath.Join(cfg.data, draalDBFile))
	if err != nil {
		return fmt.Errorf("the database: %w", err)
	}
	// Deferred first, so that it happens last: after the listener is
	// closed and Serve, which writes to the database, has returned.
	defer func() { err = errors.Join(err, database.Close()) }()

	if len(cfg.operators) == 0 {
		log.Warn("no --operator was given: nobody can act as an operator over iroh")
	}
	listener, err := node.Listen(ctx, node.ListenConfig{Key: key, Bind: cfg.bind, Log: log})
	if err != nil {
		return fmt.Errorf("binding the iroh endpoint: %w", err)
	}
	defer func() { err = errors.Join(err, listener.Close()) }()
	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators))

	d := draal.New(draal.Config{
		DB:    database,
		Authz: authz.NewAllowlist(cfg.operators),
		Store: cfg.store,
		Log:   log,
	})
	if err := d.Serve(ctx, listener); err != nil && ctx.Err() == nil {
		return err
	}
	log.Info("stopping")
	return nil
}
```

`cli/zathras.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/health"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/preload"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/stats"
	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
	"github.com/fables-for-robots/great-machine/zathras"
	"github.com/urfave/cli/v2"
)

// The ring of a zathras: the last 400 lines, of at most 16 KiB each.
const (
	ringLines   = 400
	ringMaxLine = 16 << 10
)

// zathrasCommand is `great-machine zathras`, the init of an app container.
func zathrasCommand() *cli.Command {
	return &cli.Command{
		Name:  "zathras",
		Usage: "the init of an app container: runs the service draal tells it to run",
		Description: "The health check, GET /healthz on --health-addr, passes while the service\n" +
			"runs and at no other time. It is a readiness probe, not a liveness probe.",
		Flags: []cli.Flag{
			draalFlag(),
			&cli.StringFlag{Name: "service", EnvVars: []string{"GREAT_MACHINE_SERVICE"},
				Usage: "`NAME` of the service this zathras belongs to (required)"},
			&cli.StringFlag{Name: "name", EnvVars: []string{"GREAT_MACHINE_NAME"},
				Usage: "`NAME` the operator sees (default: the host name, which in a pod is the pod's name)"},
			&cli.StringFlag{Name: "preload-url", EnvVars: []string{"GREAT_MACHINE_PRELOAD_URL"}, Value: "http://127.0.0.1:9889/v1/preload",
				Usage: "`URL` of the preload endpoint of the sidecar"},
			&cli.StringFlag{Name: "shell", EnvVars: []string{"GREAT_MACHINE_SHELL"}, Value: "/bin/sh",
				Usage: "`PATH` of the program of a shell session"},
			&cli.DurationFlag{Name: "stop-timeout", EnvVars: []string{"GREAT_MACHINE_STOP_TIMEOUT"}, Value: 2 * time.Minute,
				Usage: "how long a service has to end after SIGTERM, before SIGKILL"},
			&cli.StringFlag{Name: "health-addr", EnvVars: []string{"GREAT_MACHINE_HEALTH_ADDR"}, Value: ":9890",
				Usage: "`ADDR`ess the health check listens on; empty for none"},
		},
		Action: func(c *cli.Context) error {
			cfg, err := readZathras(c)
			if err != nil {
				return err
			}
			return runZathras(c.Context, cfg, slog.New(slog.NewTextHandler(c.App.ErrWriter, nil)))
		},
	}
}

// zathrasConfig is what the command line of zathras says.
type zathrasConfig struct {
	draal       wire.ID
	service     string
	name        string
	preloadURL  string
	shell       string
	stopTimeout time.Duration
	healthAddr  string // "" for no health check
}

// readZathras reads the command line of zathras.
func readZathras(c *cli.Context) (zathrasConfig, error) {
	if err := noArguments(c); err != nil {
		return zathrasConfig{}, err
	}
	cfg := zathrasConfig{
		name:        strings.TrimSpace(c.String("name")),
		preloadURL:  c.String("preload-url"),
		shell:       c.String("shell"),
		stopTimeout: c.Duration("stop-timeout"),
		healthAddr:  c.String("health-addr"),
	}
	var err error
	if cfg.draal, err = draalID(c); err != nil {
		return zathrasConfig{}, err
	}
	name, err := required(c, "service", "GREAT_MACHINE_SERVICE")
	if err != nil {
		return zathrasConfig{}, err
	}
	if cfg.service, err = service(name); err != nil {
		return zathrasConfig{}, err
	}
	if cfg.stopTimeout <= 0 {
		return zathrasConfig{}, usageError("--stop-timeout: %s is not a time a service has to end in", cfg.stopTimeout)
	}
	if cfg.name == "" {
		if cfg.name, err = os.Hostname(); err != nil {
			return zathrasConfig{}, fmt.Errorf("no host name to go by, give a name with --name: %w", err)
		}
	}
	return cfg, nil
}

// runZathras is zathras until ctx is done, which SIGINT and SIGTERM do:
// it then stops the service, ends the shells and returns nil.
func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) error {
	// The health check listens first: an address that is taken ends the
	// command before anything is started or draal has heard of it.
	var listener net.Listener
	if cfg.healthAddr != "" {
		var err error
		if listener, err = net.Listen("tcp", cfg.healthAddr); err != nil {
			return fmt.Errorf("the health check: %w", err)
		}
	}

	// A new key every time: the endpoint ID is the identity of this
	// process, and two pods must not share one.
	key, err := node.NewKey()
	if err != nil {
		if listener != nil {
			listener.Close()
		}
		return fmt.Errorf("making a key: %w", err)
	}

	ring := logring.New(ringLines, ringMaxLine)
	sup := supervisor.New(supervisor.Config{
		Ring:        ring,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		StopTimeout: cfg.stopTimeout,
		Log:         log,
	})
	agent := zathras.New(zathras.Config{
		Service: cfg.service,
		Name:    cfg.name,
		Version: Version,
		Dial: func(ctx context.Context) (node.Conn, error) {
			return node.Dial(ctx, node.DialConfig{Key: key, Server: cfg.draal, Log: log})
		},
		Supervisor: sup,
		Ring:       ring,
		Stats:      stats.NewReader(),
		Preload:    preload.Client{URL: cfg.preloadURL},
		Shell:      ptyshell.Config{Shell: cfg.shell},
		Log:        log,
	})
	log.Info("zathras", "endpoint", string(key.ID()), "service", cfg.service, "name", cfg.name, "draal", string(cfg.draal))

	// Whatever ends first ends the rest: a signal, or a part that failed.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	failed := make(chan error, 3)

	var web *http.Server
	if listener != nil {
		web = &http.Server{
			Handler:           health.Handler(func() wire.Phase { return sup.State().Phase }),
			ReadHeaderTimeout: 10 * time.Second,
		}
		log.Info("health check", "addr", listener.Addr().String())
		go func() {
			if err := web.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("the health check: %w", err)
				cancel()
			}
		}()
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := sup.Run(ctx); err != nil {
			failed <- fmt.Errorf("the supervisor: %w", err)
		}
		cancel()
	})
	wg.Go(func() {
		if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
			failed <- fmt.Errorf("the connection to draal: %w", err)
		}
		cancel()
	})

	// The order of the end, once ctx is done:
	//
	//  1. The agent ends the shells and leaves draal, and the supervisor
	//     stops the service: SIGTERM, and SIGKILL after --stop-timeout.
	//     Both are waited for here.
	//  2. The health check is shut down last. While the service is being
	//     stopped it goes on answering, with 503 as the phase is no longer
	//     running, so that the pod is taken out of its Service by an
	//     answer and not by a connection that is refused.
	wg.Wait()
	if web != nil {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		web.Shutdown(shutdown)
	}
	select {
	case err := <-failed:
		return err
	default:
		log.Info("stopped")
		return nil
	}
}
```

`cli/id.go`:

```go
package cli

import (
	"fmt"
	"io"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/urfave/cli/v2"
)

// idCommand is `great-machine id`. It talks to nobody.
func idCommand() *cli.Command {
	return &cli.Command{
		Name:        "id",
		Usage:       "print the endpoint ID of the operator's key, creating the key if it is missing",
		Description: "The ID is what draal is given with --operator to let this operator in.",
		Flags:       []cli.Flag{keyFlag()},
		Action: func(c *cli.Context) error {
			if err := noArguments(c); err != nil {
				return err
			}
			path, err := keyPath(c)
			if err != nil {
				return err
			}
			return runID(path, c.App.Writer)
		},
	}
}

// runID prints the endpoint ID of the key in the file at path, which it
// creates if it is missing.
func runID(path string, stdout io.Writer) error {
	k, err := node.LoadOrCreateKey(path)
	if err != nil {
		return fmt.Errorf("the operator's key: %w", err)
	}
	_, err = fmt.Fprintln(stdout, k.ID())
	return err
}
```

`cli/app.go`, the whole file. Against Task 1's it differs in three
places: the comment of `Run` names the two new exit statuses; `commands()`
lists every command; and `run` returns the status of a `cli.ExitCoder`
before it looks for a usage error.

```go
// Package cli is the command line of great-machine: one command for
// draal, one for zathras, and the operator's.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v2"
)

// Version is the release this program was built as. The linker sets it:
// -X github.com/fables-for-robots/great-machine/cli.Version=v0.1.0
var Version = "dev"

// Run runs the command line and returns the exit status: 0, 1 for a
// failure, 2 for a command line that was not valid. A failure is written
// to stderr as "great-machine: <error>". `shell` returns the exit status
// of the remote shell, and a command that was interrupted while it
// followed a rollout returns 130.
//
// The context of a command ends with the first SIGINT or SIGTERM. From
// then on signals are the system's again, so a second one ends the
// process at once.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
	}()
	return run(ctx, newApp(stdout, stderr), args)
}

// commands returns every command of the program.
func commands() []*cli.Command {
	return []*cli.Command{
		draalCommand(),
		zathrasCommand(),
		idCommand(),
		rolloutCommand(),
		rollbackCommand(),
		statusCommand(),
		logsCommand(),
		shellCommand(),
		versionCommand(),
	}
}

// newApp returns the program with its commands, writing to stdout and
// stderr.
func newApp(stdout, stderr io.Writer) *cli.App {
	return &cli.App{
		Name:            "great-machine",
		Usage:           "roll a program built with Nix out to a group of pods",
		Writer:          stdout,
		ErrWriter:       stderr,
		HideHelpCommand: true,
		Commands:        commands(),
	}
}

// usage is an error that says the command line was not valid.
type usage struct{ err error }

func (u *usage) Error() string { return u.err.Error() }
func (u *usage) Unwrap() error { return u.err }

// usageError returns an error that makes Run return 2 instead of 1: the
// command line was not valid. A command returns it from its Action for a
// missing argument, one too many, a flag that is required and not set, or
// a flag whose value cannot be. The format is that of fmt.Errorf, %w
// included.
func usageError(format string, args ...any) error {
	return &usage{err: fmt.Errorf(format, args...)}
}

// run runs app and turns what it returns into an exit status. It is
// apart from Run so that a test can run an app with commands of its own.
func run(ctx context.Context, app *cli.App, args []string) int {
	// The library would print a flag it does not know and the whole help
	// by itself, and leave the process by itself for some errors. Both
	// are this function's to do.
	onUsage := func(_ *cli.Context, err error, _ bool) error { return usageError("%w", err) }
	app.OnUsageError = onUsage
	setOnUsage(app.Commands, onUsage)
	app.ExitErrHandler = func(*cli.Context, error) {}
	// Without a command the help is shown; a word that is no command is
	// not a reason to show it.
	app.Action = func(c *cli.Context) error {
		if c.Args().Present() {
			return usageError("unknown command %q", c.Args().First())
		}
		return cli.ShowAppHelp(c)
	}
	err := app.RunContext(ctx, args)
	if err == nil {
		return 0
	}
	// A command that chooses its exit status returns a cli.ExitCoder: the
	// status of the remote shell, or 130 after an interrupt. Its message
	// is printed like any other, and one that is empty is not printed.
	var coder cli.ExitCoder
	if errors.As(err, &coder) {
		if msg := coder.Error(); msg != "" {
			fmt.Fprintln(app.ErrWriter, "great-machine:", msg)
		}
		return coder.ExitCode()
	}
	fmt.Fprintln(app.ErrWriter, "great-machine:", err)
	var u *usage
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

// setOnUsage makes every command, and the commands inside it, report a
// flag that could not be read through onUsage.
func setOnUsage(cmds []*cli.Command, onUsage cli.OnUsageErrorFunc) {
	for _, cmd := range cmds {
		cmd.OnUsageError = onUsage
		setOnUsage(cmd.Subcommands, onUsage)
	}
}
```

- [ ] **Step 24: Run the tests to see them pass**

Run:

```bash
go mod tidy
go vet ./...
go test ./cli/ -race -count=3
go build ./...
```

Expected: PASS, three times over, and everything builds (`go build ./...` writes no file). The new tests
are `TestInvalidCommandLines` (49 command lines, each with the exit status
2, one line on stderr and nothing on stdout), `TestReadDraal`,
`TestReadZathras`, `TestZathrasHealthAddressTaken`, `TestID`,
`TestIDDefaultKey`, `TestRunExitStatus` and `TestHelpNamesEveryCommand`.
Task 1's tests in `cli/app_test.go` pass unchanged.

- [ ] **Step 25: Commit**

```bash
git add cli/ go.mod go.sum
git commit -m "CLI: draal, zathras and id; every command in the program"
```
