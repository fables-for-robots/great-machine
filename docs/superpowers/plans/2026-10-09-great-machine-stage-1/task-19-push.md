### Task 19: push — store paths to jaccard-store

**Files:**
- Modify: `go.mod`, `go.sum` (jaccard-store, amber-store core and `golang.org/x/sync` become direct requirements)
- Create: `push/push.go`
- Test: `push/server_test.go` (a jaccard-store server in the test's process), `push/push_test.go`, `push/jobs_test.go`

**Interfaces:**
- Consumes, from the contract:

```go
package wire

// ID is an endpoint ID: 64 lowercase hexadecimal characters.
type ID string
```

```go
package node

type Key struct{ /* unexported */ }

func NewKey() (Key, error)

// Iroh returns the key as go-iroh has it (key.SecretKey of
// github.com/tmc/go-iroh/key).
func (k Key) Iroh() key.SecretKey
```

- Consumes, from outside (settled by a spike against the real modules):
  - `github.com/amber-store/jaccard-store v0.7.0`: `node.Dial(ctx, node.DialConfig{Key, ALPN, Server, Addrs, Log}) (*node.Conn, error)`, `wire.ALPN` (`"amber/jaccard-store/1"`), `client.New(conn, nil) *client.Client`, `(*client.Client).List(ctx, prefix) ([]client.Ref, error)`, `(*client.Client).Push(ctx, objects, name, root, client.PushOptions{MinDedup, TempDir}) (client.PushResult, error)`; in the tests also `(*client.Client).Pull`, `node.Bind`, `node.LoadOrCreateKey`, `server.New`, `db.Open`, `bucket/buckettest.New`.
  - `github.com/amber-store/core v0.10.0`: `packstore.Open(dir, packstore.WithSync(false))`, `ingest.Objects(path, ingest.Opts{NoIgnore: true})`, `(*packstore.Store).WriteParallel`, `reference.ValidateName`; in the tests also `tarexport.Write`, `tarextract.Extract`, `fstree.WriteContent`.
  - `github.com/tmc/go-iroh v0.3.0`: `key.ParseEndpointID` (it reads the 64 hexadecimal characters of a `wire.ID`).
- Produces (the contract's `push` section):

```go
package push

type Config struct {
	Key      node.Key         // the operator's
	Server   wire.ID          // the jaccard-store server
	Addrs    []netip.AddrPort // for tests: where the server is
	Prefix   string
	Jobs     int      // 0: 5
	TempDir  string   // "": the system's
	Progress Progress // nil: none
}

type Progress interface {
	Found(present, missing int)
	Pushed(storePath string, bytes uint64)
}

type Result struct {
	Pushed  int
	Present int
}

func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error)
```

What the contract leaves open, and this task settles (item 3 of the spec's section 15 among it):

- **The reference** of a store path is `Prefix` + the last element of the path. `Push` does not care where the path is, so the tests keep their store paths in a temporary directory. A path given twice is pushed once.
- **What is present** is asked of the server once for every name (`client.List` with the whole name as the prefix, and an exact match in what comes back), 16 at a time on the one connection. One listing of everything under `Prefix` would bring every store path that was ever rolled out, a thousand to a request; the closure is a few hundred names.
- **A store path that is a directory** is imported with `ingest.Opts{NoIgnore: true}`: the options of `jaccard-store push-dir`, but a `.amberignore` in the store path is not honored. Modes, symbolic links inside the tree and execute bits are kept.
- **A store path that is a single regular file** is imported by the same call. Its root is the file's content, and jaccard-nfs-nix-store serves such a reference as a regular file (executable, as nothing says whether it is).
- **A store path that is a symbolic link is refused.** The root of a reference is a directory or the content of a file; a link has no place there. amber-store core would follow the link and import what it points at, and the sidecar would serve a copy of the target under the name, not a link. So `Push` refuses, before it pushes anything, with an error that names the store path: `pushing <path>: the store path is a symbolic link, which cannot be pushed`. Only the store paths the server lacks are looked at.
- **`Progress.Found`** is called once, after the server was asked and before the first push. **`Progress.Pushed`** gets the size of the pack that was uploaded (`client.PushResult.DataSize`), which is 0 when the server had a pack of the same content already.
- **The `Result`** counts what was done until `Push` returned, also beside an error.
- **A failure** ends the context of the other pushes; `Push` waits for them, so that every temporary store is removed before it returns. The error is `pushing <path>: <cause>`. When the caller's context ended, the error has `context.Canceled` (or `context.DeadlineExceeded`) in its chain.
- **Temporary stores** are directories `great-machine-push-*` under `TempDir`, one for each store path that is pushed, each removed when its push ends.

The tests run a jaccard-store server in their own process: its database in a temporary directory, its bucket jaccard-store's `buckettest` (a `gofakes3` S3 in memory behind `httptest`), its endpoint bound to `127.0.0.1` without a relay. Nothing leaves the machine and no Docker is needed. `buckettest` sets the environment with `t.Setenv`, so no test of this package may call `t.Parallel`. The whole package takes about 1.5 seconds (about 7 seconds for three runs under `-race`).

The tests are in package `push` itself, because `jobs_test.go` replaces an unexported hook. They pass on macOS and on Linux.

- [ ] **Step 1: Add the dependencies**

`github.com/tmc/go-iroh v0.3.0` is in `go.mod` already. Run:

```bash
go get github.com/amber-store/jaccard-store@v0.7.0 github.com/amber-store/core@v0.10.0 golang.org/x/sync@v0.22.0
```

Expected: `go.mod` gains the three requirements (for now marked `// indirect`; Step 5 puts that right). No `replace` directive is needed: neither jaccard-store nor jaccard-nfs-nix-store has one.

- [ ] **Step 2: Write the server for the tests, and the failing tests**

Create `push/server_test.go`:

```go
package push

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amber-store/core/fstree"
	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/core/tarexport"
	"github.com/amber-store/core/tarextract"
	"github.com/amber-store/jaccard-store/bucket/buckettest"
	"github.com/amber-store/jaccard-store/client"
	jsdb "github.com/amber-store/jaccard-store/db"
	jsnode "github.com/amber-store/jaccard-store/node"
	jsserver "github.com/amber-store/jaccard-store/server"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// testTimeout is how long one step of a test may take before the test
// gives up. Nothing waits for it when all is well.
const testTimeout = 30 * time.Second

// quiet drops what the server and the endpoints log.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// testServer is a jaccard-store server in the process of the test: its
// database in a temporary directory, its bucket a fake S3 in memory, its
// endpoint on the loopback interface.
type testServer struct {
	t     *testing.T
	id    wire.ID
	addrs []netip.AddrPort
}

// startServer starts a server that is stopped and removed when the test
// ends. The bucket sets the environment of the test with t.Setenv, so a
// test that starts a server cannot be parallel.
func startServer(t *testing.T) *testServer {
	t.Helper()
	b := buckettest.New(t)
	dir := t.TempDir()

	database, err := jsdb.Open(filepath.Join(dir, "store.sqlite"))
	if err != nil {
		t.Fatalf("opening the server's database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	srv, err := jsserver.New(jsserver.Config{DB: database, Bucket: b, Scratch: filepath.Join(dir, "scratch"), Log: quiet})
	if err != nil {
		t.Fatalf("the server: %v", err)
	}
	sk, err := jsnode.LoadOrCreateKey(filepath.Join(dir, "server.key"))
	if err != nil {
		t.Fatalf("the server's key: %v", err)
	}
	ctx, stop := context.WithCancel(context.Background())
	ep, err := jsnode.Bind(ctx, jsnode.ServerConfig{Key: sk, ALPN: jswire.ALPN, Local: true, Log: quiet})
	if err != nil {
		stop()
		t.Fatalf("binding the server's endpoint: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		srv.Serve(ctx, ep)
	}()
	// Cleanups run last in, first out: the serving ends before the
	// database under it is closed.
	t.Cleanup(func() {
		stop()
		<-served
		ep.Shutdown(context.Background())
	})
	return &testServer{t: t, id: wire.ID(ep.ID().String()), addrs: []netip.AddrPort{ep.LocalAddr()}}
}

// config returns a Config for the server with a key of its own, the
// prefix "store/" and a temporary directory that is empty.
func (s *testServer) config() Config {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	return Config{Key: k, Server: s.id, Addrs: s.addrs, Prefix: "store/", TempDir: s.t.TempDir()}
}

// client dials the server with a key of its own. The connection ends with
// the test.
func (s *testServer) client() *client.Client {
	s.t.Helper()
	k, err := node.NewKey()
	if err != nil {
		s.t.Fatal(err)
	}
	server, err := irohkey.ParseEndpointID(string(s.id))
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{Key: k.Iroh(), ALPN: jswire.ALPN, Server: server, Addrs: s.addrs, Log: quiet})
	if err != nil {
		s.t.Fatalf("dialing the server: %v", err)
	}
	s.t.Cleanup(func() { conn.Close() })
	return client.New(conn, nil)
}

// refs returns the names of the references the server has.
func (s *testServer) refs() []string {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	listed, err := s.client().List(ctx, "")
	if err != nil {
		s.t.Fatalf("listing the server's references: %v", err)
	}
	names := make([]string, len(listed))
	for i, r := range listed {
		names[i] = r.Name
	}
	return names
}

// pull fetches the reference name into a store of its own and returns the
// store and the root of the reference.
func (s *testServer) pull(name string) (*packstore.Store, key.Key) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	objects, err := packstore.Open(filepath.Join(s.t.TempDir(), "packstore"), packstore.WithSync(false))
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { objects.Close() })
	res, err := s.client().Pull(ctx, objects, name, client.PullOptions{})
	if err != nil {
		s.t.Fatalf("pulling %q: %v", name, err)
	}
	return objects, res.Root
}

// pullDir fetches the reference name, whose root has to be a directory,
// and extracts it into a new directory, as `jaccard-store pull-dir` does.
func (s *testServer) pullDir(name string) string {
	s.t.Helper()
	objects, root := s.pull(name)
	if t := root.Type(); t != key.DirLeaf && t != key.DirNode {
		s.t.Fatalf("the root of %q is a %v, want a directory", name, t)
	}
	dest := filepath.Join(s.t.TempDir(), "pulled")
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarexport.Write(pw, root, objects.Get)) }()
	err := tarextract.Extract(pr, dest)
	pr.CloseWithError(err)
	if err != nil {
		s.t.Fatalf("extracting %q: %v", name, err)
	}
	return dest
}

// pullFile fetches the reference name, whose root has to be the content
// of a file, and returns that content.
func (s *testServer) pullFile(name string) []byte {
	s.t.Helper()
	objects, root := s.pull(name)
	if t := root.Type(); t != key.Blob && t != key.FileNode {
		s.t.Fatalf("the root of %q is a %v, want the content of a file", name, t)
	}
	var content bytes.Buffer
	if err := fstree.WriteContent(&content, root, objects.Get); err != nil {
		s.t.Fatalf("reading %q: %v", name, err)
	}
	return content.Bytes()
}

// empty fails the test when the directory holds anything.
func empty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("%s was left behind in the temporary directory", e.Name())
	}
}
```

Create `push/push_test.go`:

```go
package push

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// recorder is a Progress that keeps what it is told.
type recorder struct {
	mu      sync.Mutex
	found   [][2]int // present, missing
	pushed  []string
	bytesOf map[string]uint64
}

func (r *recorder) Found(present, missing int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.found = append(r.found, [2]int{present, missing})
}

func (r *recorder) Pushed(storePath string, bytes uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pushed = append(r.pushed, storePath)
	if r.bytesOf == nil {
		r.bytesOf = make(map[string]uint64)
	}
	r.bytesOf[storePath] = bytes
}

// check fails the test unless Found was called once, with present and
// missing, and Pushed once for each of the paths.
func (r *recorder) check(t *testing.T, present, missing int, paths ...string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.found) != 1 || r.found[0] != [2]int{present, missing} {
		t.Errorf("Found was called with %v, want once with [%d %d]", r.found, present, missing)
	}
	got := slices.Sorted(slices.Values(r.pushed))
	want := slices.Sorted(slices.Values(paths))
	if !slices.Equal(got, want) {
		t.Errorf("Pushed was called for %q, want %q", got, want)
	}
}

// write writes a file with the mode, whatever the umask is.
func write(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// storePathDir makes a store path that is a directory, named name, in a
// directory that stands for the store: an executable file, a file nobody
// may write, an empty file, a symbolic link, a nested directory, and an
// ignore file that names one of the files.
func storePathDir(t *testing.T, store, name string) string {
	t.Helper()
	p := filepath.Join(store, name)
	write(t, filepath.Join(p, "bin", "web"), "#!/bin/sh\necho "+name+"\n", 0o555)
	write(t, filepath.Join(p, "share", "doc", "README"), "read me: "+name+"\n", 0o444)
	write(t, filepath.Join(p, "share", "empty"), "", 0o444)
	write(t, filepath.Join(p, "share", "ignored.log"), "pushed all the same\n", 0o444)
	write(t, filepath.Join(p, ".amberignore"), "*.log\n", 0o444)
	if err := os.Symlink("../share/doc/README", filepath.Join(p, "bin", "readme")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nix/store/eeee-elsewhere/lib", filepath.Join(p, "lib")); err != nil {
		t.Fatal(err)
	}
	return p
}

// storePaths makes n store paths that are directories.
func storePaths(t *testing.T, n int) []string {
	t.Helper()
	store := t.TempDir()
	paths := make([]string, n)
	for i := range paths {
		paths[i] = storePathDir(t, store, fmt.Sprintf("%04d-pkg-%d", i, i))
	}
	return paths
}

// sameTree fails the test unless the trees at want and got hold the same
// names, with the same types, permission bits, contents and link targets.
// The modes of the two roots are not compared: a reference records none.
func sameTree(t *testing.T, want, got string) {
	t.Helper()
	list := func(root string) map[string]string {
		entries := make(map[string]string)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == "." {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				entries[rel] = "link to " + target
			case info.IsDir():
				entries[rel] = fmt.Sprintf("directory %o", info.Mode().Perm())
			default:
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				entries[rel] = fmt.Sprintf("file %o %q", info.Mode().Perm(), content)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	w, g := list(want), list(got)
	for name, entry := range w {
		if g[name] != entry {
			t.Errorf("%s: pulled as %q, was pushed as %q", name, g[name], entry)
		}
	}
	for name, entry := range g {
		if _, ok := w[name]; !ok {
			t.Errorf("%s: pulled as %q, was not pushed", name, entry)
		}
	}
}

// push calls Push with a deadline.
func push(t *testing.T, cfg Config, paths []string) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	return Push(ctx, cfg, paths)
}

func TestPushDirectory(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	path := storePathDir(t, t.TempDir(), "aaaa-web-1.0")

	res, err := push(t, cfg, []string{path})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 0}) {
		t.Errorf("Push = %+v, want one pushed and none present", res)
	}
	rec.check(t, 0, 1, path)
	if rec.bytesOf[path] == 0 {
		t.Error("Pushed was told of a pack of 0 bytes")
	}
	if got, want := srv.refs(), []string{"store/aaaa-web-1.0"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
	// Modes, links and the file an ignore file names: all as they were.
	sameTree(t, path, srv.pullDir("store/aaaa-web-1.0"))
	empty(t, cfg.TempDir)
}

func TestPushSingleFile(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	store := t.TempDir()
	script := filepath.Join(store, "bbbb-builder.sh")
	write(t, script, "#!/bin/sh\nexit 0\n", 0o555)
	// Larger than one chunk, so that its root is no single blob.
	large := filepath.Join(store, "cccc-source.tar")
	write(t, large, strings.Repeat("0123456789abcdef", 1<<16)+"the end", 0o444)
	none := filepath.Join(store, "dddd-empty")
	write(t, none, "", 0o444)

	res, err := push(t, cfg, []string{script, large, none})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if res != (Result{Pushed: 3, Present: 0}) {
		t.Errorf("Push = %+v, want three pushed and none present", res)
	}
	for _, path := range []string{script, large, none} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// The root of the reference is the file's content.
		got := srv.pullFile("store/" + filepath.Base(path))
		if !bytes.Equal(got, want) {
			t.Errorf("%s: pulled %d bytes, pushed %d, and they differ", path, len(got), len(want))
		}
	}
	empty(t, cfg.TempDir)
}

// A store path that is a symbolic link cannot be a reference: Push
// refuses it, names it, and pushes nothing of what it was given.
func TestPushSymbolicLink(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	store := t.TempDir()
	dir := storePathDir(t, store, "aaaa-web-1.0")
	link := filepath.Join(store, "bbbb-web-current")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	res, err := push(t, cfg, []string{dir, link})
	if err == nil {
		t.Fatalf("Push = %+v, want an error for the symbolic link", res)
	}
	for _, want := range []string{link, "symbolic link"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if res.Pushed != 0 {
		t.Errorf("Push = %+v, want nothing pushed", res)
	}
	if got := srv.refs(); len(got) != 0 {
		t.Errorf("the server has the references %q, want none", got)
	}
	if len(rec.pushed) != 0 {
		t.Errorf("Pushed was called for %q", rec.pushed)
	}
	empty(t, cfg.TempDir)
}

func TestPushAgain(t *testing.T) {
	srv := startServer(t)
	paths := storePaths(t, 3)
	if _, err := push(t, srv.config(), paths); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	res, err := push(t, cfg, paths)
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 0, Present: 3}) {
		t.Errorf("the second Push = %+v, want none pushed and three present", res)
	}
	rec.check(t, 3, 0)
	empty(t, cfg.TempDir)
}

func TestPushPresentAndMissing(t *testing.T) {
	srv := startServer(t)
	paths := storePaths(t, 5)
	if _, err := push(t, srv.config(), paths[:2]); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	rec := &recorder{}
	cfg.Progress = rec
	res, err := push(t, cfg, paths)
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 3, Present: 2}) {
		t.Errorf("the second Push = %+v, want three pushed and two present", res)
	}
	rec.check(t, 2, 3, paths[2:]...)
	if got := srv.refs(); len(got) != 5 {
		t.Errorf("the server has the references %q, want five", got)
	}
	sameTree(t, paths[4], srv.pullDir("store/"+filepath.Base(paths[4])))
	empty(t, cfg.TempDir)
}

// A name that begins with the name of another is not taken for it, and a
// path given twice is pushed once.
func TestPushNamesThatBeginAlike(t *testing.T) {
	srv := startServer(t)
	store := t.TempDir()
	long := storePathDir(t, store, "aaaa-web-1.0")
	short := storePathDir(t, store, "aaaa-web")
	if _, err := push(t, srv.config(), []string{long}); err != nil {
		t.Fatalf("the first Push: %v", err)
	}

	cfg := srv.config()
	res, err := push(t, cfg, []string{short, long, short})
	if err != nil {
		t.Fatalf("the second Push: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 1}) {
		t.Errorf("the second Push = %+v, want one pushed and one present", res)
	}
	if got, want := srv.refs(), []string{"store/aaaa-web", "store/aaaa-web-1.0"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
}

func TestPushPathThatDoesNotExist(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	cfg.Jobs = 1 // one after the other, in the order given
	rec := &recorder{}
	cfg.Progress = rec
	store := t.TempDir()
	first := storePathDir(t, store, "aaaa-first")
	nothing := filepath.Join(store, "bbbb-nothing")
	last := storePathDir(t, store, "cccc-last")

	res, err := push(t, cfg, []string{first, nothing, last})
	if err == nil {
		t.Fatalf("Push = %+v, want an error for the path that does not exist", res)
	}
	if !strings.Contains(err.Error(), nothing) {
		t.Errorf("the error does not name %s: %v", nothing, err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the error does not say that the path does not exist: %v", err)
	}
	if res != (Result{Pushed: 1, Present: 0}) {
		t.Errorf("Push = %+v, want the one before pushed", res)
	}
	rec.check(t, 0, 3, first)
	// What was not begun when the push failed was not pushed.
	if got, want := srv.refs(), []string{"store/aaaa-first"}; !slices.Equal(got, want) {
		t.Errorf("the server has the references %q, want %q", got, want)
	}
	empty(t, cfg.TempDir)
}

func TestPushRefusesBeforeDialing(t *testing.T) {
	// No server: what is refused here is refused before one is dialed.
	tests := []struct {
		name string
		path string
		want string
	}{
		{"a name no reference can have", "/nix/store/aaaa-web@1.0", "/nix/store/aaaa-web@1.0"},
		{"an empty path", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := push(t, Config{Prefix: "store/"}, []string{"/nix/store/bbbb-fine", tt.path})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Push: %v; want an error that says %q", err, tt.want)
			}
		})
	}
}

func TestPushNothing(t *testing.T) {
	rec := &recorder{}
	res, err := push(t, Config{Prefix: "store/", Progress: rec}, nil)
	if err != nil || res != (Result{}) {
		t.Errorf("Push of nothing = %+v, %v", res, err)
	}
	rec.check(t, 0, 0)
}

func TestPushContextEndedBefore(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Push(ctx, cfg, storePaths(t, 2))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Push with a cancelled context: %v; want context.Canceled", err)
	}
	empty(t, cfg.TempDir)
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go mod tidy && go test ./push/`
Expected: FAIL — the build fails with `undefined: Config`, `undefined: Push` and `undefined: Result`

- [ ] **Step 4: Write `Push`**

Create `push/push.go`:

```go
// Package push takes store paths to a jaccard-store server: every store
// path becomes the reference prefix+name there, which the sidecar of a pod
// serves as /nix/store/<name>.
//
// A store path never changes under its name, so one whose reference the
// server has is not pushed again. Every other one is imported into a
// temporary store of its own, as `jaccard-store push-dir` imports a
// directory, and pushed with jaccard-store's client.
package push

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/core/reference"
	"github.com/amber-store/jaccard-store/client"
	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"
	"golang.org/x/sync/errgroup"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// defaultJobs is how many store paths are pushed at a time when Config
	// does not say.
	defaultJobs = 5
	// lookups is how many references the server is asked about at a time.
	lookups = 16
	// minDedup is the share of a store path's bytes a pack on the server
	// must hold for the store path to be pushed as a patch against it: the
	// default of jaccard-store's push-dir.
	minDedup = 0.5
)

// Config says where store paths are pushed to, and how.
type Config struct {
	Key      node.Key         // the operator's
	Server   wire.ID          // the jaccard-store server
	Addrs    []netip.AddrPort // for tests: where the server is
	Prefix   string
	Jobs     int      // 0: 5
	TempDir  string   // "": the system's
	Progress Progress // nil: none
}

// Progress is told what a push does. Its methods are called from several
// goroutines.
type Progress interface {
	// Found says how many of the store paths the server has already, and
	// how many are to be pushed.
	Found(present, missing int)
	// Pushed says that one store path was pushed, and how many bytes its
	// pack came to.
	Pushed(storePath string, bytes uint64)
}

// Result says what a push did.
type Result struct {
	Pushed  int
	Present int
}

// silent is the Progress of a caller that wants none.
type silent struct{}

func (silent) Found(int, int)        {}
func (silent) Pushed(string, uint64) {}

// item is one store path and the reference it is pushed as.
type item struct {
	path string
	ref  string
}

// Push makes the server hold the reference Prefix+name for every store
// path. It ends at the first path that fails, with that path in the error.
//
// The name of a store path is its last element. A store path may be a
// directory or a single regular file, which becomes a reference whose root
// is the file. One that is a symbolic link is refused before anything is
// pushed: the root of a reference cannot be a link, so the sidecar could
// not serve it as one.
//
// The Result counts what was done until Push returned, also when it
// returns an error.
func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error) {
	progress := cfg.Progress
	if progress == nil {
		progress = silent{}
	}
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = defaultJobs
	}

	// Before anything is dialed: a name the server will not take should
	// cost nothing.
	items := make([]item, 0, len(storePaths))
	seen := make(map[string]bool, len(storePaths))
	for _, p := range storePaths {
		ref := cfg.Prefix + filepath.Base(filepath.Clean(p))
		if p == "" {
			return Result{}, errors.New("pushing a store path: its path is empty")
		}
		if err := reference.ValidateName(ref); err != nil {
			return Result{}, fmt.Errorf("pushing %s as the reference %q: %w", p, ref, err)
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		items = append(items, item{path: p, ref: ref})
	}
	if len(items) == 0 {
		progress.Found(0, 0)
		return Result{}, nil
	}

	server, err := irohkey.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server %q: %w", cfg.Server, err)
	}
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    cfg.Key.Iroh(),
		ALPN:   jswire.ALPN,
		Server: server,
		Addrs:  cfg.Addrs,
	})
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server: %w", err)
	}
	defer conn.Close()
	c := client.New(conn, nil)

	missing, err := absent(ctx, c, items)
	if err != nil {
		return Result{}, err
	}
	res := Result{Present: len(items) - len(missing)}
	for _, it := range missing {
		info, err := os.Lstat(it.path)
		// A path that is not there fails when its turn comes.
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return res, fmt.Errorf("pushing %s: the store path is a symbolic link, which cannot be pushed", it.path)
		}
	}
	progress.Found(res.Present, len(missing))

	var mu sync.Mutex // guards res.Pushed
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(jobs)
	for _, it := range missing {
		// Go waits for a free place. A push that failed meanwhile has
		// ended gctx, and nothing more is begun.
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			size, err := one(gctx, c, it, cfg.TempDir)
			if err != nil {
				return fmt.Errorf("pushing %s: %w", it.path, err)
			}
			mu.Lock()
			res.Pushed++
			mu.Unlock()
			progress.Pushed(it.path, size)
			return nil
		})
	}
	err = g.Wait()
	// The end of the caller's context is the cause of whatever it ended.
	if cerr := ctx.Err(); cerr != nil {
		err = fmt.Errorf("the push was interrupted: %w", cerr)
	}
	return res, err
}

// absent asks the server about the reference of every item and returns
// the items it has no reference for, in their order.
//
// The server is asked once for every name, with the name as the prefix of
// a listing, and not once for everything under the prefix: a closure is a
// few hundred store paths, and the store of a fleet holds every store path
// that was ever rolled out, which a listing would bring a thousand at a
// time.
func absent(ctx context.Context, c *client.Client, items []item) ([]item, error) {
	there := make([]bool, len(items))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(lookups)
	for i, it := range items {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			refs, err := c.List(gctx, it.ref)
			if err != nil {
				return fmt.Errorf("asking the jaccard-store server for the reference %q: %w", it.ref, err)
			}
			// The listing is of every name that begins with this one.
			for _, r := range refs {
				if r.Name == it.ref {
					there[i] = true
					break
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var missing []item
	for i, it := range items {
		if !there[i] {
			missing = append(missing, it)
		}
	}
	return missing, nil
}

// one pushes one store path through a temporary store of its own, which
// it removes however the push ends. It returns the size of the pack that
// was uploaded: 0 when the server had a pack of the same content already.
func one(ctx context.Context, c *client.Client, it item, tempDir string) (size uint64, err error) {
	dir, err := os.MkdirTemp(tempDir, "great-machine-push-*")
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			err = errors.Join(err, fmt.Errorf("removing the temporary store: %w", rerr))
		}
	}()
	// Nothing here outlives the push: waiting for the disk buys nothing.
	objects, err := packstore.Open(filepath.Join(dir, "packstore"), packstore.WithSync(false))
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer objects.Close()

	// The options of jaccard-store's push-dir, but for the ignore files: a
	// store path is pushed whole, whatever a .amberignore in it says.
	built, root, err := ingest.Objects(it.path, ingest.Opts{NoIgnore: true})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}
	_, err = objects.WriteParallel(func(yield func(packstore.Object, error) bool) {
		for o, err := range built {
			// The build knows nothing of ctx: an interrupt reaches it
			// here, between two objects.
			if err == nil {
				err = ctx.Err()
			}
			if !yield(packstore.Object{Key: o.Key, Data: o.Bytes}, err) || err != nil {
				return
			}
		}
	}, packstore.WriteOpts{})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}

	// The pack is built beside the store and goes with it.
	pushed, err := c.Push(ctx, objects, it.ref, *root, client.PushOptions{MinDedup: minDedup, TempDir: dir})
	if err != nil {
		return 0, err
	}
	return pushed.DataSize, nil
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go mod tidy && go vet ./push/ && go test ./push/ -v`
Expected: PASS — `TestPushDirectory`, `TestPushSingleFile`, `TestPushSymbolicLink`, `TestPushAgain`, `TestPushPresentAndMissing`, `TestPushNamesThatBeginAlike`, `TestPushPathThatDoesNotExist`, `TestPushRefusesBeforeDialing`, `TestPushNothing`, `TestPushContextEndedBefore`, in about a second.

After `go mod tidy`, the first `require` block of `go.mod` holds, among the others, these lines without `// indirect`:

```
	github.com/amber-store/core v0.10.0
	github.com/amber-store/jaccard-store v0.7.0
	github.com/tmc/go-iroh v0.3.0
	golang.org/x/sync v0.22.0
```

and the second block has `github.com/johannesboyne/gofakes3 v1.2.0 // indirect`, which the tests need through `buckettest`, and the AWS SDK modules jaccard-store's `bucket` package needs.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum push/
git commit -m "Push: store paths to jaccard-store, the missing ones alone"
```

- [ ] **Step 7: Write the failing tests of `Jobs` and of an interrupt**

The tests look at how many store paths are at work through a hook of the package, `pushing`, which they replace.

Create `push/jobs_test.go`:

```go
package push

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// watch replaces the hook of the package for the length of the test. The
// work on a store path waits in it until hold is closed. watch returns a
// function that says how many are at work now and how many were at most.
func watch(t *testing.T, hold <-chan struct{}) func() (now, most int) {
	t.Helper()
	var mu sync.Mutex
	var now, most int
	old := pushing
	pushing = func(string) func() {
		mu.Lock()
		now++
		most = max(most, now)
		mu.Unlock()
		<-hold
		return func() {
			mu.Lock()
			now--
			mu.Unlock()
		}
	}
	t.Cleanup(func() { pushing = old })
	return func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return now, most
	}
}

// until fails the test unless cond comes true before the deadline.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPushJobs(t *testing.T) {
	tests := []struct {
		jobs, want int
	}{
		{1, 1},
		{2, 2},
		{0, 5}, // the default
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("jobs %d", tt.jobs), func(t *testing.T) {
			srv := startServer(t)
			cfg := srv.config()
			cfg.Jobs = tt.jobs
			paths := storePaths(t, 8)
			hold := make(chan struct{})
			count := watch(t, hold)

			type outcome struct {
				res Result
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := push(t, cfg, paths)
				done <- outcome{res, err}
			}()
			// As many as may be at work are held in the hook. Were more
			// let in, they would be counted there too.
			until(t, fmt.Sprintf("%d store paths are being pushed", tt.want), func() bool {
				now, _ := count()
				return now >= tt.want
			})
			close(hold)
			out := <-done
			if out.err != nil {
				t.Fatalf("Push: %v", out.err)
			}
			if out.res != (Result{Pushed: 8, Present: 0}) {
				t.Errorf("Push = %+v, want eight pushed", out.res)
			}
			if _, most := count(); most != tt.want {
				t.Errorf("%d store paths were pushed at a time, want %d", most, tt.want)
			}
			empty(t, cfg.TempDir)
		})
	}
}

// An interrupt while store paths are being pushed: Push returns soon,
// says why, and leaves nothing in the temporary directory.
func TestPushCancelled(t *testing.T) {
	srv := startServer(t)
	cfg := srv.config()
	cfg.Jobs = 2
	paths := storePaths(t, 6)
	hold := make(chan struct{})
	count := watch(t, hold)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := Push(ctx, cfg, paths)
		done <- outcome{res, err}
	}()
	until(t, "two store paths are being pushed", func() bool {
		now, _ := count()
		return now == 2
	})
	cancel()
	close(hold)

	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) {
			t.Errorf("Push = %+v, %v; want context.Canceled", out.res, out.err)
		}
		if out.res.Pushed != 0 {
			t.Errorf("Push = %+v, want nothing pushed", out.res)
		}
	case <-time.After(testTimeout):
		t.Fatal("Push did not return after its context was cancelled")
	}
	if _, most := count(); most != 2 {
		t.Errorf("%d store paths were begun, want the 2 that were at work", most)
	}
	if got := srv.refs(); len(got) != 0 {
		t.Errorf("the server has the references %q, want none", got)
	}
	empty(t, cfg.TempDir)
}
```

- [ ] **Step 8: Run the tests to see them fail**

Run: `go test ./push/`
Expected: FAIL — the build fails with `undefined: pushing`

- [ ] **Step 9: Add the hook**

Modify `push/push.go` in two places. After the two methods of `silent`, before the type `item`, add:

```go
// pushing is called when the work on one store path begins, and what it
// returns when that work has ended. The tests replace it.
var pushing = func(storePath string) (done func()) { return func() {} }
```

And make the call the first statement of the function `one`, so that the function begins:

```go
func one(ctx context.Context, c *client.Client, it item, tempDir string) (size uint64, err error) {
	defer pushing(it.path)()

	dir, err := os.MkdirTemp(tempDir, "great-machine-push-*")
```

`pushing(it.path)` is called at once, and what it returns when `one` returns: the whole work on a store path, its temporary store included, is between the two.

After the change `push/push.go` is, whole:

```go
// Package push takes store paths to a jaccard-store server: every store
// path becomes the reference prefix+name there, which the sidecar of a pod
// serves as /nix/store/<name>.
//
// A store path never changes under its name, so one whose reference the
// server has is not pushed again. Every other one is imported into a
// temporary store of its own, as `jaccard-store push-dir` imports a
// directory, and pushed with jaccard-store's client.
package push

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sync"

	"github.com/amber-store/core/ingest"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/core/reference"
	"github.com/amber-store/jaccard-store/client"
	jsnode "github.com/amber-store/jaccard-store/node"
	jswire "github.com/amber-store/jaccard-store/wire"
	irohkey "github.com/tmc/go-iroh/key"
	"golang.org/x/sync/errgroup"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// defaultJobs is how many store paths are pushed at a time when Config
	// does not say.
	defaultJobs = 5
	// lookups is how many references the server is asked about at a time.
	lookups = 16
	// minDedup is the share of a store path's bytes a pack on the server
	// must hold for the store path to be pushed as a patch against it: the
	// default of jaccard-store's push-dir.
	minDedup = 0.5
)

// Config says where store paths are pushed to, and how.
type Config struct {
	Key      node.Key         // the operator's
	Server   wire.ID          // the jaccard-store server
	Addrs    []netip.AddrPort // for tests: where the server is
	Prefix   string
	Jobs     int      // 0: 5
	TempDir  string   // "": the system's
	Progress Progress // nil: none
}

// Progress is told what a push does. Its methods are called from several
// goroutines.
type Progress interface {
	// Found says how many of the store paths the server has already, and
	// how many are to be pushed.
	Found(present, missing int)
	// Pushed says that one store path was pushed, and how many bytes its
	// pack came to.
	Pushed(storePath string, bytes uint64)
}

// Result says what a push did.
type Result struct {
	Pushed  int
	Present int
}

// silent is the Progress of a caller that wants none.
type silent struct{}

func (silent) Found(int, int)        {}
func (silent) Pushed(string, uint64) {}

// pushing is called when the work on one store path begins, and what it
// returns when that work has ended. The tests replace it.
var pushing = func(storePath string) (done func()) { return func() {} }

// item is one store path and the reference it is pushed as.
type item struct {
	path string
	ref  string
}

// Push makes the server hold the reference Prefix+name for every store
// path. It ends at the first path that fails, with that path in the error.
//
// The name of a store path is its last element. A store path may be a
// directory or a single regular file, which becomes a reference whose root
// is the file. One that is a symbolic link is refused before anything is
// pushed: the root of a reference cannot be a link, so the sidecar could
// not serve it as one.
//
// The Result counts what was done until Push returned, also when it
// returns an error.
func Push(ctx context.Context, cfg Config, storePaths []string) (Result, error) {
	progress := cfg.Progress
	if progress == nil {
		progress = silent{}
	}
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = defaultJobs
	}

	// Before anything is dialed: a name the server will not take should
	// cost nothing.
	items := make([]item, 0, len(storePaths))
	seen := make(map[string]bool, len(storePaths))
	for _, p := range storePaths {
		ref := cfg.Prefix + filepath.Base(filepath.Clean(p))
		if p == "" {
			return Result{}, errors.New("pushing a store path: its path is empty")
		}
		if err := reference.ValidateName(ref); err != nil {
			return Result{}, fmt.Errorf("pushing %s as the reference %q: %w", p, ref, err)
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		items = append(items, item{path: p, ref: ref})
	}
	if len(items) == 0 {
		progress.Found(0, 0)
		return Result{}, nil
	}

	server, err := irohkey.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server %q: %w", cfg.Server, err)
	}
	conn, err := jsnode.Dial(ctx, jsnode.DialConfig{
		Key:    cfg.Key.Iroh(),
		ALPN:   jswire.ALPN,
		Server: server,
		Addrs:  cfg.Addrs,
	})
	if err != nil {
		return Result{}, fmt.Errorf("the jaccard-store server: %w", err)
	}
	defer conn.Close()
	c := client.New(conn, nil)

	missing, err := absent(ctx, c, items)
	if err != nil {
		return Result{}, err
	}
	res := Result{Present: len(items) - len(missing)}
	for _, it := range missing {
		info, err := os.Lstat(it.path)
		// A path that is not there fails when its turn comes.
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return res, fmt.Errorf("pushing %s: the store path is a symbolic link, which cannot be pushed", it.path)
		}
	}
	progress.Found(res.Present, len(missing))

	var mu sync.Mutex // guards res.Pushed
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(jobs)
	for _, it := range missing {
		// Go waits for a free place. A push that failed meanwhile has
		// ended gctx, and nothing more is begun.
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			size, err := one(gctx, c, it, cfg.TempDir)
			if err != nil {
				return fmt.Errorf("pushing %s: %w", it.path, err)
			}
			mu.Lock()
			res.Pushed++
			mu.Unlock()
			progress.Pushed(it.path, size)
			return nil
		})
	}
	err = g.Wait()
	// The end of the caller's context is the cause of whatever it ended.
	if cerr := ctx.Err(); cerr != nil {
		err = fmt.Errorf("the push was interrupted: %w", cerr)
	}
	return res, err
}

// absent asks the server about the reference of every item and returns
// the items it has no reference for, in their order.
//
// The server is asked once for every name, with the name as the prefix of
// a listing, and not once for everything under the prefix: a closure is a
// few hundred store paths, and the store of a fleet holds every store path
// that was ever rolled out, which a listing would bring a thousand at a
// time.
func absent(ctx context.Context, c *client.Client, items []item) ([]item, error) {
	there := make([]bool, len(items))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(lookups)
	for i, it := range items {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			refs, err := c.List(gctx, it.ref)
			if err != nil {
				return fmt.Errorf("asking the jaccard-store server for the reference %q: %w", it.ref, err)
			}
			// The listing is of every name that begins with this one.
			for _, r := range refs {
				if r.Name == it.ref {
					there[i] = true
					break
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var missing []item
	for i, it := range items {
		if !there[i] {
			missing = append(missing, it)
		}
	}
	return missing, nil
}

// one pushes one store path through a temporary store of its own, which
// it removes however the push ends. It returns the size of the pack that
// was uploaded: 0 when the server had a pack of the same content already.
func one(ctx context.Context, c *client.Client, it item, tempDir string) (size uint64, err error) {
	defer pushing(it.path)()

	dir, err := os.MkdirTemp(tempDir, "great-machine-push-*")
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			err = errors.Join(err, fmt.Errorf("removing the temporary store: %w", rerr))
		}
	}()
	// Nothing here outlives the push: waiting for the disk buys nothing.
	objects, err := packstore.Open(filepath.Join(dir, "packstore"), packstore.WithSync(false))
	if err != nil {
		return 0, fmt.Errorf("temporary store: %w", err)
	}
	defer objects.Close()

	// The options of jaccard-store's push-dir, but for the ignore files: a
	// store path is pushed whole, whatever a .amberignore in it says.
	built, root, err := ingest.Objects(it.path, ingest.Opts{NoIgnore: true})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}
	_, err = objects.WriteParallel(func(yield func(packstore.Object, error) bool) {
		for o, err := range built {
			// The build knows nothing of ctx: an interrupt reaches it
			// here, between two objects.
			if err == nil {
				err = ctx.Err()
			}
			if !yield(packstore.Object{Key: o.Key, Data: o.Bytes}, err) || err != nil {
				return
			}
		}
	}, packstore.WriteOpts{})
	if err != nil {
		return 0, fmt.Errorf("importing: %w", err)
	}

	// The pack is built beside the store and goes with it.
	pushed, err := c.Push(ctx, objects, it.ref, *root, client.PushOptions{MinDedup: minDedup, TempDir: dir})
	if err != nil {
		return 0, err
	}
	return pushed.DataSize, nil
}
```

- [ ] **Step 10: Run the tests to see them pass**

Run: `go vet ./push/ && go test ./push/ -v && go test -race -count=3 ./push/`
Expected: PASS — the tests of Step 5 and `TestPushJobs` (with `jobs_1`, `jobs_2`, `jobs_0`) and `TestPushCancelled`; about 1.5 seconds, and about 7 seconds under `-race`.

- [ ] **Step 11: Commit**

```bash
git add push/
git commit -m "Push: a hook for the tests of --jobs and of an interrupt"
```
