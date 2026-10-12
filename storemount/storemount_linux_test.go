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
	"syscall"
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
	on := dir
	if on == "" {
		on = DefaultDir
	}
	s.t.Cleanup(func() { jmount.Detach(on) })
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
	reach := connect
	connect = func(context.Context, jnode.DialConfig) (refs.Conn, error) {
		return nil, errors.New("no way to the store")
	}
	t.Cleanup(func() { connect = reach })

	dir := t.TempDir()
	t.Cleanup(func() { jmount.Detach(dir) })
	m, err := Start(Config{Server: anID(t), Service: "web", Cache: t.TempDir(), Dir: dir,
		PullTimeout: 300 * time.Millisecond, Log: quiet})
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

// Stop stops once: a second call of a mount that is stopped returns what
// the first did, and takes no mount away that was made on the directory
// since.
func TestStopOfAStoppedMountLeavesALaterMount(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	s.push("web", storePath(t, t.TempDir(), "aaa-one-1.0"))
	dir, cache := t.TempDir(), t.TempDir()
	first := s.mount("web", dir, cache)
	if err := first.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	second := s.mount("web", dir, cache)
	if err := first.Stop(); err != nil {
		t.Errorf("Stop of a mount that is stopped = %v, want what the first Stop returned", err)
	}
	if !mounted(t, dir) {
		t.Fatal("the Stop of a mount that was stopped took the mount made since away")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := second.Preload(ctx, []string{"aaa-one-1.0"}); err != nil {
		t.Errorf("Preload through the later mount: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "aaa-one-1.0", "share", "name")); err != nil || string(content) != "aaa-one-1.0\n" {
		t.Errorf("the file of a store path through the later mount: %q, %v", content, err)
	}
}

// The process that serves the store starts programs from it, through a
// launcher: the service, of all things, is one. The process that is
// started stands for a moment between the two, and the collector of this
// process must not be held by it. With an empty cache, where the start is
// what fetches the store path, and three hundred times more while the
// collector runs without pause, every other time with a working directory
// that is in the store as well, and one that nothing has looked at yet, so
// that the kernel has to ask for it: os/exec changes to a working
// directory between the fork and the execution, which is where nothing may
// wait for the store, so the launcher has the started process change to
// it.
func TestAProgramIsStartedFromTheMountByTheProcessThatServesIt(t *testing.T) {
	needsRoot(t)
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
	// A directory for every start that has its working directory in the
	// store: the kernel remembers a directory it was told of.
	for i := range 150 {
		if err := os.MkdirAll(filepath.Join(src, name, "in", strconv.Itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.push("web", filepath.Join(src, name))

	dir := t.TempDir()
	m := s.mount("web", dir, t.TempDir())
	l, err := launcher.New(launcher.Config{Log: quiet})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	defer l.Close()
	inStore := filepath.Join(dir, name, "bin", "testservice")
	start := func(text, in string) (string, error) {
		cmd := exec.Command(inStore, "--stdout", text, "--exit-after", "0")
		cmd.Dir = in
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := l.Start(cmd, cmd.Start); err != nil {
			return "", err
		}
		err := cmd.Wait()
		return out.String(), err
	}

	began := time.Now()
	out, err := start("from an empty cache", "")
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
		in := ""
		if i%2 == 1 {
			in = filepath.Join(dir, name, "in", strconv.Itoa(i/2))
		}
		out, err := start(text, in)
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
	if mounted(t, dir) {
		t.Error("after Stop the store is still mounted")
	}
}

// Unless it is told otherwise the store is mounted on /nix/store, which
// Start makes if it is not there.
//
// On a machine that has a /nix the test would cover its store with one of
// its own, for every process of the machine. It runs where there is none:
// in the container of scripts/test-linux.sh. No other test of any package
// mounts there, since the tests of the packages run at the same time.
func TestTheStoreIsMountedOnNixStore(t *testing.T) {
	needsRoot(t)
	if _, err := os.Lstat("/nix"); err == nil {
		t.Skip("this machine has a /nix, which the test would mount over: scripts/test-linux.sh runs it in a container that has none")
	}
	t.Cleanup(func() {
		os.Remove(DefaultDir)
		os.Remove("/nix")
	})
	s := newStore(t)
	s.push("web", storePath(t, t.TempDir(), "aaa-one-1.0"))
	m := s.mount("web", "", t.TempDir())
	if !mounted(t, DefaultDir) {
		t.Fatalf("nothing is mounted on %s", DefaultDir)
	}
	if content, err := os.ReadFile(filepath.Join(DefaultDir, "aaa-one-1.0", "share", "name")); err != nil || string(content) != "aaa-one-1.0\n" {
		t.Errorf("the file of a store path under %s: %q, %v", DefaultDir, content, err)
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

// The cache is the zathras's alone to write, whatever it was before: a
// volume of a pod comes writable by everybody, and whoever can write the
// directory of the cache can put files of their own in the place of the
// store's, whatever user the service runs as.
func TestTheCacheIsMadeTheZathrassOwnToWrite(t *testing.T) {
	needsRoot(t)
	s := newStore(t)
	cache := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mkdir is under the umask: the mode a volume with a group comes as
	// is made by hand.
	if err := os.Chmod(cache, 0o2777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(cache, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	s.mount("web", t.TempDir(), cache)

	info, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("the cache has no Stat_t: %T", info.Sys())
	}
	if int(st.Uid) != os.Geteuid() || int(st.Gid) != os.Getegid() || info.Mode().Perm() != 0o755 {
		t.Errorf("the cache is owned by %d:%d with mode %o, want %d:%d with mode %o: it is the zathras's alone to write",
			st.Uid, st.Gid, info.Mode().Perm(), os.Geteuid(), os.Getegid(), 0o755)
	}
}
