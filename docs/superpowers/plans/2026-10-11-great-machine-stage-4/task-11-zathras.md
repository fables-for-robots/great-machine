### Task 11: zathras mounts its store, and starts everything through its launcher

The parts of Tasks 8 to 10 become the zathras a pod runs. Its `prepare` asks whatever is its store to fetch the closure, where it posted a list to a sidecar; its command line loses `--preload-url` and gets `--store-cache`, `--no-store` and `--user`; and `great-machine zathras` makes its launcher, mounts its store, runs as before, and unmounts when what ran from the store has ended. The package `preload`, the client of the endpoint that is no more, goes.

**The order is the task.** At the start: the launcher first, because a zathras that cannot keep its capability from what it starts must not mount; then the store; then everything as before. At the end: the agent and the supervisor, which stops the service; then the store; then the health check, which answers 503 until then. Every way out after the store was started stops it, once.

A zathras without a store of its own (`--no-store`, and every zathras on a system that is not Linux) fetches nothing: its `prepare` checks the binary alone, and `/nix/store` is whatever is there. The pods of `e2e` are such zathrases with a store that notes what it was asked for.

**The last test of this task is what a review found missing.** The tests of the command started no service, and so held nothing of how the command is put together: a zathras that gave its launcher to nobody, forgot `--user` and stopped its store as soon as it was told to end passed them all. That test runs the command under a draal of its own with a service that ignores SIGTERM, on any system; on Linux as root it also looks at what the service and a shell may do, once more as another user. From it on the test binary of `cli` is its own `exec-in-place`.

**Files:**
- Modify: `zathras/agent.go`, `zathras/commands.go`, `cli/zathras.go`, `e2e/doc.go`, `demo/fleet.go`
- Delete: `preload/preload.go`, `preload/preload_test.go`
- Test: `zathras/agent_test.go`, `zathras/commands_test.go`, `cli/commands_test.go`, `cli/fake_test.go`, `cli/zathras_linux_test.go`, `cli/zathras_test.go`, `e2e/e2e_test.go`, `e2e/harness_test.go`, `draal/reconcile_test.go`, `rollout/decide_test.go`, `cli/main_test.go`, `cli/zathras_service_test.go`

**Interfaces:**
- Consumes: `launcher.New`, `launcher.Config`, `launcher.ParseUser`, `(*Launcher).Start`, `Close` (Task 8); `supervisor.Config.Start`, `ptyshell.Config.Start` (Task 9); `storemount.Start`, `storemount.Config`, `(*Mount).Preload`, `Stop`, `*storemount.NotFoundError` (Task 10).
- Produces:
  - `zathras.Preloader`: `interface { Preload(ctx context.Context, names []string) error }`, which `*storemount.Mount` is. `zathras.Config.Preload` is one; nil: nothing is fetched.
  - The reason of a `prepare` that failed, as draal records it and the operator reads it: the text of the store's error, which names the store path and the service (Task 10).
  - `great-machine zathras`: `--store-cache DIR` (`GREAT_MACHINE_STORE_CACHE`, `/var/cache/great-machine`), `--no-store` (`GREAT_MACHINE_NO_STORE`), `--user UID[:GID]` (`GREAT_MACHINE_USER`); `--preload-url` is gone. An empty `--store-cache` without `--no-store`, and a `--user` that is no user, end the command line with 2.
  - Three seams of `zathrasConfig` for tests: `addrs` (where draal is), `storeDir` (where the store is mounted, `/nix/store` when empty) and `startStore` (what starts the store).
  - No package `preload`.

- [ ] **Step 1: Write the failing test: a prepare has the store of the zathras fetch the closure, and a zathras without one checks the binary alone**

Change `zathras/agent_test.go`:

```diff
--- a/zathras/agent_test.go
+++ b/zathras/agent_test.go
@@ -13,7 +13,6 @@ import (
 	"github.com/fables-for-robots/great-machine/logring"
 	"github.com/fables-for-robots/great-machine/node"
 	"github.com/fables-for-robots/great-machine/node/nodetest"
-	"github.com/fables-for-robots/great-machine/preload"
 	"github.com/fables-for-robots/great-machine/ptyshell"
 	"github.com/fables-for-robots/great-machine/stats"
 	"github.com/fables-for-robots/great-machine/supervisor"
@@ -57,10 +56,10 @@ func within[T any](t *testing.T, what string, f func() T) T {
 
 // setup is what a test wants of its world to be other than usual.
 type setup struct {
-	preloadURL string        // the preload endpoint; "": one that nothing listens at
-	stats      *stats.Reader // nil: no samples
-	shell      string        // the program of a shell; "": /bin/sh
-	deaf       bool          // nobody listens as draal until the test does
+	store Preloader     // the store of the zathras; nil: it has none of its own
+	stats *stats.Reader // nil: no samples
+	shell string        // the program of a shell; "": /bin/sh
+	deaf  bool          // nobody listens as draal until the test does
 }
 
 // world is an agent that runs, its supervisor, which runs too, and the
@@ -91,9 +90,6 @@ func newWorld(t *testing.T, s setup) *world {
 		StopTimeout: 5 * time.Second,
 		Log:         log,
 	})
-	if s.preloadURL == "" {
-		s.preloadURL = "http://127.0.0.1:1/v1/preload"
-	}
 	w.agent = New(Config{
 		Service: "web",
 		Name:    "web-0",
@@ -105,7 +101,7 @@ func newWorld(t *testing.T, s setup) *world {
 		Supervisor: w.sup,
 		Ring:       w.ring,
 		Stats:      s.stats,
-		Preload:    preload.Client{URL: s.preloadURL},
+		Preload:    s.store,
 		Shell: ptyshell.Config{
 			Shell: s.shell,
 			Env:   []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PS1=$ "},
```

Change `zathras/commands_test.go`:

```diff
--- a/zathras/commands_test.go
+++ b/zathras/commands_test.go
@@ -1,57 +1,55 @@
 package zathras
 
 import (
-	"io"
-	"net/http"
-	"net/http/httptest"
+	"context"
+	"errors"
 	"os"
 	"path/filepath"
 	"slices"
 	"strings"
 	"testing"
 
+	"github.com/fables-for-robots/great-machine/storemount"
 	"github.com/fables-for-robots/great-machine/testservice/build"
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
-// sidecar is a preload endpoint of a test.
-type sidecar struct {
-	url     string
-	bodies  chan string   // what was posted
-	entered chan struct{} // gets a value when a request has come
-	release chan struct{} // if hang is set, a request waits for a value
-	gone    chan struct{} // gets a value when a request that waited was ended by its client
+// store is the store of a test: a Preloader that says what it was asked
+// to fetch, and answers as the test has it.
+type store struct {
+	err     error         // what Preload returns when it does not hang
+	hang    bool          // a preload waits for a value on release, or for its context
+	asked   chan []string // the names of every preload
+	entered chan struct{} // gets a value when a preload has begun
+	release chan struct{}
+	gone    chan struct{} // gets a value when a preload that waited was ended by its context
 }
 
-// newSidecar returns an endpoint that answers with status and answer. One
-// that hangs answers only when the test sends on release, or never if the
-// client goes first.
-func newSidecar(t *testing.T, status int, answer string, hang bool) *sidecar {
-	t.Helper()
-	sc := &sidecar{
-		bodies:  make(chan string, 16),
+// newStore returns a store whose preloads return err. One that hangs
+// returns only when the test sends on release, or when its context ends.
+func newStore(err error, hang bool) *store {
+	return &store{
+		err:     err,
+		hang:    hang,
+		asked:   make(chan []string, 16),
 		entered: make(chan struct{}, 16),
 		release: make(chan struct{}),
 		gone:    make(chan struct{}, 16),
 	}
-	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-		body, _ := io.ReadAll(r.Body)
-		sc.bodies <- string(body)
-		sc.entered <- struct{}{}
-		if hang {
-			select {
-			case <-sc.release:
-			case <-r.Context().Done():
-				sc.gone <- struct{}{}
-				return
-			}
+}
+
+func (s *store) Preload(ctx context.Context, names []string) error {
+	s.asked <- slices.Clone(names)
+	s.entered <- struct{}{}
+	if s.hang {
+		select {
+		case <-s.release:
+		case <-ctx.Done():
+			s.gone <- struct{}{}
+			return ctx.Err()
 		}
-		w.WriteHeader(status)
-		io.WriteString(w, answer)
-	}))
-	t.Cleanup(srv.Close)
-	sc.url = srv.URL + "/v1/preload"
-	return sc
+	}
+	return s.err
 }
 
 // notExecutable returns a file that is there and may not be executed.
@@ -73,22 +71,22 @@ func activate(generation uint64, binary string, args ...string) *wire.Request {
 }
 
 func TestPrepare(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "fetched 2 paths\n", false)
-	w := newWorld(t, setup{preloadURL: sc.url})
+	st := newStore(nil, false)
+	w := newWorld(t, setup{store: st})
 	l := w.accept()
 	binary := build.Binary(t)
 
-	resp, err := l.call(prepare(7, binary, "6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0", "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
+	closure := []string{"6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0", "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"}
+	resp, err := l.call(prepare(7, binary, closure...))
 	if err != nil || resp.Prepared == nil {
 		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
 	}
-	want := "/nix/store/6in5jlbspq9szjvlrdxq9rpmxyvca529-busybox-1.37.0\n/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0\n"
-	if got := <-sc.bodies; got != want {
-		t.Errorf("the endpoint was sent %q, want %q", got, want)
+	if got := <-st.asked; !slices.Equal(got, closure) {
+		t.Errorf("the store was asked to fetch %q, want the closure, %q", got, closure)
 	}
-	st := l.report("with generation 7 prepared", func(st wire.State) bool { return slices.Contains(st.Prepared, 7) })
-	if st.Preparing != 0 || st.Phase != wire.PhaseIdle {
-		t.Errorf("the report is %+v, want nothing preparing and the service idle: a prepare starts nothing", st)
+	state := l.report("with generation 7 prepared", func(st wire.State) bool { return slices.Contains(st.Prepared, 7) })
+	if state.Preparing != 0 || state.Phase != wire.PhaseIdle {
+		t.Errorf("the report is %+v, want nothing preparing and the service idle: a prepare starts nothing", state)
 	}
 
 	// Prepared is kept ascending, and holds a generation once.
@@ -105,8 +103,8 @@ func TestPrepare(t *testing.T) {
 }
 
 func TestPrepareIsReportedWhileItRuns(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", true)
-	w := newWorld(t, setup{preloadURL: sc.url})
+	store := newStore(nil, true)
+	w := newWorld(t, setup{store: store})
 	l := w.accept()
 
 	s := l.open(prepare(4, build.Binary(t), "a"))
@@ -115,7 +113,7 @@ func TestPrepareIsReportedWhileItRuns(t *testing.T) {
 		t.Errorf("Prepared = %v while the prepare runs, want none", st.Prepared)
 	}
 
-	sc.release <- struct{}{}
+	store.release <- struct{}{}
 	if resp, err := l.read(s); err != nil || resp.Prepared == nil {
 		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
 	}
@@ -124,18 +122,22 @@ func TestPrepareIsReportedWhileItRuns(t *testing.T) {
 	})
 }
 
-func TestPrepareThatTheSidecarRefuses(t *testing.T) {
-	const said = "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0: the server has no such path"
-	sc := newSidecar(t, http.StatusNotFound, said+"\n", false)
-	w := newWorld(t, setup{preloadURL: sc.url})
+// A store path of the closure that the store does not have for the
+// service is what the operator has to read: the reason of the failure
+// names the path and the service, as the store said it.
+func TestPrepareOfAStorePathTheStoreDoesNotHave(t *testing.T) {
+	missing := &storemount.NotFoundError{Path: "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0", Service: "web"}
+	w := newWorld(t, setup{store: newStore(missing, false)})
 	l := w.accept()
 
 	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
-	if err == nil {
-		t.Fatalf("prepare answered %+v, want an error", resp)
+	if wire.CodeOf(err) != wire.CodeInternal {
+		t.Fatalf("prepare answered %+v, %v, want an internal error", resp, err)
 	}
-	if !strings.Contains(err.Error(), said) || !strings.Contains(err.Error(), "404") {
-		t.Errorf("the error is %q, want one with the status and what the sidecar said, %q", err, said)
+	for _, word := range []string{"/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0", "the service web", "the store has no"} {
+		if !strings.Contains(err.Error(), word) {
+			t.Errorf("the error does not say %q: %v", word, err)
+		}
 	}
 	w.idle()
 	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
@@ -143,18 +145,39 @@ func TestPrepareThatTheSidecarRefuses(t *testing.T) {
 	}
 }
 
-func TestPrepareWithoutASidecar(t *testing.T) {
-	w := newWorld(t, setup{}) // nothing listens at the preload endpoint
+// And one that could not be fetched: the reason is the store's, with the
+// path and why.
+func TestPrepareOfAStorePathThatCouldNotBeFetched(t *testing.T) {
+	const said = "/nix/store/2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0 could not be fetched from the store: dialing the server: no route to host"
+	w := newWorld(t, setup{store: newStore(errors.New(said), false)})
 	l := w.accept()
-	resp, err := l.call(prepare(5, build.Binary(t), "a"))
-	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), "preload") {
-		t.Fatalf("prepare answered %+v, %v, want an internal error that says preload", resp, err)
+	resp, err := l.call(prepare(5, build.Binary(t), "2sm7dby7g2sfp4cjr3537zhwkgmq3f3b-nginx-1.24.0"))
+	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), said) {
+		t.Fatalf("prepare answered %+v, %v, want an internal error with what the store said, %q", resp, err, said)
+	}
+}
+
+// A zathras without a store of its own (--no-store) fetches nothing: its
+// /nix/store is what the container has, and a prepare is the check of
+// the binary alone.
+func TestPrepareWithoutAStore(t *testing.T) {
+	w := newWorld(t, setup{})
+	l := w.accept()
+	if resp, err := l.call(prepare(5, build.Binary(t), "a")); err != nil || resp.Prepared == nil {
+		t.Fatalf("prepare answered %+v, %v, want prepared", resp, err)
+	}
+	missing := filepath.Join(t.TempDir(), "missing")
+	if resp, err := l.call(prepare(6, missing, "a")); wire.CodeOf(err) != wire.CodeBadRequest || !strings.Contains(err.Error(), missing) {
+		t.Errorf("prepare of a binary that is not there answered %+v, %v, want bad-request with the path", resp, err)
+	}
+	w.idle()
+	if got := w.agent.State().Prepared; !slices.Equal(got, []uint64{5}) {
+		t.Errorf("State().Prepared = %v, want [5]", got)
 	}
 }
 
 func TestPrepareOfABinaryThatIsNotExecutable(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "fetched 1 paths\n", false)
-	w := newWorld(t, setup{preloadURL: sc.url})
+	w := newWorld(t, setup{store: newStore(nil, false)})
 	l := w.accept()
 
 	for _, binary := range []string{notExecutable(t), filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
@@ -178,16 +201,16 @@ func TestPrepareWithoutAGeneration(t *testing.T) {
 }
 
 func TestPrepareIsWithdrawnByClosingItsStream(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "", true)
-	w := newWorld(t, setup{preloadURL: sc.url})
+	st := newStore(nil, true)
+	w := newWorld(t, setup{store: st})
 	l := w.accept()
 
 	s := l.open(prepare(6, build.Binary(t), "a"))
-	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
+	within(t, "the preload to begin", func() struct{} { return <-st.entered })
 	s.Close()
 
-	// The request to the sidecar is ended, which is how its fetches end.
-	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
+	// The context of the preload ends, which is how its fetches end.
+	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
 	w.idle()
 	if st := w.agent.State(); len(st.Prepared) != 0 || st.Preparing != 0 {
 		t.Errorf("State() = %+v after a prepare that was withdrawn, want nothing prepared or preparing", st)
@@ -195,20 +218,34 @@ func TestPrepareIsWithdrawnByClosingItsStream(t *testing.T) {
 }
 
 func TestPrepareEndsWithTheConnection(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "", true)
-	w := newWorld(t, setup{preloadURL: sc.url})
+	st := newStore(nil, true)
+	w := newWorld(t, setup{store: st})
 	l := w.accept()
 
 	l.open(prepare(6, build.Binary(t), "a"))
-	within(t, "the request to reach the endpoint", func() struct{} { return <-sc.entered })
+	within(t, "the preload to begin", func() struct{} { return <-st.entered })
 	l.conn.Close()
 
-	within(t, "the endpoint to be told that the client is gone", func() struct{} { return <-sc.gone })
+	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
 	w.idle()
 	// And the agent is back.
 	w.accept()
 }
 
+// A zathras that is told to stop while it prepares does not wait for the
+// store: the preload is ended, and Run returns.
+func TestPrepareEndsWhenTheAgentIsStopped(t *testing.T) {
+	st := newStore(nil, true)
+	w := newWorld(t, setup{store: st})
+	l := w.accept()
+
+	l.open(prepare(6, build.Binary(t), "a"))
+	within(t, "the preload to begin", func() struct{} { return <-st.entered })
+	w.stop()
+	within(t, "the preload to be ended by its context", func() struct{} { return <-st.gone })
+	w.idle()
+}
+
 func TestActivate(t *testing.T) {
 	w := newWorld(t, setup{})
 	l := w.accept()
@@ -273,8 +310,7 @@ func TestServiceOutlivesTheConnection(t *testing.T) {
 }
 
 func TestStateIsTheWholeState(t *testing.T) {
-	sc := newSidecar(t, http.StatusOK, "", false)
-	w := newWorld(t, setup{preloadURL: sc.url, stats: fixtureStats(t)})
+	w := newWorld(t, setup{store: newStore(nil, false), stats: fixtureStats(t)})
 	l := w.accept()
 	binary := build.Binary(t)
 
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./zathras/`

Expected: it fails, with

```text
zathras/agent_test.go:59:8: undefined: Preloader
```

- [ ] **Step 3: Write the code: zathras.Preloader: the store of a zathras is whatever fetches a closure, and may be none**

Change `zathras/agent.go`:

```diff
--- a/zathras/agent.go
+++ b/zathras/agent.go
@@ -21,13 +21,22 @@ import (
 
 	"github.com/fables-for-robots/great-machine/logring"
 	"github.com/fables-for-robots/great-machine/node"
-	"github.com/fables-for-robots/great-machine/preload"
 	"github.com/fables-for-robots/great-machine/ptyshell"
 	"github.com/fables-for-robots/great-machine/stats"
 	"github.com/fables-for-robots/great-machine/supervisor"
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
+// Preloader fetches the store paths of a closure before anything of it is
+// used: *storemount.Mount, the store of the zathras.
+type Preloader interface {
+	// Preload fetches the store paths of names, each the name of a store
+	// path without /nix/store/. Its error is the reason a prepare fails
+	// with: it names the store path that could not be fetched, and says
+	// why. It ends with ctx.
+	Preload(ctx context.Context, names []string) error
+}
+
 // Config is what an agent is made of.
 type Config struct {
 	Service string // the service this zathras belongs to
@@ -42,8 +51,10 @@ type Config struct {
 	Ring *logring.Ring
 	// Stats reads the samples. nil: no samples.
 	Stats *stats.Reader
-	// Preload is the client of the sidecar, for prepare.
-	Preload preload.Client
+	// Preload is the store of the zathras, which a prepare has fetch the
+	// closure. nil: the zathras has no store of its own (--no-store);
+	// nothing is fetched, and a prepare is the check of the binary alone.
+	Preload Preloader
 	// Shell is what a shell session is started with. Term, Cols and Rows
 	// come with the request.
 	Shell ptyshell.Config
```

Change `zathras/commands.go`:

```diff
--- a/zathras/commands.go
+++ b/zathras/commands.go
@@ -10,10 +10,10 @@ import (
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
-// prepare has the sidecar fetch the closure and then checks the binary.
-// ctx is done when draal has withdrawn the prepare, which ends the request
-// to the sidecar. The generation is in Preparing while this runs, and in
-// Prepared when it has worked.
+// prepare has the store fetch the closure and then checks the binary. ctx
+// is done when draal has withdrawn the prepare, or the zathras is told to
+// stop, which ends the fetching. The generation is in Preparing while this
+// runs, and in Prepared when it has worked.
 func (a *Agent) prepare(ctx context.Context, req *wire.Prepare) error {
 	if req.Generation == 0 {
 		return wire.Errorf(wire.CodeBadRequest, "a prepare needs a generation")
@@ -46,11 +46,14 @@ func (a *Agent) prepare(ctx context.Context, req *wire.Prepare) error {
 }
 
 // fetch is the work of a prepare. Its errors are as they travel: the
-// message is the reason, which for an answer of the sidecar that is not
-// 200 holds what the sidecar said.
+// message is the reason, which for a store path that could not be fetched
+// is what the store said: the path, and whether the store has none of it
+// for the service or why it could not be fetched.
 func (a *Agent) fetch(ctx context.Context, req *wire.Prepare) error {
-	if err := a.cfg.Preload.Preload(ctx, req.Closure); err != nil {
-		return wire.Errorf(wire.CodeInternal, "%s", reason(err))
+	if a.cfg.Preload != nil {
+		if err := a.cfg.Preload.Preload(ctx, req.Closure); err != nil {
+			return wire.Errorf(wire.CodeInternal, "%s", reason(err))
+		}
 	}
 	if err := supervisor.CheckExecutable(req.Binary); err != nil {
 		return wire.Errorf(wire.CodeBadRequest, "%s", reason(err))
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./zathras/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add zathras/agent_test.go zathras/commands_test.go zathras/agent.go zathras/commands.go
git commit -m 'Zathras: a prepare has the store of the zathras fetch the closure, and a zathras without one checks the binary alone'
```

- [ ] **Step 6: Write the failing test: great-machine zathras: --store-cache, --no-store and --user; the store is started first and stopped last**

Change `cli/commands_test.go`:

```diff
--- a/cli/commands_test.go
+++ b/cli/commands_test.go
@@ -13,6 +13,7 @@ import (
 	"time"
 
 	"github.com/amber-store/jaccard-store/bucket"
+	"github.com/fables-for-robots/great-machine/launcher"
 	"github.com/fables-for-robots/great-machine/wire"
 	"github.com/urfave/cli/v2"
 )
@@ -134,6 +135,13 @@ func TestInvalidCommandLines(t *testing.T) {
 		{"zathras: a --stop-timeout that is no duration", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "soon"}, `"soon"`},
 		{"zathras: a --stop-timeout of nothing", nil, []string{"zathras", "--draal", idA, "--service", "web", "--stop-timeout", "0s"}, "--stop-timeout: 0s is not a time"},
 		{"zathras: an argument", nil, []string{"zathras", "--draal", idA, "--service", "web", "now"}, `zathras takes no argument, got "now"`},
+		{"zathras: a --user that is a name", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "nobody"}, `--user: "nobody" is no user: want UID or UID:GID, both numbers`},
+		{"zathras: a --user below nothing", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "-1"}, `--user: "-1" is no user`},
+		{"zathras: a --user above every ID", nil, []string{"zathras", "--draal", idA, "--service", "web", "--user", "1000:4294967296"}, `--user: "1000:4294967296" is no user`},
+		{"zathras: the variable stands in for --user", map[string]string{"GREAT_MACHINE_USER": "root"},
+			[]string{"zathras", "--draal", idA, "--service", "web"}, `--user: "root" is no user`},
+		{"zathras: the flag of the sidecar's endpoint, which is gone", nil,
+			[]string{"zathras", "--draal", idA, "--service", "web", "--preload-url", "http://127.0.0.1:9889/v1/preload"}, "flag provided but not defined: -preload-url"},
 	}
 	for _, tt := range tests {
 		t.Run(tt.name, func(t *testing.T) {
@@ -322,41 +330,54 @@ func TestReadZathras(t *testing.T) {
 		draal:       wire.ID(idA),
 		service:     "web",
 		name:        host,
-		preloadURL:  "http://127.0.0.1:9889/v1/preload",
 		shell:       "/bin/sh",
 		stopTimeout: 2 * time.Minute,
 		healthAddr:  ":9890",
+		storeCache:  "/var/cache/great-machine",
 	}
-	if got != want {
+	if !reflect.DeepEqual(got, want) {
 		t.Errorf("the defaults: got %+v, want %+v", got, want)
 	}
 
-	// The variables, and two flags that win over theirs. An empty
+	// The variables, and the flags that win over theirs. An empty
 	// --health-addr is no health check.
 	t.Setenv("GREAT_MACHINE_DRAAL", idB)
 	t.Setenv("GREAT_MACHINE_SERVICE", "api")
 	t.Setenv("GREAT_MACHINE_NAME", "api-0")
-	t.Setenv("GREAT_MACHINE_PRELOAD_URL", "http://127.0.0.1:1/preload")
 	t.Setenv("GREAT_MACHINE_SHELL", "/bin/bash")
 	t.Setenv("GREAT_MACHINE_STOP_TIMEOUT", "30s")
 	t.Setenv("GREAT_MACHINE_HEALTH_ADDR", "127.0.0.1:9999")
+	t.Setenv("GREAT_MACHINE_STORE_CACHE", "/cache")
+	t.Setenv("GREAT_MACHINE_NO_STORE", "true")
+	t.Setenv("GREAT_MACHINE_USER", "1000:100")
 	read(t, zathrasCommand(), reader, "--name", "api-1", "--health-addr", "")
 	want = zathrasConfig{
 		draal:       wire.ID(idB),
 		service:     "api",
 		name:        "api-1",
-		preloadURL:  "http://127.0.0.1:1/preload",
 		shell:       "/bin/bash",
 		stopTimeout: 30 * time.Second,
 		healthAddr:  "",
+		storeCache:  "/cache",
+		noStore:     true,
+		user:        &launcher.User{UID: 1000, GID: 100},
+	}
+	if !reflect.DeepEqual(got, want) {
+		t.Errorf("the variables: got %+v (user %+v), want %+v (user %+v)", got, got.user, want, want.user)
 	}
-	if got != want {
-		t.Errorf("the variables: got %+v, want %+v", got, want)
+
+	// A user without a group has the group of the same number.
+	read(t, zathrasCommand(), reader, "--store-cache", "/var/cache/other", "--user", "65534", "--no-store=false")
+	want.name, want.healthAddr = "api-0", "127.0.0.1:9999"
+	want.storeCache, want.noStore, want.user = "/var/cache/other", false, &launcher.User{UID: 65534, GID: 65534}
+	if !reflect.DeepEqual(got, want) {
+		t.Errorf("the flags: got %+v (user %+v), want %+v (user %+v)", got, got.user, want, want.user)
 	}
 }
 
 // A health check that cannot listen ends zathras at once, before it has
-// made a key or dialed anyone.
+// made a key or dialed anyone. Without a store of its own, which it would
+// have mounted before.
 func TestZathrasHealthAddressTaken(t *testing.T) {
 	cleanEnvironment(t)
 	taken, err := net.Listen("tcp", "127.0.0.1:0")
@@ -366,7 +387,7 @@ func TestZathrasHealthAddressTaken(t *testing.T) {
 	defer taken.Close()
 	var stdout, stderr bytes.Buffer
 	got := run(context.Background(), newApp(&stdout, &stderr),
-		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--health-addr", taken.Addr().String()})
+		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--no-store", "--health-addr", taken.Addr().String()})
 	if got != 1 || !strings.Contains(stderr.String(), "great-machine: the health check: listen tcp") {
 		t.Errorf("the exit status is %d and stderr %q, want 1 and the failure to listen", got, stderr.String())
 	}
```

Change `cli/fake_test.go`:

```diff
--- a/cli/fake_test.go
+++ b/cli/fake_test.go
@@ -190,10 +190,11 @@ var variables = []string{
 	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "GREAT_MACHINE_BIND", "GREAT_MACHINE_WEB_ADDR",
 	"GREAT_MACHINE_S3_BUCKET", "GREAT_MACHINE_S3_PREFIX", "GREAT_MACHINE_S3_ENDPOINT", "GREAT_MACHINE_S3_REGION", "GREAT_MACHINE_S3_PATH_STYLE",
 	"GREAT_MACHINE_STORE_SCRATCH", "GREAT_MACHINE_STORE_VERIFY_JOBS", "GREAT_MACHINE_STORE_MAX_PACK_SIZE", "GREAT_MACHINE_STORE_NO_VERIFY",
-	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME", "GREAT_MACHINE_PRELOAD_URL",
+	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME",
 	"GREAT_MACHINE_SHELL", "GREAT_MACHINE_STOP_TIMEOUT", "GREAT_MACHINE_HEALTH_ADDR",
 	"GREAT_MACHINE_KEY", "GREAT_MACHINE_PARALLEL", "GREAT_MACHINE_DETACH", "GREAT_MACHINE_JOBS",
 	"GREAT_MACHINE_TEMP_DIR", "GREAT_MACHINE_FOLLOW",
+	"GREAT_MACHINE_STORE_CACHE", "GREAT_MACHINE_NO_STORE", "GREAT_MACHINE_USER",
 }
 
 // cleanEnvironment takes the variables of the commands out of the
```

Create `cli/zathras_linux_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/amber-store/jaccard-nfs-nix-store/jstest"
	jmount "github.com/amber-store/jaccard-nfs-nix-store/mount"

	"github.com/fables-for-robots/great-machine/wire"
)

// The command as a whole, with the store it really has: a zathras that
// runs has its store mounted, and one that has ended has not.
//
// The store is mounted on a directory of the test. /nix/store itself is
// mounted on by the tests of package storemount alone: the tests of the
// packages run at the same time, and there is one /nix/store.
func TestRunZathrasMountsItsStore(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("mounting needs root: scripts/test-linux.sh runs this as root, in a container")
	}
	dial := jstest.Start(t).DialConfig()
	cfg := aZathras(t)
	// The store is draal's: one endpoint ID. Here that is a store alone,
	// which takes no zathras: this one dials for as long as it runs.
	cfg.draal, cfg.addrs = wire.ID(dial.Server.String()), dial.Addrs
	cfg.storeDir = t.TempDir()
	t.Cleanup(func() { jmount.Detach(cfg.storeDir) })
	mounted := func() bool {
		_, is, err := jmount.Mounted(cfg.storeDir)
		if err != nil {
			t.Fatal(err)
		}
		return is
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
	answering(t, cfg.healthAddr)
	// The health check listens after the store is mounted.
	if !mounted() {
		t.Errorf("the zathras runs and its store is not mounted\n%s", logged.String())
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("runZathras did not return\n%s", logged.String())
	}
	if mounted() {
		t.Errorf("the zathras has ended and its store is still mounted\n%s", logged.String())
	}
}
```

Create `cli/zathras_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/storemount"
	"github.com/fables-for-robots/great-machine/wire"
)

// fakeStore is the store of a zathras as a test has it: it says what it
// was started with, and counts how often it was stopped.
type fakeStore struct {
	err     error                  // what the start fails with; nil: it starts
	started chan storemount.Config // gets what the store was started with
	stops   atomic.Int64
	onStop  func() // called by Stop, if set
}

func newFakeStore() *fakeStore {
	return &fakeStore{started: make(chan storemount.Config, 4)}
}

func (f *fakeStore) start(cfg storemount.Config) (zathrasStore, error) {
	f.started <- cfg
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeStore) Preload(context.Context, []string) error { return nil }

func (f *fakeStore) Stop() error {
	f.stops.Add(1)
	if f.onStop != nil {
		f.onStop()
	}
	return nil
}

// freeAddr returns an address of the loopback interface that nothing
// listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// aZathras is the configuration of a zathras whose draal is never
// reached: nothing listens where it is dialed.
func aZathras(t *testing.T) zathrasConfig {
	t.Helper()
	return zathrasConfig{
		draal:       wire.ID(idA),
		service:     "web",
		name:        "web-0",
		shell:       "/bin/sh",
		stopTimeout: 5 * time.Second,
		healthAddr:  freeAddr(t),
		storeCache:  t.TempDir(),
		addrs:       []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")},
	}
}

// healthz asks the health check of a zathras, and returns the status, or
// 0 when nothing answers.
func healthz(addr string) int {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// answering waits until the health check of a zathras answers.
func answering(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for healthz(addr) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the health check of the zathras does not answer")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// returned waits for runZathras to return, and returns what it returned.
func returned(t *testing.T, done <-chan error, logged *bytes.Buffer) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("runZathras did not return\n%s", logged.String())
		return nil
	}
}

// A zathras starts its store before anything else that could use it, with
// the endpoint ID of draal as the store's and its own service, and stops
// it last of what runs from it: when the agent and the supervisor have
// ended, and while the health check still answers.
func TestRunZathrasStartsItsStoreAndStopsItLast(t *testing.T) {
	cfg := aZathras(t)
	store := newFakeStore()
	cfg.startStore = store.start
	var healthAtStop atomic.Int64
	store.onStop = func() { healthAtStop.Store(int64(healthz(cfg.healthAddr))) }

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()

	var got storemount.Config
	select {
	case got = <-store.started:
	case err := <-done:
		t.Fatalf("runZathras returned before it started its store: %v\n%s", err, logged.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("the store was not started\n%s", logged.String())
	}
	want := storemount.Config{Server: cfg.draal, Addrs: cfg.addrs, Service: "web", Cache: cfg.storeCache}
	got.Log = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the store was started with %+v, want %+v", got, want)
	}
	answering(t, cfg.healthAddr)
	if n := store.stops.Load(); n != 0 {
		t.Fatalf("the store was stopped %d times while the zathras runs", n)
	}

	stop()
	if err := returned(t, done, &logged); err != nil {
		t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
	}
	if n := store.stops.Load(); n != 1 {
		t.Errorf("the store was stopped %d times, want once", n)
	}
	if got := healthAtStop.Load(); got != http.StatusServiceUnavailable {
		t.Errorf("while the store was stopped the health check answered %d, want 503: it is shut down last", got)
	}
	if got := healthz(cfg.healthAddr); got != 0 {
		t.Errorf("the health check answers %d after runZathras has returned", got)
	}
}

// With --no-store a zathras starts no store, and runs.
func TestRunZathrasWithoutAStore(t *testing.T) {
	cfg := aZathras(t)
	cfg.noStore = true
	cfg.startStore = func(storemount.Config) (zathrasStore, error) {
		t.Error("a store was started, and the zathras was told to have none")
		return nil, errors.New("no store")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
	answering(t, cfg.healthAddr)
	if !strings.Contains(logged.String(), "--no-store") {
		t.Errorf("the log does not say that the zathras has no store of its own:\n%s", logged.String())
	}
	stop()
	if err := returned(t, done, &logged); err != nil {
		t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
	}
}

// A store that cannot be started ends the zathras at once, with what the
// store said, and before anything listens: without a store nothing can
// run, and the kubelet starts the container again.
func TestRunZathrasWhoseStoreCannotBeStarted(t *testing.T) {
	cfg := aZathras(t)
	store := newFakeStore()
	store.err = errors.New("storemount: mounting: operation not permitted: the container of a zathras has to let it mount")
	cfg.startStore = store.start
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(context.Background(), cfg, quietLog(&logged)) }()
	err := returned(t, done, &logged)
	if err == nil || !errors.Is(err, store.err) || !strings.Contains(err.Error(), "the store of the zathras") {
		t.Errorf("runZathras returned %v, want the error of the store, said to be the store's", err)
	}
	if n := store.stops.Load(); n != 0 {
		t.Errorf("a store that was not started was stopped %d times", n)
	}
	l, err := net.Listen("tcp", cfg.healthAddr)
	if err != nil {
		t.Fatalf("the address of the health check is taken after runZathras has returned: %v", err)
	}
	l.Close()
}

// Every way out after the store was started stops the store: a mount is
// not left behind by a zathras that ends for another reason.
func TestRunZathrasStopsItsStoreWhenItCannotGoOn(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := aZathras(t)
	cfg.healthAddr = taken.Addr().String()
	store := newFakeStore()
	cfg.startStore = store.start
	var logged bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(context.Background(), cfg, quietLog(&logged)) }()
	if err := returned(t, done, &logged); err == nil || !strings.Contains(err.Error(), "the health check: listen tcp") {
		t.Errorf("runZathras returned %v, want the failure of the health check to listen", err)
	}
	if n := store.stops.Load(); n != 1 {
		t.Errorf("the store was stopped %d times, want once", n)
	}
}
```

- [ ] **Step 7: Run it, and see it fail**

Run: `go test ./cli/`

Expected: it fails, with

```text
cli/zathras_test.go:34:51: undefined: zathrasStore
```

- [ ] **Step 8: Write the code: great-machine zathras makes its launcher, starts its store and stops it after what ran from it; --preload-url is gone**

Change `cli/zathras.go`:

```diff
--- a/cli/zathras.go
+++ b/cli/zathras.go
@@ -7,17 +7,19 @@ import (
 	"log/slog"
 	"net"
 	"net/http"
+	"net/netip"
 	"os"
 	"strings"
 	"sync"
 	"time"
 
 	"github.com/fables-for-robots/great-machine/health"
+	"github.com/fables-for-robots/great-machine/launcher"
 	"github.com/fables-for-robots/great-machine/logring"
 	"github.com/fables-for-robots/great-machine/node"
-	"github.com/fables-for-robots/great-machine/preload"
 	"github.com/fables-for-robots/great-machine/ptyshell"
 	"github.com/fables-for-robots/great-machine/stats"
+	"github.com/fables-for-robots/great-machine/storemount"
 	"github.com/fables-for-robots/great-machine/supervisor"
 	"github.com/fables-for-robots/great-machine/wire"
 	"github.com/fables-for-robots/great-machine/zathras"
@@ -35,7 +37,10 @@ func zathrasCommand() *cli.Command {
 	return &cli.Command{
 		Name:  "zathras",
 		Usage: "the init of an app container: runs the service draal tells it to run",
-		Description: "The health check, GET /healthz on --health-addr, passes while the service\n" +
+		Description: "It serves the store of its service itself and mounts it on /nix/store,\n" +
+			"for which its container needs CAP_SYS_ADMIN; the service and the shells\n" +
+			"are started without that capability.\n\n" +
+			"The health check, GET /healthz on --health-addr, passes while the service\n" +
 			"runs and at no other time. It is a readiness probe, not a liveness probe.",
 		Flags: []cli.Flag{
 			draalFlag(),
@@ -43,8 +48,12 @@ func zathrasCommand() *cli.Command {
 				Usage: "`NAME` of the service this zathras belongs to (required)"},
 			&cli.StringFlag{Name: "name", EnvVars: []string{"GREAT_MACHINE_NAME"},
 				Usage: "`NAME` the operator sees (default: the host name, which in a pod is the pod's name)"},
-			&cli.StringFlag{Name: "preload-url", EnvVars: []string{"GREAT_MACHINE_PRELOAD_URL"}, Value: "http://127.0.0.1:9889/v1/preload",
-				Usage: "`URL` of the preload endpoint of the sidecar"},
+			&cli.StringFlag{Name: "store-cache", EnvVars: []string{"GREAT_MACHINE_STORE_CACHE"}, Value: "/var/cache/great-machine",
+				Usage: "`DIR`ectory of what the store has fetched; it is of one draal and one service"},
+			&cli.BoolFlag{Name: "no-store", EnvVars: []string{"GREAT_MACHINE_NO_STORE"},
+				Usage: "serve and mount no store: /nix/store is what the container has, and nothing is fetched"},
+			&cli.StringFlag{Name: "user", EnvVars: []string{"GREAT_MACHINE_USER"},
+				Usage: "`UID[:GID]` the service and the shells run as, with that group and no other (default: the user of zathras)"},
 			&cli.StringFlag{Name: "shell", EnvVars: []string{"GREAT_MACHINE_SHELL"}, Value: "/bin/sh",
 				Usage: "`PATH` of the program of a shell session"},
 			&cli.DurationFlag{Name: "stop-timeout", EnvVars: []string{"GREAT_MACHINE_STOP_TIMEOUT"}, Value: 2 * time.Minute,
@@ -62,15 +71,34 @@ func zathrasCommand() *cli.Command {
 	}
 }
 
+// zathrasStore is the store of a zathras, which it serves and mounts
+// itself: *storemount.Mount.
+type zathrasStore interface {
+	zathras.Preloader
+	// Stop takes the mount away and stops the server.
+	Stop() error
+}
+
 // zathrasConfig is what the command line of zathras says.
 type zathrasConfig struct {
 	draal       wire.ID
 	service     string
 	name        string
-	preloadURL  string
 	shell       string
 	stopTimeout time.Duration
-	healthAddr  string // "" for no health check
+	healthAddr  string         // "" for no health check
+	storeCache  string         // what the store has fetched
+	noStore     bool           // no store of its own
+	user        *launcher.User // who the service and the shells run as; nil: the user of zathras
+	// addrs is for tests: draal, and with it the store, is dialed at these
+	// addresses, without a relay and without a lookup. No flag sets it.
+	addrs []netip.AddrPort
+	// storeDir is for tests: the directory the store is mounted on. "":
+	// /nix/store. No flag sets it.
+	storeDir string
+	// startStore is for tests: what starts the store. nil:
+	// storemount.Start. No flag sets it.
+	startStore func(storemount.Config) (zathrasStore, error)
 }
 
 // readZathras reads the command line of zathras.
@@ -80,10 +108,11 @@ func readZathras(c *cli.Context) (zathrasConfig, error) {
 	}
 	cfg := zathrasConfig{
 		name:        strings.TrimSpace(c.String("name")),
-		preloadURL:  c.String("preload-url"),
 		shell:       c.String("shell"),
 		stopTimeout: c.Duration("stop-timeout"),
 		healthAddr:  c.String("health-addr"),
+		storeCache:  c.String("store-cache"),
+		noStore:     c.Bool("no-store"),
 	}
 	var err error
 	if cfg.draal, err = draalID(c); err != nil {
@@ -99,6 +128,16 @@ func readZathras(c *cli.Context) (zathrasConfig, error) {
 	if cfg.stopTimeout <= 0 {
 		return zathrasConfig{}, usageError("--stop-timeout: %s is not a time a service has to end in", cfg.stopTimeout)
 	}
+	if text := c.String("user"); text != "" {
+		user, err := launcher.ParseUser(text)
+		if err != nil {
+			return zathrasConfig{}, usageError("--user: %v", err)
+		}
+		cfg.user = &user
+	}
+	if !cfg.noStore && cfg.storeCache == "" {
+		return zathrasConfig{}, usageError("--store-cache: the store needs a directory for what it fetches; --no-store is a zathras without a store of its own")
+	}
 	if cfg.name == "" {
 		if cfg.name, err = os.Hostname(); err != nil {
 			return zathrasConfig{}, fmt.Errorf("no host name to go by, give a name with --name: %w", err)
@@ -108,13 +147,57 @@ func readZathras(c *cli.Context) (zathrasConfig, error) {
 }
 
 // runZathras is zathras until ctx is done, which SIGINT and SIGTERM do:
-// it then stops the service, ends the shells and returns nil.
-func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) error {
-	// The health check listens first: an address that is taken ends the
-	// command before anything is started or draal has heard of it.
+// it then stops the service, ends the shells, stops its store and returns
+// nil.
+func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) (err error) {
+	// What the service and the shells are started with comes first: a
+	// zathras that cannot keep the capability it mounts with from what it
+	// starts does not mount.
+	starter, err := launcher.New(launcher.Config{User: cfg.user, Log: log})
+	if err != nil {
+		return err
+	}
+	defer starter.Close()
+
+	// The store, before anything that could use it. It is not dialed yet:
+	// draal is needed when a store path is first named.
+	var store zathrasStore
+	if cfg.noStore {
+		log.Info("no store of its own (--no-store): /nix/store is what the container has, and a prepare fetches nothing")
+	} else {
+		start := cfg.startStore
+		if start == nil {
+			start = func(c storemount.Config) (zathrasStore, error) { return storemount.Start(c) }
+		}
+		store, err = start(storemount.Config{
+			Server: cfg.draal, Addrs: cfg.addrs, Service: cfg.service,
+			Cache: cfg.storeCache, Dir: cfg.storeDir, Log: log,
+		})
+		if err != nil {
+			return fmt.Errorf("the store of the zathras: %w", err)
+		}
+	}
+	// Every way out from here stops the store, once: at the end, when
+	// what ran from it has ended, and on the ways out before that.
+	var stopping sync.Once
+	stopStore := func() {
+		if store == nil {
+			return
+		}
+		stopping.Do(func() {
+			if serr := store.Stop(); serr != nil {
+				log.Error("stopping the store", "error", serr)
+				err = errors.Join(err, fmt.Errorf("stopping the store of the zathras: %w", serr))
+			}
+		})
+	}
+	defer stopStore()
+
+	// The health check listens before anything is started or draal has
+	// heard of this zathras: an address that is taken ends the command
+	// here.
 	var listener net.Listener
 	if cfg.healthAddr != "" {
-		var err error
 		if listener, err = net.Listen("tcp", cfg.healthAddr); err != nil {
 			return fmt.Errorf("the health check: %w", err)
 		}
@@ -136,22 +219,28 @@ func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) error
 		Stdout:      os.Stdout,
 		Stderr:      os.Stderr,
 		StopTimeout: cfg.stopTimeout,
+		Start:       starter.Start,
 		Log:         log,
 	})
-	agent := zathras.New(zathras.Config{
+	agentCfg := zathras.Config{
 		Service: cfg.service,
 		Name:    cfg.name,
 		Version: Version,
 		Dial: func(ctx context.Context) (node.Conn, error) {
-			return node.Dial(ctx, node.DialConfig{Key: key, Server: cfg.draal, Log: log})
+			return node.Dial(ctx, node.DialConfig{Key: key, Server: cfg.draal, Addrs: cfg.addrs, Log: log})
 		},
 		Supervisor: sup,
 		Ring:       ring,
 		Stats:      stats.NewReader(),
-		Preload:    preload.Client{URL: cfg.preloadURL},
-		Shell:      ptyshell.Config{Shell: cfg.shell},
+		Shell:      ptyshell.Config{Shell: cfg.shell, Start: starter.Start},
 		Log:        log,
-	})
+	}
+	if store != nil {
+		// Not assigned when there is none: an interface that holds a nil
+		// store would not be nil.
+		agentCfg.Preload = store
+	}
+	agent := zathras.New(agentCfg)
 	log.Info("zathras", "endpoint", string(key.ID()), "service", cfg.service, "name", cfg.name, "draal", string(cfg.draal))
 
 	// Whatever ends first ends the rest: a signal, or a part that failed.
@@ -190,24 +279,30 @@ func runZathras(ctx context.Context, cfg zathrasConfig, log *slog.Logger) error
 
 	// The order of the end, once ctx is done:
 	//
-	//  1. The agent ends the shells and leaves draal, and the supervisor
-	//     stops the service: SIGTERM, and SIGKILL after --stop-timeout.
-	//     Both are waited for here.
-	//  2. The health check is shut down last. While the service is being
+	//  1. The agent ends the shells and what it prepares, and leaves
+	//     draal, and the supervisor stops the service: SIGTERM, and
+	//     SIGKILL after --stop-timeout. Both are waited for here.
+	//  2. The store is stopped: nothing runs from it any more. The mount
+	//     goes, and then its server.
+	//  3. The health check is shut down last. While the service is being
 	//     stopped it goes on answering, with 503 as the phase is no longer
 	//     running, so that the pod is taken out of its Service by an
 	//     answer and not by a connection that is refused.
 	wg.Wait()
+	stopStore()
 	if web != nil {
 		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
 		defer done()
 		web.Shutdown(shutdown)
 	}
 	select {
-	case err := <-failed:
-		return err
+	case ferr := <-failed:
+		return errors.Join(ferr, err)
 	default:
-		log.Info("stopped")
-		return nil
 	}
+	if err != nil {
+		return err
+	}
+	log.Info("stopped")
+	return nil
 }
```

- [ ] **Step 9: Run it, and see it pass**

Run: `go test ./cli/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 10: Commit**

```bash
git add cli/commands_test.go cli/fake_test.go cli/zathras_linux_test.go cli/zathras_test.go cli/zathras.go
git commit -m 'CLI: great-machine zathras: --store-cache, --no-store and --user; the store is started first and stopped last'
```

- [ ] **Step 11: Write the failing test: the pods of e2e have a store, which is asked to fetch the closure of a rollout**

Change `e2e/e2e_test.go`:

```diff
--- a/e2e/e2e_test.go
+++ b/e2e/e2e_test.go
@@ -4,6 +4,7 @@ import (
 	"context"
 	"io"
 	"net/http"
+	"slices"
 	"strings"
 	"testing"
 	"time"
@@ -22,8 +23,8 @@ func TestRolloutCompletes(t *testing.T) {
 		t.Errorf("the stable generation is %d, want %d", svc.Stable, info.Generation)
 	}
 	for _, p := range h.pods {
-		if got := p.preloads(); !strings.Contains(got, "/nix/store/"+storeName) {
-			t.Errorf("the preload endpoint of %s was sent %q, want the closure", p.name, got)
+		if got := p.fetched(); !slices.Contains(got, storeName) {
+			t.Errorf("the store of %s was asked to fetch %q, want the closure, %q", p.name, got, storeName)
 		}
 		st := p.agent.State()
 		if st.Generation != info.Generation || st.Phase != wire.PhaseRunning || st.Binary != h.binary {
```

- [ ] **Step 12: Run it, and see it fail**

Run: `go test ./e2e/`

Expected: it fails, with

```text
e2e/e2e_test.go:26:15: p.fetched undefined (type *pod has no field or method fetched)
```

- [ ] **Step 13: Write the code: package preload is gone, with the endpoint it was the client of; a pod of e2e is the store of its zathras**

Change `e2e/harness_test.go`:

```diff
--- a/e2e/harness_test.go
+++ b/e2e/harness_test.go
@@ -12,6 +12,7 @@ import (
 	"net/netip"
 	"os"
 	"path/filepath"
+	"slices"
 	"strings"
 	"sync"
 	"syscall"
@@ -28,7 +29,6 @@ import (
 	"github.com/fables-for-robots/great-machine/logring"
 	"github.com/fables-for-robots/great-machine/node"
 	"github.com/fables-for-robots/great-machine/operator"
-	"github.com/fables-for-robots/great-machine/preload"
 	"github.com/fables-for-robots/great-machine/ptyshell"
 	"github.com/fables-for-robots/great-machine/storeserver"
 	"github.com/fables-for-robots/great-machine/supervisor"
@@ -84,18 +84,17 @@ type harness struct {
 
 // pod is a zathras with all that belongs to it.
 type pod struct {
-	name    string
-	id      wire.ID
-	sup     *supervisor.Supervisor
-	ring    *logring.Ring
-	agent   *zathras.Agent
-	health  *httptest.Server
-	preload *httptest.Server
-
-	mu        sync.Mutex
-	conn      node.Conn // the latest connection to draal
-	away      bool      // it does not dial draal: see keepAway
-	preloaded []string  // the bodies the preload endpoint was sent
+	name   string
+	id     wire.ID
+	sup    *supervisor.Supervisor
+	ring   *logring.Ring
+	agent  *zathras.Agent
+	health *httptest.Server
+
+	mu    sync.Mutex
+	conn  node.Conn // the latest connection to draal
+	away  bool      // it does not dial draal: see keepAway
+	asked []string  // the store paths the store was asked to fetch
 }
 
 // start builds testservice into a store of the test, starts a draal, the
@@ -265,8 +264,18 @@ func (h *harness) operator(key node.Key) *operator.Client {
 	return c
 }
 
+// Preload is the pod as the store of its zathras: it fetches nothing, and
+// notes what it was asked to fetch. The store of the tests is a directory
+// that is there from the start.
+func (p *pod) Preload(ctx context.Context, names []string) error {
+	p.mu.Lock()
+	p.asked = append(p.asked, names...)
+	p.mu.Unlock()
+	return nil
+}
+
 // startPod starts a zathras: its supervisor, its agent with a key of its
-// own, its health check and its preload endpoint.
+// own, its health check and its store.
 func (h *harness) startPod(name string) *pod {
 	h.t.Helper()
 	key, err := node.NewKey()
@@ -274,12 +283,6 @@ func (h *harness) startPod(name string) *pod {
 		h.t.Fatalf("NewKey: %v", err)
 	}
 	p := &pod{name: name, id: key.ID(), ring: logring.New(400, 16<<10)}
-	p.preload = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
-		body, _ := io.ReadAll(r.Body)
-		p.mu.Lock()
-		p.preloaded = append(p.preloaded, string(body))
-		p.mu.Unlock()
-	}))
 	p.sup = supervisor.New(supervisor.Config{
 		Ring:        p.ring,
 		StopTimeout: 5 * time.Second,
@@ -312,7 +315,7 @@ func (h *harness) startPod(name string) *pod {
 		},
 		Supervisor:  p.sup,
 		Ring:        p.ring,
-		Preload:     preload.Client{URL: p.preload.URL},
+		Preload:     p,
 		Shell:       ptyshell.Config{},
 		ReportEvery: 50 * time.Millisecond,
 		RedialMin:   50 * time.Millisecond,
@@ -330,7 +333,6 @@ func (h *harness) startPod(name string) *pod {
 		cancel()
 		running.Wait()
 		p.health.Close()
-		p.preload.Close()
 	})
 	return p
 }
@@ -361,11 +363,12 @@ func (p *pod) comeBack() {
 	p.mu.Unlock()
 }
 
-// preloads returns everything the preload endpoint was sent.
-func (p *pod) preloads() string {
+// fetched returns the store paths the store of the zathras was asked to
+// fetch, by their names.
+func (p *pod) fetched() []string {
 	p.mu.Lock()
 	defer p.mu.Unlock()
-	return strings.Join(p.preloaded, "\n")
+	return slices.Clone(p.asked)
 }
 
 // healthz asks the health check, and returns the status and the body.
```

Delete `preload/preload.go`:

```bash
git rm preload/preload.go
```

Delete `preload/preload_test.go`:

```bash
git rm preload/preload_test.go
```

- [ ] **Step 14: Run it, and see it pass**

Run: `go test ./e2e/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 15: Commit**

```bash
git add e2e/e2e_test.go e2e/harness_test.go preload/preload.go preload/preload_test.go
git commit -m 'Zathras: the pods of e2e have a store, which is asked to fetch the closure of a rollout'
```

- [ ] **Step 16: Write the code: what e2e says of its stand-ins**

Change `e2e/doc.go`:

```diff
--- a/e2e/doc.go
+++ b/e2e/doc.go
@@ -1,9 +1,9 @@
 // Package e2e holds the tests of the whole: a draal, several zathrases
 // with real supervisors and real child processes, and the operator's
 // client, in one process over go-iroh on the loopback interface. Only the
-// sidecar's preload endpoint and the Nix store are stand-ins: the first is
-// an HTTP server of the test, the second a temporary directory that draal
-// is pointed at with Config.StoreDir.
+// store of a zathras and the Nix store are stand-ins: the first fetches
+// nothing and notes what it was asked to fetch, the second is a temporary
+// directory that draal is pointed at with Config.StoreDir.
 //
 // The package has no code but its tests.
 package e2e
```

- [ ] **Step 17: Run it, and see it pass**

Run: `go vet ./e2e/`

Expected: nothing: a comment alone changed.

- [ ] **Step 18: Commit**

```bash
git add e2e/doc.go
git commit -m 'E2E: what e2e says of its stand-ins'
```

- [ ] **Step 19: Write the code: the made-up reasons of the tests and of the demo are those a zathras gives now**

Change `demo/fleet.go`:

```diff
--- a/demo/fleet.go
+++ b/demo/fleet.go
@@ -505,8 +505,9 @@ func (d *Demo) cycle(at time.Time, n int) {
 		begin = 9 * time.Second
 		d.after(at, 2*time.Second, func(time.Time) { d.prepared(zs[0]) })
 		d.after(at, 5*time.Second, func(at time.Time) {
-			d.finish(last(), wire.RolloutHalted, zs[2].name+": preload: the store has no "+
-				strings.TrimSuffix(strings.TrimPrefix(binaryOf("web", web.generation), "/nix/store/"), "/bin/web"), at)
+			d.finish(last(), wire.RolloutHalted, zs[2].name+": the store has no "+
+				strings.TrimSuffix(binaryOf("web", web.generation), "/bin/web")+
+				" for the service web: it holds what the rollouts of web have pushed", at)
 			for _, z := range zs {
 				z.state.Preparing = 0
 			}
```

Change `draal/reconcile_test.go`:

```diff
--- a/draal/reconcile_test.go
+++ b/draal/reconcile_test.go
@@ -165,13 +165,13 @@ func TestAPrepareThatFailsHaltsTheRollout(t *testing.T) {
 	w.stableWeb()
 	a := w.newFake(10, "web", "web-a", running(1))
 	a.onPrepare = func(wire.Prepare, node.Stream) error {
-		return wire.Errorf(wire.CodeInternal, "the sidecar said no")
+		return wire.Errorf(wire.CodeInternal, "the store said no")
 	}
 	a.connect()
 	b := w.zathras(11, "web", "web-b", running(1))
 
 	halted := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
-	if !strings.Contains(halted.Reason, "web-a") || !strings.Contains(halted.Reason, "the sidecar said no") {
+	if !strings.Contains(halted.Reason, "web-a") || !strings.Contains(halted.Reason, "the store said no") {
 		t.Errorf("the reason is %q, want the name of the zathras and what it answered", halted.Reason)
 	}
 	if halted.Finished.IsZero() {
@@ -559,7 +559,7 @@ func TestAFailedCommandIsLogged(t *testing.T) {
 	cfg := testConfig(t)
 	log := logTo(&cfg)
 	w := startWorld(t, cfg)
-	no := func(wire.Prepare, node.Stream) error { return wire.Errorf(wire.CodeInternal, "the sidecar said no") }
+	no := func(wire.Prepare, node.Stream) error { return wire.Errorf(wire.CodeInternal, "the store said no") }
 
 	// Outside a rollout: a new zathras is sent the stable generation.
 	first := w.stableWeb()
@@ -574,7 +574,7 @@ func TestAFailedCommandIsLogged(t *testing.T) {
 	a.connect()
 	outside := map[string]string{
 		"zathras": string(a.id), "name": "web-a", "service": "web",
-		"command": "prepare", "generation": fmt.Sprint(first.Generation), "reason": "the sidecar said no",
+		"command": "prepare", "generation": fmt.Sprint(first.Generation), "reason": "the store said no",
 	}
 	eventually(t, "the failure outside a rollout to be logged", func() bool {
 		_, ok := log.find(slog.LevelWarn, outside)
@@ -588,7 +588,7 @@ func TestAFailedCommandIsLogged(t *testing.T) {
 	info := w.awaitRollout(w.rollout(newWeb(1)), wire.RolloutHalted)
 	inside := map[string]string{
 		"zathras": string(b.id), "name": "web-b", "service": "web",
-		"command": "prepare", "generation": fmt.Sprint(info.Generation), "reason": "the sidecar said no",
+		"command": "prepare", "generation": fmt.Sprint(info.Generation), "reason": "the store said no",
 	}
 	if _, ok := log.find(slog.LevelWarn, inside); !ok {
 		t.Error("the failure in a rollout was not logged")
```

Change `rollout/decide_test.go`:

```diff
--- a/rollout/decide_test.go
+++ b/rollout/decide_test.go
@@ -117,12 +117,12 @@ func TestPreparing(t *testing.T) {
 		in := input(newRollout(wire.RolloutPreparing, 2, 1), 1, old()...)
 		in.Failed[id("web-b")] = Failure{
 			Generation: 2,
-			Reason:     "preload: 502 Bad Gateway: fetch abc-web: no such reference",
+			Reason:     "the store has no /nix/store/abc-web for the service web",
 			At:         now,
 		}
 		check(t, Decide(in), Decision{
 			State:   wire.RolloutHalted,
-			Reason:  "web-b: preload: 502 Bad Gateway: fetch abc-web: no such reference",
+			Reason:  "web-b: the store has no /nix/store/abc-web for the service web",
 			Awaited: 3,
 		})
 	})
@@ -426,8 +426,8 @@ func TestActivatingFailures(t *testing.T) {
 		m := ready()
 		m[0] = member("web-a", running(1, hour))
 		in := input(newRollout(wire.RolloutActivating, 2, 1), 1, m...)
-		in.Failed[id("web-a")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: now}
-		d := halted("web-a: preload: 503 Service Unavailable")
+		in.Failed[id("web-a")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched from the store: no answer", At: now}
+		d := halted("web-a: /nix/store/abc-web could not be fetched from the store: no answer")
 		d.Prepared = 2
 		check(t, Decide(in), d)
 	})
@@ -767,7 +767,7 @@ func TestCompleted(t *testing.T) {
 			member("web-a", running(2, hour)),
 			member("web-n"),
 		)
-		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: failedAt}
+		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched from the store: no answer", At: failedAt}
 		check(t, Decide(in), Decision{
 			Prepared:  1,
 			Activated: 1,
@@ -790,7 +790,7 @@ func TestCompleted(t *testing.T) {
 			member("web-a", running(2, hour)),
 			member("web-n"),
 		)
-		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503 Service Unavailable", At: failedAt}
+		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched from the store: no answer", At: failedAt}
 		check(t, Decide(in), Decision{
 			Commands:  []Command{prepare("web-n", 2)},
 			Prepared:  1,
@@ -818,7 +818,7 @@ func TestCompleted(t *testing.T) {
 			case len(d.Commands) == 1 && d.Commands[0] == prepare("web-n", 2):
 				// The prepare fails at once.
 				sentAt = append(sentAt, in.Now.Sub(start))
-				in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: in.Now}
+				in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched", At: in.Now}
 			case len(d.Commands) == 0 && d.Recheck.After(in.Now):
 				in.Now = d.Recheck
 			default:
@@ -857,7 +857,7 @@ func TestCompleted(t *testing.T) {
 
 	t.Run("Review Focus 3: a failure of another generation holds nothing back", func(t *testing.T) {
 		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2, member("web-n"))
-		in.Failed[id("web-n")] = Failure{Generation: 1, Reason: "preload: 503", At: now}
+		in.Failed[id("web-n")] = Failure{Generation: 1, Reason: "/nix/store/abc-web could not be fetched", At: now}
 		check(t, Decide(in), Decision{
 			Commands: []Command{prepare("web-n", 2)},
 			Awaited:  1,
@@ -868,8 +868,8 @@ func TestCompleted(t *testing.T) {
 		in := input(newRollout(wire.RolloutCompleted, 2, 1), 2,
 			member("web-m"), member("web-n"), member("web-o"),
 		)
-		in.Failed[id("web-m")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-5 * time.Second)}
-		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-20 * time.Second)}
+		in.Failed[id("web-m")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched", At: now.Add(-5 * time.Second)}
+		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched", At: now.Add(-20 * time.Second)}
 		check(t, Decide(in), Decision{
 			Commands: []Command{prepare("web-o", 2)},
 			Awaited:  3,
@@ -925,7 +925,7 @@ func TestHalted(t *testing.T) {
 
 	t.Run("and tried again after a failure like any other", func(t *testing.T) {
 		in := input(halted(), 2, member("web-n"))
-		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "preload: 503", At: now}
+		in.Failed[id("web-n")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched", At: now}
 		check(t, Decide(in), Decision{Awaited: 1, Recheck: now.Add(retryAfter)})
 		in.Now = now.Add(retryAfter)
 		check(t, Decide(in), Decision{
@@ -1162,7 +1162,7 @@ func scenarios() map[string]Input {
 	held.Absent[id("web-b")] = now.Add(-30 * time.Second)
 	held.Absent[id("web-d")] = now.Add(-time.Minute)
 	held.Absent[id("web-f")] = now.Add(-hour)
-	completed.Failed[id("web-c")] = Failure{Generation: 2, Reason: "preload: 503", At: now.Add(-time.Second)}
+	completed.Failed[id("web-c")] = Failure{Generation: 2, Reason: "/nix/store/abc-web could not be fetched", At: now.Add(-time.Second)}
 	halted := input(newRollout(wire.RolloutHalted, 3, 1), 2,
 		sample(member("web-a", running(3, hour))),
 		sample(member("web-b", backoff(3))),
```

- [ ] **Step 20: Run it, and see it pass**

Run: `go test ./draal/ ./rollout/ ./demo/ ./tui/...`

Expected: `ok` for each: the reasons are text that a test or the demo makes up and compares with itself, and say now what a zathras says.

- [ ] **Step 21: Commit**

```bash
git add demo/fleet.go draal/reconcile_test.go rollout/decide_test.go
git commit -m 'Zathras: the made-up reasons of the tests and of the demo are those a zathras gives now'
```

- [ ] **Step 22: A test that holds as it is written: the command with a service in it: the launcher starts the service and the shells, as the user of --user, and the store is stopped after the service has ended**

A review found that the tests of the command started no service, and so held none of this: a zathras that gave its launcher to nobody, forgot --user and stopped its store as soon as it was told to end passed all of them. The test binary is its own exec-in-place from here on.

Create `cli/main_test.go`:

```go
package cli

import (
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The test binary is its own exec-in-place, as the program is: a zathras
// of these tests that starts a service or a shell starts this binary,
// which then becomes what was meant.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
```

Create `cli/zathras_service_test.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// testDraal is a draal for a zathras of these tests to register with, and
// an operator of it.
type testDraal struct {
	id   wire.ID
	addr netip.AddrPort
	op   *operator.Client
}

// aDraal serves a draal on loopback, whose Nix store is storeDir, until
// the test ends.
func aDraal(t *testing.T, storeDir string) *testDraal {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)
	database, err := db.Open(filepath.Join(t.TempDir(), "draal.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	draalKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := node.Listen(ctx, node.ListenConfig{Key: draalKey, Local: true, Log: quiet})
	if err != nil {
		cancel()
		database.Close()
		t.Fatalf("node.Listen: %v", err)
	}
	d := draal.New(draal.Config{
		DB:          database,
		Authz:       authz.NewAllowlist([]wire.ID{opKey.ID()}),
		ID:          draalKey.ID(),
		StoreDir:    storeDir,
		Settle:      10 * time.Millisecond,
		StableAfter: 200 * time.Millisecond,
		RetryAfter:  200 * time.Millisecond,
		WatchEvery:  20 * time.Millisecond,
		Log:         quiet,
	})
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		<-served
		l.Close()
		database.Close()
	})
	dialing, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	conn, err := node.Dial(dialing, node.DialConfig{Key: opKey, Server: draalKey.ID(), Addrs: []netip.AddrPort{l.LocalAddr()}, Log: quiet})
	if err != nil {
		t.Fatalf("dialing draal: %v", err)
	}
	op := operator.New(conn)
	t.Cleanup(func() { op.Close() })
	return &testDraal{id: draalKey.ID(), addr: l.LocalAddr(), op: op}
}

// zathras watches the fleet until its one zathras meets cond, and returns
// it.
func (d *testDraal) zathras(t *testing.T, what string, cond func(wire.Zathras) bool) wire.Zathras {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fleets, err := d.op.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	var last wire.Fleet
	for f := range fleets {
		last = f
		for _, svc := range f.Services {
			for _, z := range svc.Zathrases {
				if cond(z) {
					return z
				}
			}
		}
	}
	t.Fatalf("timed out waiting for %s; the last fleet was %+v", what, last)
	return wire.Zathras{}
}

// wrote follows the log of a zathras until its service has written text
// to its standard output.
func (d *testDraal) wrote(t *testing.T, zathras wire.ID, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lines, err := d.op.Logs(ctx, zathras, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	for line := range lines {
		if line.Stream == "stdout" && line.Text == text {
			return
		}
	}
	t.Fatalf("timed out waiting for the service to write %q", text)
}

// processAlive says whether a process is there and has not ended.
func processAlive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // a system without /proc
	}
	// The state is the field behind the name, which is in brackets.
	rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
	return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
}

// statusOfProcess returns the lines of /proc/<pid>/status by their names.
func statusOfProcess(t *testing.T, pid int) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatalf("the status of process %d: %v", pid, err)
	}
	status := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, ":"); ok {
			status[name] = strings.TrimSpace(value)
		}
	}
	return status
}

// The command as a whole with a service in it, which is where its parts
// have to be put together rightly: the launcher is what starts the service
// and the shells, with the user of --user; the store is asked for the
// closure before the service is started; and at the end the store is
// stopped when the service has ended, and not before, for the service
// runs from it.
//
// The service ignores SIGTERM, so that it runs on for the stop timeout: a
// store that is stopped too early is stopped under a service that runs.
func TestRunZathrasRunsItsServiceThroughItsLauncherAndStopsItsStoreAfterIt(t *testing.T) {
	asRoot := runtime.GOOS == "linux" && os.Geteuid() == 0
	users := map[string]*launcher.User{"as the user of zathras": nil}
	if asRoot {
		users["as another user"] = &launcher.User{UID: 65534, GID: 65534}
	}
	for name, user := range users {
		t.Run(name, func(t *testing.T) {
			// The Nix store of the test, where whoever the service
			// runs as reaches it.
			storeDir, err := os.MkdirTemp("", "great-machine-store-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(storeDir) })
			if storeDir, err = filepath.EvalSymlinks(storeDir); err != nil {
				t.Fatal(err)
			}
			const storeName = "0123456789abcdfghijklmnpqrsvwxyz-testservice"
			bin := filepath.Join(storeDir, storeName, "bin", "testservice")
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				t.Fatal(err)
			}
			program, err := os.ReadFile(build.Binary(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bin, program, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{storeDir, filepath.Dir(filepath.Dir(bin)), filepath.Dir(bin)} {
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			d := aDraal(t, storeDir)

			cfg := aZathras(t)
			cfg.draal, cfg.addrs = d.id, []netip.AddrPort{d.addr}
			cfg.stopTimeout = time.Second
			cfg.user = user
			store := newFakeStore()
			cfg.startStore = store.start
			var pid atomic.Int64
			var ranAtStop atomic.Bool
			store.onStop = func() { ranAtStop.Store(processAlive(int(pid.Load()))) }

			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var logged buffer
			done := make(chan error, 1)
			go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
			ended := func() error {
				t.Helper()
				select {
				case err := <-done:
					return err
				case <-time.After(30 * time.Second):
					t.Fatalf("runZathras did not return\n%s", logged.String())
					return nil
				}
			}
			d.zathras(t, "the zathras to register", func(wire.Zathras) bool { return true })

			asking, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := d.op.Rollout(asking, wire.RolloutRequest{
				Service: "web", Binary: bin, Args: []string{"--ignore-term", "--stdout", "the service is up"}, Closure: []string{storeName}, Parallel: 1,
			}); err != nil {
				t.Fatalf("Rollout: %v", err)
			}
			z := d.zathras(t, "the service to run", func(z wire.Zathras) bool {
				return z.State.Phase == wire.PhaseRunning && z.State.PID != 0
			})
			pid.Store(int64(z.State.PID))
			// The process is the service only when it says so: before
			// that it is still the program that becomes it, or the
			// service before it has said what it ignores.
			d.wrote(t, z.ID, "the service is up")
			if got := store.fetched(); !slices.Equal(got, []string{storeName}) {
				t.Errorf("the store was asked to fetch %q before the service was started, want the closure, %q", got, storeName)
			}

			// A shell of the zathras, which says who it is.
			sh, err := d.op.Shell(asking, z.ID, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
			if err != nil {
				t.Fatalf("Shell: %v", err)
			}
			defer sh.Close()
			if _, err := sh.Write([]byte("echo \"user=$(id -u).\"; grep NoNewPrivs /proc/self/status; exit\n")); err != nil {
				t.Fatalf("writing to the shell: %v", err)
			}
			said, _ := io.ReadAll(sh)

			uid := os.Getuid()
			if user != nil {
				uid = int(user.UID)
			}
			if !strings.Contains(string(said), "user="+strconv.Itoa(uid)+".") {
				t.Errorf("the shell does not run as user %d:\n%s", uid, said)
			}
			if asRoot {
				// What was started through the launcher cannot gain
				// privileges, and runs as the user that was asked for.
				// What it has of capabilities is the launcher's own to
				// test: here it is that the launcher is what starts.
				status := statusOfProcess(t, z.State.PID)
				if status["NoNewPrivs"] != "1" {
					t.Errorf("NoNewPrivs of the service is %q, want 1: it was not started by the launcher", status["NoNewPrivs"])
				}
				if ids := strings.Fields(status["Uid"]); len(ids) == 0 || ids[0] != strconv.Itoa(uid) {
					t.Errorf("the service runs as user %q, want %d", status["Uid"], uid)
				}
				if !strings.Contains(string(said), "NoNewPrivs:\t1") {
					t.Errorf("the shell was not started by the launcher: it can gain privileges:\n%s", said)
				}
			}

			stopping := time.Now()
			stop()
			if err := ended(); err != nil {
				t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
			}
			// The service did not end when it was asked to: it had the
			// stop timeout, and the store was there for all of it.
			if took := time.Since(stopping); took < cfg.stopTimeout {
				t.Errorf("runZathras returned after %s, and the service ignores SIGTERM: it had no stop timeout of %s\n%s", took, cfg.stopTimeout, logged.String())
			}
			if n := store.stops.Load(); n != 1 {
				t.Errorf("the store was stopped %d times, want once", n)
			}
			if ranAtStop.Load() {
				t.Error("the store was stopped while the service still ran from it")
			}
			if processAlive(z.State.PID) {
				t.Errorf("the service, process %d, runs on after runZathras has returned", z.State.PID)
			}
		})
	}
}
```

Change `cli/zathras_test.go`:

```diff
--- a/cli/zathras_test.go
+++ b/cli/zathras_test.go
@@ -9,7 +9,9 @@ import (
 	"net/http"
 	"net/netip"
 	"reflect"
+	"slices"
 	"strings"
+	"sync"
 	"sync/atomic"
 	"testing"
 	"time"
@@ -25,6 +27,9 @@ type fakeStore struct {
 	started chan storemount.Config // gets what the store was started with
 	stops   atomic.Int64
 	onStop  func() // called by Stop, if set
+
+	mu    sync.Mutex
+	asked []string // the names it was asked to fetch
 }
 
 func newFakeStore() *fakeStore {
@@ -39,7 +44,19 @@ func (f *fakeStore) start(cfg storemount.Config) (zathrasStore, error) {
 	return f, nil
 }
 
-func (f *fakeStore) Preload(context.Context, []string) error { return nil }
+func (f *fakeStore) Preload(ctx context.Context, names []string) error {
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	f.asked = append(f.asked, names...)
+	return nil
+}
+
+// fetched returns the names the store was asked to fetch.
+func (f *fakeStore) fetched() []string {
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	return slices.Clone(f.asked)
+}
 
 func (f *fakeStore) Stop() error {
 	f.stops.Add(1)
```

- [ ] **Step 23: Run it, and see it pass**

Run: `go test -run TestRunZathrasRunsItsService -v ./cli/ && scripts/test-linux.sh -run TestRunZathrasRunsItsService -v ./cli/`

Expected: it passes: the command of this task is put together rightly. On Linux as root it runs twice, the second time with the service as another user.

- [ ] **Step 24: Commit**

```bash
git add cli/main_test.go cli/zathras_service_test.go cli/zathras_test.go
git commit -m 'CLI: the command with a service in it: the launcher starts the service and the shells, as the user of --user, and the store is stopped after the service has ended'
```

- [ ] **Step 25: The whole, here and on Linux as root**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
scripts/test-linux.sh
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 33 packages that have tests, both times; and nothing from `git status`. The run on Linux is the one that counts for what this task changed: on a Mac the tests that mount, and those of what the launcher's thread gives up, skip themselves.
