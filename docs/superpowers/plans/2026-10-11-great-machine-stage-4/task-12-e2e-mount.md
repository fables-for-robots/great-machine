### Task 12: `e2e`: a rollout into a store that is really mounted

Every part has its tests by now, and each test has something that stands in for a neighbour: the mount is tested against jaccard-nfs-nix-store's test server, draal's store against a client of the test, the pods of `e2e` against a store that fetches nothing. This task is the one test with nothing standing in: the closure is pushed to draal's own endpoint ID under the prefix of the service; the store of the zathras is the real mount, which pulls from draal's store with a key draal does not know; the rollout prepares by that fetch and activates through a launcher; and the service that runs is the binary of the mount, without the capability the mount was made with.

It needs Linux and root, and runs in the container of `scripts/test-linux.sh`. On a Mac it is not built (the file is `_linux_test.go`), and without root it is skipped.

Two things make it possible with the harness as it is. The store of the pod is mounted **over** the directory that is the Nix store of the tests, after the store path was pushed from it: the path of the binary is then the same on "the laptop" and in "the pod", as `/nix/store/…` is in life. And the test binary is its own `exec-in-place`, as the program is.

**Only one mount can be on a directory, and the tests of a package run one after another**, but packages run at the same time: this test mounts on a directory of its own, never on `/nix/store`.

**Files:**
- Modify: `e2e/doc.go`
- Test: `e2e/main_test.go`, `e2e/mount_linux_test.go`, `e2e/harness_test.go`

**Interfaces:**
- Consumes: the harness with `options{store: true}` and `pushAs` (Task 7); `storemount.Start`, `Preload`, `Stop`, `*storemount.NotFoundError` (Task 10); `launcher.New`, `(*Launcher).Start`, `launcher.Replace` (Task 8); `supervisor.Config.Start`, `ptyshell.Config.Start` (Task 9); `zathras.Preloader` (Task 11); `scripts/test-linux.sh` (Task 8).
- Produces: nothing that other code uses. In the harness, `podStore` and `podStart`, for a pod with a real store; `e2e/main_test.go`, the `TestMain` of the package.

- [ ] **Step 1: Write the failing test: a rollout into a store that is really mounted, from draal's store, started by a launcher**

Create `e2e/main_test.go`:

```go
package e2e

import (
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The test binary is its own exec-in-place, as the program is: a pod
// whose service is started through a launcher starts this binary, which
// then becomes the service.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
```

Create `e2e/mount_linux_test.go`:

```go
package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/storemount"
	"github.com/fables-for-robots/great-machine/wire"
)

// capSysAdmin is the number of CAP_SYS_ADMIN, which a zathras mounts its
// store with and its service is started without.
const capSysAdmin = 21

// statusOf returns the lines of /proc/<pid>/status by their names.
func statusOf(t *testing.T, pid int) map[string]string {
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

// mountedAs returns the type of the file system that is mounted on dir,
// and "" when nothing is.
func mountedAs(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		t.Fatalf("the mounts: %v", err)
	}
	fstype := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == dir {
			fstype = f[2]
		}
	}
	return fstype
}

// The whole of a rollout as a pod has it, without a stand-in between the
// laptop and the service: the closure is pushed to draal's own endpoint
// ID under the prefix of the service; a zathras whose store is a real
// mount, served by this process and fetched from draal's store with a key
// draal does not know, prepares it; and the service is started from the
// mount by a launcher, without the capability the mount was made with.
//
// It needs Linux and root, and runs in the container of
// scripts/test-linux.sh, which is not privileged: that such a container
// can mount what it serves, and execute from it, is part of what the test
// shows.
func TestARolloutIntoAMountedStore(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mounting the store of a zathras takes root and the kernel: scripts/test-linux.sh runs this test")
	}
	h := start(t, options{store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	// The laptop: the store path is pushed for the service, from the
	// directory that is the Nix store of the tests. It is still an
	// ordinary directory here.
	info, err := h.op.Store(ctx, service)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, res, err := h.pushAs(ctx, h.opKey, info.Prefix, filepath.Join(h.storeDir, storeName)); err != nil || res.Pushed != 1 {
		t.Fatalf("pushing the store path: %+v, %v", res, err)
	}

	// The pod: its store is mounted over that same directory, which is
	// where draal takes the binary of a rollout to be. From here on
	// nothing is in it but what the mount has fetched.
	mount, err := storemount.Start(storemount.Config{
		Server:  h.draalKey.ID(),
		Addrs:   []netip.AddrPort{h.addr},
		Service: service,
		Cache:   t.TempDir(),
		Dir:     h.storeDir,
		Log:     quiet,
	})
	if err != nil {
		t.Fatalf("storemount.Start: %v", err)
	}
	// Registered before the pod is started, so that it happens after the
	// pod has stopped its service: the cleanups run last one first.
	t.Cleanup(func() {
		if err := mount.Stop(); err != nil {
			t.Errorf("stopping the mount: %v", err)
		}
		if fstype := mountedAs(t, h.storeDir); fstype != "" {
			t.Errorf("%s is still mounted on %s after the mount was stopped", fstype, h.storeDir)
		}
	})
	if fstype := mountedAs(t, h.storeDir); fstype != "nfs4" {
		t.Fatalf("%q is mounted on %s, want nfs4", fstype, h.storeDir)
	}
	if entries, err := os.ReadDir(h.storeDir); err != nil || len(entries) != 0 {
		t.Fatalf("the store of the pod lists %d entries (%v) before anything was fetched, want none", len(entries), err)
	}

	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)
	h.podStore, h.podStart = mount, l.Start
	p := h.startPod("web-0")
	h.pods = append(h.pods, p)
	h.fleetWhere("the zathras to register", func(f wire.Fleet) bool { return len(zathrasesOf(f)) == 1 })

	// The rollout: prepared by a fetch from draal's store, activated by
	// the launcher.
	rolled := h.rollout()
	fleet := h.await(rolled, wire.RolloutCompleted)
	z, ok := zathrasOf(fleet, p.id)
	if !ok || z.State.Phase != wire.PhaseRunning || z.State.PID == 0 {
		t.Fatalf("the zathras is %+v after the rollout completed, want its service running", z)
	}
	pid := z.State.PID

	// What runs is the binary of the mount, and no copy of it.
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err != nil || exe != h.binary {
		t.Errorf("process %d runs %q (%v), want %s", pid, exe, err, h.binary)
	}
	if entries, err := os.ReadDir(h.storeDir); err != nil || len(entries) != 1 || entries[0].Name() != storeName {
		t.Errorf("the store of the pod lists %v (%v), want the one store path that was fetched", entries, err)
	}
	// It was started without the capability this process mounted with,
	// and cannot gain it.
	status := statusOf(t, pid)
	for _, set := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		bits, err := strconv.ParseUint(status[set], 16, 64)
		if err != nil {
			t.Fatalf("%s of the service is %q: %v", set, status[set], err)
		}
		if bits&(1<<capSysAdmin) != 0 {
			t.Errorf("%s of the service has CAP_SYS_ADMIN: %s", set, status[set])
		}
	}
	if status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the service is %q, want 1", status["NoNewPrivs"])
	}

	// Another service has a store of its own in draal's, in which the
	// store path of this one is not: its pod is told so, by name. That
	// its mount can be made at all, now that there is a launcher, shows
	// that the launcher gave the capability up for its own thread alone.
	other, err := storemount.Start(storemount.Config{
		Server:  h.draalKey.ID(),
		Addrs:   []netip.AddrPort{h.addr},
		Service: "api",
		Cache:   t.TempDir(),
		Dir:     t.TempDir(),
		Log:     quiet,
	})
	if err != nil {
		t.Fatalf("the mount of another service: %v", err)
	}
	defer other.Stop()
	err = other.Preload(ctx, []string{storeName})
	var missing *storemount.NotFoundError
	if !errors.As(err, &missing) || missing.Service != "api" || missing.Path != "/nix/store/"+storeName {
		t.Errorf("the pod of another service was told %v, want that the store has no %s for the service api", err, storeName)
	}
}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `scripts/test-linux.sh -run TestARolloutIntoAMountedStore -v ./e2e/`

Expected: it fails, with

```text
e2e/mount_linux_test.go:122:4: h.podStore undefined (type *harness has no field or method podStore)
```

- [ ] **Step 3: Write the code: a pod of the harness can have a real store and a launcher; the test binary of e2e is its own exec-in-place**

Change `e2e/harness_test.go`:

```diff
--- a/e2e/harness_test.go
+++ b/e2e/harness_test.go
@@ -11,6 +11,7 @@ import (
 	"net/http/httptest"
 	"net/netip"
 	"os"
+	"os/exec"
 	"path/filepath"
 	"slices"
 	"strings"
@@ -74,6 +75,12 @@ type harness struct {
 	opKey    node.Key
 	pods     []*pod
 	op       *operator.Client
+	// podStore and podStart are what a pod that is started from now on
+	// fetches a closure with and starts its processes with. nil: the pod
+	// itself, which fetches nothing, and a start as it is. A test whose
+	// pod has a real store sets them (mount_linux_test.go).
+	podStore zathras.Preloader
+	podStart func(cmd *exec.Cmd, start func() error) error
 
 	mu        sync.Mutex
 	addr      netip.AddrPort     // where draal listens now
@@ -283,8 +290,13 @@ func (h *harness) startPod(name string) *pod {
 		h.t.Fatalf("NewKey: %v", err)
 	}
 	p := &pod{name: name, id: key.ID(), ring: logring.New(400, 16<<10)}
+	var store zathras.Preloader = p
+	if h.podStore != nil {
+		store = h.podStore
+	}
 	p.sup = supervisor.New(supervisor.Config{
 		Ring:        p.ring,
+		Start:       h.podStart,
 		StopTimeout: 5 * time.Second,
 		// A service of these tests that ends has ended quickly, however
 		// long the machine took to start it.
@@ -315,8 +327,8 @@ func (h *harness) startPod(name string) *pod {
 		},
 		Supervisor:  p.sup,
 		Ring:        p.ring,
-		Preload:     p,
-		Shell:       ptyshell.Config{},
+		Preload:     store,
+		Shell:       ptyshell.Config{Start: h.podStart},
 		ReportEvery: 50 * time.Millisecond,
 		RedialMin:   50 * time.Millisecond,
 		RedialMax:   200 * time.Millisecond,
```

- [ ] **Step 4: Run it, and see it pass**

Run: `scripts/test-linux.sh -run TestARolloutIntoAMountedStore -v ./e2e/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add e2e/main_test.go e2e/mount_linux_test.go e2e/harness_test.go
git commit -m 'E2E: a rollout into a store that is really mounted, from draal'\''s store, started by a launcher'
```

- [ ] **Step 6: Write the code: what the package e2e says of its stand-ins, and of the test that has none**

Change `e2e/doc.go`:

```diff
--- a/e2e/doc.go
+++ b/e2e/doc.go
@@ -5,5 +5,12 @@
 // nothing and notes what it was asked to fetch, the second is a temporary
 // directory that draal is pointed at with Config.StoreDir.
 //
+// A test that asks for it has draal's store as well, over a bucket in
+// memory and under draal's own endpoint ID (store_test.go). And one test
+// has no stand-in at all: on Linux, as root, the store of its zathras is
+// mounted over that directory and fetched from draal's store, and its
+// service is started by a launcher (mount_linux_test.go). The test binary
+// is therefore its own exec-in-place (main_test.go).
+//
 // The package has no code but its tests.
 package e2e
```

- [ ] **Step 7: Run it, and see it pass**

Run: `go vet ./e2e/ && CGO_ENABLED=0 GOOS=linux go vet ./e2e/`

Expected: nothing: a comment alone changed.

- [ ] **Step 8: Commit**

```bash
git add e2e/doc.go
git commit -m 'E2E: what the package e2e says of its stand-ins, and of the test that has none'
```

- [ ] **Step 9: The whole, here and on Linux as root**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
scripts/test-linux.sh
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 33 packages that have tests, both times; and nothing from `git status`. The run on Linux is the one that counts for what this task changed: on a Mac the tests that mount, and those of what the launcher's thread gives up, skip themselves.
