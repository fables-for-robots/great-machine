package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/storedir"
	"github.com/fables-for-robots/great-machine/storeserver"
	"github.com/fables-for-robots/great-machine/wire"
)

// asked is the store of a pod that notes what it is asked to fetch before
// it fetches it.
type asked struct {
	*storedir.Store

	mu    sync.Mutex
	names [][]string
}

func (a *asked) Preload(ctx context.Context, names []string) error {
	a.mu.Lock()
	a.names = append(a.names, slices.Clone(names))
	a.mu.Unlock()
	return a.Store.Preload(ctx, names)
}

// writable makes every directory under dir one that its owner may write
// to. A store path that was written out has directories nobody may write
// to, which the removal of a test's temporary directory stops at.
func writable(dir string) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(path, 0o700)
		}
		return nil
	})
}

// The whole of a rollout as a pod has it, without a stand-in between the
// laptop and the service: the closure is pushed to draal's own endpoint
// ID under the prefix of the service; a zathras whose store is a real
// one, fetched from draal's store with a key draal does not know,
// prepares it; and the service is started from what was written out, by
// a launcher.
//
// It needs neither Linux nor root: the store of a zathras is a directory
// it fills, and nothing is mounted.
func TestARolloutIntoAStoreDirectory(t *testing.T) {
	h := start(t, options{store: true})
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	// Before the directories of the test are removed, and after the pod
	// and its store have ended: the cleanups run last one first.
	t.Cleanup(func() { writable(h.storeDir) })

	// The laptop: the store path is pushed for the service, from the
	// directory that is the Nix store of the tests, and then taken out of
	// it. The directory is the store of the pod from here on, and draal
	// takes the binary of a rollout to be in it: what is in it is what
	// the pod has fetched.
	program, err := os.ReadFile(h.binary)
	if err != nil {
		t.Fatalf("reading the binary: %v", err)
	}
	info, err := h.op.Store(ctx, service)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, res, err := h.pushAs(ctx, h.opKey, info.Prefix, filepath.Join(h.storeDir, storeName)); err != nil || res.Pushed != 1 {
		t.Fatalf("pushing the store path: %+v, %v", res, err)
	}
	if err := os.Rename(filepath.Join(h.storeDir, storeName), filepath.Join(t.TempDir(), storeName)); err != nil {
		t.Fatalf("taking the store path out of the store of the pod: %v", err)
	}

	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}

	// The store of the pod is dialed from the endpoint of its connection
	// to draal, as a zathras dials it: one key, and both protocols.
	var pid int
	var running atomic.Pointer[pod]
	real, err := storedir.Open(storedir.Config{
		Dial: func(ctx context.Context) (node.Opener, error) {
			p := running.Load()
			if p == nil {
				return nil, errors.New("the pod is not there yet")
			}
			p.mu.Lock()
			conn := p.conn
			p.mu.Unlock()
			if conn == nil {
				return nil, errors.New("the pod has no connection to draal")
			}
			return node.Again(ctx, conn, storeserver.ALPN)
		},
		Service: service,
		Cache:   t.TempDir(),
		Dir:     h.storeDir,
		Log:     quiet,
	})
	if err != nil {
		t.Fatalf("storedir.Open: %v", err)
	}
	// Registered before the pod is started, so that it happens after the
	// pod has stopped its service.
	t.Cleanup(func() {
		if pid != 0 && alive(pid) {
			t.Errorf("process %d, the service, still runs when the store is to be closed", pid)
		}
		if err := real.Close(); err != nil {
			t.Errorf("closing the store: %v", err)
		}
	})
	if entries, err := os.ReadDir(h.storeDir); err != nil || len(entries) != 0 {
		t.Fatalf("the store of the pod lists %d entries (%v) before anything was fetched, want none", len(entries), err)
	}
	store := &asked{Store: real}
	h.podStore, h.podStart = store, l.Start
	p := h.startPod("web-0")
	running.Store(p)
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
	pid = z.State.PID

	// The prepare asked the store for the closure of the rollout, by
	// name.
	store.mu.Lock()
	fetched := slices.Clone(store.names)
	store.mu.Unlock()
	if len(fetched) == 0 {
		t.Errorf("the prepare did not ask the store of the pod to fetch anything")
	}
	for _, names := range fetched {
		if !slices.Equal(names, []string{storeName}) {
			t.Errorf("the store of the pod was asked to fetch %q, want the closure of the rollout, %q", names, storeName)
		}
	}

	// What is in the store of the pod is the one store path, as the files
	// of a Nix store, and what runs is its binary.
	if entries, err := os.ReadDir(h.storeDir); err != nil || len(entries) != 1 || entries[0].Name() != storeName {
		t.Errorf("the store of the pod lists %v (%v), want the one store path that was fetched", entries, err)
	}
	for _, dir := range []string{filepath.Join(h.storeDir, storeName), filepath.Dir(h.binary)} {
		if info, err := os.Lstat(dir); err != nil || info.Mode() != os.ModeDir|0o555 {
			t.Errorf("%s: %v, %v, want a directory of mode 0555", dir, info, err)
		}
	}
	if info, err := os.Lstat(h.binary); err != nil || info.Mode() != 0o555 {
		t.Errorf("the binary: %v, %v, want a file of mode 0555", info, err)
	}
	if got, err := os.ReadFile(h.binary); err != nil || !bytes.Equal(got, program) {
		t.Errorf("the binary that was fetched is not the one that was pushed (%v)", err)
	}
	if runtime.GOOS == "linux" {
		if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err != nil || exe != h.binary {
			t.Errorf("process %d runs %q (%v), want %s", pid, exe, err, h.binary)
		}
	}

	// Another service has a store of its own in draal's, in which the
	// store path of this one is not: its pod is told so, by name.
	otherDir := filepath.Join(t.TempDir(), "store")
	elsewhere, err := h.dial(ctx, newKey(t))
	if err != nil {
		t.Fatalf("the connection of another pod to draal: %v", err)
	}
	defer elsewhere.Close()
	other, err := storedir.Open(storedir.Config{
		Dial: func(ctx context.Context) (node.Opener, error) {
			return node.Again(ctx, elsewhere, storeserver.ALPN)
		},
		Service: "api",
		Cache:   t.TempDir(),
		Dir:     otherDir,
		Log:     quiet,
	})
	if err != nil {
		t.Fatalf("the store of another service: %v", err)
	}
	defer other.Close()
	err = other.Preload(ctx, []string{storeName})
	var missing *storedir.NotFoundError
	if !errors.As(err, &missing) || missing.Service != "api" || missing.Path != filepath.Join(otherDir, storeName) {
		t.Errorf("the pod of another service was told %v, want that the store has no %s for the service api", err, storeName)
	}
}
