package storedir

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// several pushes n small store paths for the service web and returns
// their names.
func several(t *testing.T, srv *testServer, prefix string, n int) []string {
	t.Helper()
	var paths, names []string
	for i := range n {
		name := fmt.Sprintf("%s%d-part", prefix, i)
		names = append(names, name)
		paths = append(paths, storePath(t, name, map[string][]byte{"file": noise(uint64(1000+i), 2<<10)}))
	}
	srv.push("web", paths...)
	return names
}

// A dial that fails has failed for everybody who waited for it. Were the
// store dialed once for each of them, one after another, ten store paths
// would take ten times as long to be given up as one.
func TestADialThatFailsIsEverybodysWhoWaitedForIt(t *testing.T) {
	srv := startServer(t)
	tp := newTap()
	tp.blockDials = true
	cfg := testConfig(t, srv, "web", tp)
	cfg.DialTimeout = 100 * time.Millisecond
	cfg.PullTimeout = 300 * time.Millisecond
	cfg.Jobs = 10
	s := open(t, cfg)
	var names []string
	for i := range 10 {
		names = append(names, fmt.Sprintf("uuu%d-app", i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	began := time.Now()
	err := s.Preload(ctx, names)
	took := time.Since(began)
	if err == nil || !strings.Contains(err.Error(), "could not be fetched from the store") {
		t.Fatalf("the Preload returned %v, want that a store path could not be fetched", err)
	}
	// One store path is given up after the time of a pull and the dial
	// that was under way when it ran out: half a second here.
	if took > 1500*time.Millisecond {
		t.Errorf("ten store paths were given up after %s, with 300ms to try again in and 100ms for a dial", took)
	}
	if n := tp.dialed(); n > 6 {
		t.Errorf("the store was dialed %d times for ten store paths that waited together", n)
	}
}

// Fetches that nobody waits for any more do not go on dialing, one after
// another, a store that does not answer.
func TestFetchesNobodyWaitsForDoNotWaitForADial(t *testing.T) {
	srv := startServer(t)
	tp := newTap()
	tp.blockDials = true
	cfg := testConfig(t, srv, "web", tp)
	cfg.DialTimeout = 300 * time.Millisecond
	cfg.Jobs = 10
	s := open(t, cfg)
	var names []string
	for i := range 10 {
		names = append(names, fmt.Sprintf("vvv%d-app", i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Preload(ctx, names) }()
	eventually(t, "the store to be dialed", func() bool { return tp.dialed() > 0 })
	cancel()
	<-done
	began := time.Now()
	eventually(t, "the fetches to be gone", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.fetches) == 0
	})
	if took := time.Since(began); took > time.Second {
		t.Errorf("the fetches nobody waited for were gone after %s: they dialed in turn, 300ms each", took)
	}
}

// A pull that fails on a connection costs that connection: the next
// attempt is on one that was dialed anew.
func TestAPullThatFailsCostsItsConnection(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "wwww-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.failPulls = 2
	s := open(t, testConfig(t, srv, "web", tp))
	preload(t, s, "wwww-app")
	if n := tp.count(ref("web", "wwww-app")); n != 3 {
		t.Errorf("the store path was pulled %d times, want 3: two that failed and one that did not", n)
	}
	if n := tp.dialed(); n != 3 {
		t.Errorf("the store was dialed %d times, want 3: a pull that fails costs the connection it failed on", n)
	}
	sameFiles(t, filepath.Join(s.dir, "wwww-app"), path)
}

// An attempt that takes longer than it may is ended, and costs its
// connection like any other that failed: the next is not made on the
// connection that hung.
func TestAnAttemptThatTakesTooLongCostsItsConnection(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "xxxx-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.gate = make(chan struct{})
	cfg := testConfig(t, srv, "web", tp)
	cfg.AttemptTimeout = 50 * time.Millisecond
	s := open(t, cfg)
	done := make(chan error, 1)
	go func() { done <- s.Preload(context.Background(), []string{"xxxx-app"}) }()
	eventually(t, "an attempt to be ended", func() bool {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		return len(tp.ended) > 0
	})
	eventually(t, "the store to be dialed anew", func() bool { return tp.dialed() > 1 })
	close(tp.gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the Preload: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("the Preload did not return")
	}
	sameFiles(t, filepath.Join(s.dir, "xxxx-app"), path)
}

// The connection to the store is kept while a fetch is under way, and
// closed when none is: store paths that are fetched one after another are
// fetched over one connection.
func TestTheConnectionLastsAsLongAsFetchesAreUnderWay(t *testing.T) {
	srv := startServer(t)
	names := several(t, srv, "yyy", 4)
	tp := newTap()
	cfg := testConfig(t, srv, "web", tp)
	cfg.Jobs = 1
	s := open(t, cfg)
	preload(t, s, names[:3]...)
	if n := tp.dialed(); n != 1 {
		t.Errorf("the store was dialed %d times for three store paths fetched one after another, want once", n)
	}
	eventually(t, "the connection to be closed when no fetch is under way", func() bool { return tp.closed() == 1 })
	preload(t, s, names[3])
	if n := tp.dialed(); n != 2 {
		t.Errorf("the store was dialed %d times in all, want 2: a fetch that begins when none is under way dials", n)
	}
}

// The longest name a store path of Nix has is 244 bytes, a hash of 32,
// a dash and a name of 211, and a file system takes names of 255: what is
// being written has to have a name that fits beside it.
func TestAStorePathWithTheLongestNameThereIs(t *testing.T) {
	srv := startServer(t)
	name := strings.Repeat("a", 32) + "-" + strings.Repeat("n", 211)
	path := storePath(t, name, map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	s := open(t, testConfig(t, srv, "web", newTap()))
	preload(t, s, name)
	sameFiles(t, filepath.Join(s.dir, name), path)
	noPartial(t, s.dir)
}

// A move whose directory could not be synced has moved a store path that
// is whole, and is on disk: it stays under its name, where the next
// prepare finds it, and the error says what the disk said.
func TestAMoveThatCouldNotBeSyncedLeavesTheStorePath(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "zzzz-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	cfg := testConfig(t, srv, "web", newTap())
	failing := true
	cfg.syncDir = func(dir string) error {
		if failing {
			return errors.New("input/output error")
		}
		return syncDir(dir)
	}
	s := open(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	err := s.Preload(ctx, []string{"zzzz-app"})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(s.dir, "zzzz-app")) || !strings.Contains(err.Error(), "input/output error") {
		t.Fatalf("the Preload returned %v, want the store path and what the disk said", err)
	}
	sameFiles(t, filepath.Join(s.dir, "zzzz-app"), path)
	noPartial(t, s.dir)
	failing = false
	preload(t, s, "zzzz-app")
}

// A store path that holds what a store does not is refused when it is
// written out, by the name of what it holds, and leaves nothing behind.
// It was pulled, so it is not tried again.
func TestPreloadOfAStorePathThatHoldsAFIFO(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "0000-queue", map[string][]byte{"bin/app": []byte("app")})
	if err := unix.Mkfifo(filepath.Join(path, "bin", "queue"), 0o644); err != nil {
		t.Fatalf("making a FIFO: %v", err)
	}
	srv.push("web", path)
	tp := newTap()
	s := open(t, testConfig(t, srv, "web", tp))
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	err := s.Preload(ctx, []string{"0000-queue"})
	if err == nil {
		t.Fatal("a store path with a FIFO in it was fetched")
	}
	for _, word := range []string{filepath.Join(s.dir, "0000-queue") + " could not be written out", "bin/queue", "FIFO"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the error does not say %q: %v", word, err)
		}
	}
	if n := tp.count(ref("web", "0000-queue")); n != 1 {
		t.Errorf("the store path was pulled %d times, want once", n)
	}
	if _, err := os.Lstat(filepath.Join(s.dir, "0000-queue")); !os.IsNotExist(err) {
		t.Errorf("something is under the name of a store path that was refused (%v)", err)
	}
	noPartial(t, s.dir)
}
