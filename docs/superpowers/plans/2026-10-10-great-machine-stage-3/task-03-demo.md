### Task 3: The made-up fleet becomes a package

`web/demo` is a program whose files `fleet.go`, `figures.go`, `lines.go` and `shell.go` are a made-up `draal.Controller`. The terminal UI is developed against the same fleet, so the fleet moves into a package `demo` at the top of the module, and `web/demo` keeps its `main.go` and uses it. **Nothing changes in what either does: this task is a move.** The type `demo` becomes `Demo`, its method `run` becomes `Run`, and one function is new: `New`.

**Files:**
- Create: `demo/fleet.go`, `demo/figures.go`, `demo/lines.go`, `demo/shell.go` (moved from `web/demo/` with `git mv`; `package main` becomes `package demo`, the type `demo` becomes `Demo`, `run` becomes `Run`; `fleet.go` gets the package comment and `New`)
- Create: `demo/demo_test.go` (moved from `web/demo/demo_test.go`: the three tests of the fleet, and a new one of `New` and `Run`)
- Create: `web/demo/main_test.go` (the test of the program, `TestDemoServes`, as it was, with the two helpers it uses)
- Modify: `web/demo/main.go` (uses the package; its doc comment says so)

**Interfaces:**
- Consumes: `draal.Controller`, `draal.Shell` (`draal/draal.go`); `ptyshell.Start`; `logring.Ring`; `wire`.
- Produces, as the contract has them:
  - `package demo` (import path `github.com/fables-for-robots/great-machine/demo`)
  - `func New(empty bool) *Demo`: the made-up controller, four services (`empty`, `fresh`, `web`, `worker`) or none when `empty` is set; the fleet of the time it is called at.
  - `func (d *Demo) Run(ctx context.Context)`: moves the fleet until `ctx` is done.
  - `*Demo` is a `draal.Controller`: `Watch`, `Rollouts`, `History`, `Logs`, `Shell`, `ServiceOf`, and `Store`, `Rollout`, `Rollback`, which refuse. Its `Shell` starts **a real shell on the machine that runs it**.
  - Unexported and used by the tests of the package alone: `newDemo(start time.Time, empty bool) *Demo`, which is the fleet of a given time.

- [ ] **Step 1: Move the files, and rename what they declare**

The files are moved with `git mv`, so that git sees a rename, and the names are changed in place. `sed -i.bak` is the form of the command that works on macOS and on Linux alike.

```bash
mkdir demo
git mv web/demo/fleet.go web/demo/figures.go web/demo/lines.go web/demo/shell.go web/demo/demo_test.go demo/
sed -i.bak -e 's/^package main$/package demo/' demo/*.go
sed -i.bak \
  -e 's/\*demo)/*Demo)/g' \
  -e 's/\*demo {/*Demo {/' \
  -e 's/&demo{/\&Demo{/' \
  -e 's/^type demo struct/type Demo struct/' \
  -e 's/^\/\/ demo is the made-up fleet\. It is/\/\/ Demo is the made-up fleet. It is/' \
  -e 's/^\/\/ A demo is a Controller/\/\/ A Demo is a Controller/' \
  -e 's/^func (d \*Demo) run(/func (d *Demo) Run(/' \
  -e 's/^\/\/ run moves the fleet along with the clock until ctx is done\./\/\/ Run moves the fleet until ctx is done./' \
  -e 's/(d \*demo, /(d *Demo, /' \
  demo/*.go
rm demo/*.bak
```

Check that nothing else is named `demo` as a type: `grep -n '\bdemo\b' demo/*.go | grep -v 'the demo\|package demo'` prints one line, a comment of the test (`// The demo is what the page is developed against…`).

- [ ] **Step 2: Write the failing test**

Three changes of `demo/demo_test.go`, and one new file.

1. The imports of `demo/demo_test.go` become (the test of the program goes, and with it `net/http` and the WebSocket; the new test needs `draal`):

```go
import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/wire"
)
```

2. Delete the last test of `demo/demo_test.go`, `TestDemoServes`, with its comment: everything from the line `// The smoke test: the program is started on a free port, asked for one` to the end of the file. It moves to `web/demo/main_test.go` below.

3. Append to `demo/demo_test.go`:

```go
// New and Run are what the two demos use: the fleet of now, which a
// watcher is sent again once Run moves it, and Run ends with its context.
func TestNewFleetMovesWhileRunRuns(t *testing.T) {
	var c draal.Controller = New(false)
	fleets, err := c.Watch(t.Context())
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if f := <-fleets; len(f.Services) != 4 {
		t.Fatalf("the first fleet has %d services, want four", len(f.Services))
	}
	if f, _ := New(true).Watch(t.Context()); len((<-f).Services) != 0 {
		t.Error("New(true) has a service, want none")
	}

	ctx, stop := context.WithCancel(t.Context())
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		c.(*Demo).Run(ctx)
	}()
	select {
	case f, ok := <-fleets:
		if !ok || len(f.Services) != 4 {
			t.Errorf("the second fleet is %+v (open: %v), want four services", f, ok)
		}
	case <-time.After(deadline):
		t.Error("timed out waiting for the fleet to move")
	}
	stop()
	select {
	case <-ended:
	case <-time.After(deadline):
		t.Fatal("timed out waiting for Run to end")
	}
}
```

Create `web/demo/main_test.go`. It is `TestDemoServes` as it was, unchanged, with the constant and the helper of the old file that it uses:

```go
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
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `nix develop .#bare --command go test ./demo/ ./web/demo/`
Expected: FAIL, both packages do not build:

```
# github.com/fables-for-robots/great-machine/demo [github.com/fables-for-robots/great-machine/demo.test]
demo/demo_test.go:285:27: undefined: New
demo/demo_test.go:293:13: undefined: New
FAIL	github.com/fables-for-robots/great-machine/demo [build failed]
# github.com/fables-for-robots/great-machine/web/demo [github.com/fables-for-robots/great-machine/web/demo.test]
web/demo/main.go:69:11: undefined: newDemo
FAIL	github.com/fables-for-robots/great-machine/web/demo [build failed]
FAIL
```

- [ ] **Step 4: Write the implementation**

In `demo/fleet.go`, the package gets its comment. The first line of the file, `package demo`, becomes:

```go
// Package demo is a made-up fleet behind the interface of draal: a
// draal.Controller that invents four services, their zathrases, their
// figures and their logs, and moves them along. The web page and the
// terminal UI are developed against it (web/demo and tui/demo).
//
// Everything of it is made up but the shell: Shell starts A REAL SHELL ON
// THE MACHINE THAT RUNS IT, as the user who runs it.
//
// It is not part of the program, and nothing of great-machine uses it.
package demo
```

and behind the function `newDemo` (it ends with `return d` and `}`), add:

```go
// New returns the made-up controller of web/demo: four services
// (empty, fresh, web, worker), or none when empty is set, with a fleet
// that moves by itself once Run is called.
func New(empty bool) *Demo {
	return newDemo(time.Now(), empty)
}
```

In `web/demo/main.go`, three changes. The first paragraph of the doc comment becomes:

```go
// Command demo serves the web page of great-machine over a made-up fleet,
// for developing the page: the real handler of the package web, and
// behind it the controller of the package demo, which invents four
// services, their zathrases, their figures and their logs, and moves
// them along.
```

the imports of the module become:

```go
	"github.com/fables-for-robots/great-machine/demo"
	"github.com/fables-for-robots/great-machine/web"
```

and in `run`, the four lines that make the fleet become:

```go
	fleet := demo.New(empty)
	moving, stopMoving := context.WithCancel(context.Background())
	defer stopMoving()
	go fleet.Run(moving)
```

(`time` is still imported: `ReadHeaderTimeout` and the shutdown use it.)

- [ ] **Step 5: Run the tests to see them pass**

Run: `nix develop .#bare --command go test ./demo/ ./web/demo/`
Expected: PASS (`ok` for both packages)

Run: `nix develop .#bare --command gofmt -l demo web/demo`
Expected: no output

The demo of the page behaves as before. Run `nix develop .#bare --command go run ./web/demo --addr 127.0.0.1:0` and end it with Ctrl-C: its first two lines are, as before,

```
demo: a made-up fleet behind the real API of great-machine, at http://127.0.0.1:PORT/
demo: the shell of the page is a REAL SHELL ON THIS MACHINE, as the user who runs the demo
```

- [ ] **Step 6: Check that git sees a move, and commit**

```bash
git add -A demo web/demo
git diff --cached --stat -M
```

Expected: the five files as renames, and the two of the program:

```
 {web/demo => demo}/demo_test.go |  85 +++++++++++---------------------
 {web/demo => demo}/figures.go   |   2 +-
 {web/demo => demo}/fleet.go     | 104 +++++++++++++++++++++++-----------------
 {web/demo => demo}/lines.go     |   4 +-
 {web/demo => demo}/shell.go     |   2 +-
 web/demo/main.go                |  10 ++--
 web/demo/main_test.go           |  94 ++++++++++++++++++++++++++++++++++++
 7 files changed, 192 insertions(+), 109 deletions(-)
```

```bash
git commit -m "Demo: the made-up fleet of web/demo is a package, for the terminal UI to be developed against it too"
```
