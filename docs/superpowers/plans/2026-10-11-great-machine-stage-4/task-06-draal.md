### Task 6: draal serves the store

Here the parts meet. draal opens the store in its data directory, binds its endpoint for both protocols, answers the `store` request with its own endpoint ID, and serves the store's page under `/store/` of its own. Its command line loses `--jaccard-server` and `--jaccard-prefix` and gets the bucket and the settings of the store. From this task on a draal needs a bucket and no other server, and `great-machine rollout` pushes to draal.

**The order of draal's end is part of the task.** The web page first, as before; then `Serve` returns; then the store is stopped, which is when its requests have their grace, while their connections are still there; then the endpoint is closed; then the store's database; then draal's. In `runDraal` that is the order of the `defer`s, last one first.

**Files:**
- Modify: `web/web.go`, `cli/draal.go`, `cli/flags.go`, `draal/control.go`, `draal/draal.go`, `operator/client.go`, `wire/messages.go`, `ui/src/components/Layout.tsx`, `ui/src/styles/base.css`, `ui/vite.config.ts`, `push/push.go`, `scripts/test-page.sh`, `web/demo/main.go`, `cli/rollout.go`
- Test: `web/store_test.go`, `cli/commands_test.go`, `cli/draal_test.go`, `cli/fake_test.go`, `draal/control_test.go`, `draal/serve_test.go`, `e2e/harness_test.go`, `storeserver/storeserver_test.go`, `web/demo/main_test.go`, `push/jobs_test.go`

**Interfaces:**
- Consumes: `storeserver.Open`, `Config`, `ALPN`, `(*Store).ServeConn`, `Admin`, `Stop`, `Close` (Task 5); `node.ListenConfig.Handlers` (Task 4); `wire.StorePrefix`, `Controller.Store(ctx, service)` (Task 3).
- Produces:
  - `draal.Config.ID wire.ID`, in the place of `Config.Store`: the endpoint ID of draal, which `Store` answers with.
  - `web.Config.Store http.Handler`: served under `/store/` with that part of the path taken off; `/store` redirects to `/store/`; nil: both are a `not-found` in JSON, as an unknown path of the API is.
  - `great-machine draal`: `--s3-bucket` (required), `--s3-prefix`, `--s3-endpoint` (which has to be `http://` or `https://`, a host, and nothing but an address), `--s3-region`, `--s3-path-style`, `--store-scratch`, `--store-verify-jobs` (2), `--store-max-pack-size` (`16GiB`; bytes, or a number with `KiB`, `MiB` or `GiB`), `--store-no-verify`, each with the variable `GREAT_MACHINE_` and its name in capitals.
  - Two seams of `draalConfig` for tests, as `local` and `listenWeb` are: `objects server.Bucket` (the bucket itself) and `storeGrace time.Duration` (the grace of the store's requests when draal ends).
  - What a push says of where it goes: "the store", and no "jaccard-store server".
  - `push.MaxJobs = 32`: the most store paths pushed at a time. `push.Push` refuses more, and `rollout --jobs` with it. More than that made the push's own streams go beyond the 64 the store answers of a connection.
  - The page's header has a link, "Store", to `/store/`, which the page's router leaves to the browser (`rel="external"`).

- [ ] **Step 1: Write the failing test: the store's page under /store/ of draal's page**

Create `web/store_test.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

// echo is a store's page for the tests: it answers with the method and
// the path it was asked with.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
})

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
		if got := a.header.Values("Content-Security-Policy"); len(got) != 1 {
			t.Errorf("GET %s: there are %d policies, want one: %q", path, len(got), got)
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
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./web/`

Expected: it fails, with

```text
web/store_test.go:21:25: unknown field Store in struct literal of type Config
```

- [ ] **Step 3: Write the code: web.Config.Store, served under /store/**

Change `web/web.go`:

```diff
--- a/web/web.go
+++ b/web/web.go
@@ -23,6 +23,7 @@ import (
 	"time"
 
 	"github.com/fables-for-robots/great-machine/draal"
+	"github.com/fables-for-robots/great-machine/wire"
 )
 
 // embedded is the build of the page, as it was in web/dist when the
@@ -54,6 +55,10 @@ type Config struct {
 	Controller draal.Controller
 	// Dist is the build of the page. nil: the one that is embedded.
 	Dist fs.FS
+	// Store is the page of the store, served under /store/ and asked
+	// with that part of the path taken off: jaccard-store's admin page,
+	// which is read-only. nil: there is none.
+	Store http.Handler
 	// Each of these has a default when it is zero:
 	PingEvery time.Duration // how often an idle socket is pinged; 30 seconds
 	WriteWait time.Duration // how long one message to a browser may take; 10 seconds
@@ -131,10 +136,28 @@ func New(cfg Config) http.Handler {
 	h.mux.HandleFunc("/api/zathras/{id}/logs", h.get(h.logs))
 	h.mux.HandleFunc("/api/zathras/{id}/shell", h.get(h.shell))
 	h.mux.HandleFunc("/api/", h.noSuchPath)
+	// The page of the store asks for what it shows by paths beside its
+	// own, so it is asked for with the slash: without it those paths
+	// would be beside /store.
+	h.mux.HandleFunc("/store", h.store(http.RedirectHandler("/store/", http.StatusMovedPermanently)))
+	h.mux.HandleFunc("/store/", h.store(http.StripPrefix("/store", cfg.Store)))
 	h.mux.HandleFunc("/", h.page)
 	return h
 }
 
+// store returns next for a draal that has a page of the store, and a
+// refusal for one that has none: the page of the fleet, which answers
+// every path it does not know, is not the answer to /store.
+func (h *handler) store(next http.Handler) http.HandlerFunc {
+	return func(w http.ResponseWriter, r *http.Request) {
+		if h.cfg.Store == nil {
+			h.refuse(w, r, wire.Errorf(wire.CodeNotFound, "this draal has no page of the store"))
+			return
+		}
+		next.ServeHTTP(w, r)
+	}
+}
+
 // ServeHTTP answers a request, under whatever host name it was asked:
 // with the headers every answer has, and within the time an answer has.
 func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./web/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add web/store_test.go web/web.go
git commit -m 'Web: the store'\''s page under /store/ of draal'\''s page'
```

- [ ] **Step 6: Write the failing test: draal is the store: its flags, its own ID as the server of a push, the store's page, a push under way when it stops**

Change `cli/commands_test.go`:

```diff
--- a/cli/commands_test.go
+++ b/cli/commands_test.go
@@ -12,6 +12,7 @@ import (
 	"testing"
 	"time"
 
+	"github.com/amber-store/jaccard-store/bucket"
 	"github.com/fables-for-robots/great-machine/wire"
 	"github.com/urfave/cli/v2"
 )
@@ -74,20 +75,38 @@ func TestInvalidCommandLines(t *testing.T) {
 		{"id: an argument", nil, []string{"id", "now"}, `id takes no argument, got "now"`},
 
 		// draal.
-		{"draal: no --data", nil, []string{"draal", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, "--data is required: give it, or set GREAT_MACHINE_DATA"},
-		{"draal: no --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-prefix", "nix/"}, "--jaccard-server is required: give it, or set JACCARD_SERVER"},
-		{"draal: no --jaccard-prefix", nil, []string{"draal", "--data", "d", "--jaccard-server", idB}, "--jaccard-prefix is required: give it, or set JACCARD_PREFIX"},
-		{"draal: a bad --jaccard-server", nil, []string{"draal", "--data", "d", "--jaccard-server", "nonsense", "--jaccard-prefix", "nix/"}, `--jaccard-server: "nonsense" is not an endpoint ID`},
+		{"draal: no --data", nil, []string{"draal", "--s3-bucket", "packs"}, "--data is required: give it, or set GREAT_MACHINE_DATA"},
+		{"draal: no --s3-bucket", nil, []string{"draal", "--data", "d"}, "--s3-bucket is required: give it, or set GREAT_MACHINE_S3_BUCKET"},
+		// The flags of a draal of before, which was told where the store
+		// is: it is the store now.
+		{"draal: --jaccard-server, which is gone", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--jaccard-server", idB},
+			"flag provided but not defined: -jaccard-server"},
+		{"draal: --jaccard-prefix, which is gone", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--jaccard-prefix", "nix/"},
+			"flag provided but not defined: -jaccard-prefix"},
+		{"draal: an --s3-endpoint that is no URL", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--s3-endpoint", "minio:9000"},
+			`--s3-endpoint: "minio:9000" is not the URL of an S3 service`},
+		{"draal: the variable stands in for --s3-endpoint", map[string]string{"GREAT_MACHINE_S3_ENDPOINT": "s3.example.com"},
+			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--s3-endpoint: "s3.example.com" is not the URL of an S3 service`},
+		{"draal: a --store-max-pack-size that is no size", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "lots"},
+			`--store-max-pack-size: "lots" is not a size: want bytes, or a number with KiB, MiB or GiB`},
+		{"draal: a --store-max-pack-size of nothing", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "0"},
+			`--store-max-pack-size: "0" is not a size`},
+		{"draal: a --store-max-pack-size in a unit there is not", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "16GB"},
+			`--store-max-pack-size: "16GB" is not a size`},
+		{"draal: the variable stands in for --store-max-pack-size", map[string]string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE": "-1"},
+			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--store-max-pack-size: "-1" is not a size`},
+		{"draal: no --store-verify-jobs at all", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-verify-jobs", "0"},
+			"--store-verify-jobs: 0 is not a number of packs to verify at once: want 1 or more"},
 		{"draal: an operator that is no endpoint ID", nil,
-			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--operator", idA, "--operator", "alice"},
+			[]string{"draal", "--data", "d", "--s3-bucket", "packs", "--operator", idA, "--operator", "alice"},
 			`--operator: "alice" is not an endpoint ID`},
 		{"draal: the variable stands in for --operator", map[string]string{"GREAT_MACHINE_OPERATORS": idA + ",bob"},
-			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--operator: "bob" is not an endpoint ID`},
-		{"draal: a bad --bind", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--bind", "4433"}, `--bind: "4433" is not an address to bind`},
-		{"draal: a bad --web-addr", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "--web-addr", "8080"}, `--web-addr: "8080" is not an address to listen on`},
+			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--operator: "bob" is not an endpoint ID`},
+		{"draal: a bad --bind", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--bind", "4433"}, `--bind: "4433" is not an address to bind`},
+		{"draal: a bad --web-addr", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--web-addr", "8080"}, `--web-addr: "8080" is not an address to listen on`},
 		{"draal: the variable stands in for --web-addr", map[string]string{"GREAT_MACHINE_WEB_ADDR": "localhost"},
-			[]string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/"}, `--web-addr: "localhost" is not an address to listen on`},
-		{"draal: an argument", nil, []string{"draal", "--data", "d", "--jaccard-server", idB, "--jaccard-prefix", "nix/", "now"}, `draal takes no argument, got "now"`},
+			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--web-addr: "localhost" is not an address to listen on`},
+		{"draal: an argument", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "now"}, `draal takes no argument, got "now"`},
 
 		// zathras.
 		{"zathras: no --draal", nil, []string{"zathras", "--service", "web"}, "--draal is required: give it, or set GREAT_MACHINE_DRAAL"},
@@ -132,38 +151,90 @@ func TestReadDraal(t *testing.T) {
 		return err
 	}
 
+	// The least there is to say: where the data is, and the bucket. The
+	// store has its defaults.
+	read(t, draalCommand(), reader, "--data", "data", "--s3-bucket", "packs")
+	want := draalConfig{
+		data:    "data",
+		store:   storeConfig{bucket: bucket.Config{Bucket: "packs"}, verifyJobs: 2, maxPackBytes: 16 << 30},
+		webAddr: "127.0.0.1:8080",
+	}
+	if !reflect.DeepEqual(got, want) {
+		t.Errorf("the least: got %+v, want %+v", got, want)
+	}
+
 	// The variables alone. The operators have commas between them, and
 	// what a comma too many leaves empty is passed over.
 	t.Setenv("GREAT_MACHINE_DATA", "/var/lib/draal")
 	t.Setenv("GREAT_MACHINE_OPERATORS", idA+", "+idB+",")
-	t.Setenv("JACCARD_SERVER", idC)
-	t.Setenv("JACCARD_PREFIX", "nix/")
+	t.Setenv("GREAT_MACHINE_S3_BUCKET", "packs")
+	t.Setenv("GREAT_MACHINE_S3_PREFIX", "great-machine")
+	t.Setenv("GREAT_MACHINE_S3_ENDPOINT", "http://rustfs.storage:9000")
+	t.Setenv("GREAT_MACHINE_S3_REGION", "eu-central-1")
+	t.Setenv("GREAT_MACHINE_S3_PATH_STYLE", "true")
+	t.Setenv("GREAT_MACHINE_STORE_SCRATCH", "/scratch")
+	t.Setenv("GREAT_MACHINE_STORE_VERIFY_JOBS", "4")
+	t.Setenv("GREAT_MACHINE_STORE_MAX_PACK_SIZE", "64MiB")
+	t.Setenv("GREAT_MACHINE_STORE_NO_VERIFY", "true")
 	read(t, draalCommand(), reader)
-	want := draalConfig{
+	want = draalConfig{
 		data:      "/var/lib/draal",
 		operators: []wire.ID{wire.ID(idA), wire.ID(idB)},
-		store:     wire.StoreInfo{Server: wire.ID(idC), Prefix: "nix/"},
-		webAddr:   "127.0.0.1:8080",
+		store: storeConfig{
+			bucket:       bucket.Config{Bucket: "packs", Prefix: "great-machine", Endpoint: "http://rustfs.storage:9000", Region: "eu-central-1", PathStyle: true},
+			scratch:      "/scratch",
+			verifyJobs:   4,
+			maxPackBytes: 64 << 20,
+			noVerify:     true,
+		},
+		webAddr: "127.0.0.1:8080",
 	}
 	if !reflect.DeepEqual(got, want) {
 		t.Errorf("from the variables: got %+v, want %+v", got, want)
 	}
 
 	// A flag wins over its variable, and --operator may be repeated.
-	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA,
-		"--jaccard-server", idA, "--jaccard-prefix", "store/", "--bind", "0.0.0.0:4433")
+	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA, "--bind", "0.0.0.0:4433",
+		"--s3-bucket", "other", "--s3-prefix", "", "--s3-endpoint", "https://s3.example.com", "--s3-region", "auto", "--s3-path-style=false",
+		"--store-scratch", "scratch", "--store-verify-jobs", "1", "--store-max-pack-size", "1073741824", "--store-no-verify=false")
 	want = draalConfig{
 		data:      "data",
 		operators: []wire.ID{wire.ID(idC), wire.ID(idA)},
-		store:     wire.StoreInfo{Server: wire.ID(idA), Prefix: "store/"},
-		webAddr:   "127.0.0.1:8080",
-		bind:      netip.MustParseAddrPort("0.0.0.0:4433"),
+		store: storeConfig{
+			bucket:       bucket.Config{Bucket: "other", Endpoint: "https://s3.example.com", Region: "auto"},
+			scratch:      "scratch",
+			verifyJobs:   1,
+			maxPackBytes: 1 << 30,
+		},
+		webAddr: "127.0.0.1:8080",
+		bind:    netip.MustParseAddrPort("0.0.0.0:4433"),
 	}
 	if !reflect.DeepEqual(got, want) {
 		t.Errorf("from the flags: got %+v, want %+v", got, want)
 	}
 }
 
+// A size is bytes, or a number with a unit of 1024.
+func TestParseSize(t *testing.T) {
+	for text, want := range map[string]int64{
+		"1":       1,
+		"4096":    4096,
+		"5KiB":    5 << 10,
+		"64MiB":   64 << 20,
+		"16GiB":   16 << 30,
+		" 2 GiB ": 2 << 30,
+	} {
+		if got, err := parseSize(text); err != nil || got != want {
+			t.Errorf("parseSize(%q) = %d, %v; want %d", text, got, err, want)
+		}
+	}
+	for _, text := range []string{"", "0", "-1", "lots", "16GB", "1.5GiB", "GiB", "9223372036854775807GiB", "1TiB"} {
+		if got, err := parseSize(text); err == nil {
+			t.Errorf("parseSize(%q) = %d, want an error", text, got)
+		}
+	}
+}
+
 // TestReadDraalWebAddr: --web-addr has a default, its variable stands in
 // for it, the flag wins, and an empty value is no web page.
 func TestReadDraalWebAddr(t *testing.T) {
@@ -173,7 +244,7 @@ func TestReadDraalWebAddr(t *testing.T) {
 		got, err = readDraal(c)
 		return err
 	}
-	args := []string{"--data", "d", "--jaccard-server", idA, "--jaccard-prefix", "nix/"}
+	args := []string{"--data", "d", "--s3-bucket", "packs"}
 	with := func(more ...string) []string { return append(append([]string{}, args...), more...) }
 
 	read(t, draalCommand(), reader, args...)
```

Change `cli/draal_test.go`:

```diff
--- a/cli/draal_test.go
+++ b/cli/draal_test.go
@@ -7,23 +7,32 @@ import (
 	"net"
 	"net/http"
 	"net/netip"
+	"os"
+	"path/filepath"
 	"regexp"
 	"strings"
 	"testing"
 	"time"
 
+	"github.com/amber-store/jaccard-store/bucket/buckettest"
+	jsserver "github.com/amber-store/jaccard-store/server"
 	"github.com/coder/websocket"
+	"github.com/fables-for-robots/great-machine/node"
+	"github.com/fables-for-robots/great-machine/operator"
+	"github.com/fables-for-robots/great-machine/push"
 	"github.com/fables-for-robots/great-machine/web"
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
 // draalForTest is the configuration of a draal whose iroh endpoint stays
-// on this machine.
+// on this machine, and whose store keeps its packs in a bucket in memory.
+// The bucket sets the environment of the test, so a test that runs draal
+// cannot be parallel.
 func draalForTest(t *testing.T, webAddr string) draalConfig {
 	t.Helper()
 	return draalConfig{
 		data:    t.TempDir(),
-		store:   wire.StoreInfo{Server: wire.ID(idB), Prefix: "nix/"},
+		objects: buckettest.New(t),
 		webAddr: webAddr,
 		local:   true,
 	}
@@ -331,3 +340,214 @@ func TestRunDraalWebFails(t *testing.T) {
 		return true
 	})
 }
+
+// served returns the endpoint ID of a draal that runs and the address its
+// endpoint is bound to, as its log has them (servingInLog).
+func served(t *testing.T, logged *buffer) (wire.ID, netip.AddrPort) {
+	t.Helper()
+	m := servingInLog.FindStringSubmatch(logged.String())
+	if m == nil {
+		t.Fatalf("draal did not log what it serves:\n%s", logged.String())
+	}
+	addr, err := netip.ParseAddrPort(m[2])
+	if err != nil {
+		t.Fatalf("the address in the log, %q: %v", m[2], err)
+	}
+	return wire.ID(m[1]), addr
+}
+
+// webStorePath makes a directory as a store path is one, with a program
+// in it, and returns its path.
+func webStorePath(t *testing.T) string {
+	t.Helper()
+	dir := filepath.Join(t.TempDir(), "abc-web")
+	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
+		t.Fatal(err)
+	}
+	if err := os.WriteFile(filepath.Join(dir, "bin", "web"), []byte("the program of web"), 0o755); err != nil {
+		t.Fatal(err)
+	}
+	return dir
+}
+
+// TestRunDraalServesTheStore: draal is the store. Asked where the
+// closures of a service go, it names itself and the prefix of the
+// service; a store path pushed there with an operator's key is in the
+// store, whose page is a part of draal's; and the database of the store
+// is beside draal's own.
+func TestRunDraalServesTheStore(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	defer cancel()
+	opKey, err := node.NewKey()
+	if err != nil {
+		t.Fatal(err)
+	}
+	cfg := draalForTest(t, "127.0.0.1:0")
+	cfg.operators = []wire.ID{opKey.ID()}
+	var logged buffer
+	url, done := startDraal(t, ctx, cfg, &logged)
+	id, addr := served(t, &logged)
+
+	asking, stop := context.WithTimeout(ctx, 20*time.Second)
+	defer stop()
+	conn, err := node.Dial(asking, node.DialConfig{Key: opKey, Server: id, Addrs: []netip.AddrPort{addr}})
+	if err != nil {
+		t.Fatalf("dialing draal: %v", err)
+	}
+	client := operator.New(conn)
+	store, err := client.Store(asking, "web")
+	// The key of the operator has one endpoint at a time, as in rollout.
+	client.Close()
+	if want := (wire.StoreInfo{Server: id, Prefix: "/services/web/nix/store/"}); err != nil || store != want {
+		t.Fatalf("Store = %+v, %v; want draal itself and the prefix of the service, %+v", store, err, want)
+	}
+
+	res, err := push.Push(asking, push.Config{
+		Key:     opKey,
+		Server:  store.Server,
+		Addrs:   []netip.AddrPort{addr},
+		Prefix:  store.Prefix,
+		TempDir: t.TempDir(),
+	}, []string{webStorePath(t)})
+	if err != nil || res != (push.Result{Pushed: 1}) {
+		t.Fatalf("Push to draal = %+v, %v; want one store path pushed", res, err)
+	}
+
+	get := func(path string) (int, string) {
+		t.Helper()
+		client := &http.Client{Timeout: 5 * time.Second}
+		defer client.CloseIdleConnections()
+		resp, err := client.Get(url + path)
+		if err != nil {
+			t.Fatalf("GET %s: %v", path, err)
+		}
+		defer resp.Body.Close()
+		body, _ := io.ReadAll(resp.Body)
+		return resp.StatusCode, string(body)
+	}
+	if status, body := get("store/api/refs"); status != http.StatusOK ||
+		!strings.Contains(body, "/services/web/nix/store/abc-web") || !strings.Contains(body, string(opKey.ID())) {
+		t.Errorf("GET store/api/refs = %d %s, want the reference that was pushed and who pushed it", status, body)
+	}
+	// The page of the store, with the script and the stylesheet it asks
+	// for beside itself.
+	if status, body := get("store/"); status != http.StatusOK || !strings.Contains(body, "jaccard-store") {
+		t.Errorf("GET store/ = %d %q, want the page of the store", status, body)
+	}
+	for _, file := range []string{"store/app.js", "store/style.css"} {
+		if status, _ := get(file); status != http.StatusOK {
+			t.Errorf("GET %s = %d, want 200", file, status)
+		}
+	}
+	for _, name := range []string{draalDBFile, "store.sqlite"} {
+		if _, err := os.Stat(filepath.Join(cfg.data, name)); err != nil {
+			t.Errorf("the data directory: %v", err)
+		}
+	}
+	if !strings.Contains(logged.String(), "operators=1") {
+		t.Errorf("the log does not say how many operators there are:\n%s", logged.String())
+	}
+
+	cancel()
+	select {
+	case err := <-done:
+		if err != nil {
+			t.Errorf("runDraal returned %v, want nil", err)
+		}
+	case <-time.After(20 * time.Second):
+		t.Fatalf("runDraal did not return when its context was done\n%s", logged.String())
+	}
+}
+
+// heldBucket is a bucket that signs no upload: whoever asks waits until
+// the request is ended.
+type heldBucket struct {
+	jsserver.Bucket
+	asked chan struct{} // gets a value for every upload that is asked for
+}
+
+func (b *heldBucket) PresignPut(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
+	b.asked <- struct{}{}
+	<-ctx.Done()
+	return "", ctx.Err()
+}
+
+// TestRunDraalStopsWhileAPushIsUnderWay: draal is told to end while a
+// store path is being pushed to it. The request under way has its grace
+// and is then ended: draal returns with no error, and the push with one.
+// Nothing waits for the other.
+func TestRunDraalStopsWhileAPushIsUnderWay(t *testing.T) {
+	ctx, cancel := context.WithCancel(context.Background())
+	defer cancel()
+	opKey, err := node.NewKey()
+	if err != nil {
+		t.Fatal(err)
+	}
+	cfg := draalForTest(t, "127.0.0.1:0")
+	cfg.operators = []wire.ID{opKey.ID()}
+	held := &heldBucket{Bucket: cfg.objects, asked: make(chan struct{}, 16)}
+	cfg.objects = held
+	cfg.storeGrace = 100 * time.Millisecond
+	var logged buffer
+	_, done := startDraal(t, ctx, cfg, &logged)
+	id, addr := served(t, &logged)
+
+	pushed := make(chan error, 1)
+	go func() {
+		_, err := push.Push(context.Background(), push.Config{
+			Key:     opKey,
+			Server:  id,
+			Addrs:   []netip.AddrPort{addr},
+			Prefix:  wire.StorePrefix("web"),
+			TempDir: t.TempDir(),
+		}, []string{webStorePath(t)})
+		pushed <- err
+	}()
+	select {
+	case <-held.asked:
+	case err := <-pushed:
+		t.Fatalf("the push ended before it was under way: %v", err)
+	case <-time.After(20 * time.Second):
+		t.Fatal("timed out waiting for the push to be under way")
+	}
+
+	cancel()
+	select {
+	case err := <-done:
+		if err != nil {
+			t.Errorf("runDraal returned %v, want nil", err)
+		}
+	case <-time.After(20 * time.Second):
+		t.Fatalf("runDraal did not return while a push was under way\n%s", logged.String())
+	}
+	select {
+	case err := <-pushed:
+		if err == nil {
+			t.Error("the push that draal ended reported no error")
+		}
+	case <-time.After(20 * time.Second):
+		t.Fatal("the push still waits for a draal that has stopped")
+	}
+	if !strings.Contains(logged.String(), "stopping the store with requests under way") {
+		t.Errorf("draal does not say that it stopped with a request under way:\n%s", logged.String())
+	}
+}
+
+// TestRunDraalStoreFails: a store that cannot be opened ends the command
+// before anything is bound.
+func TestRunDraalStoreFails(t *testing.T) {
+	cfg := draalForTest(t, "127.0.0.1:0")
+	// A file where the scratch directory of the store is to be.
+	cfg.store.scratch = filepath.Join(t.TempDir(), "scratch")
+	if err := os.WriteFile(cfg.store.scratch, nil, 0o600); err != nil {
+		t.Fatal(err)
+	}
+	var logged buffer
+	err := runDraal(context.Background(), cfg, quietLog(&logged))
+	if err == nil || !strings.Contains(err.Error(), "the store: ") {
+		t.Fatalf("runDraal returned %v, want an error of the store", err)
+	}
+	if strings.Contains(logged.String(), "msg=serving") {
+		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
+	}
+}
```

Change `cli/fake_test.go`:

```diff
--- a/cli/fake_test.go
+++ b/cli/fake_test.go
@@ -187,7 +187,9 @@ var (
 
 // variables are the environment variables the commands read.
 var variables = []string{
-	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "JACCARD_SERVER", "JACCARD_PREFIX", "GREAT_MACHINE_BIND", "GREAT_MACHINE_WEB_ADDR",
+	"GREAT_MACHINE_DATA", "GREAT_MACHINE_OPERATORS", "GREAT_MACHINE_BIND", "GREAT_MACHINE_WEB_ADDR",
+	"GREAT_MACHINE_S3_BUCKET", "GREAT_MACHINE_S3_PREFIX", "GREAT_MACHINE_S3_ENDPOINT", "GREAT_MACHINE_S3_REGION", "GREAT_MACHINE_S3_PATH_STYLE",
+	"GREAT_MACHINE_STORE_SCRATCH", "GREAT_MACHINE_STORE_VERIFY_JOBS", "GREAT_MACHINE_STORE_MAX_PACK_SIZE", "GREAT_MACHINE_STORE_NO_VERIFY",
 	"GREAT_MACHINE_DRAAL", "GREAT_MACHINE_SERVICE", "GREAT_MACHINE_NAME", "GREAT_MACHINE_PRELOAD_URL",
 	"GREAT_MACHINE_SHELL", "GREAT_MACHINE_STOP_TIMEOUT", "GREAT_MACHINE_HEALTH_ADDR",
 	"GREAT_MACHINE_KEY", "GREAT_MACHINE_PARALLEL", "GREAT_MACHINE_DETACH", "GREAT_MACHINE_JOBS",
```

Change `draal/control_test.go`:

```diff
--- a/draal/control_test.go
+++ b/draal/control_test.go
@@ -55,7 +55,7 @@ func testConfig(t *testing.T) Config {
 	return Config{
 		DB:             openDB(t, filepath.Join(t.TempDir(), "draal.sqlite")),
 		Authz:          authz.NewAllowlist([]wire.ID{operatorID}),
-		Store:          wire.StoreInfo{Server: nodetest.ID(99), Prefix: "nix/"},
+		ID:             nodetest.ID(99),
 		Settle:         time.Millisecond,
 		StableAfter:    50 * time.Millisecond,
 		PrepareTimeout: time.Minute,
@@ -86,7 +86,8 @@ func TestStore(t *testing.T) {
 	// pushes before draal knows the service.
 	for _, service := range []string{"web", "never-rolled-out"} {
 		got, err := d.Store(context.Background(), service)
-		want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: "/services/" + service + "/nix/store/"}
+		// The store is draal itself: its own endpoint ID is the server.
+		want := wire.StoreInfo{Server: cfg.ID, Prefix: "/services/" + service + "/nix/store/"}
 		if err != nil || got != want {
 			t.Errorf("Store(%q) = %+v, %v; want %+v", service, got, err, want)
 		}
```

Change `draal/serve_test.go`:

```diff
--- a/draal/serve_test.go
+++ b/draal/serve_test.go
@@ -597,7 +597,7 @@ func TestProtocolStore(t *testing.T) {
 	w := startWorld(t, cfg)
 	op := w.dial(operatorID)
 	_, resp, err := ask(t, op, &wire.Request{Store: &wire.StoreRequest{Service: "web"}})
-	want := wire.StoreInfo{Server: cfg.Store.Server, Prefix: wire.StorePrefix("web")}
+	want := wire.StoreInfo{Server: cfg.ID, Prefix: wire.StorePrefix("web")}
 	if err != nil || resp.Store == nil || *resp.Store != want {
 		t.Fatalf("store of web = %+v, %v; want %+v", resp, err, want)
 	}
```

Change `e2e/harness_test.go`:

```diff
--- a/e2e/harness_test.go
+++ b/e2e/harness_test.go
@@ -152,7 +152,7 @@ func (h *harness) startDraal() {
 	d := draal.New(draal.Config{
 		DB:             database,
 		Authz:          authz.NewAllowlist([]wire.ID{h.opKey.ID()}),
-		Store:          wire.StoreInfo{Server: h.draalKey.ID(), Prefix: "test/"},
+		ID:             h.draalKey.ID(),
 		StoreDir:       h.storeDir,
 		Settle:         h.opts.settle,
 		StableAfter:    h.opts.stableAfter,
```

- [ ] **Step 7: Run it, and see it fail**

Run: `go test ./draal/ ./cli/ ./e2e/`

Expected: it fails, with

```text
draal/control_test.go:58:3: unknown field ID in struct literal of type Config
cli/commands_test.go:159:12: undefined: storeConfig
```

- [ ] **Step 8: Write the code: draal serves the store on its own endpoint: draal.Config.ID, the flags of the bucket and of the store, the store's page**

Change `cli/draal.go`:

```diff
--- a/cli/draal.go
+++ b/cli/draal.go
@@ -7,14 +7,19 @@ import (
 	"log/slog"
 	"net"
 	"net/netip"
+	"net/url"
 	"os"
 	"path/filepath"
 	"strings"
+	"time"
 
+	"github.com/amber-store/jaccard-store/bucket"
+	jsserver "github.com/amber-store/jaccard-store/server"
 	"github.com/fables-for-robots/great-machine/authz"
 	"github.com/fables-for-robots/great-machine/db"
 	"github.com/fables-for-robots/great-machine/draal"
 	"github.com/fables-for-robots/great-machine/node"
+	"github.com/fables-for-robots/great-machine/storeserver"
 	"github.com/fables-for-robots/great-machine/web"
 	"github.com/fables-for-robots/great-machine/wire"
 	"github.com/urfave/cli/v2"
@@ -32,16 +37,34 @@ func draalCommand() *cli.Command {
 		Name:  "draal",
 		Usage: "the controller: zathrases report to it, operators roll out through it",
 		Description: "The endpoint ID draal logs at its start is all that a zathras or an\n" +
-			"operator needs to reach it.",
+			"operator needs to reach it. draal is the store as well: closures are\n" +
+			"pushed to it and the pods fetch them from it, under the same endpoint\n" +
+			"ID, and their packs are kept in an S3 bucket. The credentials of the\n" +
+			"bucket are those the AWS SDK finds: AWS_ACCESS_KEY_ID and the rest, a\n" +
+			"profile, or a role.",
 		Flags: []cli.Flag{
 			&cli.StringFlag{Name: "data", EnvVars: []string{"GREAT_MACHINE_DATA"},
 				Usage: "`DIR`ectory of draal: its key (created on first run) and its database (required)"},
 			&cli.StringSliceFlag{Name: "operator", EnvVars: []string{"GREAT_MACHINE_OPERATORS"},
 				Usage: "endpoint `ID` that may act as an operator, as `great-machine id` prints it; may be repeated, and the variable takes several with commas between them"},
-			&cli.StringFlag{Name: "jaccard-server", EnvVars: []string{"JACCARD_SERVER"},
-				Usage: "endpoint `ID` of the jaccard-store server that closures are pushed to (required)"},
-			&cli.StringFlag{Name: "jaccard-prefix", EnvVars: []string{"JACCARD_PREFIX"},
-				Usage: "`PREFIX` put before the name of a store path to make its reference; the same value the sidecars are given (required)"},
+			&cli.StringFlag{Name: "s3-bucket", EnvVars: []string{"GREAT_MACHINE_S3_BUCKET"},
+				Usage: "`NAME` of the bucket the store keeps its packs in (required)"},
+			&cli.StringFlag{Name: "s3-prefix", EnvVars: []string{"GREAT_MACHINE_S3_PREFIX"},
+				Usage: "`PREFIX` of the name of every object in the bucket"},
+			&cli.StringFlag{Name: "s3-endpoint", EnvVars: []string{"GREAT_MACHINE_S3_ENDPOINT"},
+				Usage: "`URL` of an S3 service other than AWS; it is in the URLs that clients are given, so the laptop and the pods have to reach it under this name"},
+			&cli.StringFlag{Name: "s3-region", EnvVars: []string{"GREAT_MACHINE_S3_REGION"},
+				Usage: "`REGION` of the bucket (default: what the AWS configuration says)"},
+			&cli.BoolFlag{Name: "s3-path-style", EnvVars: []string{"GREAT_MACHINE_S3_PATH_STYLE"},
+				Usage: "address the bucket in the path of a URL, not in its host name"},
+			&cli.StringFlag{Name: "store-scratch", EnvVars: []string{"GREAT_MACHINE_STORE_SCRATCH"},
+				Usage: "`DIR` in which packs are verified; it is emptied at start (default: scratch in --data)"},
+			&cli.IntFlag{Name: "store-verify-jobs", EnvVars: []string{"GREAT_MACHINE_STORE_VERIFY_JOBS"}, Value: 2,
+				Usage: "packs verified at once; each needs scratch space for its uncompressed data"},
+			&cli.StringFlag{Name: "store-max-pack-size", EnvVars: []string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE"}, Value: "16GiB",
+				Usage: "largest uncompressed `SIZE` of a pack, in bytes or with KiB, MiB or GiB; the scratch space needed is this times --store-verify-jobs"},
+			&cli.BoolFlag{Name: "store-no-verify", EnvVars: []string{"GREAT_MACHINE_STORE_NO_VERIFY"},
+				Usage: "record a pack without reading its data: one that is incomplete or corrupt is then found by whoever pulls it"},
 			&cli.StringFlag{Name: "web-addr", EnvVars: []string{"GREAT_MACHINE_WEB_ADDR"}, Value: "127.0.0.1:8080",
 				Usage: "`ADDR`ess the web page listens on; empty for no web page"},
 			&cli.StringFlag{Name: "bind", EnvVars: []string{"GREAT_MACHINE_BIND"},
@@ -61,7 +84,7 @@ func draalCommand() *cli.Command {
 type draalConfig struct {
 	data      string
 	operators []wire.ID
-	store     wire.StoreInfo
+	store     storeConfig
 	webAddr   string         // "" for no web page
 	bind      netip.AddrPort // the zero value: every address, a port the system picks
 	// local is for tests: the iroh endpoint uses no relay, announces
@@ -71,6 +94,22 @@ type draalConfig struct {
 	// listenWeb is for tests: what binds the address of the web page.
 	// Nil means net.Listen. No flag sets it.
 	listenWeb func(network, addr string) (net.Listener, error)
+	// objects is for tests: the bucket of the store itself. Nil means
+	// the bucket the flags name. No flag sets it.
+	objects jsserver.Bucket
+	// storeGrace is for tests: how long the requests of the store that
+	// are under way have when draal ends. 0: the 10 seconds of the
+	// store. No flag sets it.
+	storeGrace time.Duration
+}
+
+// storeConfig is what the command line of draal says of the store.
+type storeConfig struct {
+	bucket       bucket.Config
+	scratch      string // "": the directory scratch in the data directory
+	verifyJobs   int
+	maxPackBytes int64
+	noVerify     bool
 }
 
 // readDraal reads the command line of draal.
@@ -95,14 +134,7 @@ func readDraal(c *cli.Context) (draalConfig, error) {
 		}
 		cfg.operators = append(cfg.operators, id)
 	}
-	server, err := required(c, "jaccard-server", "JACCARD_SERVER")
-	if err != nil {
-		return draalConfig{}, err
-	}
-	if cfg.store.Server, err = node.ParseID(server); err != nil {
-		return draalConfig{}, usageError("--jaccard-server: %q is not an endpoint ID: %w", server, err)
-	}
-	if cfg.store.Prefix, err = required(c, "jaccard-prefix", "JACCARD_PREFIX"); err != nil {
+	if cfg.store, err = readStore(c); err != nil {
 		return draalConfig{}, err
 	}
 	if cfg.webAddr = strings.TrimSpace(c.String("web-addr")); cfg.webAddr != "" {
@@ -118,6 +150,39 @@ func readDraal(c *cli.Context) (draalConfig, error) {
 	return cfg, nil
 }
 
+// readStore reads what the command line of draal says of the store.
+func readStore(c *cli.Context) (storeConfig, error) {
+	cfg := storeConfig{
+		bucket: bucket.Config{
+			Prefix:    c.String("s3-prefix"),
+			Endpoint:  strings.TrimSpace(c.String("s3-endpoint")),
+			Region:    strings.TrimSpace(c.String("s3-region")),
+			PathStyle: c.Bool("s3-path-style"),
+		},
+		scratch:    c.String("store-scratch"),
+		verifyJobs: c.Int("store-verify-jobs"),
+		noVerify:   c.Bool("store-no-verify"),
+	}
+	var err error
+	if cfg.bucket.Bucket, err = required(c, "s3-bucket", "GREAT_MACHINE_S3_BUCKET"); err != nil {
+		return storeConfig{}, err
+	}
+	// The endpoint is in every URL a client is given. One without a
+	// scheme would be taken for a path, and found out by the first push.
+	if e := cfg.bucket.Endpoint; e != "" {
+		if u, err := url.Parse(e); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
+			return storeConfig{}, usageError("--s3-endpoint: %q is not the URL of an S3 service: want http://HOST[:PORT] or https://HOST[:PORT]", e)
+		}
+	}
+	if cfg.verifyJobs < 1 {
+		return storeConfig{}, usageError("--store-verify-jobs: %d is not a number of packs to verify at once: want 1 or more", cfg.verifyJobs)
+	}
+	if cfg.maxPackBytes, err = parseSize(c.String("store-max-pack-size")); err != nil {
+		return storeConfig{}, usageError("--store-max-pack-size: %w", err)
+	}
+	return cfg, nil
+}
+
 // runDraal is draal until ctx is done, which SIGINT and SIGTERM do. That
 // is how draal is meant to end, and no error.
 func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error) {
@@ -137,14 +202,52 @@ func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error
 	defer func() { err = errors.Join(err, database.Close()) }()
 
 	if len(cfg.operators) == 0 {
-		log.Warn("no --operator was given: nobody can act as an operator over iroh")
+		log.Warn("no --operator was given: nobody can act as an operator over iroh, and nobody can push to the store")
+	}
+	allow := authz.NewAllowlist(cfg.operators)
+
+	// The store is opened before the endpoint is bound: one that cannot
+	// be opened ends the command before draal has announced itself.
+	store, err := storeserver.Open(ctx, storeserver.Config{
+		Dir:          cfg.data,
+		Bucket:       cfg.store.bucket,
+		Objects:      cfg.objects,
+		Scratch:      cfg.store.scratch,
+		VerifyJobs:   cfg.store.verifyJobs,
+		MaxPackBytes: cfg.store.maxPackBytes,
+		NoVerify:     cfg.store.noVerify,
+		Authz:        allow,
+		StopGrace:    cfg.storeGrace,
+		Log:          log,
+	})
+	if err != nil {
+		return fmt.Errorf("the store: %w", err)
 	}
-	listener, err := node.Listen(ctx, node.ListenConfig{Key: key, Bind: cfg.bind, Local: cfg.local, Log: log})
+	// Deferred before the listener is, so that it happens after it: the
+	// database of the store is closed when its connections are.
+	defer func() { err = errors.Join(err, store.Close()) }()
+	if cfg.store.noVerify {
+		log.Warn("the store records packs without verifying them (--store-no-verify)")
+	}
+
+	// One endpoint for both protocols: the endpoint ID of draal is that
+	// of the store.
+	listener, err := node.Listen(ctx, node.ListenConfig{
+		Key:      key,
+		Bind:     cfg.bind,
+		Local:    cfg.local,
+		Handlers: map[string]func(node.Conn){storeserver.ALPN: store.ServeConn},
+		Log:      log,
+	})
 	if err != nil {
 		return fmt.Errorf("binding the iroh endpoint: %w", err)
 	}
 	defer func() { err = errors.Join(err, listener.Close()) }()
-	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators))
+	// Deferred behind the listener, so that it happens before it: the
+	// requests of the store that are under way have their grace while
+	// their connections are still there.
+	defer store.Stop()
+	log.Info("serving", "endpoint", string(listener.ID()), "addr", listener.LocalAddr().String(), "operators", len(cfg.operators), "bucket", cfg.store.bucket.Bucket)
 
 	// The web page listens before draal serves: an address that is taken
 	// ends the command here, with the endpoint and the database closed
@@ -162,22 +265,23 @@ func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error
 
 	d := draal.New(draal.Config{
 		DB:    database,
-		Authz: authz.NewAllowlist(cfg.operators),
-		Store: cfg.store,
+		Authz: allow,
+		ID:    listener.ID(),
 		Log:   log,
 	})
 
 	// The order of the end, once ctx is done: the web page is stopped
 	// first, its WebSockets with it, and only then does Serve get to
-	// know, through a context of its own. The endpoint and the database
-	// are closed after Serve has returned, as before.
+	// know, through a context of its own. When Serve has returned the
+	// store is stopped, the endpoint is closed, and the two databases
+	// are closed, the store's first.
 	serving, stopServing := context.WithCancel(context.WithoutCancel(ctx))
 	defer stopServing()
 	pageFailed := make(chan error, 1)
 	if pageListener == nil {
 		defer context.AfterFunc(ctx, stopServing)()
 	} else {
-		page := startWeb(pageListener, web.New(web.Config{Controller: d, Log: log}), webStopWait, log)
+		page := startWeb(pageListener, web.New(web.Config{Controller: d, Store: store.Admin(), Log: log}), webStopWait, log)
 		// For the ways out of this function that are not the end of ctx.
 		defer page.Stop()
 		announceWeb(log, cfg.webAddr, pageListener.Addr(), web.IsLoopback(cfg.webAddr), web.Built())
```

Change `cli/flags.go`:

```diff
--- a/cli/flags.go
+++ b/cli/flags.go
@@ -1,6 +1,8 @@
 package cli
 
 import (
+	"fmt"
+	"strconv"
 	"strings"
 
 	"github.com/fables-for-robots/great-machine/node"
@@ -108,3 +110,25 @@ func service(name string) (string, error) {
 	}
 	return name, nil
 }
+
+// parseSize reads a count of bytes: a number, or one with the unit KiB,
+// MiB or GiB, as jaccard-stored reads one.
+func parseSize(text string) (int64, error) {
+	units := []struct {
+		suffix string
+		factor int64
+	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}}
+	factor := int64(1)
+	digits := strings.TrimSpace(text)
+	for _, u := range units {
+		if rest, ok := strings.CutSuffix(digits, u.suffix); ok {
+			digits, factor = strings.TrimSpace(rest), u.factor
+			break
+		}
+	}
+	n, err := strconv.ParseInt(digits, 10, 64)
+	if err != nil || n <= 0 || n > (1<<62)/factor {
+		return 0, fmt.Errorf("%q is not a size: want bytes, or a number with KiB, MiB or GiB", text)
+	}
+	return n * factor, nil
+}
```

Change `draal/control.go`:

```diff
--- a/draal/control.go
+++ b/draal/control.go
@@ -14,14 +14,15 @@ import (
 // rolloutsListed is how many rollouts of a service Rollouts returns.
 const rolloutsListed = 20
 
-// Store returns the jaccard-store server draal was given and the prefix
-// of the service. A service draal has not heard of has a prefix like any
-// other: the first rollout of a service pushes before draal knows it.
+// Store returns draal's own endpoint ID, which is the store's, and the
+// prefix of the service. A service draal has not heard of has a prefix
+// like any other: the first rollout of a service pushes before draal
+// knows it.
 func (d *Draal) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
 	if err := wire.ValidService(service); err != nil {
 		return wire.StoreInfo{}, err
 	}
-	return wire.StoreInfo{Server: d.cfg.Store.Server, Prefix: wire.StorePrefix(service)}, nil
+	return wire.StoreInfo{Server: d.cfg.ID, Prefix: wire.StorePrefix(service)}, nil
 }
 
 // ServiceOf returns the service of a connected zathras.
```

Change `draal/draal.go`:

```diff
--- a/draal/draal.go
+++ b/draal/draal.go
@@ -20,9 +20,10 @@ import (
 type Config struct {
 	DB    *db.DB
 	Authz authz.Authorizer
-	// Store names the jaccard-store server. Its prefix is not looked at:
-	// every service has a prefix of its own, wire.StorePrefix.
-	Store wire.StoreInfo
+	// ID is the endpoint ID of draal. It is that of the store as well,
+	// which draal answers on the same endpoint: what the store request
+	// of an operator is answered with.
+	ID wire.ID
 	// StoreDir is the directory a rollout's binary has to be in. "":
 	// /nix/store. The tests of the whole point it at a directory they can
 	// write to.
@@ -118,9 +119,10 @@ type Shell interface {
 // Controller is what an operator can do. The handler of the protocol and,
 // in stage 2, the HTTP API are written against it.
 type Controller interface {
-	// Store returns where the closures of a service are pushed to: the
-	// jaccard-store server and the prefix of the service. A name that is
-	// no name of a service is a bad-request.
+	// Store returns where the closures of a service are pushed to:
+	// draal's own endpoint ID, on which it answers jaccard-store's
+	// protocol, and the prefix of the service. A name that is no name of
+	// a service is a bad-request.
 	Store(ctx context.Context, service string) (wire.StoreInfo, error)
 	// Rollout records a new generation of a service and the rollout to it.
 	Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error)
```

Change `operator/client.go`:

```diff
--- a/operator/client.go
+++ b/operator/client.go
@@ -142,7 +142,8 @@ func unexpected(what string) error {
 }
 
 // Store asks where the closures of a service are to be pushed: for the
-// jaccard-store server and the prefix of the service.
+// endpoint ID of the store, which is draal's own, and the prefix of the
+// service.
 func (c *Client) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
 	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{Service: service}})
 	if err != nil {
```

Change `wire/messages.go`:

```diff
--- a/wire/messages.go
+++ b/wire/messages.go
@@ -184,9 +184,10 @@ type StoreRequest struct {
 	Service string `json:"service"`
 }
 
-// StoreInfo is where the closures of a service are pushed to: the
-// jaccard-store server, and the prefix of the references a push makes
-// there, which is the service's own (StorePrefix).
+// StoreInfo is where the closures of a service are pushed to: the server,
+// which is draal itself, answering jaccard-store's protocol under its own
+// endpoint ID, and the prefix of the references a push makes there, which
+// is the service's own (StorePrefix).
 type StoreInfo struct {
 	Server ID     `json:"server"`
 	Prefix string `json:"prefix"`
```

- [ ] **Step 9: Run it, and see it pass**

Run: `go test ./draal/ ./cli/ ./e2e/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 10: Commit**

```bash
git add cli/commands_test.go cli/draal_test.go cli/fake_test.go draal/control_test.go draal/serve_test.go e2e/harness_test.go cli/draal.go cli/flags.go draal/control.go draal/draal.go operator/client.go wire/messages.go
git commit -m 'Draal: draal is the store: its flags, its own ID as the server of a push, the store'\''s page, a push under way when it stops'
```

- [ ] **Step 11: A test that holds as it is written: an answer of the store's page has one content security policy**

Change `cli/draal_test.go`:

```diff
--- a/cli/draal_test.go
+++ b/cli/draal_test.go
@@ -423,6 +423,12 @@ func TestRunDraalServesTheStore(t *testing.T) {
 		}
 		defer resp.Body.Close()
 		body, _ := io.ReadAll(resp.Body)
+		// The page of the store has a policy of its own, draal's page
+		// another: an answer has one of them, never both, which a
+		// browser would hold it to at once.
+		if policies := resp.Header.Values("Content-Security-Policy"); len(policies) != 1 || !strings.Contains(policies[0], "default-src 'self'") {
+			t.Errorf("GET %s: the policies are %q, want one that allows the page's own origin alone", path, policies)
+		}
 		return resp.StatusCode, string(body)
 	}
 	if status, body := get("store/api/refs"); status != http.StatusOK ||
```

- [ ] **Step 12: Run it, and see it pass**

Run: `go test ./cli/ -run TestRunDraalServesTheStore`

Expected: it passes: jaccard-store's page sets its own policy in the place of draal's, and what it shows keeps draal's.

- [ ] **Step 13: Commit**

```bash
git add cli/draal_test.go
git commit -m 'CLI: an answer of the store'\''s page has one content security policy'
```

- [ ] **Step 14: Write the code: a link to the store's page in the header of the page**

No unit test: the page's tests are of what has no part in the document.

Change `ui/src/components/Layout.tsx`:

```diff
--- a/ui/src/components/Layout.tsx
+++ b/ui/src/components/Layout.tsx
@@ -43,8 +43,9 @@ function crumbsOf(path: string): Crumb[] {
 const WORDS = { connecting: "Connecting", live: "Live", lost: "Lost" } as const;
 
 // The frame of every view: the header with the name of the program, the
-// path as breadcrumbs and the state of the connection; what there is to
-// say while the connection is lost; and the view.
+// path as breadcrumbs, the way to the page of the store and the state of
+// the connection; what there is to say while the connection is lost; and
+// the view.
 export default function Layout(props: RouteSectionProps) {
   const location = useLocation();
   const crumbs = createMemo(() => crumbsOf(location.pathname));
@@ -88,6 +89,18 @@ export default function Layout(props: RouteSectionProps) {
               </For>
             </ol>
           </nav>
+          {/* The page of the store is jaccard-store's own, which draal
+              serves under /store/: a page to load, and none of the
+              views of this one. rel="external" keeps the router from
+              taking the link for a path of its own. */}
+          <a
+            class="store"
+            href="/store/"
+            rel="external"
+            title="The store: the closures that were pushed, and what sharing and compression save"
+          >
+            Store
+          </a>
           <span class="link" data-state={connection()} role="status" title="The connection of this page to draal">
             {WORDS[connection()]}
           </span>
```

Change `ui/src/styles/base.css`:

```diff
--- a/ui/src/styles/base.css
+++ b/ui/src/styles/base.css
@@ -176,6 +176,18 @@ button {
   font-weight: 600;
 }
 
+/* The way to the page of the store, which is a page of its own. */
+.store {
+  color: var(--ffr-ink-100);
+  font-size: 13px;
+  font-weight: 500;
+  white-space: nowrap;
+}
+
+.store:hover {
+  color: var(--ffr-white);
+}
+
 .link {
   display: inline-flex;
   align-items: center;
@@ -478,7 +490,7 @@ h2 {
     flex-basis: 100%;
   }
 
-  .link {
+  .store {
     margin-left: auto;
   }
 
```

Change `ui/vite.config.ts`:

```diff
--- a/ui/vite.config.ts
+++ b/ui/vite.config.ts
@@ -73,8 +73,10 @@ export default defineConfig({
     // passed on with the Host it came with (no changeOrigin): the API
     // refuses a WebSocket whose Origin is not its Host, and the Origin a
     // browser sends is this server.
+    // The page of the store is draal's to serve as well.
     proxy: {
       "/api": { target: "http://127.0.0.1:8080", ws: true },
+      "/store": { target: "http://127.0.0.1:8080" },
     },
   },
 });
```

- [ ] **Step 15: Run it, and see it pass**

Run: `cd ui && npm ci && npm run check && npm test && npm run build; go test -count=1 ./web/... ./cli/ ./e2e/`

Expected: 116 tests of the page; the Go packages with the page built.

- [ ] **Step 16: Commit**

```bash
git add ui/src/components/Layout.tsx ui/src/styles/base.css ui/vite.config.ts
git commit -m 'Page: a link to the store'\''s page in the header of the page'
```

- [ ] **Step 17: Write the failing test: what a push says when the store refuses it, or does not answer**

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -448,8 +448,22 @@ func TestAPushByNoOperatorSaysSo(t *testing.T) {
 		Prefix:  wire.StorePrefix("web"),
 		TempDir: t.TempDir(),
 	}, []string{dir})
-	if err == nil || !strings.Contains(err.Error(), "unauthorized: list is for operators") {
-		t.Fatalf("Push as a stranger: %v, want it to say unauthorized: list is for operators", err)
+	if want := `asking the store for the reference "/services/web/nix/store/abc-web": list: bad_request: unauthorized: list is for operators`; err == nil || err.Error() != want {
+		t.Fatalf("Push as a stranger: %v\nwant: %s", err, want)
+	}
+	// And when nothing answers under the ID it was given: the store is
+	// what it names, with what the dial said.
+	dialing, stop := context.WithTimeout(ctx, 2*time.Second)
+	defer stop()
+	_, err = push.Push(dialing, push.Config{
+		Key:     newKey(t),
+		Server:  newKey(t).ID(),
+		Addrs:   []netip.AddrPort{w.listener.LocalAddr()},
+		Prefix:  wire.StorePrefix("web"),
+		TempDir: t.TempDir(),
+	}, []string{dir})
+	if err == nil || !strings.HasPrefix(err.Error(), "the store: ") {
+		t.Fatalf("Push to an ID nothing answers under: %v, want an error of the store", err)
 	}
 	// With the operator's key the same push goes through, to the same
 	// endpoint.
```

- [ ] **Step 18: Run it, and see it fail**

Run: `go test ./storeserver/ -run TestAPushByNoOperatorSaysSo`

Expected: it fails, with

```text
storeserver_test.go:443: Push as a stranger: asking the jaccard-store server for the reference ...
```

- [ ] **Step 19: Write the code: the push names the store, which is draal, and no jaccard-store server**

Change `push/push.go`:

```diff
--- a/push/push.go
+++ b/push/push.go
@@ -1,9 +1,10 @@
-// Package push takes store paths to a jaccard-store server: every store
-// path becomes the reference prefix+name there, which the sidecar of a pod
-// serves as /nix/store/<name>.
+// Package push takes store paths to the store, which draal is, answering
+// jaccard-store's protocol under its own endpoint ID: every store path
+// becomes the reference prefix+name there, which a pod of the service has
+// as /nix/store/<name>.
 //
 // A store path never changes under its name, so one whose reference the
-// server has is not pushed again. Every other one is imported into a
+// store has is not pushed again. Every other one is imported into a
 // temporary store of its own, as `jaccard-store push-dir` imports a
 // directory, and pushed with jaccard-store's client.
 package push
@@ -46,7 +47,7 @@ const (
 // Config says where store paths are pushed to, and how.
 type Config struct {
 	Key      node.Key         // the operator's
-	Server   wire.ID          // the jaccard-store server
+	Server   wire.ID          // the store: the endpoint ID of draal
 	Addrs    []netip.AddrPort // for tests: where the server is
 	Prefix   string
 	Jobs     int      // 0: 20
@@ -87,14 +88,14 @@ type item struct {
 	ref  string
 }
 
-// Push makes the server hold the reference Prefix+name for every store
+// Push makes the store hold the reference Prefix+name for every store
 // path. It ends at the first path that fails, with that path in the error.
 //
 // The name of a store path is its last element. A store path may be a
 // directory or a single regular file, which becomes a reference whose root
 // is the file. One that is a symbolic link is refused before anything is
-// pushed: the root of a reference cannot be a link, so the sidecar could
-// not serve it as one.
+// pushed: the root of a reference cannot be a link, so a pod could not
+// be given it as one.
 //
 // The Result counts what was done until Push returned, also when it
 // returns an error.
@@ -133,7 +134,7 @@ func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error)
 
 	server, err := irohkey.ParseEndpointID(string(cfg.Server))
 	if err != nil {
-		return Result{}, fmt.Errorf("the jaccard-store server %q: %w", cfg.Server, err)
+		return Result{}, fmt.Errorf("the store %q: %w", cfg.Server, err)
 	}
 	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
 		Key:    cfg.Key.Iroh(),
@@ -142,7 +143,7 @@ func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error)
 		Addrs:  cfg.Addrs,
 	})
 	if err != nil {
-		return Result{}, fmt.Errorf("the jaccard-store server: %w", err)
+		return Result{}, fmt.Errorf("the store: %w", err)
 	}
 	defer conn.Close()
 	c := client.New(conn, nil)
@@ -212,7 +213,7 @@ func absent(ctx context.Context, c *client.Client, items []item) ([]item, error)
 			}
 			refs, err := c.List(gctx, it.ref)
 			if err != nil {
-				return fmt.Errorf("asking the jaccard-store server for the reference %q: %w", it.ref, err)
+				return fmt.Errorf("asking the store for the reference %q: %w", it.ref, err)
 			}
 			// The listing is of every name that begins with this one.
 			for _, r := range refs {
```

- [ ] **Step 20: Run it, and see it pass**

Run: `go test ./storeserver/ -run TestAPushByNoOperatorSaysSo`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 21: Commit**

```bash
git add storeserver/storeserver_test.go push/push.go
git commit -m 'Draal: what a push says when the store refuses it, or does not answer'
```

- [ ] **Step 22: Write the code: scripts/test-page.sh starts its draal with a bucket, and asks for the store's page as well**

The script is what CI asks the program that Nix built, and the image, for the page with. Its draal was started with the two flags that are gone. A draal asks its bucket nothing until something is pushed, so the name of a bucket and a region are all it takes, and no credentials.

Change `scripts/test-page.sh`:

```diff
--- a/scripts/test-page.sh
+++ b/scripts/test-page.sh
@@ -11,8 +11,11 @@
 # dials iroh's relays as every draal does.
 set -eu
 
-# A jaccard-store server that is never dialed: draal only hands it on.
-server=0000000000000000000000000000000000000000000000000000000000000000
+# A bucket that is never asked anything: draal asks its bucket nothing
+# until something is pushed, so the name of one, and a region to sign
+# URLs for, is all it takes, and no credentials.
+bucket=great-machine-test-page
+region=us-east-1
 
 if [ "${1:-}" = --image ]; then
 	image=${2:?usage: test-page.sh --image IMAGE}
@@ -67,10 +70,10 @@ if [ -n "$image" ]; then
 	# loopback address of the container, where only the container reaches
 	# it. That is why it is asked from inside, with busybox's wget.
 	docker run -d --name "$name" --tmpfs /nix/store "$image" \
-		draal --data /tmp/draal --jaccard-server "$server" --jaccard-prefix nix/ >/dev/null
+		draal --data /tmp/draal --s3-bucket "$bucket" --s3-region "$region" >/dev/null
 else
 	# Port 0 is a port the system picks; draal logs the one it got.
-	"$program" draal --data "$tmp/data" --jaccard-server "$server" --jaccard-prefix nix/ \
+	"$program" draal --data "$tmp/data" --s3-bucket "$bucket" --s3-region "$region" \
 		--web-addr 127.0.0.1:0 2>"$tmp/log" &
 	pid=$!
 fi
@@ -94,5 +97,8 @@ echo "$page" | grep -qi '<!doctype html>' || fail "GET $url is no page: $page"
 # A path that is no file is the page too: its routes are the browser's.
 page=$(get "${url}s/web") || fail "GET ${url}s/web failed"
 echo "$page" | grep -qi '<!doctype html>' || fail "GET ${url}s/web is no page: $page"
+# The page of the store is jaccard-store's own, and is in the program too.
+page=$(get "${url}store/") || fail "GET ${url}store/ failed"
+echo "$page" | grep -qi 'jaccard-store' || fail "GET ${url}store/ is not the page of the store: $page"
 
 echo ok
```

- [ ] **Step 23: Run it, and see it pass**

Run: `(cd ui && npm ci && npm run build) && go build -o /tmp/great-machine-page ./cmd/great-machine && scripts/test-page.sh /tmp/great-machine-page; rm -f /tmp/great-machine-page; git clean -fdxq web/dist ui`

Expected: `ok`, and `git status` shows nothing left behind.

- [ ] **Step 24: Commit**

```bash
git add scripts/test-page.sh
git commit -m 'Scripts: scripts/test-page.sh starts its draal with a bucket, and asks for the store'\''s page as well'
```

- [ ] **Step 25: Write the failing test: a draal whose bucket has no region says what gives it one**

Change `cli/draal_test.go`:

```diff
--- a/cli/draal_test.go
+++ b/cli/draal_test.go
@@ -557,3 +557,31 @@ func TestRunDraalStoreFails(t *testing.T) {
 		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
 	}
 }
+
+// TestRunDraalWithoutARegion: the URLs of the store are signed for a
+// region, which --s3-region gives or the AWS configuration has. A draal
+// that has neither ends at its start, before anything is bound, and says
+// what gives it one.
+func TestRunDraalWithoutARegion(t *testing.T) {
+	absent := filepath.Join(t.TempDir(), "absent")
+	for _, name := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE"} {
+		t.Setenv(name, "")
+	}
+	t.Setenv("AWS_CONFIG_FILE", absent)
+	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", absent)
+	cfg := draalConfig{data: t.TempDir(), local: true}
+	cfg.store.bucket.Bucket = "packs"
+	var logged buffer
+	err := runDraal(context.Background(), cfg, quietLog(&logged))
+	if err == nil {
+		t.Fatal("runDraal returned nil for a bucket without a region")
+	}
+	for _, want := range []string{"the store: ", "no region", "--s3-region", "GREAT_MACHINE_S3_REGION", "AWS_REGION"} {
+		if !strings.Contains(err.Error(), want) {
+			t.Errorf("the error does not have %q: %v", want, err)
+		}
+	}
+	if strings.Contains(logged.String(), "msg=serving") {
+		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
+	}
+}
```

- [ ] **Step 26: Run it, and see it fail**

Run: `go test -run TestRunDraalWithoutARegion ./cli/`

Expected: it fails, with

```text
draal_test.go:581: the error does not have "--s3-region": the store: storeserver: bucket: no region: none was given, and the AWS configuration has none
```

- [ ] **Step 27: Write the code: a draal without a region for its bucket names the flag and the variables that give it one**

Change `cli/draal.go`:

```diff
--- a/cli/draal.go
+++ b/cli/draal.go
@@ -54,7 +54,7 @@ func draalCommand() *cli.Command {
 			&cli.StringFlag{Name: "s3-endpoint", EnvVars: []string{"GREAT_MACHINE_S3_ENDPOINT"},
 				Usage: "`URL` of an S3 service other than AWS; it is in the URLs that clients are given, so the laptop and the pods have to reach it under this name"},
 			&cli.StringFlag{Name: "s3-region", EnvVars: []string{"GREAT_MACHINE_S3_REGION"},
-				Usage: "`REGION` of the bucket (default: what the AWS configuration says)"},
+				Usage: "`REGION` of the bucket, which the URLs of the store are signed for (default: what the AWS configuration says, as AWS_REGION; one of the two has to say it)"},
 			&cli.BoolFlag{Name: "s3-path-style", EnvVars: []string{"GREAT_MACHINE_S3_PATH_STYLE"},
 				Usage: "address the bucket in the path of a URL, not in its host name"},
 			&cli.StringFlag{Name: "store-scratch", EnvVars: []string{"GREAT_MACHINE_STORE_SCRATCH"},
@@ -221,6 +221,11 @@ func runDraal(ctx context.Context, cfg draalConfig, log *slog.Logger) (err error
 		Log:          log,
 	})
 	if err != nil {
+		// The URLs of the store are signed for a region. jaccard-store
+		// says that it has none, and cannot say where one is given.
+		if cfg.store.bucket.Region == "" && strings.Contains(err.Error(), "no region") {
+			return fmt.Errorf("the store: %w: give the region of the bucket with --s3-region, or set GREAT_MACHINE_S3_REGION or AWS_REGION", err)
+		}
 		return fmt.Errorf("the store: %w", err)
 	}
 	// Deferred before the listener is, so that it happens after it: the
```

- [ ] **Step 28: Run it, and see it pass**

Run: `go test -run TestRunDraalWithoutARegion ./cli/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 29: Commit**

```bash
git add cli/draal_test.go cli/draal.go
git commit -m 'CLI: a draal whose bucket has no region says what gives it one'
```

- [ ] **Step 30: Write the failing test: the demo of the page answers the link to the store with a line that says it has none**

Change `web/demo/main_test.go`:

```diff
--- a/web/demo/main_test.go
+++ b/web/demo/main_test.go
@@ -78,6 +78,18 @@ func TestDemoServes(t *testing.T) {
 		t.Fatalf("the history is %d with %d samples (%v), want 200 with fifteen minutes of them", resp.StatusCode, len(answer.Samples), err)
 	}
 
+	// The header of the page has a link to the store's page. The demo
+	// has no store, and says so there in the place of an error.
+	store, err := client.Get(url + "store/")
+	if err != nil {
+		t.Fatalf("GET store/: %v", err)
+	}
+	defer store.Body.Close()
+	said, _ := io.ReadAll(store.Body)
+	if store.StatusCode != http.StatusOK || !strings.Contains(string(said), "the demo has a made-up fleet and no store") {
+		t.Errorf("GET store/ is %d %q, want 200 and a line that says the demo has no store", store.StatusCode, said)
+	}
+
 	// Ended as an interrupt ends it: the socket is closed as going away.
 	stop()
 	select {
```

- [ ] **Step 31: Run it, and see it fail**

Run: `go test ./web/demo/`

Expected: it fails, with

```text
main_test.go:90: GET store/ is 404 "{\"error\":{\"code\":\"not-found\",\"message\":\"this draal has no page of the store\"}}\n", want 200 and a line that says the demo has no store
```

- [ ] **Step 32: Write the code: the demo of the page says that it has no store, where a draal serves the page of its own**

Change `web/demo/main.go`:

```diff
--- a/web/demo/main.go
+++ b/web/demo/main.go
@@ -7,7 +7,8 @@
 //	go run ./web/demo [--addr 127.0.0.1:8080] [--dist DIR] [--empty]
 //
 // With --empty there is no service at all, as draal has none when it was
-// started for the first time.
+// started for the first time. There is no store either: where a draal
+// serves the page of its store, the demo serves a line that says so.
 //
 // Everything it shows is made up but the shell: THE SHELL OF THE PAGE IS A
 // REAL SHELL ON THE MACHINE THAT RUNS THE DEMO, as the user who runs it.
@@ -73,6 +74,12 @@ func run(ctx context.Context, addr, dist string, empty bool, out io.Writer, read
 	defer stopMoving()
 	go fleet.Run(moving)
 	cfg.Controller = fleet
+	// The header of the page has a link to the page of the store, which
+	// a draal has from jaccard-store, over the database of its store.
+	cfg.Store = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
+		fmt.Fprintln(w, "great-machine: the demo has a made-up fleet and no store. A draal serves the page of its store here: what was pushed, and what sharing and compression save.")
+	})
 
 	// The context of every request. A socket lives on when a server is
 	// shut down, and ends when this is cancelled.
```

- [ ] **Step 33: Run it, and see it pass**

Run: `go test ./web/demo/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 34: Commit**

```bash
git add web/demo/main_test.go web/demo/main.go
git commit -m 'Web: the demo of the page answers the link to the store with a line that says it has none'
```

- [ ] **Step 35: Write the failing test: values of the command lines that cannot be meant are refused: more jobs than the store answers, an endpoint that is no address, a size with a sign; a scratch that is the data directory loses nothing**

A review found them. The first is the one a person meets: a rollout with more than 64 jobs failed in its middle against a store that was well, with an error that named neither --jobs nor the bound.

Change `cli/commands_test.go`:

```diff
--- a/cli/commands_test.go
+++ b/cli/commands_test.go
@@ -70,6 +70,10 @@ func TestInvalidCommandLines(t *testing.T) {
 		{"rollout: --jobs 0", nil, []string{"rollout", "--draal", idA, "--jobs", "0", "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
 		{"rollout: the variable stands in for --jobs", map[string]string{"GREAT_MACHINE_JOBS": "0"},
 			[]string{"rollout", "--draal", idA, "web", "/nix/store/abc-web/bin/web"}, "--jobs: 0 is not a number of store paths"},
+		// The store answers 64 requests of a connection at a time, and a
+		// push has two under way when it goes from one step to the next.
+		{"rollout: more --jobs than the store answers", nil, []string{"rollout", "--draal", idA, "--jobs", "33", "web", "/nix/store/abc-web/bin/web"},
+			"--jobs: 33 is more than 32 store paths at a time, which is the most"},
 		{"rollout: no --draal", nil, []string{"rollout", "web", "/nix/store/abc-web/bin/web"}, "--draal is required"},
 		{"rollout: a bad --draal", nil, []string{"rollout", "--draal", "nonsense", "web", "/nix/store/abc-web/bin/web"}, `--draal: "nonsense"`},
 		{"id: an argument", nil, []string{"id", "now"}, `id takes no argument, got "now"`},
@@ -87,6 +91,16 @@ func TestInvalidCommandLines(t *testing.T) {
 			`--s3-endpoint: "minio:9000" is not the URL of an S3 service`},
 		{"draal: the variable stands in for --s3-endpoint", map[string]string{"GREAT_MACHINE_S3_ENDPOINT": "s3.example.com"},
 			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--s3-endpoint: "s3.example.com" is not the URL of an S3 service`},
+		// The endpoint is in every URL a client is handed: it names a
+		// host, and carries nothing that is not the service's address.
+		{"draal: an --s3-endpoint without a host", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--s3-endpoint", "http://:9000"},
+			`--s3-endpoint: "http://:9000" is not the URL of an S3 service`},
+		{"draal: an --s3-endpoint with a user in it", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--s3-endpoint", "http://user:secret@host"},
+			`--s3-endpoint: "http://user:secret@host" is not the URL of an S3 service`},
+		{"draal: an --s3-endpoint with a query", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--s3-endpoint", "http://host/path?x=1"},
+			`--s3-endpoint: "http://host/path?x=1" is not the URL of an S3 service`},
+		{"draal: an --s3-endpoint with a fragment", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--s3-endpoint", "http://host#frag"},
+			`--s3-endpoint: "http://host#frag" is not the URL of an S3 service`},
 		{"draal: a --store-max-pack-size that is no size", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "lots"},
 			`--store-max-pack-size: "lots" is not a size: want bytes, or a number with KiB, MiB or GiB`},
 		{"draal: a --store-max-pack-size of nothing", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "0"},
@@ -95,6 +109,8 @@ func TestInvalidCommandLines(t *testing.T) {
 			`--store-max-pack-size: "16GB" is not a size`},
 		{"draal: the variable stands in for --store-max-pack-size", map[string]string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE": "-1"},
 			[]string{"draal", "--data", "d", "--s3-bucket", "packs"}, `--store-max-pack-size: "-1" is not a size`},
+		{"draal: a --store-max-pack-size with a sign", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-max-pack-size", "+1GiB"},
+			`--store-max-pack-size: "+1GiB" is not a size`},
 		{"draal: no --store-verify-jobs at all", nil, []string{"draal", "--data", "d", "--s3-bucket", "packs", "--store-verify-jobs", "0"},
 			"--store-verify-jobs: 0 is not a number of packs to verify at once: want 1 or more"},
 		{"draal: an operator that is no endpoint ID", nil,
@@ -193,6 +209,18 @@ func TestReadDraal(t *testing.T) {
 		t.Errorf("from the variables: got %+v, want %+v", got, want)
 	}
 
+	// A scratch directory of blanks is none: the store then makes its own
+	// in the data directory.
+	read(t, draalCommand(), reader, "--data", "data", "--store-scratch", "   ")
+	if got.store.scratch != "" {
+		t.Errorf("--store-scratch of blanks is read as %q, want none", got.store.scratch)
+	}
+	// And an endpoint may have a path, which some services are under.
+	read(t, draalCommand(), reader, "--data", "data", "--s3-endpoint", "https://gateway.example.com/s3")
+	if got.store.bucket.Endpoint != "https://gateway.example.com/s3" {
+		t.Errorf("--s3-endpoint with a path is read as %q", got.store.bucket.Endpoint)
+	}
+
 	// A flag wins over its variable, and --operator may be repeated.
 	read(t, draalCommand(), reader, "--data", "data", "--operator", idC, "--operator", idA, "--bind", "0.0.0.0:4433",
 		"--s3-bucket", "other", "--s3-prefix", "", "--s3-endpoint", "https://s3.example.com", "--s3-region", "auto", "--s3-path-style=false",
@@ -228,7 +256,7 @@ func TestParseSize(t *testing.T) {
 			t.Errorf("parseSize(%q) = %d, %v; want %d", text, got, err, want)
 		}
 	}
-	for _, text := range []string{"", "0", "-1", "lots", "16GB", "1.5GiB", "GiB", "9223372036854775807GiB", "1TiB"} {
+	for _, text := range []string{"", "0", "-1", "+1", "+1GiB", "0x10", "1e3", "lots", "16GB", "1.5GiB", "GiB", "9223372036854775807GiB", "1TiB"} {
 		if got, err := parseSize(text); err == nil {
 			t.Errorf("parseSize(%q) = %d, want an error", text, got)
 		}
```

Change `cli/draal_test.go`:

```diff
--- a/cli/draal_test.go
+++ b/cli/draal_test.go
@@ -585,3 +585,40 @@ func TestRunDraalWithoutARegion(t *testing.T) {
 		t.Errorf("the endpoint was bound though the store could not be opened:\n%s", logged.String())
 	}
 }
+
+// TestRunDraalWithAScratchThatIsItsDataDirectory: the store empties its
+// scratch directory at every start. A draal that is told to keep
+// everything in one place, --store-scratch being --data, loses nothing by
+// it: its key and both databases are there after a run, and it is the
+// same draal the next time.
+func TestRunDraalWithAScratchThatIsItsDataDirectory(t *testing.T) {
+	cfg := draalForTest(t, "127.0.0.1:0")
+	cfg.store.scratch = cfg.data
+	run := func() wire.ID {
+		t.Helper()
+		ctx, cancel := context.WithCancel(context.Background())
+		defer cancel()
+		var logged buffer
+		_, done := startDraal(t, ctx, cfg, &logged)
+		id, _ := served(t, &logged)
+		cancel()
+		select {
+		case err := <-done:
+			if err != nil {
+				t.Fatalf("runDraal returned %v", err)
+			}
+		case <-time.After(30 * time.Second):
+			t.Fatal("runDraal did not return")
+		}
+		return id
+	}
+	first := run()
+	for _, name := range []string{draalKeyFile, draalDBFile, "store.sqlite"} {
+		if _, err := os.Stat(filepath.Join(cfg.data, name)); err != nil {
+			t.Errorf("after one run of draal: %v", err)
+		}
+	}
+	if second := run(); second != first {
+		t.Errorf("draal was %s, and is %s after it was started once more", first, second)
+	}
+}
```

Change `push/jobs_test.go`:

```diff
--- a/push/jobs_test.go
+++ b/push/jobs_test.go
@@ -204,3 +204,13 @@ func TestPushThatFailedBeforeTheContextEnded(t *testing.T) {
 	}
 	empty(t, cfg.TempDir)
 }
+
+// More jobs than the store answers the streams of is refused before
+// anything is dialed: the store would close the streams beyond, and the
+// push would fail in its middle with nothing that says why.
+func TestPushRefusesMoreJobsThanTheStoreAnswers(t *testing.T) {
+	_, err := Push(context.Background(), Config{Server: "nobody is dialed", Prefix: "/services/web/nix/store/", Jobs: MaxJobs + 1}, []string{"/nix/store/abc-web"})
+	if err == nil || !strings.Contains(err.Error(), "33 store paths at a time") || !strings.Contains(err.Error(), "32 is the most") {
+		t.Fatalf("Push with %d jobs: %v, want it refused, with the most there is", MaxJobs+1, err)
+	}
+}
```

Change `storeserver/storeserver_test.go`:

```diff
--- a/storeserver/storeserver_test.go
+++ b/storeserver/storeserver_test.go
@@ -5,6 +5,7 @@ import (
 	"context"
 	"encoding/json"
 	"errors"
+	"fmt"
 	"io"
 	"log/slog"
 	"net/http"
@@ -954,3 +955,31 @@ func TestABucketThatCannotBeReached(t *testing.T) {
 		t.Errorf("a pull of what could not be pushed: %v, want client.ErrNotFound", err)
 	}
 }
+
+// The most jobs a push has, of many more store paths than that: every
+// job has a stream of its own for each of its requests, one after the
+// other, and all of them stay within what the store answers of a
+// connection at a time.
+func TestAPushWithTheMostJobsThereAre(t *testing.T) {
+	w := start(t, nil)
+	var paths []string
+	for i := range 90 {
+		paths = append(paths, storePath(t, fmt.Sprintf("p%02d-thing", i), fmt.Sprintf("the program number %d", i)))
+	}
+	ctx, cancel := context.WithTimeout(context.Background(), 4*patience)
+	defer cancel()
+	res, err := push.Push(ctx, push.Config{
+		Key:     w.operator,
+		Server:  w.listener.ID(),
+		Addrs:   []netip.AddrPort{w.listener.LocalAddr()},
+		Prefix:  wire.StorePrefix("web"),
+		Jobs:    push.MaxJobs,
+		TempDir: t.TempDir(),
+	}, paths)
+	if err != nil || res.Pushed != 90 {
+		t.Fatalf("a push of 90 store paths with %d jobs: %+v, %v", push.MaxJobs, res, err)
+	}
+	if strings.Contains(w.log.String(), "more streams than the store answers") {
+		t.Errorf("the push opened more streams than the store answers at a time:\n%s", w.log.String())
+	}
+}
```

- [ ] **Step 36: Run it, and see it fail**

Run: `go test ./cli/ ./push/ ./storeserver/`

Expected: it fails, with

```text
push/jobs_test.go:212:116: undefined: MaxJobs
commands_test.go:158: stderr is "resolving the binary: failed after 0.0s ...", want it to say "--jobs: 33 is more than 32 store paths at a time, which is the most"
commands_test.go:155: the exit status is 1, want 2 (draal: an --s3-endpoint without a host, with a user in it, with a query, with a fragment; a --store-max-pack-size with a sign)
```

The command lines that are not refused yet start a draal, in the directory they name, which is `cli/d`: this run leaves a key and a database there. Remove them, so that neither is committed:

```bash
rm -rf cli/d
```

- [ ] **Step 37: Write the code: 32 jobs of a push at the most; an endpoint that names a host and nothing but an address; a size of digits; a scratch directory of blanks is none**

Change `cli/draal.go`:

```diff
--- a/cli/draal.go
+++ b/cli/draal.go
@@ -58,7 +58,7 @@ func draalCommand() *cli.Command {
 			&cli.BoolFlag{Name: "s3-path-style", EnvVars: []string{"GREAT_MACHINE_S3_PATH_STYLE"},
 				Usage: "address the bucket in the path of a URL, not in its host name"},
 			&cli.StringFlag{Name: "store-scratch", EnvVars: []string{"GREAT_MACHINE_STORE_SCRATCH"},
-				Usage: "`DIR` in which packs are verified; it is emptied at start (default: scratch in --data)"},
+				Usage: "`DIR` in which packs are verified: the store makes a directory of its own there, great-machine-scratch, which it empties at every start (default: --data)"},
 			&cli.IntFlag{Name: "store-verify-jobs", EnvVars: []string{"GREAT_MACHINE_STORE_VERIFY_JOBS"}, Value: 2,
 				Usage: "packs verified at once; each needs scratch space for its uncompressed data"},
 			&cli.StringFlag{Name: "store-max-pack-size", EnvVars: []string{"GREAT_MACHINE_STORE_MAX_PACK_SIZE"}, Value: "16GiB",
@@ -106,7 +106,7 @@ type draalConfig struct {
 // storeConfig is what the command line of draal says of the store.
 type storeConfig struct {
 	bucket       bucket.Config
-	scratch      string // "": the directory scratch in the data directory
+	scratch      string // where the store makes its scratch directory; "": the data directory
 	verifyJobs   int
 	maxPackBytes int64
 	noVerify     bool
@@ -159,7 +159,7 @@ func readStore(c *cli.Context) (storeConfig, error) {
 			Region:    strings.TrimSpace(c.String("s3-region")),
 			PathStyle: c.Bool("s3-path-style"),
 		},
-		scratch:    c.String("store-scratch"),
+		scratch:    strings.TrimSpace(c.String("store-scratch")),
 		verifyJobs: c.Int("store-verify-jobs"),
 		noVerify:   c.Bool("store-no-verify"),
 	}
@@ -168,10 +168,14 @@ func readStore(c *cli.Context) (storeConfig, error) {
 		return storeConfig{}, err
 	}
 	// The endpoint is in every URL a client is given. One without a
-	// scheme would be taken for a path, and found out by the first push.
+	// scheme would be taken for a path, and found out by the first push;
+	// one without a host names no service; and a user, a query or a
+	// fragment is no part of where a service is.
 	if e := cfg.bucket.Endpoint; e != "" {
-		if u, err := url.Parse(e); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
-			return storeConfig{}, usageError("--s3-endpoint: %q is not the URL of an S3 service: want http://HOST[:PORT] or https://HOST[:PORT]", e)
+		u, err := url.Parse(e)
+		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
+			u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
+			return storeConfig{}, usageError("--s3-endpoint: %q is not the URL of an S3 service: want http://HOST[:PORT] or https://HOST[:PORT], with the path the service is under if it has one", e)
 		}
 	}
 	if cfg.verifyJobs < 1 {
```

Change `cli/flags.go`:

```diff
--- a/cli/flags.go
+++ b/cli/flags.go
@@ -126,6 +126,12 @@ func parseSize(text string) (int64, error) {
 			break
 		}
 	}
+	// Digits alone: ParseInt would take a sign.
+	for _, c := range digits {
+		if c < '0' || c > '9' {
+			return 0, fmt.Errorf("%q is not a size: want bytes, or a number with KiB, MiB or GiB", text)
+		}
+	}
 	n, err := strconv.ParseInt(digits, 10, 64)
 	if err != nil || n <= 0 || n > (1<<62)/factor {
 		return 0, fmt.Errorf("%q is not a size: want bytes, or a number with KiB, MiB or GiB", text)
```

Change `cli/rollout.go`:

```diff
--- a/cli/rollout.go
+++ b/cli/rollout.go
@@ -120,6 +120,9 @@ func readRollout(c *cli.Context) (rolloutConfig, error) {
 	if cfg.jobs < 1 {
 		return rolloutConfig{}, usageError("--jobs: %d is not a number of store paths to push at a time: want 1 or more", cfg.jobs)
 	}
+	if cfg.jobs > push.MaxJobs {
+		return rolloutConfig{}, usageError("--jobs: %d is more than %d store paths at a time, which is the most: the store answers 64 requests of a connection at a time, and a push has two under way when it goes from one step to the next", cfg.jobs, push.MaxJobs)
+	}
 	return cfg, nil
 }
 
```

Change `push/push.go`:

```diff
--- a/push/push.go
+++ b/push/push.go
@@ -32,6 +32,14 @@ import (
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
+// MaxJobs is the most store paths that are pushed at a time. The store
+// answers 64 requests of a connection at a time and closes the stream of
+// one more without an answer. Every job has one request under way, on a
+// stream of its own, and two for the moment in which it goes from one
+// step to the next: the store has not let go of the stream that was
+// answered when the next one arrives.
+const MaxJobs = 32
+
 const (
 	// defaultJobs is how many store paths are pushed at a time when Config
 	// does not say.
@@ -50,7 +58,7 @@ type Config struct {
 	Server   wire.ID          // the store: the endpoint ID of draal
 	Addrs    []netip.AddrPort // for tests: where the server is
 	Prefix   string
-	Jobs     int      // 0: 20
+	Jobs     int      // 0: 20; MaxJobs at the most
 	TempDir  string   // "": the system's
 	Progress Progress // nil: none
 }
@@ -108,6 +116,9 @@ func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error)
 	if jobs <= 0 {
 		jobs = defaultJobs
 	}
+	if jobs > MaxJobs {
+		return Result{}, fmt.Errorf("pushing %d store paths at a time: %d is the most, as the store answers 64 requests of a connection at a time and a push has two under way when it goes from one step to the next", jobs, MaxJobs)
+	}
 
 	// Before anything is dialed: a name the server will not take should
 	// cost nothing.
```

- [ ] **Step 38: Run it, and see it pass**

Run: `go test ./cli/ ./push/ ./storeserver/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 39: Commit**

```bash
git add cli/commands_test.go cli/draal_test.go push/jobs_test.go storeserver/storeserver_test.go cli/draal.go cli/flags.go cli/rollout.go push/push.go
git commit -m 'Draal: values of the command lines that cannot be meant are refused: more jobs than the store answers, an endpoint that is no address, a size with a sign; a scratch that is the data directory loses nothing'
```

- [ ] **Step 40: The page as a person sees it**

The link in the header has no unit test, since the tests of the page run without a document. Look at it once:

```bash
(cd ui && npm ci && npm run build)
nix develop .#bare --command go run ./web/demo
```

and open `http://127.0.0.1:8080/` in a browser: the header has "Store" beside the breadcrumbs, in light and in dark, and it leads to a line that says that the demo has no store. With a real draal it leads to jaccard-store's page (Task 14 looks at that). Then stop the demo, and leave nothing built behind:

```bash
git clean -fdxq web/dist ui
git status --short
```

Expected: nothing from `git status`.

- [ ] **Step 41: The whole**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
```

Expected: nothing from `gofmt` and `go vet`, and `ok` for the 31 packages that have tests. From here on `great-machine draal` takes a bucket and no `--jaccard-server`: `deploy/draal.yaml` and the README still say otherwise until Task 13.
