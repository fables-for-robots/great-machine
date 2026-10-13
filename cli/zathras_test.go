package cli

import (
	"bytes"
	"context"
	"errors"
	"github.com/fables-for-robots/great-machine/node"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/storedir"
	"github.com/fables-for-robots/great-machine/wire"
)

// fakeStore is the store of a zathras as a test has it: it says what it
// was opened with, and counts how often it was closed.
type fakeStore struct {
	err     error                // what the opening fails with; nil: it opens
	opened  chan storedir.Config // gets what the store was opened with
	closes  atomic.Int64
	onClose func() // called by Close, if set

	mu    sync.Mutex
	asked []string // the names it was asked to fetch
}

func newFakeStore() *fakeStore {
	return &fakeStore{opened: make(chan storedir.Config, 4)}
}

func (f *fakeStore) open(cfg storedir.Config) (zathrasStore, error) {
	f.opened <- cfg
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeStore) Preload(ctx context.Context, names []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, names...)
	return nil
}

// fetched returns the names the store was asked to fetch.
func (f *fakeStore) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func (f *fakeStore) Close() error {
	f.closes.Add(1)
	if f.onClose != nil {
		f.onClose()
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
		roleKey:     aRoleKey(t),
		addrs:       []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")},
	}
}

// aRoleKey makes a role key in a directory of the test and returns its
// file.
func aRoleKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "role.key")
	if _, err := node.LoadOrCreateKey(path); err != nil {
		t.Fatalf("making a role key: %v", err)
	}
	return path
}

// A zathras has to be given the role key of its service, and makes none:
// a key it made itself would be one nobody granted anything.
func TestZathrasNeedsItsRoleKey(t *testing.T) {
	cleanEnvironment(t)
	var stdout, stderr bytes.Buffer
	got := run(context.Background(), newApp(&stdout, &stderr),
		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--no-store", "--health-addr", ""})
	if want := "--role-key is required: give it, or set GREAT_MACHINE_ROLE_KEY"; got != 2 || !strings.Contains(stderr.String(), want) {
		t.Errorf("without --role-key: the exit status is %d and stderr %q, want 2 and %q", got, stderr.String(), want)
	}

	missing := filepath.Join(t.TempDir(), "not-there.key")
	stderr.Reset()
	got = run(context.Background(), newApp(&stdout, &stderr),
		[]string{"great-machine", "zathras", "--draal", idA, "--service", "web", "--no-store", "--health-addr", "", "--role-key", missing})
	if got != 1 || !strings.Contains(stderr.String(), "the role key") || !strings.Contains(stderr.String(), missing) ||
		!strings.Contains(stderr.String(), "great-machine id --key") {
		t.Errorf("with a file that is not there: the exit status is %d and stderr %q, want 1, the file, and how a role key is made", got, stderr.String())
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("the zathras made the role key it did not find (%v)", err)
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
func returned(t *testing.T, done <-chan error, logged *buffer) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("runZathras did not return\n%s", logged.String())
		return nil
	}
}

// A zathras opens its store before anything else that could use it, with
// the endpoint ID of draal as the store's and its own service, and closes
// it last of what runs from it: when the agent and the supervisor have
// ended, and while the health check still answers.
func TestRunZathrasOpensItsStoreAndClosesItLast(t *testing.T) {
	cfg := aZathras(t)
	store := newFakeStore()
	cfg.openStore = store.open
	var healthAtStop atomic.Int64
	store.onClose = func() { healthAtStop.Store(int64(healthz(cfg.healthAddr))) }

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()

	var got storedir.Config
	select {
	case got = <-store.opened:
	case err := <-done:
		t.Fatalf("runZathras returned before it opened its store: %v\n%s", err, logged.String())
	case <-time.After(20 * time.Second):
		t.Fatalf("the store was not opened\n%s", logged.String())
	}
	if got.Service != "web" || got.Cache != cfg.storeCache || got.Dir != "" {
		t.Errorf("the store was opened with the service %q, the cache %q and the directory %q; want web, %q and none", got.Service, got.Cache, got.Dir, cfg.storeCache)
	}
	// It is given what dials the store, and that is from the endpoint of
	// the connection to draal: without one there is nothing to dial from.
	if got.Dial == nil {
		t.Fatal("the store was opened with nothing to dial the store with")
	}
	if o, err := got.Dial(context.Background()); err == nil {
		o.Close()
		t.Error("the store was dialed while the zathras has no connection to draal")
	} else if !strings.Contains(err.Error(), "no connection to draal") {
		t.Errorf("dialing the store without a connection to draal: %v", err)
	}
	answering(t, cfg.healthAddr)
	if n := store.closes.Load(); n != 0 {
		t.Fatalf("the store was closed %d times while the zathras runs", n)
	}

	stop()
	if err := returned(t, done, &logged); err != nil {
		t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
	}
	if n := store.closes.Load(); n != 1 {
		t.Errorf("the store was closed %d times, want once", n)
	}
	if got := healthAtStop.Load(); got != http.StatusServiceUnavailable {
		t.Errorf("while the store was closed the health check answered %d, want 503: it is shut down last", got)
	}
	if got := healthz(cfg.healthAddr); got != 0 {
		t.Errorf("the health check answers %d after runZathras has returned", got)
	}
}

// With --no-store a zathras starts no store, and runs.
func TestRunZathrasWithoutAStore(t *testing.T) {
	cfg := aZathras(t)
	cfg.noStore = true
	cfg.openStore = func(storedir.Config) (zathrasStore, error) {
		t.Error("a store was opened, and the zathras was told to have none")
		return nil, errors.New("no store")
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logged buffer
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

// A store that cannot be opened ends the zathras at once, with what the
// store said, and before anything listens: without a store nothing can
// run, and the kubelet starts the container again.
func TestRunZathrasWhoseStoreCannotBeOpened(t *testing.T) {
	cfg := aZathras(t)
	store := newFakeStore()
	store.err = errors.New("storedir: /nix/store has to be the zathras's alone to write to, and cannot be made so: read-only file system")
	cfg.openStore = store.open
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(context.Background(), cfg, quietLog(&logged)) }()
	err := returned(t, done, &logged)
	if err == nil || !errors.Is(err, store.err) || !strings.Contains(err.Error(), "the store of the zathras") {
		t.Errorf("runZathras returned %v, want the error of the store, said to be the store's", err)
	}
	if n := store.closes.Load(); n != 0 {
		t.Errorf("a store that was not opened was closed %d times", n)
	}
	l, err := net.Listen("tcp", cfg.healthAddr)
	if err != nil {
		t.Fatalf("the address of the health check is taken after runZathras has returned: %v", err)
	}
	l.Close()
}

// Every way out after the store was opened closes the store: its fetches
// and its pack cache are not left open by a zathras that ends for another
// reason.
func TestRunZathrasClosesItsStoreWhenItCannotGoOn(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	cfg := aZathras(t)
	cfg.healthAddr = taken.Addr().String()
	store := newFakeStore()
	cfg.openStore = store.open
	var logged buffer
	done := make(chan error, 1)
	go func() { done <- runZathras(context.Background(), cfg, quietLog(&logged)) }()
	if err := returned(t, done, &logged); err == nil || !strings.Contains(err.Error(), "the health check: listen tcp") {
		t.Errorf("runZathras returned %v, want the failure of the health check to listen", err)
	}
	if n := store.closes.Load(); n != 1 {
		t.Errorf("the store was closed %d times, want once", n)
	}
}
