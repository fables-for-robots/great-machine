package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// testDraal is a draal for a zathras of these tests to register with, and
// an operator of it.
type testDraal struct {
	id   wire.ID
	addr netip.AddrPort
	op   *operator.Client
}

// aDraal serves a draal on loopback, whose Nix store is storeDir, until
// the test ends.
func aDraal(t *testing.T, storeDir string) *testDraal {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)
	database, err := db.Open(filepath.Join(t.TempDir(), "draal.sqlite"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	draalKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	opKey, err := node.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := node.Listen(ctx, node.ListenConfig{Key: draalKey, Local: true, Log: quiet})
	if err != nil {
		cancel()
		database.Close()
		t.Fatalf("node.Listen: %v", err)
	}
	d := draal.New(draal.Config{
		DB:          database,
		Authz:       authz.NewAllowlist([]wire.ID{opKey.ID()}),
		ID:          draalKey.ID(),
		StoreDir:    storeDir,
		Settle:      10 * time.Millisecond,
		StableAfter: 200 * time.Millisecond,
		RetryAfter:  200 * time.Millisecond,
		WatchEvery:  20 * time.Millisecond,
		Log:         quiet,
	})
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		<-served
		l.Close()
		database.Close()
	})
	dialing, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	conn, err := node.Dial(dialing, node.DialConfig{Key: opKey, Server: draalKey.ID(), Addrs: []netip.AddrPort{l.LocalAddr()}, Log: quiet})
	if err != nil {
		t.Fatalf("dialing draal: %v", err)
	}
	op := operator.New(conn)
	t.Cleanup(func() { op.Close() })
	return &testDraal{id: draalKey.ID(), addr: l.LocalAddr(), op: op}
}

// zathras watches the fleet until its one zathras meets cond, and returns
// it.
func (d *testDraal) zathras(t *testing.T, what string, cond func(wire.Zathras) bool) wire.Zathras {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fleets, err := d.op.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	var last wire.Fleet
	for f := range fleets {
		last = f
		for _, svc := range f.Services {
			for _, z := range svc.Zathrases {
				if cond(z) {
					return z
				}
			}
		}
	}
	t.Fatalf("timed out waiting for %s; the last fleet was %+v", what, last)
	return wire.Zathras{}
}

// wrote follows the log of a zathras until its service has written text
// to its standard output.
func (d *testDraal) wrote(t *testing.T, zathras wire.ID, text string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lines, err := d.op.Logs(ctx, zathras, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	for line := range lines {
		if line.Stream == "stdout" && line.Text == text {
			return
		}
	}
	t.Fatalf("timed out waiting for the service to write %q", text)
}

// processAlive says whether a process is there and has not ended.
func processAlive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true // a system without /proc
	}
	// The state is the field behind the name, which is in brackets.
	rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
	return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
}

// statusOfProcess returns the lines of /proc/<pid>/status by their names.
func statusOfProcess(t *testing.T, pid int) map[string]string {
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

// The command as a whole with a service in it, which is where its parts
// have to be put together rightly: the launcher is what starts the service
// and the shells, with the user of --user; the store is asked for the
// closure before the service is started; and at the end the store is
// stopped when the service has ended, and not before, for the service
// runs from it.
//
// The service ignores SIGTERM, so that it runs on for the stop timeout: a
// store that is stopped too early is stopped under a service that runs.
func TestRunZathrasRunsItsServiceThroughItsLauncherAndStopsItsStoreAfterIt(t *testing.T) {
	asRoot := runtime.GOOS == "linux" && os.Geteuid() == 0
	users := map[string]*launcher.User{"as the user of zathras": nil}
	if asRoot {
		users["as another user"] = &launcher.User{UID: 65534, GID: 65534}
	}
	for name, user := range users {
		t.Run(name, func(t *testing.T) {
			// The Nix store of the test, where whoever the service
			// runs as reaches it.
			storeDir, err := os.MkdirTemp("", "great-machine-store-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(storeDir) })
			if storeDir, err = filepath.EvalSymlinks(storeDir); err != nil {
				t.Fatal(err)
			}
			const storeName = "0123456789abcdfghijklmnpqrsvwxyz-testservice"
			bin := filepath.Join(storeDir, storeName, "bin", "testservice")
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
			for _, dir := range []string{storeDir, filepath.Dir(filepath.Dir(bin)), filepath.Dir(bin)} {
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			d := aDraal(t, storeDir)

			cfg := aZathras(t)
			cfg.draal, cfg.addrs = d.id, []netip.AddrPort{d.addr}
			cfg.stopTimeout = time.Second
			cfg.user = user
			store := newFakeStore()
			cfg.startStore = store.start
			var pid atomic.Int64
			var ranAtStop atomic.Bool
			store.onStop = func() { ranAtStop.Store(processAlive(int(pid.Load()))) }

			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			var logged buffer
			done := make(chan error, 1)
			go func() { done <- runZathras(ctx, cfg, quietLog(&logged)) }()
			ended := func() error {
				t.Helper()
				select {
				case err := <-done:
					return err
				case <-time.After(30 * time.Second):
					t.Fatalf("runZathras did not return\n%s", logged.String())
					return nil
				}
			}
			d.zathras(t, "the zathras to register", func(wire.Zathras) bool { return true })

			asking, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := d.op.Rollout(asking, wire.RolloutRequest{
				Service: "web", Binary: bin, Args: []string{"--ignore-term", "--stdout", "the service is up"}, Closure: []string{storeName}, Parallel: 1,
			}); err != nil {
				t.Fatalf("Rollout: %v", err)
			}
			z := d.zathras(t, "the service to run", func(z wire.Zathras) bool {
				return z.State.Phase == wire.PhaseRunning && z.State.PID != 0
			})
			pid.Store(int64(z.State.PID))
			// The process is the service only when it says so: before
			// that it is still the program that becomes it, or the
			// service before it has said what it ignores.
			d.wrote(t, z.ID, "the service is up")
			if got := store.fetched(); !slices.Equal(got, []string{storeName}) {
				t.Errorf("the store was asked to fetch %q before the service was started, want the closure, %q", got, storeName)
			}

			// A shell of the zathras, which says who it is.
			sh, err := d.op.Shell(asking, z.ID, wire.ShellRequest{Cols: 80, Rows: 24, Term: "dumb"})
			if err != nil {
				t.Fatalf("Shell: %v", err)
			}
			defer sh.Close()
			if _, err := sh.Write([]byte("echo \"user=$(id -u).\"; exit\n")); err != nil {
				t.Fatalf("writing to the shell: %v", err)
			}
			said, _ := io.ReadAll(sh)

			uid := os.Getuid()
			if user != nil {
				uid = int(user.UID)
			}
			if !strings.Contains(string(said), "user="+strconv.Itoa(uid)+".") {
				t.Errorf("the shell does not run as user %d:\n%s", uid, said)
			}
			if asRoot {
				// What was started through the launcher runs as the user
				// that was asked for: with another user, that is what
				// shows that the launcher is what starts. Whether a
				// process can gain privileges is the container's to say,
				// and not the launcher's any more.
				status := statusOfProcess(t, z.State.PID)
				if ids := strings.Fields(status["Uid"]); len(ids) == 0 || ids[0] != strconv.Itoa(uid) {
					t.Errorf("the service runs as user %q, want %d", status["Uid"], uid)
				}
			}

			stopping := time.Now()
			stop()
			if err := ended(); err != nil {
				t.Errorf("runZathras returned %v, want nil\n%s", err, logged.String())
			}
			// The service did not end when it was asked to: it had the
			// stop timeout, and the store was there for all of it.
			if took := time.Since(stopping); took < cfg.stopTimeout {
				t.Errorf("runZathras returned after %s, and the service ignores SIGTERM: it had no stop timeout of %s\n%s", took, cfg.stopTimeout, logged.String())
			}
			if n := store.stops.Load(); n != 1 {
				t.Errorf("the store was stopped %d times, want once", n)
			}
			if ranAtStop.Load() {
				t.Error("the store was stopped while the service still ran from it")
			}
			if processAlive(z.State.PID) {
				t.Errorf("the service, process %d, runs on after runZathras has returned", z.State.PID)
			}
		})
	}
}
