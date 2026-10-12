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
	"slices"
	"strings"
	"sync"
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

	mu    sync.Mutex
	asked []string // the names it was asked to fetch
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
