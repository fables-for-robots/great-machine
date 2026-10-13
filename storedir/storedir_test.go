package storedir

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// testConfig is a Config for the server and the service, with a store
// directory and a cache of the test's own that are not there yet, the
// times of a pull made short, and tp between the store and its
// connections.
func testConfig(t *testing.T, srv *testServer, service string, tp *tap) Config {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "store")
	// Registered after the temporary directory, so run before its
	// removal: a store path has directories nobody may write to.
	t.Cleanup(func() {
		if err := removeTree(dir); err != nil {
			t.Errorf("removing the store directory: %v", err)
		}
	})
	return Config{
		Server:      srv.id,
		Addrs:       srv.addrs,
		Service:     service,
		Cache:       filepath.Join(root, "cache"),
		Dir:         dir,
		RetryWait:   5 * time.Millisecond,
		RetryMax:    20 * time.Millisecond,
		PullTimeout: 5 * time.Second,
		Log:         quiet,
		wrapDial:    tp.wrap,
	}
}

// open opens a store that is closed when the test ends.
func open(t *testing.T, cfg Config) *Store {
	t.Helper()
	s, err := Open(cfg)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// preload has the store fetch the names, and fails the test when it
// cannot.
func preload(t *testing.T, s *Store, names ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := s.Preload(ctx, names); err != nil {
		t.Fatalf("fetching %v: %v", names, err)
	}
}

// storePath makes a store path of the name in a directory of the test: a
// directory with the files given, by their paths in it. A file under bin/
// can be executed.
func storePath(t *testing.T, name string, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "bin/") {
			mode = 0o755
		}
		write(t, filepath.Join(path, rel), content, mode)
	}
	return path
}

// noise returns n bytes that no other seed gives.
func noise(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// ref is the name of the reference of a store path of the service.
func ref(service, name string) string { return wire.StorePrefix(service) + name }

// sameFiles fails the test unless the tree at got has the directories,
// the files and the links of the one at want, with the modes a store
// path has.
func sameFiles(t *testing.T, got, want string) {
	t.Helper()
	err := filepath.WalkDir(want, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(want, path)
		there, err := os.Lstat(filepath.Join(got, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		was, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case was.IsDir():
			if there.Mode() != os.ModeDir|0o555 {
				t.Errorf("%s is %v, want a directory of mode 0555", rel, there.Mode())
			}
		case was.Mode()&os.ModeSymlink != 0:
			a, _ := os.Readlink(path)
			b, _ := os.Readlink(filepath.Join(got, rel))
			if a != b {
				t.Errorf("the link %s points to %q, want %q", rel, b, a)
			}
		default:
			mode := os.FileMode(0o444)
			if was.Mode()&0o100 != 0 {
				mode = 0o555
			}
			if there.Mode() != mode {
				t.Errorf("%s is %v, want a file of mode %v", rel, there.Mode(), mode)
			}
			a, _ := os.ReadFile(path)
			b, _ := os.ReadFile(filepath.Join(got, rel))
			if !bytes.Equal(a, b) {
				t.Errorf("%s has not the content that was pushed", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// waiters returns how many wait for the fetch of name.
func (s *Store) waiters(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.fetches[name]; f != nil {
		return f.waiters
	}
	return 0
}

// eventually fails the test unless what becomes true within the time a
// step has.
func eventually(t *testing.T, what string, is func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !is() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for %s", testTimeout, what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// began waits for a pull to begin and returns the name of its reference.
func (tp *tap) began(t *testing.T) string {
	t.Helper()
	select {
	case name := <-tp.started:
		return name
	case <-time.After(testTimeout):
		t.Fatal("no pull began")
		return ""
	}
}

// noPartial fails the test when something of a store path that was being
// written is left in the store directory.
func noPartial(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), partialPrefix) {
			t.Errorf("%s was left in the store directory", e.Name())
		}
	}
}

// The second of what the design of stage 5 was to settle first: the pack
// cache saves a pack. Of two store paths of which the second was pushed
// after the first and leans on it, the second is pulled with two packs
// into an empty cache and with one into a cache that holds the first.
func TestAPackTheCacheSaves(t *testing.T) {
	srv := startServer(t)
	files := map[string][]byte{}
	for i := range 8 {
		files[fmt.Sprintf("share/part-%d", i)] = noise(uint64(i+1), 256<<10)
	}
	files["bin/app"] = []byte("#!/bin/sh\necho 1\n")
	first := storePath(t, "aaaa-app-1", files)
	files["share/part-7"] = noise(100, 256<<10)
	files["bin/app"] = []byte("#!/bin/sh\necho 2\n")
	second := storePath(t, "bbbb-app-2", files)
	srv.push("web", first)
	srv.push("web", second)

	empty := newTap()
	preload(t, open(t, testConfig(t, srv, "web", empty)), "bbbb-app-2")
	if got := empty.results[ref("web", "bbbb-app-2")].Packs; got != 2 {
		t.Fatalf("the second store path was pulled into an empty cache with %d packs, want 2: its own and the one it leans on", got)
	}

	warm := newTap()
	s := open(t, testConfig(t, srv, "web", warm))
	preload(t, s, "aaaa-app-1")
	preload(t, s, "bbbb-app-2")
	if got := warm.results[ref("web", "bbbb-app-2")].Packs; got != 1 {
		t.Errorf("the second store path was pulled into a cache that holds the first with %d packs, want 1", got)
	}
	sameFiles(t, filepath.Join(s.dir, "bbbb-app-2"), second)
}

func TestPreloadFetchesAClosure(t *testing.T) {
	srv := startServer(t)
	lib := storePath(t, "cccc-lib", map[string][]byte{
		"lib/libthing.so":  noise(7, 70<<10),
		"share/doc/readme": []byte("read me\n"),
	})
	if err := os.Symlink("libthing.so", filepath.Join(lib, "lib", "libthing.so.1")); err != nil {
		t.Fatal(err)
	}
	app := storePath(t, "dddd-app", map[string][]byte{"bin/app": []byte("#!/bin/sh\n")})
	if err := os.Symlink("/nix/store/cccc-lib/lib", filepath.Join(app, "lib")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "eeee-builder.sh")
	write(t, script, []byte("#!/bin/sh\nexit 0\n"), 0o644)
	srv.push("web", lib, app, script)

	tp := newTap()
	s := open(t, testConfig(t, srv, "web", tp))
	preload(t, s, "cccc-lib", "dddd-app", "eeee-builder.sh")

	sameFiles(t, filepath.Join(s.dir, "cccc-lib"), lib)
	sameFiles(t, filepath.Join(s.dir, "dddd-app"), app)
	info, err := os.Lstat(filepath.Join(s.dir, "eeee-builder.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != 0o555 {
		t.Errorf("the store path that is one file is %v, want a file of mode 0555", info.Mode())
	}
	if got, _ := os.ReadFile(filepath.Join(s.dir, "eeee-builder.sh")); string(got) != "#!/bin/sh\nexit 0\n" {
		t.Errorf("the store path that is one file has the content %q", got)
	}
	noPartial(t, s.dir)

	// A second time nothing is asked of the store: every name is there.
	before := tp.dialed()
	for _, name := range []string{"cccc-lib", "dddd-app", "eeee-builder.sh"} {
		if n := tp.count(ref("web", name)); n != 1 {
			t.Errorf("%s was pulled %d times, want once", name, n)
		}
	}
	preload(t, s, "cccc-lib", "dddd-app", "eeee-builder.sh")
	for _, name := range []string{"cccc-lib", "dddd-app", "eeee-builder.sh"} {
		if n := tp.count(ref("web", name)); n != 1 {
			t.Errorf("%s was pulled %d times, want once: it was there the second time", name, n)
		}
	}
	if tp.dialed() != before {
		t.Errorf("the store was dialed for store paths that were there")
	}
}

func TestPreloadAsksNothingForWhatIsThere(t *testing.T) {
	srv := startServer(t)
	tp := newTap()
	cfg := testConfig(t, srv, "web", tp)
	// Whatever is at the name is the store path: nobody pushed this one.
	write(t, filepath.Join(cfg.Dir, "ffff-there", "file"), []byte("x"), 0o444)
	s := open(t, cfg)
	preload(t, s, "ffff-there")
	if tp.dialed() != 0 {
		t.Errorf("the store was dialed %d times for a store path that is there", tp.dialed())
	}
}

func TestPreloadOfWhatTheStoreLacks(t *testing.T) {
	srv := startServer(t)
	elsewhere := storePath(t, "gggg-elsewhere", map[string][]byte{"file": []byte("x")})
	srv.push("other", elsewhere)

	tp := newTap()
	cfg := testConfig(t, srv, "web", tp)
	// Were it tried again, the test would take this long.
	cfg.PullTimeout = time.Hour
	s := open(t, cfg)
	for _, name := range []string{"hhhh-never-pushed", "gggg-elsewhere"} {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		err := s.Preload(ctx, []string{name})
		cancel()
		var missing *NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("fetching %s: %v, want a *NotFoundError", name, err)
		}
		if missing.Path != filepath.Join(cfg.Dir, name) || missing.Service != "web" {
			t.Errorf("the error is of %s for %s, want %s for web", missing.Path, missing.Service, filepath.Join(cfg.Dir, name))
		}
		want := fmt.Sprintf("the store has no %s for the service web: it holds what the rollouts of web have pushed", filepath.Join(cfg.Dir, name))
		if err.Error() != want {
			t.Errorf("the error reads %q, want %q", err, want)
		}
		if n := tp.count(ref("web", name)); n != 1 {
			t.Errorf("%s was asked of the store %d times, want once: what is not there is not tried again", name, n)
		}
		nothingAt(t, filepath.Join(cfg.Dir, name))
	}
}

func TestPreloadRefusesWhatNamesNoStorePath(t *testing.T) {
	srv := startServer(t)
	tp := newTap()
	s := open(t, testConfig(t, srv, "web", tp))
	for _, name := range []string{"", ".", "..", ".hidden", partialPrefix + "x", "a/b", "/abs"} {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		// A name that could be fetched goes with it, and is not.
		err := s.Preload(ctx, []string{"iiii-fine", name})
		cancel()
		if err == nil {
			t.Errorf("the name %q was taken for that of a store path", name)
			continue
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("%q", name)) {
			t.Errorf("the refusal of %q is %q, and does not name it", name, err)
		}
	}
	if tp.dialed() != 0 {
		t.Errorf("the store was dialed %d times: a closure with a name that is none is refused before anything is asked", tp.dialed())
	}
}

func TestOpenRemovesWhatWasBeingWritten(t *testing.T) {
	srv := startServer(t)
	cfg := testConfig(t, srv, "web", newTap())
	// A store path that is whole, and two that a run was writing when it
	// ended: a tree with a directory nobody may write to, and a file.
	write(t, filepath.Join(cfg.Dir, "jjjj-whole", "bin", "tool"), []byte("tool"), 0o555)
	write(t, filepath.Join(cfg.Dir, partialPrefix+"kkkk-tree", "share", "file"), []byte("half"), 0o444)
	for _, dir := range []string{"share", ""} {
		if err := os.Chmod(filepath.Join(cfg.Dir, partialPrefix+"kkkk-tree", dir), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(cfg.Dir, partialPrefix+"llll-file"), []byte("half"), 0o444)

	s := open(t, cfg)
	noPartial(t, s.dir)
	if got, err := os.ReadFile(filepath.Join(cfg.Dir, "jjjj-whole", "bin", "tool")); err != nil || string(got) != "tool" {
		t.Errorf("the store path that was whole: %q, %v", got, err)
	}
}

func TestOpenMakesTheDirectoriesItsOwn(t *testing.T) {
	srv := startServer(t)
	cfg := testConfig(t, srv, "web", newTap())
	// As the volumes of a pod come: for everybody to write to.
	for _, dir := range []string{cfg.Dir, cfg.Cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
	}
	open(t, cfg)
	for _, dir := range []string{cfg.Dir, cfg.Cache} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != os.ModeDir|0o755 {
			t.Errorf("%s is %v after the start, want a directory of mode 0755", dir, info.Mode())
		}
	}
	if info, err := os.Stat(filepath.Join(cfg.Cache, "packs")); err != nil || !info.IsDir() {
		t.Errorf("the pack cache is not a directory packs in the cache: %v", err)
	}
}

func TestOpenRefusesWhatIsNoStore(t *testing.T) {
	srv := startServer(t)
	good := testConfig(t, srv, "web", newTap())
	for what, change := range map[string]func(*Config){
		"no server":             func(c *Config) { c.Server = "" },
		"a server that is none": func(c *Config) { c.Server = "draal" },
		"no service":            func(c *Config) { c.Service = "" },
		"a service that is none": func(c *Config) {
			c.Service = "Not A Label"
		},
		"no cache": func(c *Config) { c.Cache = "" },
	} {
		cfg := good
		change(&cfg)
		if s, err := Open(cfg); err == nil {
			s.Close()
			t.Errorf("a store was opened with %s", what)
		}
	}
}

func TestAPullIsTriedAgain(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "mmmm-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.failDials = 3
	s := open(t, testConfig(t, srv, "web", tp))
	preload(t, s, "mmmm-app")
	if tp.dialed() != 4 {
		t.Errorf("the store was dialed %d times, want 4: three that failed and one that did not", tp.dialed())
	}
	sameFiles(t, filepath.Join(s.dir, "mmmm-app"), path)
}

func TestAPullIsGivenUp(t *testing.T) {
	srv := startServer(t)
	tp := newTap()
	tp.failDials = 1 << 30
	cfg := testConfig(t, srv, "web", tp)
	cfg.PullTimeout = 100 * time.Millisecond
	s := open(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	began := time.Now()
	err := s.Preload(ctx, []string{"nnnn-app"})
	if err == nil {
		t.Fatal("a store path was fetched from a store that cannot be dialed")
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("the fetch was given up after %s, with 100ms to try again in", took)
	}
	want := filepath.Join(cfg.Dir, "nnnn-app") + " could not be fetched from the store: "
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the error reads %q, want it to begin with %q", err, want)
	}
	if tp.dialed() < 2 {
		t.Errorf("the store was dialed %d times: the pull was not tried again", tp.dialed())
	}
}

func TestOneFetchForAName(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "oooo-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.gate = make(chan struct{})
	s := open(t, testConfig(t, srv, "web", tp))

	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	results := make(chan error, 2)
	go func() { results <- s.Preload(first, []string{"oooo-app"}) }()
	tp.began(t)
	second, cancelSecond := context.WithTimeout(context.Background(), testTimeout)
	defer cancelSecond()
	done := make(chan error, 1)
	go func() { done <- s.Preload(second, []string{"oooo-app"}) }()
	eventually(t, "both to wait for the one fetch", func() bool { return s.waiters("oooo-app") == 2 })

	// The first is withdrawn: it returns, and the fetch goes on for the
	// second.
	cancelFirst()
	select {
	case err := <-results:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the Preload that was withdrawn returned %v, want the error of its context", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("the Preload that was withdrawn did not return")
	}
	close(tp.gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the Preload that went on waiting: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("the Preload that went on waiting did not return")
	}
	if n := tp.count(ref("web", "oooo-app")); n != 1 {
		t.Errorf("the store path was pulled %d times, want once", n)
	}
	sameFiles(t, filepath.Join(s.dir, "oooo-app"), path)
}

func TestAFetchNobodyWaitsForIsEnded(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "pppp-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.gate = make(chan struct{})
	s := open(t, testConfig(t, srv, "web", tp))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Preload(ctx, []string{"pppp-app"}) }()
	tp.began(t)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("the Preload that was withdrawn returned %v, want the error of its context", err)
	}
	eventually(t, "the pull to be ended", func() bool {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		return len(tp.ended) == 1
	})
	eventually(t, "the fetch to be gone", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.fetches) == 0
	})
	nothingAt(t, filepath.Join(s.dir, "pppp-app"))
	noPartial(t, s.dir)

	// And it is fetched when it is asked for again.
	close(tp.gate)
	preload(t, s, "pppp-app")
	sameFiles(t, filepath.Join(s.dir, "pppp-app"), path)
}

func TestNoMoreFetchesAtATimeThanJobs(t *testing.T) {
	srv := startServer(t)
	var paths, names []string
	for i := range 7 {
		name := fmt.Sprintf("qqq%d-part", i)
		names = append(names, name)
		paths = append(paths, storePath(t, name, map[string][]byte{"file": noise(uint64(40+i), 4<<10)}))
	}
	srv.push("web", paths...)
	tp := newTap()
	tp.linger = 30 * time.Millisecond
	cfg := testConfig(t, srv, "web", tp)
	cfg.Jobs = 2
	s := open(t, cfg)
	preload(t, s, names...)
	if tp.most != 2 {
		t.Errorf("%d store paths were fetched at a time at the most, want 2", tp.most)
	}
	for i, name := range names {
		sameFiles(t, filepath.Join(s.dir, name), paths[i])
	}
}

func TestTheFirstFailureEndsAPreload(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "rrrr-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	s := open(t, testConfig(t, srv, "web", tp))
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	err := s.Preload(ctx, []string{"rrrr-app", "ssss-missing"})
	var missing *NotFoundError
	if !errors.As(err, &missing) || missing.Path != filepath.Join(s.dir, "ssss-missing") {
		t.Fatalf("the error is %v, want the one of the store path the store lacks", err)
	}
}

func TestCloseEndsWhatWaits(t *testing.T) {
	srv := startServer(t)
	path := storePath(t, "tttt-app", map[string][]byte{"bin/app": []byte("app")})
	srv.push("web", path)
	tp := newTap()
	tp.gate = make(chan struct{})
	s := open(t, testConfig(t, srv, "web", tp))
	done := make(chan error, 1)
	go func() { done <- s.Preload(context.Background(), []string{"tttt-app"}) }()
	tp.began(t)
	if err := s.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Errorf("the Preload that waited returned %v, want an error that says the store is closed", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("the Preload that waited did not return when the store was closed")
	}
	if err := s.Close(); err != nil {
		t.Errorf("closing a second time: %v", err)
	}
	if err := s.Preload(context.Background(), []string{"tttt-app"}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("a Preload of a store that is closed returned %v", err)
	}
	nothingAt(t, filepath.Join(s.dir, "tttt-app"))
	noPartial(t, s.dir)
}
