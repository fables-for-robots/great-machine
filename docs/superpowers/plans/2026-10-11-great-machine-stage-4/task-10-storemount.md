### Task 10: `storemount`: the store of a zathras

jaccard-nfs-nix-store's `sidecar` package is that project's command without its flags. This package runs it inside zathras for one service: with the prefix of the service, a key of its own, the NFS server on a loopback port the system picks, mounted on `/nix/store`, and no preload endpoint. `Preload` is the call of Task 1.

It is for Linux alone: Buildbarn's NFS server, which the sidecar is made of, does not build for macOS, and the mount is the kernel's. On another system `Start` fails and names `--no-store`.

**This is the task whose tests show the two things the stage stands on**: that a process which is not privileged, with `CAP_SYS_ADMIN`, mounts what it serves (the tests run through `scripts/test-linux.sh`, whose container is that), and that a program is started from the mount by the process that serves it, through a launcher, while the garbage collector runs without pause, 300 times, every other time with a working directory in the mount, with nothing waiting for ever.

**The module comes to need jaccard-nfs-nix-store here**, at v0.3.0 (Task 1). If that release has not been made yet, a pushed commit of its branch does until it has (`go get github.com/amber-store/jaccard-nfs-nix-store@<commit>`), and the tag before this stage is merged; say so in the ledger.

**Files:**
- Create: `storemount/storemount.go`, `storemount/storemount_other.go`, `storemount/storemount_linux.go`
- Modify: `go.mod`
- Test: `storemount/storemount_other_test.go`, `storemount/storemount_test.go`, `storemount/storemount_linux_test.go`

**Interfaces:**
- Consumes: of jaccard-nfs-nix-store v0.3.0: `sidecar.Start`, `sidecar.Config`, `(*Sidecar).Preload`, `Stop`, `Addr`, `sidecar.ErrStopped`, `sidecar.DefaultMountOptions`, `refs.Connect`, `refs.Conn`, `refs.ErrNotFound`, `preload.Failure`, and in the tests `jstest`, a jaccard-store server in one process; `wire.StorePrefix` (Task 3); `node.NewKey` and `Key.Iroh`; in the tests `launcher` (Task 8) and `scripts/test-linux.sh`.
- Produces, in package `storemount`:
  - `const DefaultDir = "/nix/store"`.
  - `type Config struct { Server wire.ID; Addrs []netip.AddrPort; Service string; Cache string; Dir string; Log *slog.Logger }`: `Server`, `Service` and `Cache` are required; `Addrs` is for tests.
  - `func Start(cfg Config) (*Mount, error)`. It dials nobody. When the container does not let it mount, the error says what the container needs: `CAP_SYS_ADMIN` and, where there is AppArmor, the profile `Unconfined`, or `--no-store`. Whatever it fails with, nothing is left mounted or listening.
  - `func (m *Mount) Preload(ctx context.Context, names []string) error`: the names of store paths, without `/nix/store/`. A `*storemount.NotFoundError{Path, Service}` for a store path the store has none of for the service, whose text is `the store has no <path> for the service <service>: it holds what the rollouts of <service> have pushed`; otherwise `<path> could not be fetched from the store: <why>`.
  - `func (m *Mount) Stop() error`.

- [ ] **Step 1: Write the failing test: what a mount is made of, and a system that is not Linux**

Create `storemount/storemount_other_test.go`:

```go
//go:build !linux

package storemount

import (
	"strings"
	"testing"
)

// Where the store cannot be served and mounted, Start says so and names
// the way for such a system.
func TestStartOnASystemThatIsNotLinux(t *testing.T) {
	m, err := Start(Config{Server: draal, Service: "web", Cache: t.TempDir(), Dir: t.TempDir()})
	if err == nil {
		m.Stop()
		t.Fatal("Start() = nil on a system that is not Linux")
	}
	for _, word := range []string{"Linux", "--no-store"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
}
```

Create `storemount/storemount_test.go`:

```go
package storemount

import (
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/wire"
)

const draal = wire.ID("d5aa1000000000000000000000000000000000000000000000000000000000d5")

// What a mount cannot be made of is said before anything is opened.
func TestWhatAMountIsMadeOf(t *testing.T) {
	good := Config{Server: draal, Service: "web", Cache: "/var/cache/great-machine"}
	if err := good.check(); err != nil {
		t.Errorf("a config with a server, a service and a cache: %v", err)
	}
	for name, c := range map[string]struct {
		change func(*Config)
		says   string
	}{
		"no server":                 {func(c *Config) { c.Server = "" }, "server"},
		"no service":                {func(c *Config) { c.Service = "" }, "service"},
		"a service that is no name": {func(c *Config) { c.Service = "Web/1" }, `"Web/1"`},
		"no cache":                  {func(c *Config) { c.Cache = "" }, "cache"},
	} {
		cfg := good
		c.change(&cfg)
		err := cfg.check()
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: check() = %v, want an error that says %s", name, err, c.says)
		}
	}
}

// What the error of a store path the store does not have says: the path,
// and the service whose store it is not in.
func TestNotFoundError(t *testing.T) {
	err := &NotFoundError{Path: "/nix/store/abc-hello-2.12", Service: "web"}
	for _, word := range []string{"/nix/store/abc-hello-2.12", "the service web"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./storemount/`

Expected: it fails, with

```text
storemount/storemount_other_test.go:13:12: undefined: Start
```

- [ ] **Step 3: Write the code: storemount.Config and what a mount says on a system that is not Linux**

Create `storemount/storemount.go`:

```go
// Package storemount is the store of a zathras: jaccard-nfs-nix-store's
// sidecar in this process. It serves the store paths of one service over
// NFS, on a port of the loopback interface, and mounts that on
// /nix/store: a store path is fetched from draal's store the first time
// it is named, or ahead of that with Preload.
//
// The server and the mount are for Linux alone. On another system Start
// fails, and a zathras runs with --no-store.
//
// The process that serves the store executes nothing from it itself: see
// package launcher.
package storemount

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/fables-for-robots/great-machine/wire"
)

// DefaultDir is where the store is mounted unless Config says otherwise.
const DefaultDir = "/nix/store"

// Config is what a mount is made of.
type Config struct {
	// Server is the endpoint ID of draal, which is the store's. Required.
	Server wire.ID
	// Addrs is for tests: where the server is. It is then dialed there,
	// and looked up nowhere.
	Addrs []netip.AddrPort
	// Service is the service of the zathras: its prefix in the store is
	// what the mount is made of. Required.
	Service string
	// Cache is the directory of what was fetched. It is of one server and
	// one service: with another, Start refuses it. Required.
	Cache string
	// Dir is the directory the store is mounted on, which is made if it
	// is not there. "": DefaultDir.
	Dir string
	// Log is where the mount logs. nil: slog.Default().
	Log *slog.Logger
}

// check returns why a mount cannot be made of cfg, or nil.
func (cfg Config) check() error {
	if cfg.Server == "" {
		return errors.New("storemount: no server: the endpoint ID of draal is needed")
	}
	if cfg.Service == "" {
		return errors.New("storemount: no service")
	}
	if err := wire.ValidService(cfg.Service); err != nil {
		return fmt.Errorf("storemount: %w", err)
	}
	if cfg.Cache == "" {
		return errors.New("storemount: no directory for the cache")
	}
	return nil
}

// NotFoundError is the error of Preload for a store path that the store
// has none of under the prefix of the service.
type NotFoundError struct {
	Path    string // of the store path: /nix/store/<name>
	Service string
}

// Error says which store path the store does not have, and for which
// service: a store path is in the store of a service when a rollout of
// that service has pushed it.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("the store has no %s for the service %s: it holds what the rollouts of %s have pushed",
		e.Path, e.Service, e.Service)
}
```

Create `storemount/storemount_other.go`:

```go
//go:build !linux

package storemount

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

// Mount is the store of a zathras, served and mounted. There is none on
// this system.
type Mount struct{}

// Start fails: the store of a zathras is served and mounted on Linux
// alone.
func Start(cfg Config) (*Mount, error) {
	return nil, fmt.Errorf("storemount: the store of a zathras is served and mounted on Linux alone, and this is %s: --no-store runs a zathras without a store of its own", runtime.GOOS)
}

// Preload fails: there is no mount.
func (m *Mount) Preload(ctx context.Context, names []string) error {
	return errors.ErrUnsupported
}

// Stop does nothing: there is no mount.
func (m *Mount) Stop() error { return nil }
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./storemount/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add storemount/storemount_other_test.go storemount/storemount_test.go storemount/storemount.go storemount/storemount_other.go
git commit -m 'Mount: what a mount is made of, and a system that is not Linux'
```

- [ ] **Step 6: Write the failing test: the store of a service mounted, preloaded, started from, and what it leaves**

The tests of the mount are jaccard-nfs-nix-store's `sidecar` against its test server, so this is where the module comes to need it, at the release of Task 1:

```bash
go get github.com/amber-store/jaccard-nfs-nix-store@v0.3.0
```

`go.mod` gets the one line, `github.com/amber-store/jaccard-nfs-nix-store v0.3.0`, and `go.sum` its sums. If the release has not been made yet, the pushed commit of its branch does until Task 13: `go get github.com/amber-store/jaccard-nfs-nix-store@<commit>`; write the commit into the ledger. **No `replace`**: neither `scripts/test-linux.sh`, which mounts this repository alone, nor Nix, nor CI can follow one to a directory.

Create `storemount/storemount_linux_test.go`:

```go
package storemount

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amber-store/jaccard-nfs-nix-store/jstest"
	jmount "github.com/amber-store/jaccard-nfs-nix-store/mount"
	"github.com/amber-store/jaccard-nfs-nix-store/refs"
	jnode "github.com/amber-store/jaccard-store/node"
	"golang.org/x/sys/unix"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/push"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// childEnv, when it is set, makes the test binary the process of
// TestWithoutTheCapabilityTheMountCannotBeMade.
const childEnv = "STOREMOUNT_TEST_CHILD"

// The test binary is started through a launcher by these tests, and then
// replaces itself as great-machine does.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	if os.Getenv(childEnv) != "" {
		os.Exit(startWithoutTheCapability())
	}
	os.Exit(m.Run())
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// needsRoot skips a test that mounts in a process that is not root.
func needsRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("mounting needs root: scripts/test-linux.sh runs this as root, in a container")
	}
}

// store is the store of a test: a jaccard-store server in this process,
// and how it is reached.
type store struct {
	t     *testing.T
	id    wire.ID
	addrs []netip.AddrPort
	key   node.Key // of whoever pushes
}

func newStore(t *testing.T) *store {
	t.Helper()
	dial := jstest.Start(t).DialConfig()
	key, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return &store{t: t, id: wire.ID(dial.Server.String()), addrs: dial.Addrs, key: key}
}

// push pushes store paths for a service, as a rollout of the service does.
func (s *store) push(service string, paths ...string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := push.Push(ctx, push.Config{
		Key: s.key, Server: s.id, Addrs: s.addrs,
		Prefix: wire.StorePrefix(service), TempDir: s.t.TempDir(),
	}, paths)
	if err != nil {
		s.t.Fatalf("pushing %q for %s: %v", paths, service, err)
	}
}

// mount starts a mount of the store for the service. It is stopped when
// the test ends, unless the test has stopped it.
func (s *store) mount(service, dir, cache string) *Mount {
	s.t.Helper()
	// Whatever becomes of the test, no mount is left for the removal of
	// the directory to walk into.
	s.t.Cleanup(func() { jmount.Detach(dir) })
	m, err := Start(Config{Server: s.id, Addrs: s.addrs, Service: service, Cache: cache, Dir: dir, Log: quiet})
	if err != nil {
		s.t.Fatalf("Start: %v", err)
	}
	s.t.Cleanup(func() { m.Stop() })
	return m
}

// storePath writes a store path that is a directory, with its name in the
// file share/name, and returns its path.
func storePath(t *testing.T, dir, name string) string {
	t.Helper()
	file := filepath.Join(dir, name, "share", "name")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

// names returns the names in dir, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

// mounted reports whether something is mounted on dir.
func mounted(t *testing.T, dir string) bool {
	t.Helper()
	_, is, err := jmount.Mounted(dir)
	if err != nil {
		t.Fatal(err)
	}
	return is
}

// A store path is fetched the first time it is named, and not before.
func TestAStorePathIsFetchedWhenItIsNamed(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	src := t.TempDir()
	s.push("web", storePath(t, src, "aaa-one-1.0"), storePath(t, src, "bbb-two-2.0"))

	dir := t.TempDir()
	s.mount("web", dir, t.TempDir())
	if !mounted(t, dir) {
		t.Fatal("after Start nothing is mounted on the directory")
	}
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("the store holds %q before anything was named", got)
	}
	content, err := os.ReadFile(filepath.Join(dir, "aaa-one-1.0", "share", "name"))
	if err != nil || string(content) != "aaa-one-1.0\n" {
		t.Fatalf("the file of a store path that was named: %q, %v", content, err)
	}
	if got, want := names(t, dir), []string{"aaa-one-1.0"}; !slices.Equal(got, want) {
		t.Errorf("the store holds %q, want %q: what was named and nothing else", got, want)
	}
}

// Preload fetches the store paths of a list before anything names them,
// and fails on the first the store does not have.
func TestPreload(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	src := t.TempDir()
	all := []string{"aaa-one-1.0", "bbb-two-2.0", "ccc-three-3.0"}
	for _, name := range all {
		s.push("web", storePath(t, src, name))
	}
	dir := t.TempDir()
	m := s.mount("web", dir, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := m.Preload(ctx, nil); err != nil {
		t.Errorf("a preload of nothing: %v", err)
	}
	if err := m.Preload(ctx, all[:2]); err != nil {
		t.Fatalf("Preload: %v", err)
	}
	// Nobody has looked one of them up: the store lists what was fetched.
	if got := names(t, dir); !slices.Equal(got, all[:2]) {
		t.Fatalf("after the preload the store holds %q, want %q", got, all[:2])
	}
	for _, name := range all[:2] {
		if content, err := os.ReadFile(filepath.Join(dir, name, "share", "name")); err != nil || string(content) != name+"\n" {
			t.Errorf("%s: %q, %v", name, content, err)
		}
	}

	// One that the store does not have ends the preload, and is named
	// with the service whose store it is not in.
	err := m.Preload(ctx, []string{all[2], "zzz-missing-0.0"})
	var notFound *NotFoundError
	if !errors.As(err, &notFound) || notFound.Path != "/nix/store/zzz-missing-0.0" || notFound.Service != "web" {
		t.Fatalf("a preload with a path the store does not have = %v, want a *NotFoundError of the path and the service", err)
	}

	// A preload ends with its context.
	done, giveUp := context.WithCancel(ctx)
	giveUp()
	if err := m.Preload(done, []string{"yyy-never-0.0"}); !errors.Is(err, context.Canceled) {
		t.Errorf("a preload whose context has ended = %v, want the context's error", err)
	}
}

// A store path that could not be fetched is no store path that the store
// does not have: the error says which, and why.
func TestAStorePathThatCouldNotBeFetched(t *testing.T) {
	needsRoot(t)
	reach, wait := connect, pullTimeout
	connect = func(context.Context, jnode.DialConfig) (refs.Conn, error) {
		return nil, errors.New("no way to the store")
	}
	pullTimeout = 300 * time.Millisecond
	t.Cleanup(func() { connect, pullTimeout = reach, wait })

	dir := t.TempDir()
	t.Cleanup(func() { jmount.Detach(dir) })
	m, err := Start(Config{Server: anID(t), Service: "web", Cache: t.TempDir(), Dir: dir, Log: quiet})
	if err != nil {
		t.Fatalf("Start without a store that can be reached: %v: the store is needed when a path is named, and not before", err)
	}
	defer m.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err = m.Preload(ctx, []string{"aaa-one-1.0"})
	var notFound *NotFoundError
	if err == nil || errors.As(err, &notFound) {
		t.Fatalf("Preload = %v, want an error that is no *NotFoundError", err)
	}
	for _, word := range []string{"/nix/store/aaa-one-1.0", "could not be fetched", "no way to the store"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
}

// A store path that is a single file, as a rollout pushes one, is served
// as that file, with its content. It is served with an execute bit
// whether it had one or not: the store keeps the mode of a file with the
// directory the file is in, and a store path that is a file is in none.
func TestAStorePathThatIsASingleFile(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	src := t.TempDir()
	script := filepath.Join(src, "ddd-script.sh")
	body := []byte("#!/bin/sh\necho a store path that is one file\n")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(src, "eee-notes.txt")
	if err := os.WriteFile(plain, []byte("no program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.push("web", script, plain)

	dir := t.TempDir()
	m := s.mount("web", dir, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// One is preloaded, and the other fetched when it is named.
	if err := m.Preload(ctx, []string{"ddd-script.sh"}); err != nil {
		t.Fatalf("Preload of a store path that is a file: %v", err)
	}
	for name, want := range map[string][]byte{"ddd-script.sh": body, "eee-notes.txt": []byte("no program\n")} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o555 {
			t.Errorf("%s is served with the mode %v, want a regular file that all may read and execute", name, info.Mode())
		}
		if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("the content of %s is %q, %v", name, got, err)
		}
	}
	// Executed by a shell: this process is the server, and executes
	// nothing from its own mount itself.
	out, err := exec.Command("/bin/sh", "-c", `exec "$0"`, filepath.Join(dir, "ddd-script.sh")).CombinedOutput()
	if err != nil || string(out) != "a store path that is one file\n" {
		t.Errorf("executed, the file printed %q, %v", out, err)
	}
}

// The store of a pod is made of what was pushed for its service: a store
// path of another service alone is not in it.
func TestAStorePathOfAnotherServiceIsNotInTheStore(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	src := t.TempDir()
	s.push("web", storePath(t, src, "aaa-one-1.0"))
	s.push("worker", storePath(t, src, "www-of-the-worker-1.0"))

	dir := t.TempDir()
	m := s.mount("web", dir, t.TempDir())
	if _, err := os.Stat(filepath.Join(dir, "www-of-the-worker-1.0")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a store path of another service: %v, want it not to be there", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := m.Preload(ctx, []string{"aaa-one-1.0", "www-of-the-worker-1.0"})
	var notFound *NotFoundError
	if !errors.As(err, &notFound) || notFound.Path != "/nix/store/www-of-the-worker-1.0" || notFound.Service != "web" {
		t.Errorf("a preload of a store path of another service = %v, want a *NotFoundError of the path and of web", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "aaa-one-1.0", "share", "name")); err != nil {
		t.Errorf("the store path of the service itself: %v", err)
	}
}

// Stop leaves nothing mounted, and a cache is of one service: started
// for another on the same cache, a mount is refused, and leaves nothing
// either.
func TestStopAndACacheOfAnotherService(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	src := t.TempDir()
	s.push("web", storePath(t, src, "aaa-one-1.0"))
	dir, cache := t.TempDir(), t.TempDir()
	m := s.mount("web", dir, cache)
	if _, err := os.Stat(filepath.Join(dir, "aaa-one-1.0")); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if mounted(t, dir) {
		t.Fatal("after Stop the store is still mounted")
	}

	other, err := Start(Config{Server: s.id, Addrs: s.addrs, Service: "worker", Cache: cache, Dir: dir, Log: quiet})
	if err == nil {
		other.Stop()
		t.Fatal("a mount for another service was started on the cache of the first")
	}
	for _, word := range []string{wire.StorePrefix("web"), wire.StorePrefix("worker"), "a cache is of one server and one prefix"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal does not say %q: %v", word, err)
		}
	}
	if mounted(t, dir) {
		t.Error("the mount that was refused left something mounted")
	}

	// The cache of the service itself is taken again, with what it holds:
	// the store path is there without the store being asked.
	again := s.mount("web", dir, cache)
	if got, want := names(t, dir), []string{"aaa-one-1.0"}; !slices.Equal(got, want) {
		t.Errorf("started again on its cache, the store holds %q, want %q", got, want)
	}
	if err := again.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// The process that serves the store starts programs from it, through a
// launcher: the service, of all things, is one. The process that is
// started stands for a moment between the two, and the collector of this
// process must not be held by it. With an empty cache, where the start is
// what fetches the store path, and three hundred times more while the
// collector runs without pause.
func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
	needsRoot(t)
	if mounted(t, DefaultDir) {
		t.Skipf("something is mounted on %s already", DefaultDir)
	}
	s := newStore(t)
	src := t.TempDir()
	const name = "ppp-testservice-1.0"
	bin := filepath.Join(src, name, "bin", "testservice")
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
	s.push("web", filepath.Join(src, name))

	// On /nix/store itself, with the directory made by Start.
	m := s.mount("web", "", t.TempDir())
	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	defer l.Close()
	inStore := filepath.Join(DefaultDir, name, "bin", "testservice")
	start := func(text string) (string, error) {
		cmd := exec.Command(inStore, "--stdout", text, "--exit-after", "0")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := l.Start(cmd, cmd.Start); err != nil {
			return "", err
		}
		err := cmd.Wait()
		return out.String(), err
	}

	began := time.Now()
	out, err := start("from an empty cache")
	if err != nil || out != "from an empty cache\n" {
		t.Fatalf("started with an empty cache, the program wrote %q, %v", out, err)
	}
	t.Logf("the start with an empty cache took %s", time.Since(began))

	var collections atomic.Int64
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
				collections.Add(1)
			}
		}
	}()
	var longest time.Duration
	for i := range 300 {
		began := time.Now()
		text := "start " + strconv.Itoa(i)
		out, err := start(text)
		if err != nil || out != text+"\n" {
			t.Fatalf("start %d wrote %q, %v", i, out, err)
		}
		longest = max(longest, time.Since(began))
	}
	close(stop)
	<-stopped
	t.Logf("300 starts under %d collections, the longest %s", collections.Load(), longest)
	// A start that is held is held until the mount gives up, minutes on.
	if longest > 30*time.Second {
		t.Errorf("the longest start took %s: the process stood still", longest)
	}
	if err := m.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if mounted(t, DefaultDir) {
		t.Errorf("after Stop the store is still mounted on %s", DefaultDir)
	}
}

// startWithoutTheCapability is the test binary as a process that was
// started through a launcher, and so has no CAP_SYS_ADMIN: it starts a
// mount, and writes what came of it and what it left.
func startWithoutTheCapability() int {
	cache, err := os.MkdirTemp("", "storemount-cache-*")
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer os.RemoveAll(cache)
	dir, err := os.MkdirTemp("", "storemount-dir-*")
	if err != nil {
		fmt.Println(err)
		return 1
	}
	defer os.RemoveAll(dir)
	// A store that is never dialed: the mount does not get that far.
	key, err := node.NewKey()
	if err != nil {
		fmt.Println(err)
		return 1
	}
	m, err := Start(Config{Server: key.ID(), Service: "web", Cache: cache, Dir: dir, Log: quiet})
	if err == nil {
		m.Stop()
		fmt.Println("started")
		return 0
	}
	fmt.Println("error:", err)
	_, is, _ := jmount.Mounted(dir)
	fmt.Println("mounted:", is)
	// What is left of the server: a socket of this process that listens.
	listening := 0
	fds, _ := os.ReadDir("/proc/self/fd")
	for _, fd := range fds {
		n, err := strconv.Atoi(fd.Name())
		if err != nil {
			continue
		}
		if accepts, err := unix.GetsockoptInt(n, unix.SOL_SOCKET, unix.SO_ACCEPTCONN); err == nil && accepts == 1 {
			listening++
		}
	}
	fmt.Println("listening:", listening)
	return 0
}

// A container that does not let a zathras mount is the first thing that
// goes wrong with a pod of one container. The zathras then says what the
// container needs, and leaves nothing behind: no mount, and no server.
func TestWithoutTheCapabilityTheMountCannotBeMade(t *testing.T) {
	needsRoot(t)
	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	defer l.Close()
	// What a launcher starts has no CAP_SYS_ADMIN.
	cmd := exec.Command("/proc/self/exe")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := l.Start(cmd, cmd.Start); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the process without the capability: %v: %s", err, out.String())
	}
	said := out.String()
	t.Logf("without the capability: %s", said)
	if !strings.HasPrefix(said, "error: ") {
		t.Fatalf("a mount was made without CAP_SYS_ADMIN: %s", said)
	}
	for _, word := range []string{"operation not permitted", "CAP_SYS_ADMIN", "AppArmor", "Unconfined", "--no-store", "mounted: false", "listening: 0"} {
		if !strings.Contains(said, word) {
			t.Errorf("the process does not say %q: %s", word, said)
		}
	}
}
```

Change `storemount/storemount_other_test.go`:

```diff
--- a/storemount/storemount_other_test.go
+++ b/storemount/storemount_other_test.go
@@ -10,7 +10,7 @@ import (
 // Where the store cannot be served and mounted, Start says so and names
 // the way for such a system.
 func TestStartOnASystemThatIsNotLinux(t *testing.T) {
-	m, err := Start(Config{Server: draal, Service: "web", Cache: t.TempDir(), Dir: t.TempDir()})
+	m, err := Start(Config{Server: anID(t), Service: "web", Cache: t.TempDir(), Dir: t.TempDir()})
 	if err == nil {
 		m.Stop()
 		t.Fatal("Start() = nil on a system that is not Linux")
```

Change `storemount/storemount_test.go`:

```diff
--- a/storemount/storemount_test.go
+++ b/storemount/storemount_test.go
@@ -4,14 +4,24 @@ import (
 	"strings"
 	"testing"
 
+	"github.com/fables-for-robots/great-machine/node"
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
-const draal = wire.ID("d5aa1000000000000000000000000000000000000000000000000000000000d5")
+// anID returns the endpoint ID of a key that is made for the test: one a
+// store could have.
+func anID(t *testing.T) wire.ID {
+	t.Helper()
+	key, err := node.NewKey()
+	if err != nil {
+		t.Fatal(err)
+	}
+	return key.ID()
+}
 
 // What a mount cannot be made of is said before anything is opened.
 func TestWhatAMountIsMadeOf(t *testing.T) {
-	good := Config{Server: draal, Service: "web", Cache: "/var/cache/great-machine"}
+	good := Config{Server: anID(t), Service: "web", Cache: "/var/cache/great-machine"}
 	if err := good.check(); err != nil {
 		t.Errorf("a config with a server, a service and a cache: %v", err)
 	}
@@ -19,10 +29,11 @@ func TestWhatAMountIsMadeOf(t *testing.T) {
 		change func(*Config)
 		says   string
 	}{
-		"no server":                 {func(c *Config) { c.Server = "" }, "server"},
-		"no service":                {func(c *Config) { c.Service = "" }, "service"},
-		"a service that is no name": {func(c *Config) { c.Service = "Web/1" }, `"Web/1"`},
-		"no cache":                  {func(c *Config) { c.Cache = "" }, "cache"},
+		"no server":                       {func(c *Config) { c.Server = "" }, "server"},
+		"a server that is no endpoint ID": {func(c *Config) { c.Server = "d5aa1" }, `"d5aa1"`},
+		"no service":                      {func(c *Config) { c.Service = "" }, "service"},
+		"a service that is no name":       {func(c *Config) { c.Service = "Web/1" }, `"Web/1"`},
+		"no cache":                        {func(c *Config) { c.Cache = "" }, "cache"},
 	} {
 		cfg := good
 		c.change(&cfg)
```

- [ ] **Step 7: Run it, and see it fail**

Run: `scripts/test-linux.sh ./storemount/`

Expected: it fails, with

```text
storemount/storemount_linux_test.go:96:52: undefined: Mount
```

- [ ] **Step 8: Write the code: storemount.Start, Preload and Stop on Linux: the sidecar of jaccard-nfs-nix-store in this process, for one service, mounted**

Change `storemount/storemount.go`:

```diff
--- a/storemount/storemount.go
+++ b/storemount/storemount.go
@@ -17,6 +17,7 @@ import (
 	"log/slog"
 	"net/netip"
 
+	"github.com/fables-for-robots/great-machine/node"
 	"github.com/fables-for-robots/great-machine/wire"
 )
 
@@ -48,6 +49,9 @@ func (cfg Config) check() error {
 	if cfg.Server == "" {
 		return errors.New("storemount: no server: the endpoint ID of draal is needed")
 	}
+	if _, err := node.ParseID(string(cfg.Server)); err != nil {
+		return fmt.Errorf("storemount: the server is no endpoint ID: %w", err)
+	}
 	if cfg.Service == "" {
 		return errors.New("storemount: no service")
 	}
```

Create `storemount/storemount_linux.go`:

```go
package storemount

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/amber-store/jaccard-nfs-nix-store/preload"
	"github.com/amber-store/jaccard-nfs-nix-store/refs"
	"github.com/amber-store/jaccard-nfs-nix-store/sidecar"
	jnode "github.com/amber-store/jaccard-store/node"
	irohkey "github.com/tmc/go-iroh/key"
	"golang.org/x/sys/unix"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// connect dials the store for jaccard-store's protocol. The tests replace
// it.
var connect = refs.Connect

// pullTimeout is how long a fetch that fails is tried again for. 0: what
// jaccard-nfs-nix-store has, 2 minutes. A variable, so that the tests
// make it short.
var pullTimeout time.Duration

// Mount is the store of a zathras, served and mounted.
type Mount struct {
	service string
	sidecar *sidecar.Sidecar
}

// Start makes a key, opens the cache, serves it over NFS on a loopback
// port the system picks, and mounts that on cfg.Dir, which it makes if it
// is not there. When it returns, the directory is the store.
//
// The store is not dialed here: it is needed when a store path is first
// named, and not before.
//
// A container that does not let the process mount makes Start fail with
// what the container needs. Whatever Start fails with, nothing is left
// mounted and nothing is left listening.
func Start(cfg Config) (*Mount, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	dir := cfg.Dir
	if dir == "" {
		dir = DefaultDir
	}
	server, err := irohkey.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return nil, fmt.Errorf("storemount: the server %q is no endpoint ID: %w", cfg.Server, err)
	}
	// A key of the mount's own, new with every start, as the key of the
	// zathras is: the connections of the mount come and go at other times
	// than that of the zathras, and two endpoints with one key evict each
	// other at the relay.
	key, err := node.NewKey()
	if err != nil {
		return nil, fmt.Errorf("storemount: making a key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storemount: the directory of the store: %w", err)
	}
	sc, err := sidecar.Start(sidecar.Config{
		Prefix: wire.StorePrefix(cfg.Service),
		Server: string(cfg.Server),
		Cache:  cfg.Cache,
		Dial: func(ctx context.Context) (refs.Conn, error) {
			return connect(ctx, jnode.DialConfig{Key: key.Iroh(), Server: server, Addrs: cfg.Addrs, Log: log})
		},
		// Loopback keeps the server in the pod, and no port has to be
		// agreed on: the one process serves and mounts.
		Listen:       "127.0.0.1:0",
		Mount:        dir,
		MountOptions: sidecar.DefaultMountOptions,
		PullTimeout:  pullTimeout,
		// No preload endpoint: Preload is called.
		Log: log,
	})
	if err != nil {
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return nil, fmt.Errorf("storemount: %w: the container of a zathras has to let it mount: it needs CAP_SYS_ADMIN and, "+
				"where there is AppArmor, the profile Unconfined, as deploy/pod.yaml has them; "+
				"--no-store runs a zathras without a store of its own", err)
		}
		return nil, fmt.Errorf("storemount: %w", err)
	}
	log.Info("the store is mounted", "on", dir, "service", cfg.Service, "server", string(cfg.Server),
		"nfs", sc.Addr().String(), "cache", cfg.Cache, "client", string(key.ID()))
	return &Mount{service: cfg.Service, sidecar: sc}, nil
}

// Preload fetches the store paths of names, each the name of a store path
// without /nix/store/, 20 at a time. The first that cannot be fetched ends
// the others and is the error: a *NotFoundError for a store path the
// store has none of for the service, and otherwise one that names the
// store path and says why it could not be fetched. It ends with ctx, and
// with the mount.
func (m *Mount) Preload(ctx context.Context, names []string) error {
	err := m.sidecar.Preload(ctx, names)
	var failed *preload.Failure
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, sidecar.ErrStopped):
		return errors.New("the store of the zathras is stopped")
	case errors.As(err, &failed) && errors.Is(failed.Err, refs.ErrNotFound):
		return &NotFoundError{Path: failed.Path(), Service: m.service}
	case errors.As(err, &failed):
		return fmt.Errorf("%s could not be fetched from the store: %w", failed.Path(), failed.Err)
	}
	return fmt.Errorf("fetching from the store: %w", err)
}

// Stop takes the mount away and stops the server, in the order that
// jaccard-nfs-nix-store's Sidecar.Stop keeps: the mount goes while the
// server still answers, and the server goes on until the kernel has let
// go of it.
func (m *Mount) Stop() error {
	if err := m.sidecar.Stop(); err != nil {
		return fmt.Errorf("storemount: %w", err)
	}
	return nil
}
```

And the module is made whole, now that code imports what only the tests did:

```bash
go mod tidy
```

`go.mod` gets the modules that jaccard-nfs-nix-store needs as indirect requirements, Buildbarn's and theirs, some sixty lines, and `go.sum` their sums. Both are committed with this step.

- [ ] **Step 9: Run it, and see it pass**

Run: `scripts/test-linux.sh ./storemount/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 10: Commit**

```bash
git add go.mod storemount/storemount_linux_test.go storemount/storemount_other_test.go storemount/storemount_test.go go.sum storemount/storemount.go storemount/storemount_linux.go
git commit -m 'Mount: the store of a service mounted, preloaded, started from, and what it leaves'
```

- [ ] **Step 11: A test that holds as it is written: /nix/store itself is mounted on only where there is no /nix; the starts from the mount are from a directory of the test**

Change `storemount/storemount_linux_test.go`:

```diff
--- a/storemount/storemount_linux_test.go
+++ b/storemount/storemount_linux_test.go
@@ -97,7 +97,11 @@ func (s *store) mount(service, dir, cache string) *Mount {
 	s.t.Helper()
 	// Whatever becomes of the test, no mount is left for the removal of
 	// the directory to walk into.
-	s.t.Cleanup(func() { jmount.Detach(dir) })
+	on := dir
+	if on == "" {
+		on = DefaultDir
+	}
+	s.t.Cleanup(func() { jmount.Detach(on) })
 	m, err := Start(Config{Server: s.id, Addrs: s.addrs, Service: service, Cache: cache, Dir: dir, Log: quiet})
 	if err != nil {
 		s.t.Fatalf("Start: %v", err)
@@ -374,9 +378,6 @@ func TestStopAndACacheOfAnotherService(t *testing.T) {
 // collector runs without pause.
 func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	needsRoot(t)
-	if mounted(t, DefaultDir) {
-		t.Skipf("something is mounted on %s already", DefaultDir)
-	}
 	s := newStore(t)
 	src := t.TempDir()
 	const name = "ppp-testservice-1.0"
@@ -393,14 +394,14 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	}
 	s.push("web", filepath.Join(src, name))
 
-	// On /nix/store itself, with the directory made by Start.
-	m := s.mount("web", "", t.TempDir())
+	dir := t.TempDir()
+	m := s.mount("web", dir, t.TempDir())
 	l, err := launcher.New(launcher.Config{Log: quiet})
 	if err != nil {
 		t.Fatalf("launcher.New: %v", err)
 	}
 	defer l.Close()
-	inStore := filepath.Join(DefaultDir, name, "bin", "testservice")
+	inStore := filepath.Join(dir, name, "bin", "testservice")
 	start := func(text string) (string, error) {
 		cmd := exec.Command(inStore, "--stdout", text, "--exit-after", "0")
 		var out bytes.Buffer
@@ -453,6 +454,39 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	if err := m.Stop(); err != nil {
 		t.Errorf("Stop: %v", err)
 	}
+	if mounted(t, dir) {
+		t.Error("after Stop the store is still mounted")
+	}
+}
+
+// Unless it is told otherwise the store is mounted on /nix/store, which
+// Start makes if it is not there.
+//
+// On a machine that has a /nix the test would cover its store with one of
+// its own, for every process of the machine. It runs where there is none:
+// in the container of scripts/test-linux.sh. No other test of any package
+// mounts there, since the tests of the packages run at the same time.
+func TestTheStoreIsMountedOnNixStore(t *testing.T) {
+	needsRoot(t)
+	if _, err := os.Lstat("/nix"); err == nil {
+		t.Skip("this machine has a /nix, which the test would mount over: scripts/test-linux.sh runs it in a container that has none")
+	}
+	t.Cleanup(func() {
+		os.Remove(DefaultDir)
+		os.Remove("/nix")
+	})
+	s := newStore(t)
+	s.push("web", storePath(t, t.TempDir(), "aaa-one-1.0"))
+	m := s.mount("web", "", t.TempDir())
+	if !mounted(t, DefaultDir) {
+		t.Fatalf("nothing is mounted on %s", DefaultDir)
+	}
+	if content, err := os.ReadFile(filepath.Join(DefaultDir, "aaa-one-1.0", "share", "name")); err != nil || string(content) != "aaa-one-1.0\n" {
+		t.Errorf("the file of a store path under %s: %q, %v", DefaultDir, content, err)
+	}
+	if err := m.Stop(); err != nil {
+		t.Errorf("Stop: %v", err)
+	}
 	if mounted(t, DefaultDir) {
 		t.Errorf("after Stop the store is still mounted on %s", DefaultDir)
 	}
```

- [ ] **Step 12: Run it, and see it pass**

Run: `scripts/test-linux.sh ./storemount/`

Expected: it passes: a change of the tests alone, in where they mount

- [ ] **Step 13: Commit**

```bash
git add storemount/storemount_linux_test.go
git commit -m 'Mount: /nix/store itself is mounted on only where there is no /nix; the starts from the mount are from a directory of the test'
```

- [ ] **Step 14: A test that holds as it is written: every other start from the mount has its working directory in the store, in a directory the kernel has not been told of**

A review found that a working directory in the store stood the process still as an execution from the store did. Without the launcher's change of Task 8, which has the started process change to it, this test stands still at its first such start until it is killed.

Change `storemount/storemount_linux_test.go`:

```diff
--- a/storemount/storemount_linux_test.go
+++ b/storemount/storemount_linux_test.go
@@ -375,7 +375,12 @@ func TestStopAndACacheOfAnotherService(t *testing.T) {
 // started stands for a moment between the two, and the collector of this
 // process must not be held by it. With an empty cache, where the start is
 // what fetches the store path, and three hundred times more while the
-// collector runs without pause.
+// collector runs without pause, every other time with a working directory
+// that is in the store as well, and one that nothing has looked at yet, so
+// that the kernel has to ask for it: os/exec changes to a working
+// directory between the fork and the execution, which is where nothing may
+// wait for the store, so the launcher has the started process change to
+// it.
 func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	needsRoot(t)
 	s := newStore(t)
@@ -392,6 +397,13 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	if err := os.WriteFile(bin, program, 0o755); err != nil {
 		t.Fatal(err)
 	}
+	// A directory for every start that has its working directory in the
+	// store: the kernel remembers a directory it was told of.
+	for i := range 150 {
+		if err := os.MkdirAll(filepath.Join(src, name, "in", strconv.Itoa(i)), 0o755); err != nil {
+			t.Fatal(err)
+		}
+	}
 	s.push("web", filepath.Join(src, name))
 
 	dir := t.TempDir()
@@ -402,8 +414,9 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	}
 	defer l.Close()
 	inStore := filepath.Join(dir, name, "bin", "testservice")
-	start := func(text string) (string, error) {
+	start := func(text, in string) (string, error) {
 		cmd := exec.Command(inStore, "--stdout", text, "--exit-after", "0")
+		cmd.Dir = in
 		var out bytes.Buffer
 		cmd.Stdout, cmd.Stderr = &out, &out
 		if err := l.Start(cmd, cmd.Start); err != nil {
@@ -414,7 +427,7 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	}
 
 	began := time.Now()
-	out, err := start("from an empty cache")
+	out, err := start("from an empty cache", "")
 	if err != nil || out != "from an empty cache\n" {
 		t.Fatalf("started with an empty cache, the program wrote %q, %v", out, err)
 	}
@@ -438,7 +451,11 @@ func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
 	for i := range 300 {
 		began := time.Now()
 		text := "start " + strconv.Itoa(i)
-		out, err := start(text)
+		in := ""
+		if i%2 == 1 {
+			in = filepath.Join(dir, name, "in", strconv.Itoa(i/2))
+		}
+		out, err := start(text, in)
 		if err != nil || out != text+"\n" {
 			t.Fatalf("start %d wrote %q, %v", i, out, err)
 		}
```

- [ ] **Step 15: Run it, and see it pass**

Run: `scripts/test-linux.sh -run TestAProgramIsStartedFromTheMountByTheProcessThatServesIt -v ./storemount/`

Expected: it passes: with the launcher as Task 8 leaves it. The longest of the 300 starts was between 14 and 26 ms while this plan was written.

- [ ] **Step 16: Commit**

```bash
git add storemount/storemount_linux_test.go
git commit -m 'Mount: every other start from the mount has its working directory in the store, in a directory the kernel has not been told of'
```

- [ ] **Step 17: The whole, here and on Linux as root**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
scripts/test-linux.sh
scripts/test-linux.sh -count=5 ./storemount/
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 34 packages that have tests, both times; `ok` for the five runs of `storemount`; nothing from `git status`. On a Mac the tests of `storemount` are the two that need no mount; on Linux none is skipped but `TestTheStoreIsMountedOnNixStore` where the machine has a `/nix` of its own, which the script's container has not.

**Look at one number**: `scripts/test-linux.sh -run TestAProgramIsStartedFromTheMountByTheProcessThatServesIt -v ./storemount/` logs how long the start with an empty cache took, and the longest of the 300 starts with how many collections ran meanwhile. While this plan was written the first took between 22 and 27 ms, and the longest of the 300 between 9 and 63 ms under some 3,400 collections. A start that takes seconds is the stall this stage is built around (the spec's §13): stop and say so.

- [ ] **Step 18: The program still builds as Nix builds it**

The module has Buildbarn's packages in it from here on, and the program has them on Linux:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version && rm result
```

Expected: the commit. This is the build for the machine it runs on; on a Mac the mount's packages are not in it. **The build for Linux was not run while this plan was written** (Docker's disk had no room for a Nix store): on a Linux machine, or where Nix has a Linux builder, run the same there and note in the ledger that it built, how long it took, and how large `result/bin/great-machine` is. If the release of Task 1 does not exist yet and the module requires a commit, this works all the same; with a `replace` to a directory it cannot, which is why there is none.
