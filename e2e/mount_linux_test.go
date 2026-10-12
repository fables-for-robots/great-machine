package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// asked is the store of a pod that notes what it is asked to fetch before
// it fetches it. The mount fetches a store path when it is first looked
// up as well, so a prepare that asked for nothing would not show in what
// the mount holds afterwards.
type asked struct {
	*storemount.Mount

	mu    sync.Mutex
	names [][]string
}

func (a *asked) Preload(ctx context.Context, names []string) error {
	a.mu.Lock()
	a.names = append(a.names, slices.Clone(names))
	a.mu.Unlock()
	return a.Mount.Preload(ctx, names)
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

	// The launcher first, as the command has it: a zathras that could
	// not keep its capability from what it starts would not mount.
	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)

	// The pod: its store is mounted over that same directory, which is
	// where draal takes the binary of a rollout to be. From here on
	// nothing is in it but what the mount has fetched.
	var pid int
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
		// The pod has stopped its service by now: the store is stopped
		// when what ran from it has ended.
		if pid != 0 && alive(pid) {
			t.Errorf("process %d, the service, still runs when the store is to be stopped", pid)
		}
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
	store := &asked{Mount: mount}
	h.podStore, h.podStart = store, l.Start
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
	pid = z.State.PID

	// The prepare asked the store for the closure of the rollout, by
	// name. The mount would have fetched the store path in any case, when
	// the binary was first looked at.
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

	// What runs is the binary of the mount, and no copy of it.
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err != nil || exe != h.binary {
		t.Errorf("process %d runs %q (%v), want %s", pid, exe, err, h.binary)
	}
	if entries, err := os.ReadDir(h.storeDir); err != nil || len(entries) != 1 || entries[0].Name() != storeName {
		t.Errorf("the store of the pod lists %v (%v), want the one store path that was fetched", entries, err)
	}
	// It was started without the capability this process mounted with,
	// and cannot gain it.
	//
	// This process has the capability, or it had not mounted: what is
	// read of the service below is read of a process that could have it.
	mine := statusOf(t, os.Getpid())
	if bits, err := strconv.ParseUint(mine["CapEff"], 16, 64); err != nil || bits&(1<<capSysAdmin) == 0 {
		t.Fatalf("CapEff of this process is %q (%v), want CAP_SYS_ADMIN in it", mine["CapEff"], err)
	}
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
