### Task 1: jaccard-nfs-nix-store: a sidecar that preloads by a call

**This task is in another repository**: `github.com/amber-store/jaccard-nfs-nix-store`, checked out beside this one's organisation at `../../amber-store/jaccard-nfs-nix-store` (on the machine this plan was written on: `/Users/dragan/amber-store/jaccard-nfs-nix-store`), at its release v0.2.0. Everything in this task happens there, on a branch `sidecar-preload`, in that repository's own style and with its own commands. It needs Go 1.27, which that repository's flake has.

great-machine is going to run that repository's package `sidecar` inside zathras, with no preload endpoint: nothing in a pod is to take requests to fetch. But the endpoint is the sidecar's only way to fetch a list ahead. This task gives it a second one, a call, by making the endpoint's own fetching a function that both use. Nothing changes for the sidecar as a command.

**It ends in a release, which is the human partner's to make.** Task 10 needs the module at v0.3.0. Stop at the last step and ask.

**Files:**
- Modify: `preload/preload.go`, `README.md`, `sidecar/sidecar.go`
- Test: `preload/preload_test.go`, `sidecar/preload_test.go`

**Interfaces:**
- Consumes: nothing of the other tasks.
- Produces, in jaccard-nfs-nix-store v0.3.0:
  - `preload.Fetch(ctx context.Context, fetch preload.Fetcher, jobs int, names []string) error`: the store paths of the names, `jobs` at once (fewer than one: `preload.DefaultJobs`); the first that cannot be fetched ends the others and is returned as a `*preload.Failure`.
  - `preload.Failure{Name string; Err error}` with `Path() string` (`/nix/store/<name>`), `Error` and `Unwrap`: `errors.Is` finds `refs.ErrNotFound` in it for a store path the server does not have.
  - `(*sidecar.Sidecar).Preload(ctx context.Context, names []string) error`: `preload.Fetch` with the sidecar's store and `Config.PreloadJobs`, whether or not there is a preload endpoint. It ends with `ctx`, whose error it then returns, and with the sidecar, with `sidecar.ErrStopped`.
  - `sidecar.ErrStopped`.

- [ ] **Step 1: The branch**

In the checkout of jaccard-nfs-nix-store:

```bash
git status --short && git describe --tags
git checkout -b sidecar-preload v0.2.0
go version
```

Expected: nothing from `git status`, `v0.2.0` or what follows it on the main branch, and `go1.27`. If the main branch has gone on since v0.2.0, branch from it and not from the tag, and say so in the ledger.

- [ ] **Step 2: Write the failing test: the fetching of a list without the endpoint**

Change `preload/preload_test.go`:

```diff
--- a/preload/preload_test.go
+++ b/preload/preload_test.go
@@ -300,3 +300,100 @@ func TestAListThatIsTooLong(t *testing.T) {
 		t.Fatalf("%d fetches of a list that was refused", asked)
 	}
 }
+
+// Fetch is the fetching without the endpoint: what a program that has the
+// sidecar in it calls with the names it has.
+func TestFetchFetchesEveryName(t *testing.T) {
+	f := &fetcher{}
+	names := []string{"aaa-hello-2.12", "bbb-glibc-2.42", "ccc-bash-5.3"}
+	if err := Fetch(context.Background(), f, 2, names); err != nil {
+		t.Fatalf("Fetch: %v", err)
+	}
+	if got := f.names(); !slices.Equal(got, names) {
+		t.Errorf("fetched %q, want %q", got, names)
+	}
+	nothing := &fetcher{}
+	if err := Fetch(context.Background(), nothing, 2, nil); err != nil {
+		t.Errorf("Fetch of no name: %v", err)
+	}
+	if _, asked := nothing.now(); asked != 0 {
+		t.Errorf("%d fetches for no name", asked)
+	}
+}
+
+func TestFetchNamesThePathThatFailed(t *testing.T) {
+	broken := errors.New("the bucket answered 503")
+	for _, c := range []struct {
+		name     string
+		err      error
+		notFound bool
+	}{
+		{"bbb-missing", fmt.Errorf("%w: %q", refs.ErrNotFound, "bbb-missing"), true},
+		{"bbb-broken", broken, false},
+	} {
+		f := &fetcher{fail: map[string]error{c.name: c.err}}
+		// One job: the names are fetched in their order.
+		err := Fetch(context.Background(), f, 1, []string{"aaa-there", c.name, "ccc-never"})
+		var failed *Failure
+		if !errors.As(err, &failed) {
+			t.Fatalf("%s: Fetch = %v, want a *Failure", c.name, err)
+		}
+		if failed.Name != c.name || failed.Path() != "/nix/store/"+c.name {
+			t.Errorf("%s: the failure is of %q, %q", c.name, failed.Name, failed.Path())
+		}
+		if !errors.Is(err, c.err) || errors.Is(err, refs.ErrNotFound) != c.notFound {
+			t.Errorf("%s: Fetch = %v: it does not hold why", c.name, err)
+		}
+		if want := "/nix/store/" + c.name + ": " + c.err.Error(); err.Error() != want {
+			t.Errorf("%s: the error reads %q, want %q", c.name, err, want)
+		}
+		if got := f.names(); slices.Contains(got, "ccc-never") {
+			t.Errorf("%s: fetched %q, want none after the failure", c.name, got)
+		}
+	}
+}
+
+// Fewer than one job means DefaultJobs, as with New.
+func TestFetchNoMoreAtOnceThanTheJobs(t *testing.T) {
+	f := &fetcher{hold: make(chan struct{})}
+	var names []string
+	for i := range 3 * DefaultJobs {
+		names = append(names, fmt.Sprintf("p%03d", i))
+	}
+	done := make(chan error, 1)
+	go func() { done <- Fetch(context.Background(), f, 0, names) }()
+	eventually(t, "as many fetches run as the default has jobs", func() bool { r, _ := f.now(); return r == DefaultJobs })
+	time.Sleep(50 * time.Millisecond)
+	if running, asked := f.now(); running != DefaultJobs || asked != DefaultJobs {
+		t.Fatalf("%d running and %d begun, want %d of each", running, asked, DefaultJobs)
+	}
+	close(f.hold)
+	if err := <-done; err != nil {
+		t.Fatalf("Fetch: %v", err)
+	}
+	if _, asked := f.now(); asked != len(names) {
+		t.Fatalf("%d fetched, want %d", asked, len(names))
+	}
+}
+
+func TestFetchEndsWithItsContext(t *testing.T) {
+	f := &fetcher{hold: make(chan struct{})}
+	ctx, giveUp := context.WithCancel(context.Background())
+	done := make(chan error, 1)
+	go func() { done <- Fetch(ctx, f, 2, []string{"p0", "p1", "p2"}) }()
+	eventually(t, "two fetches run", func() bool { r, _ := f.now(); return r == 2 })
+	giveUp()
+	select {
+	case err := <-done:
+		if !errors.Is(err, context.Canceled) {
+			t.Errorf("Fetch = %v, want the context's error", err)
+		}
+	case <-time.After(5 * time.Second):
+		t.Fatal("Fetch went on after its context ended")
+	}
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	if len(f.ended) != 2 || len(f.asked) != 2 {
+		t.Errorf("%d fetches told to stop of %d begun, want 2 of 2", len(f.ended), len(f.asked))
+	}
+}
```

- [ ] **Step 3: Run it, and see it fail**

Run: `go test ./preload/`

Expected: it fails, with

```text
preload/preload_test.go:309:12: undefined: Fetch
```

- [ ] **Step 4: Write the code: preload.Fetch and preload.Failure, which the endpoint is made of**

Change `preload/preload.go`:

```diff
--- a/preload/preload.go
+++ b/preload/preload.go
@@ -18,6 +18,9 @@
 // Fetched means what it means for a lookup: the reference is pulled and
 // pinned, and is served from then on. It is not waited for to be
 // materialized.
+//
+// Fetch is the same without the endpoint, for a program that has the
+// sidecar in it and the names at hand.
 package preload
 
 import (
@@ -103,8 +106,8 @@ func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 	}
 
 	began := time.Now()
-	err = h.fetchAll(r.Context(), names)
-	var failed *failure
+	err = Fetch(r.Context(), h.fetch, h.jobs, names)
+	var failed *Failure
 	switch {
 	case err == nil:
 		h.log.Info("preloaded", "paths", len(names), "took", time.Since(began))
@@ -113,33 +116,47 @@ func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
 		// Whoever asked is gone, or the sidecar is stopping: there is
 		// nobody to answer.
 		h.log.Info("preload given up", "paths", len(names), "took", time.Since(began))
-	case errors.As(err, &failed) && errors.Is(failed.err, refs.ErrNotFound):
-		h.log.Warn("preload failed", "path", failed.path(), "error", failed.err)
-		http.Error(w, failed.path()+": the server has no such path", http.StatusNotFound)
+	case errors.As(err, &failed) && errors.Is(failed.Err, refs.ErrNotFound):
+		h.log.Warn("preload failed", "path", failed.Path(), "error", failed.Err)
+		http.Error(w, failed.Path()+": the server has no such path", http.StatusNotFound)
 	case errors.As(err, &failed):
-		h.log.Warn("preload failed", "path", failed.path(), "error", failed.err)
-		http.Error(w, failed.path()+": could not be fetched: "+failed.err.Error(), http.StatusBadGateway)
+		h.log.Warn("preload failed", "path", failed.Path(), "error", failed.Err)
+		http.Error(w, failed.Path()+": could not be fetched: "+failed.Err.Error(), http.StatusBadGateway)
 	default:
 		http.Error(w, err.Error(), http.StatusBadGateway)
 	}
 }
 
-// failure is the path of a list that could not be fetched, and why.
-type failure struct {
-	name string
-	err  error
+// Failure is the store path of a list that could not be fetched, and why.
+type Failure struct {
+	// Name is the name of the store path, without /nix/store/.
+	Name string
+	// Err is why: refs.ErrNotFound for a path the server does not have.
+	Err error
 }
 
-func (f *failure) path() string  { return storeDir + "/" + f.name }
-func (f *failure) Error() string { return f.path() + ": " + f.err.Error() }
-func (f *failure) Unwrap() error { return f.err }
+// Path returns the store path: /nix/store/<name>.
+func (f *Failure) Path() string { return storeDir + "/" + f.Name }
+
+// Error returns the store path and why it could not be fetched.
+func (f *Failure) Error() string { return f.Path() + ": " + f.Err.Error() }
 
-// fetchAll fetches the store paths of the names, as many at once as there
-// are jobs. The first that fails ends the context the others run under,
-// and no further one is begun; its failure is what is returned.
-func (h *handler) fetchAll(ctx context.Context, names []string) error {
+// Unwrap returns why the store path could not be fetched.
+func (f *Failure) Unwrap() error { return f.Err }
+
+// Fetch fetches the store paths of the names with fetch, as many at once
+// as there are jobs; fewer than one job means DefaultJobs. A name is the
+// name of a store path, without /nix/store/.
+//
+// The first name that cannot be fetched ends the context the others run
+// under, and no further one is begun; it is what Fetch returns, as a
+// *Failure. When ctx ends Fetch returns an error that is ctx's.
+func Fetch(ctx context.Context, fetch Fetcher, jobs int, names []string) error {
+	if jobs < 1 {
+		jobs = DefaultJobs
+	}
 	group, ctx := errgroup.WithContext(ctx)
-	group.SetLimit(h.jobs)
+	group.SetLimit(jobs)
 	for _, name := range names {
 		// Go waits for a job to be free. One may have failed meanwhile.
 		if ctx.Err() != nil {
@@ -149,8 +166,8 @@ func (h *handler) fetchAll(ctx context.Context, names []string) error {
 			if err := ctx.Err(); err != nil {
 				return err
 			}
-			if _, err := h.fetch.Preload(ctx, name); err != nil {
-				return &failure{name: name, err: err}
+			if _, err := fetch.Preload(ctx, name); err != nil {
+				return &Failure{Name: name, Err: err}
 			}
 			return nil
 		})
```

- [ ] **Step 5: Run it, and see it pass**

Run: `go test ./preload/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 6: Commit**

```bash
git add preload/preload_test.go preload/preload.go
git commit -m 'Preloading: the fetching of a list without the endpoint'
```

- [ ] **Step 7: Write the failing test: a sidecar preloads by a call, which ends with its context and with the sidecar**

Create `sidecar/preload_test.go`:

```go
//go:build linux

package sidecar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/amber-store/jaccard-nfs-nix-store/jstest"
	"github.com/amber-store/jaccard-nfs-nix-store/refs"
)

// prefix is what the references of these tests begin with on the server.
const prefix = "sidecar/store/"

// quiet drops what the sidecar logs.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// pinned returns the names the sidecar has fetched, sorted.
func pinned(s *Sidecar) []string {
	var names []string
	for _, pin := range s.store.Pins() {
		names = append(names, pin.Name)
	}
	slices.Sort(names)
	return names
}

// A program that has the sidecar in it preloads with a call: there is no
// endpoint to send a list to, and none is needed. The sidecar only
// serves here, which needs no root.
func TestPreloadByCall(t *testing.T) {
	srv := jstest.Start(t)
	src := t.TempDir()
	paths := []string{"aaa-one-1.0", "bbb-two-2.0", "ccc-three-3.0"}
	for _, name := range paths {
		file := filepath.Join(src, name, "share", "name")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		srv.PushDir(filepath.Join(src, name), prefix+name)
	}
	sc, err := Start(Config{
		Prefix: prefix,
		Cache:  t.TempDir(),
		Dial: func(ctx context.Context) (refs.Conn, error) {
			return refs.Connect(ctx, srv.DialConfig())
		},
		Listen:      "127.0.0.1:0",
		PreloadJobs: 2,
		Log:         quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Stop()
	if sc.preloads != nil {
		t.Fatal("the sidecar has a preload endpoint, and was given no address for one")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := sc.Preload(ctx, nil); err != nil {
		t.Errorf("a preload of no name: %v", err)
	}
	if err := sc.Preload(ctx, paths[:2]); err != nil {
		t.Fatalf("Preload: %v", err)
	}
	if got := pinned(sc); !slices.Equal(got, paths[:2]) {
		t.Fatalf("after the preload the store holds %q, want %q", got, paths[:2])
	}
	// What is there is not fetched again, and a name given twice is one.
	if err := sc.Preload(ctx, []string{paths[0], paths[2], paths[2]}); err != nil {
		t.Fatalf("a preload of what is partly there: %v", err)
	}
	if got := pinned(sc); !slices.Equal(got, paths) {
		t.Fatalf("the store holds %q, want %q", got, paths)
	}

	// A path the server does not have is the error, which names it.
	err = sc.Preload(ctx, []string{paths[0], "zzz-missing-0.0"})
	if !errors.Is(err, refs.ErrNotFound) || !strings.Contains(err.Error(), "/nix/store/zzz-missing-0.0") {
		t.Errorf("a preload with a missing path = %v, want refs.ErrNotFound and the path", err)
	}
	// And so is a name that is none.
	if err := sc.Preload(ctx, []string{"../etc"}); !errors.Is(err, refs.ErrNotFound) {
		t.Errorf("a preload of a name that is none = %v, want refs.ErrNotFound", err)
	}
}

// unreached starts a sidecar whose server is never reached: every dial
// says on the channel that it has begun and then waits for its context.
func unreached(t *testing.T) (*Sidecar, <-chan struct{}) {
	t.Helper()
	dialing := make(chan struct{}, 16)
	sc, err := Start(Config{
		Prefix: prefix,
		Cache:  t.TempDir(),
		Dial: func(ctx context.Context) (refs.Conn, error) {
			dialing <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Listen: "127.0.0.1:0",
		Log:    quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc, dialing
}

// ended waits for a preload to return, and returns what it returned.
func ended(t *testing.T, after string, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("the preload went on after %s", after)
		return nil
	}
}

// A preload ends with its context.
func TestPreloadEndsWithItsContext(t *testing.T) {
	sc, dialing := unreached(t)
	defer sc.Stop()
	ctx, giveUp := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sc.Preload(ctx, []string{"aaa-one-1.0"}) }()
	<-dialing
	giveUp()
	if err := ended(t, "its context ended", done); !errors.Is(err, context.Canceled) {
		t.Errorf("a preload whose context ended = %v, want the context's error", err)
	}
}

// A preload ends with the sidecar: stopping does not wait for a fetch
// that nobody else asked for, and nothing is preloaded after it.
func TestPreloadEndsWithTheSidecar(t *testing.T) {
	sc, dialing := unreached(t)
	done := make(chan error, 1)
	go func() { done <- sc.Preload(context.Background(), []string{"bbb-two-2.0"}) }()
	<-dialing
	began := time.Now()
	if err := sc.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("Stop took %v with a preload under way", took)
	}
	if err := ended(t, "the sidecar was stopped", done); !errors.Is(err, ErrStopped) {
		t.Errorf("a preload under way when the sidecar stopped = %v, want ErrStopped", err)
	}
	if err := sc.Preload(context.Background(), []string{"ccc-three-3.0"}); !errors.Is(err, ErrStopped) {
		t.Errorf("a preload after the stop = %v, want ErrStopped", err)
	}
}
```

- [ ] **Step 8: Run it, and see it fail**

Run: `scripts/test-linux.sh ./sidecar/`

Expected: it fails, with

```text
sidecar/preload_test.go:74:15: sc.Preload undefined (type *Sidecar has no field or method Preload)
```

- [ ] **Step 9: Write the code: Sidecar.Preload, for a program that has the sidecar in it**

Change `README.md`:

````diff
--- a/README.md
+++ b/README.md
@@ -149,6 +149,13 @@ initContainers:
 There is no authentication, which is why the endpoint listens on loopback.
 `--preload-listen ''` turns it off.
 
+A program that has the sidecar in it, with package `sidecar`, needs no
+endpoint: `Sidecar.Preload` takes the names of the store paths and fetches
+them the same way, `PreloadJobs` at a time. The first that cannot be
+fetched ends the rest and is the error, which names the path and is
+`refs.ErrNotFound` for one the server does not have. The call ends with
+its context, and with the sidecar.
+
 ## Command
 
 ```sh
@@ -254,7 +261,7 @@ scripts/test-linux.sh -run Mounted -v ./e2e/
 | package | what it is |
 | --- | --- |
 | `refs` | fetching: a reference pulled once and pinned |
-| `preload` | the HTTP endpoint that fetches a list of store paths ahead |
+| `preload` | the fetching of a list of store paths ahead, and the HTTP endpoint for it |
 | `materialize` | a pinned reference written out as plain files |
 | `tree` | directories and file content read from the objects |
 | `handles` | the file handle of a path, and the table back from it |
````

Change `sidecar/sidecar.go`:

```diff
--- a/sidecar/sidecar.go
+++ b/sidecar/sidecar.go
@@ -56,7 +56,8 @@ type Config struct {
 	// lists of store paths to fetch ahead (package preload). Empty means
 	// there is none.
 	PreloadListen string
-	// PreloadJobs is how many paths of one list are fetched at once.
+	// PreloadJobs is how many paths of one list, or of one call of
+	// Preload, are fetched at once.
 	PreloadJobs int
 
 	// AdoptTimeout is how long a mount that an earlier run of the
@@ -68,6 +69,10 @@ type Config struct {
 	Log *slog.Logger
 }
 
+// ErrStopped is what Preload returns for a sidecar that was stopped,
+// before the call or while it ran.
+var ErrStopped = errors.New("sidecar: stopped")
+
 // Sidecar is a sidecar that runs.
 type Sidecar struct {
 	cfg      Config
@@ -80,11 +85,14 @@ type Sidecar struct {
 	listener net.Listener
 	served   chan struct{}
 
-	// The preload endpoint, when there is one. endPreloads ends the
-	// requests that are being answered.
+	// preloading is the context every preload runs under, the requests
+	// of the endpoint and the calls of Preload. endPreloads ends them.
+	preloading  context.Context
+	endPreloads context.CancelFunc
+
+	// The preload endpoint, when there is one.
 	preloads        *http.Server
 	preloadListener net.Listener
-	endPreloads     context.CancelFunc
 }
 
 // Start opens the cache, serves it on cfg.Listen and, when cfg.Mount
@@ -95,6 +103,7 @@ func Start(cfg Config) (_ *Sidecar, err error) {
 	if s.log == nil {
 		s.log = slog.Default()
 	}
+	s.preloading, s.endPreloads = context.WithCancel(context.Background())
 	// What was opened is closed again when a later step fails.
 	defer func() {
 		if err != nil {
@@ -179,12 +188,11 @@ func (s *Sidecar) servePreloads() error {
 	if err != nil {
 		return fmt.Errorf("the preload endpoint: %w", err)
 	}
-	ctx, cancel := context.WithCancel(context.Background())
-	s.preloadListener, s.endPreloads = l, cancel
+	s.preloadListener = l
 	s.preloads = &http.Server{
 		Handler:           preload.New(s.store, s.cfg.PreloadJobs, s.log),
 		ReadHeaderTimeout: 10 * time.Second,
-		BaseContext:       func(net.Listener) context.Context { return ctx },
+		BaseContext:       func(net.Listener) context.Context { return s.preloading },
 	}
 	go func() {
 		if err := s.preloads.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
@@ -195,13 +203,48 @@ func (s *Sidecar) servePreloads() error {
 	return nil
 }
 
-// stopPreloads closes the preload endpoint and ends its requests.
+// stopPreloads ends the preloads that are under way, and closes the
+// preload endpoint if there is one.
 func (s *Sidecar) stopPreloads() {
-	if s.preloads == nil {
-		return
-	}
 	s.endPreloads()
-	s.preloads.Close()
+	if s.preloads != nil {
+		s.preloads.Close()
+	}
+}
+
+// Preload fetches the store paths of names, each the name of a store path
+// without /nix/store/, as the preload endpoint fetches a list:
+// Config.PreloadJobs at once; the first that cannot be fetched ends the
+// others and is returned. The error names the path, and errors.Is finds
+// refs.ErrNotFound in it when the server has no such reference.
+//
+// Preload ends with ctx, whose error it then returns, and when the
+// sidecar is stopped, with ErrStopped. It needs no preload endpoint: it is
+// for a program that has the sidecar in it.
+func (s *Sidecar) Preload(ctx context.Context, names []string) error {
+	if s.preloading.Err() != nil {
+		return ErrStopped
+	}
+	fetching, cancel := context.WithCancel(ctx)
+	defer cancel()
+	defer context.AfterFunc(s.preloading, cancel)()
+
+	began := time.Now()
+	err := preload.Fetch(fetching, s.store, s.cfg.PreloadJobs, names)
+	var failed *preload.Failure
+	switch {
+	case err == nil:
+		s.log.Info("preloaded", "paths", len(names), "took", time.Since(began))
+		return nil
+	case s.preloading.Err() != nil:
+		return ErrStopped
+	case ctx.Err() != nil:
+		s.log.Info("preload given up", "paths", len(names), "took", time.Since(began))
+		return ctx.Err()
+	case errors.As(err, &failed):
+		s.log.Warn("preload failed", "path", failed.Path(), "error", failed.Err)
+	}
+	return err
 }
 
 // PreloadAddr returns the address the preload endpoint listens on. There
```

- [ ] **Step 10: Run it, and see it pass**

Run: `scripts/test-linux.sh ./sidecar/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 11: Commit**

```bash
git add sidecar/preload_test.go README.md sidecar/sidecar.go
git commit -m 'Preloading: a sidecar preloads by a call, which ends with its context and with the sidecar'
```

- [ ] **Step 12: Write the failing test: a fetch under a context that has ended says so**

Change `preload/preload_test.go`:

```diff
--- a/preload/preload_test.go
+++ b/preload/preload_test.go
@@ -397,3 +397,18 @@ func TestFetchEndsWithItsContext(t *testing.T) {
 		t.Errorf("%d fetches told to stop of %d begun, want 2 of 2", len(f.ended), len(f.asked))
 	}
 }
+
+// A context that has ended before the last name was begun is the error,
+// also when no fetch was there to report it: nothing was fetched, and
+// Fetch does not say that everything was.
+func TestFetchWithAContextThatHasEnded(t *testing.T) {
+	f := &fetcher{}
+	ctx, giveUp := context.WithCancel(context.Background())
+	giveUp()
+	if err := Fetch(ctx, f, 2, []string{"p0", "p1", "p2"}); !errors.Is(err, context.Canceled) {
+		t.Errorf("Fetch = %v, want the context's error", err)
+	}
+	if _, asked := f.now(); asked != 0 {
+		t.Errorf("%d fetches under a context that had ended", asked)
+	}
+}
```

- [ ] **Step 13: Run it, and see it fail**

Run: `go test ./preload/`

Expected: it fails, with

```text
preload_test.go:409: Fetch = <nil>, want the context's error
```

- [ ] **Step 14: Write the code: Fetch returns the error of a context that ended before every name was begun**

Change `preload/preload.go`:

```diff
--- a/preload/preload.go
+++ b/preload/preload.go
@@ -155,24 +155,29 @@ func Fetch(ctx context.Context, fetch Fetcher, jobs int, names []string) error {
 	if jobs < 1 {
 		jobs = DefaultJobs
 	}
-	group, ctx := errgroup.WithContext(ctx)
+	group, fetching := errgroup.WithContext(ctx)
 	group.SetLimit(jobs)
 	for _, name := range names {
 		// Go waits for a job to be free. One may have failed meanwhile.
-		if ctx.Err() != nil {
+		if fetching.Err() != nil {
 			break
 		}
 		group.Go(func() error {
-			if err := ctx.Err(); err != nil {
+			if err := fetching.Err(); err != nil {
 				return err
 			}
-			if _, err := fetch.Preload(ctx, name); err != nil {
+			if _, err := fetch.Preload(fetching, name); err != nil {
 				return &Failure{Name: name, Err: err}
 			}
 			return nil
 		})
 	}
-	return group.Wait()
+	if err := group.Wait(); err != nil {
+		return err
+	}
+	// No fetch failed. If ctx ended meanwhile, not every name was begun:
+	// that nothing went wrong does not make it true that all are there.
+	return ctx.Err()
 }
 
 // parse returns the names of the store paths a list holds, each once, in
```

- [ ] **Step 15: Run it, and see it pass**

Run: `go test ./preload/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 16: Commit**

```bash
git add preload/preload_test.go preload/preload.go
git commit -m 'Preloading: a fetch under a context that has ended says so'
```

- [ ] **Step 17: The whole, in that repository**

Still in jaccard-nfs-nix-store, with its own Go (1.27):

```bash
test -z "$(gofmt -l .)" && go vet ./... && go test ./...
scripts/test-linux.sh
git status --short
```

Expected: nothing from `gofmt` and `go vet`. On a Mac `go test ./...` passes for the packages that build anywhere (`nfsd`, `mount`, `sidecar` and `e2e` are for Linux). That repository's `scripts/test-linux.sh` passes for all 11 packages that have tests, `e2e` among them; its container is privileged, which is that repository's own way and none of this plan's.

- [ ] **Step 18: The release: ask**

**Stop here.** Nothing is pushed or tagged without the human partner's word. Show them the branch:

```bash
git log --oneline v0.2.0..sidecar-preload
git diff --stat v0.2.0..sidecar-preload
```

and ask whether they push, open the pull request, merge and tag, or whether you are to. The release is that repository's usual one, a tag on its main branch after the merge:

```bash
git push -u origin sidecar-preload         # then the pull request, and its merge
git checkout main && git pull
git tag v0.3.0 && git push origin v0.3.0   # its release workflow builds the image and the notes
```

Write into the ledger what came of it: that v0.3.0 exists, or the commit of the branch that was pushed and that Task 10 is to require until it does. Tasks 2 to 9 need nothing of this and can go on meanwhile.
