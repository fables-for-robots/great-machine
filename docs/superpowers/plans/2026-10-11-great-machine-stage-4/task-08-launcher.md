### Task 8: `launcher`: what starts the processes of a zathras

From Task 11 on the container of a zathras has `CAP_SYS_ADMIN`, and zathras serves the store its service is executed from. Two things follow for every process a zathras starts, and this package is both.

**The process is not to have the capability.** Linux keeps capabilities, and whether a process may gain privileges, for every thread, and a child is a copy of the thread that started it. A launcher has a goroutine locked to its OS thread for good, which drops `CAP_SYS_ADMIN` from the bounding set of that thread, takes it out of the inheritable set, clears the ambient set and sets `no_new_privs`; every process is started from there. The rest of zathras keeps what it had, and mounts.

**The process is not executed from the store by zathras.** Go starts a process with `vfork`: the starting thread stands still until the child has executed its program, and until then the runtime cannot stop the world, which a garbage collection begins with. A program in the store is read through the NFS server in the same process. When a collection begins meanwhile, the child waits for the server, the server for the collection, the collection for the child, for good: this was seen within the first few of 300 starts (the spec's §13). So what a launcher starts is the program itself, which is a file of the image, as `great-machine exec-in-place DIR PATH ARGV0 [ARG...]`, and that process changes to `DIR`, if there is one, and replaces itself with the program at `PATH`.

**Two cycles of this task are what a review found**, and each revises what an earlier cycle wrote; they are marked as such. The working directory moved into `exec-in-place` (it was the command's, and `os/exec` changes to it between the fork and the execution: a directory of the store there stands the process still as an execution from the store does), which is why the first cycles have `exec-in-place PATH ARGV0 [ARG...]` and the task ends with `DIR` before `PATH`. And a launcher with a user gives that user the pipes and the terminal a process is started on, without which a service of `--user` cannot open `/dev/stdout`.

`exec-in-place` is no command of `cli`: it is looked for first thing in `main`, before the reaper and before any flag is read, and a test binary that starts processes through a launcher looks for it in its `TestMain`.

The tests that need Linux and root run in a container that is not privileged; this task brings the script that runs them, `scripts/test-linux.sh`.

**Files:**
- Create: `launcher/launcher.go`, `launcher/launcher_linux.go`, `launcher/launcher_other.go`, `launcher/caps.go`, `scripts/test-linux.sh`
- Modify: `cmd/great-machine/main.go`
- Test: `launcher/launcher_test.go`, `launcher/start_test.go`, `launcher/caps_linux_test.go`, `launcher/caps_test.go`, `cmd/great-machine/main_test.go`, `launcher/stdio_linux_test.go`

**Interfaces:**
- Consumes: nothing of the other tasks.
- Produces, in package `launcher`:
  - `const Arg = "exec-in-place"`.
  - `func Replace(args []string) (status int, was bool)`: `(0, false)` at once when `args[1]` is not `Arg`; otherwise the process changes to the directory `args[2]`, unless that is empty, and becomes the program at `args[3]` with the arguments `args[4:]` and its own environment, and does not return; when that fails it says why on standard error and returns 127 for a program that is not there, 126 for any other failure, and true.
  - `type User struct{ UID, GID uint32 }`; `func ParseUser(text string) (User, error)`: `"UID"` or `"UID:GID"`, digits alone; without a GID it is the UID.
  - `type Config struct { User *User; Log *slog.Logger }`; `func New(cfg Config) (*Launcher, error)`. It fails when the capability cannot be dropped and the processes would be root, and when another user is asked for by a process that is not root; it warns when the processes are root with more capabilities than a container has by default.
  - `func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error`: rewrites `cmd` (`Path` becomes the program itself, `/proc/self/exe` on Linux and `os.Executable()` elsewhere; `Args` becomes `great-machine`, `Arg`, the old `Dir`, the old `Path`, the old `Args`; `Dir` becomes empty), sets the user and makes it the owner of the standard streams of `cmd` that are pipes or terminals, and runs `start`, which is `cmd.Start` or what calls it, on the launcher's thread on Linux. A `cmd` whose `Err` is set is not started. After `Close`: `launcher.ErrClosed`.
  - `func (l *Launcher) Close()`.
  - `cmd/great-machine/main.go` calls `launcher.Replace(os.Args)` before `reaper.Init()`.
  - `scripts/test-linux.sh [arguments of go test]`: the tests as root in `golang:1.27`, with `--init --cap-add SYS_ADMIN --security-opt apparmor=unconfined`, the module cache of the machine and a build cache in the volume `great-machine-go-build`.

- [ ] **Step 1: Write the failing test: the program replacing itself with another, and the user of a process**

Create `launcher/launcher_test.go`:

```go
package launcher

import (
	"strings"
	"testing"
)

func TestReplaceIsForItsArgumentAlone(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"great-machine"},
		{"great-machine", "zathras"},
		{"great-machine", "zathras", Arg, "/bin/true", "true"},
		{Arg},
	} {
		if status, was := Replace(args); status != 0 || was {
			t.Errorf("Replace(%q) = %d, %v; want 0, false", args, status, was)
		}
	}
}

// Started with Arg and no program to become, the program says what it
// wants and ends as for a program that could not be executed.
func TestReplaceWithoutAProgram(t *testing.T) {
	for _, args := range [][]string{
		{"great-machine", Arg},
		{"great-machine", Arg, "/bin/true"},
	} {
		if status, was := Replace(args); status != 126 || !was {
			t.Errorf("Replace(%q) = %d, %v; want 126, true", args, status, was)
		}
	}
}

func TestParseUser(t *testing.T) {
	for text, want := range map[string]User{
		"0":           {0, 0},
		"1000":        {1000, 1000},
		"1000:100":    {1000, 100},
		"65534:65534": {65534, 65534},
		"0:0":         {0, 0},
		"4294967294":  {4294967294, 4294967294},
	} {
		got, err := ParseUser(text)
		if err != nil || got != want {
			t.Errorf("ParseUser(%q) = %+v, %v; want %+v", text, got, err, want)
		}
	}
	for _, text := range []string{
		"", ":", "root", "nobody:nogroup", "-1", "1:-1", "1:", ":1", "1:2:3",
		"1.5", "0x10", " 1", "1 ", "1: 2", "+1",
		"4294967295",   // (uid_t)-1, which is no user
		"1:4294967295", //
		"4294967296",   // more than an ID holds
		"99999999999999999999",
	} {
		got, err := ParseUser(text)
		if err == nil {
			t.Errorf("ParseUser(%q) = %+v, want an error", text, got)
			continue
		}
		if !strings.Contains(err.Error(), `"`+text+`"`) {
			t.Errorf("ParseUser(%q): the error does not quote what was given: %v", text, err)
		}
	}
}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./launcher/`

Expected: it fails, with

```text
launcher/launcher_test.go:13:32: undefined: Arg
```

- [ ] **Step 3: Write the code: launcher.Replace, launcher.Arg and launcher.ParseUser**

Create `launcher/launcher.go`:

```go
// Package launcher starts the processes of a zathras: the service and the
// shells.
//
// A zathras serves the store of its container itself, over NFS, and
// mounts it, which takes CAP_SYS_ADMIN. Two things follow for whatever it
// starts.
//
// What it starts is not to have that capability. On Linux a Launcher has
// a thread of its own that has given it up, and every process is started
// from that thread: a child is a copy of the thread that started it.
//
// And nothing is executed from the store by the zathras itself. Go starts
// a process with vfork: the thread that starts it stands still until the
// child has executed its program, and until then the runtime cannot stop
// the world, which every garbage collection begins with. A program in the
// store is read through the NFS server of this same process, whose
// goroutines a collection that begins meanwhile holds: the child waits
// for the server, the server for the collection, the collection for the
// child. So what a Launcher starts is the program itself, which is a file
// of the image, with Arg: started so, it replaces itself with the program
// that was meant (Replace), as a process of its own that holds nobody.
package launcher

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Arg is the first argument of the program when it is started to replace
// itself with another:
//
//	great-machine exec-in-place PATH ARGV0 [ARG...]
const Arg = "exec-in-place"

// The exit status of a program that was to replace itself and could not,
// as a shell has them.
const (
	statusNotExecuted = 126 // the program could not be executed
	statusNotThere    = 127 // there is no such program
)

// Replace is called first thing in main, and in the TestMain of a test
// that starts processes through a Launcher. When args are not those of
// Arg it returns (0, false) at once. When they are, it replaces the
// process with the program at PATH, with the arguments from ARGV0 on and
// the environment of the process, and does not return; when that fails it
// says so on standard error and returns the exit status, 127 for a program
// that is not there and 126 for any other failure, and true.
func Replace(args []string) (status int, was bool) {
	if len(args) < 2 || args[1] != Arg {
		return 0, false
	}
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "great-machine: %s is how a zathras starts a program: it takes PATH ARGV0 [ARG...]\n", Arg)
		return statusNotExecuted, true
	}
	path := args[2]
	err := syscall.Exec(path, args[3:], os.Environ())
	if !errors.Is(err, syscall.ENOENT) {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v\n", path, err)
		return statusNotExecuted, true
	}
	// The kernel says "no such file" also of a program that is there
	// when what the program is to be loaded with is not: its interpreter,
	// which for a program of a Nix store is in another store path.
	if _, statErr := os.Stat(path); statErr == nil {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v: the file is there, so what is missing is what it is loaded with, its interpreter\n", path, err)
	} else {
		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v\n", path, err)
	}
	return statusNotThere, true
}

// User is who a process runs as.
type User struct{ UID, GID uint32 }

// ParseUser reads "UID" or "UID:GID", both numbers; without a GID it is
// the UID.
func ParseUser(text string) (User, error) {
	uid, gid, both := strings.Cut(text, ":")
	u, err := parseID(uid)
	if err != nil {
		return User{}, fmt.Errorf("%q is no user: want UID or UID:GID, both numbers: %w", text, err)
	}
	if !both {
		return User{UID: u, GID: u}, nil
	}
	g, err := parseID(gid)
	if err != nil {
		return User{}, fmt.Errorf("%q is no user: want UID or UID:GID, both numbers: %w", text, err)
	}
	return User{UID: u, GID: g}, nil
}

// parseID reads the ID of a user or of a group: digits alone, and not the
// largest number an ID holds, which stands for "no ID" wherever one is
// set.
func parseID(text string) (uint32, error) {
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is no number", text)
		}
	}
	n, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("%s is more than an ID holds", text)
		}
		return 0, fmt.Errorf("%q is no number", text)
	}
	if n == math.MaxUint32 {
		return 0, fmt.Errorf("%s is no ID: it stands for none", text)
	}
	return uint32(n), nil
}
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./launcher/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add launcher/launcher_test.go launcher/launcher.go
git commit -m 'Launcher: the program replacing itself with another, and the user of a process'
```

- [ ] **Step 6: Write the failing test: a program started through a launcher is started as it would have been without**

Create `launcher/start_test.go`:

```go
package launcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// The test binary is the program of these tests three times over: it is
// the tests; started with Arg it replaces itself, as great-machine does;
// and started with childEnv set it is the program that a test started,
// and says what it finds itself to be.
const (
	childEnv = "LAUNCHER_TEST_CHILD"
	valueEnv = "LAUNCHER_TEST_VALUE"
	exitEnv  = "LAUNCHER_TEST_EXIT"
)

func TestMain(m *testing.M) {
	if status, was := Replace(os.Args); was {
		os.Exit(status)
	}
	if kind := os.Getenv(childEnv); kind != "" {
		be, ok := children[kind]
		if !ok {
			fmt.Fprintln(os.Stderr, "the test binary is no child of the kind", kind)
			os.Exit(98)
		}
		os.Exit(be())
	}
	os.Exit(m.Run())
}

// children are what the test binary is as a child, by the value of
// childEnv.
var children = map[string]func() int{"report": reportChild}

// report is what the child says of itself, on standard output.
type report struct {
	Args    []string
	Environ []string
	Dir     string
	Pid     int
	Pgid    int
	Uid     int
	Gid     int
	Groups  []int
	Stdin   string            // what its standard input held
	Status  map[string]string // of /proc/self/status, where there is one
}

// reportChild is the test binary as the program a test started. It writes
// its report to standard output and a line to standard error, and returns
// the exit status it was told to end with.
func reportChild() int {
	r := report{Args: os.Args, Environ: os.Environ(), Pid: os.Getpid(), Pgid: syscall.Getpgrp(), Uid: os.Getuid(), Gid: os.Getgid()}
	slices.Sort(r.Environ)
	r.Dir, _ = os.Getwd()
	r.Groups, _ = os.Getgroups()
	in, _ := io.ReadAll(os.Stdin)
	r.Stdin = string(in)
	if f, err := os.Open("/proc/self/status"); err == nil {
		r.Status = make(map[string]string)
		lines := bufio.NewScanner(f)
		for lines.Scan() {
			if k, v, ok := strings.Cut(lines.Text(), ":"); ok {
				r.Status[k] = strings.TrimSpace(v)
			}
		}
		f.Close()
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "the child could not report:", err)
		return 99
	}
	fmt.Fprintln(os.Stderr, "the standard error of the child")
	status, _ := strconv.Atoi(os.Getenv(exitEnv))
	return status
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// newLauncher returns a launcher that is closed when the test ends.
func newLauncher(t *testing.T, cfg Config) *Launcher {
	t.Helper()
	if cfg.Log == nil {
		cfg.Log = quiet
	}
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(l.Close)
	return l
}

// reporting returns the command of a child that reports, with the
// arguments given after its name.
func reporting(args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=report")
	return cmd
}

// run starts cmd through l and waits for it, and returns what it reported,
// what it wrote to standard error, and its exit status.
func run(t *testing.T, l *Launcher, cmd *exec.Cmd) (report, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := l.Start(cmd, cmd.Start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status := 0
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("Wait: %v", err)
		}
		status = exit.ExitCode()
	}
	var r report
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("the child reported %q: %v", out.String(), err)
		}
	}
	return r, errOut.String(), status
}

// A program started through a launcher is started as it would have been
// without: its arguments, the first among them, its environment, its
// working directory, its process group, its standard streams and its exit
// status are those of the command.
func TestAProgramStartedThroughALauncher(t *testing.T) {
	l := newLauncher(t, Config{})
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "one", "two words", "", "--three")
	cmd.Args[0] = "the-first-argument"
	cmd.Env = []string{childEnv + "=report", valueEnv + "=a value", exitEnv + "=3"}
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("what was typed\n")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	r, errOut, status := run(t, l, cmd)

	if want := []string{"the-first-argument", "one", "two words", "", "--three"}; !slices.Equal(r.Args, want) {
		t.Errorf("the arguments are %q, want %q", r.Args, want)
	}
	if want := []string{childEnv + "=report", exitEnv + "=3", valueEnv + "=a value"}; !slices.Equal(r.Environ, want) {
		t.Errorf("the environment is %q, want %q", r.Environ, want)
	}
	if r.Dir != dir {
		t.Errorf("the working directory is %q, want %q", r.Dir, dir)
	}
	if r.Pid != cmd.Process.Pid {
		t.Errorf("the program is process %d, and the command's process is %d: the program is not the process that was started", r.Pid, cmd.Process.Pid)
	}
	if r.Pgid != r.Pid {
		t.Errorf("the process group is %d, want the program's own, %d", r.Pgid, r.Pid)
	}
	if r.Stdin != "what was typed\n" {
		t.Errorf("standard input held %q", r.Stdin)
	}
	if errOut != "the standard error of the child\n" {
		t.Errorf("standard error holds %q", errOut)
	}
	if status != 3 {
		t.Errorf("the exit status is %d, want 3", status)
	}
	if r.Uid != os.Getuid() || r.Gid != os.Getgid() {
		t.Errorf("the program runs as %d:%d, want this process's %d:%d", r.Uid, r.Gid, os.Getuid(), os.Getgid())
	}
}

// What a launcher makes of a command, and that it starts it with what it
// was given to start it with.
func TestStartStartsTheProgramItselfAndReturnsWhatStartReturned(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.Args[0] = "sh"
	failed := errors.New("it was not started")
	calls := 0
	err := l.Start(cmd, func() error {
		calls++
		return failed
	})
	if err != failed || calls != 1 {
		t.Errorf("Start = %v after %d calls of start, want what start returned after one", err, calls)
	}
	if want := []string{"great-machine", Arg, "/bin/sh", "sh", "-c", "exit 0"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("the arguments of the command are %q, want %q", cmd.Args, want)
	}
	if cmd.Path == "/bin/sh" || !filepath.IsAbs(cmd.Path) {
		t.Errorf("the command's program is %q, want the program itself", cmd.Path)
	}
}

// A program that is not there is found out by the process that was to
// become it: it says so and ends with 127.
func TestAProgramThatIsNotThere(t *testing.T) {
	l := newLauncher(t, Config{})
	path := filepath.Join(t.TempDir(), "nothing")
	_, errOut, status := run(t, l, exec.Command(path))
	if status != 127 {
		t.Errorf("the exit status is %d, want 127", status)
	}
	if !strings.Contains(errOut, "executing "+path) || !strings.Contains(errOut, "no such file") {
		t.Errorf("standard error does not say what could not be executed and why: %q", errOut)
	}
	if strings.Contains(errOut, "interpreter") {
		t.Errorf("standard error speaks of an interpreter, and the file is not there: %q", errOut)
	}
}

// A file that is there and is no program ends with 126.
func TestAFileThatIsNoProgram(t *testing.T) {
	l := newLauncher(t, Config{})
	dir := t.TempDir()
	noBits := filepath.Join(dir, "no-execute-bit")
	if err := os.WriteFile(noBits, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	noFormat := filepath.Join(dir, "no-format")
	if err := os.WriteFile(noFormat, bytes.Repeat([]byte{0xff, 0x00}, 512), 0o755); err != nil {
		t.Fatal(err)
	}
	// Root is denied the first as well: no execute bit is set at all.
	for path, why := range map[string]string{noBits: "permission denied", noFormat: "exec format error"} {
		_, errOut, status := run(t, l, exec.Command(path))
		if status != 126 {
			t.Errorf("%s: the exit status is %d, want 126", filepath.Base(path), status)
		}
		if !strings.Contains(errOut, "executing "+path) || !strings.Contains(errOut, why) {
			t.Errorf("%s: standard error does not say %q: %q", filepath.Base(path), why, errOut)
		}
	}
}

// The kernel says "no such file" of a program whose interpreter is
// missing, as a program of a store is when a store path of its closure is
// not there. The process says which of the two it is.
func TestAProgramWhoseInterpreterIsMissing(t *testing.T) {
	l := newLauncher(t, Config{})
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/no/such/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, errOut, status := run(t, l, exec.Command(path))
	if status != 127 {
		t.Errorf("the exit status is %d, want 127", status)
	}
	if !strings.Contains(errOut, "executing "+path) || !strings.Contains(errOut, "interpreter") {
		t.Errorf("standard error does not say that the file is there and its interpreter is not: %q", errOut)
	}
}

// A command for a name that was not found has its error already, and
// nothing is started for it.
func TestACommandThatWasNotFoundIsNotStarted(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := exec.Command("no-program-of-this-name-anywhere")
	if cmd.Err == nil {
		t.Skip("there is a program of that name")
	}
	called := false
	err := l.Start(cmd, func() error {
		called = true
		return cmd.Start()
	})
	if !errors.Is(err, exec.ErrNotFound) || called {
		t.Errorf("Start = %v, start called: %v; want the error of the command and no start", err, called)
	}
}

// Starts come from many goroutines at once, as a service is started while
// shells are opened.
func TestManyStartsAtOnce(t *testing.T) {
	l := newLauncher(t, Config{})
	const goroutines, each = 8, 25
	var wg sync.WaitGroup
	failed := make(chan string, goroutines*each)
	for g := range goroutines {
		wg.Go(func() {
			for i := range each {
				mark := fmt.Sprintf("%d-%d", g, i)
				cmd := reporting(mark)
				var out bytes.Buffer
				cmd.Stdout = &out
				if err := l.Start(cmd, cmd.Start); err != nil {
					failed <- mark + ": Start: " + err.Error()
					continue
				}
				if err := cmd.Wait(); err != nil {
					failed <- mark + ": Wait: " + err.Error()
					continue
				}
				var r report
				if err := json.Unmarshal(out.Bytes(), &r); err != nil || len(r.Args) != 2 || r.Args[1] != mark {
					failed <- fmt.Sprintf("%s: reported %q (%v)", mark, out.String(), err)
				}
			}
		})
	}
	wg.Wait()
	close(failed)
	for f := range failed {
		t.Error(f)
	}
}

func TestNothingIsStartedOnceALauncherIsClosed(t *testing.T) {
	l, err := New(Config{Log: quiet})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Close()
	l.Close()
	cmd := reporting()
	called := false
	err = l.Start(cmd, func() error {
		called = true
		return cmd.Start()
	})
	if !errors.Is(err, ErrClosed) || called {
		t.Errorf("Start after Close = %v, start called: %v; want ErrClosed and no start", err, called)
	}
}

// Only root starts a process as another user. A launcher that could not
// says so when it is made, and not when the service is to be started.
func TestAnotherUserTakesRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("this test is root")
	}
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if l, err := New(Config{User: &User{UID: uid + 1, GID: gid}, Log: quiet}); err == nil {
		l.Close()
		t.Error("New with another user succeeded, and this process is not root")
	} else if !strings.Contains(err.Error(), "root") {
		t.Errorf("the error does not say that it takes root: %v", err)
	}
	// Its own user is no other.
	l := newLauncher(t, Config{User: &User{UID: uid, GID: gid}})
	if r, _, status := run(t, l, reporting()); status != 0 || r.Uid != int(uid) || r.Gid != int(gid) {
		t.Errorf("as its own user the program ran as %d:%d and ended with %d", r.Uid, r.Gid, status)
	}
}
```

- [ ] **Step 7: Run it, and see it fail**

Run: `go test ./launcher/`

Expected: it fails, with

```text
launcher/start_test.go:97:36: undefined: Config
```

- [ ] **Step 8: Write the code: launcher.Launcher: what is started is the program itself, which replaces itself; on Linux from a thread of the launcher's own**

Change `launcher/launcher.go`:

```diff
--- a/launcher/launcher.go
+++ b/launcher/launcher.go
@@ -24,8 +24,10 @@ package launcher
 import (
 	"errors"
 	"fmt"
+	"log/slog"
 	"math"
 	"os"
+	"os/exec"
 	"strconv"
 	"strings"
 	"syscall"
@@ -118,3 +120,104 @@ func parseID(text string) (uint32, error) {
 	}
 	return uint32(n), nil
 }
+
+// Config is what a Launcher is made of.
+type Config struct {
+	// User is who the processes run as, with the group and no others.
+	// nil: the user of this process, and its groups.
+	User *User
+	// Log is where the launcher logs. nil: slog.Default().
+	Log *slog.Logger
+}
+
+// ErrClosed is what Start returns once the launcher was closed.
+var ErrClosed = errors.New("launcher: closed")
+
+// Launcher starts processes. Its methods may be called from any
+// goroutine.
+type Launcher struct {
+	user *User
+	log  *slog.Logger
+	// exe is the program itself, which is what every process is started
+	// as.
+	exe string
+	// thread is what a launcher is on Linux, and nothing elsewhere.
+	thread
+}
+
+// New returns a launcher. On Linux it starts the launcher's thread, which
+// drops CAP_SYS_ADMIN from its bounding set, takes it out of its
+// inheritable set, clears its ambient set and sets no_new_privs.
+//
+// It fails when the processes are to run as another user and this
+// process is not root, which alone can start them so.
+func New(cfg Config) (*Launcher, error) {
+	l := &Launcher{user: cfg.User, log: cfg.Log}
+	if l.log == nil {
+		l.log = slog.Default()
+	}
+	if l.user != nil && os.Geteuid() != 0 {
+		own := User{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
+		if *l.user != own {
+			return nil, fmt.Errorf("launcher: only root starts a process as another user, and this process is user %d: it cannot start one as %d:%d",
+				own.UID, l.user.UID, l.user.GID)
+		}
+		// Its own user is what a process has anyway, and to set it
+		// would be to set its groups, which takes root as well.
+		l.user = nil
+	}
+	exe, err := self()
+	if err != nil {
+		return nil, fmt.Errorf("launcher: finding the program: %w", err)
+	}
+	l.exe = exe
+	if err := l.begin(); err != nil {
+		return nil, err
+	}
+	return l, nil
+}
+
+// Start starts cmd with start, which is cmd.Start or what calls it
+// (pty.StartWithSize). Before that it makes cmd the program itself,
+// started with Arg to replace itself with what cmd was to run, and sets
+// the user of Config. On Linux start runs on the launcher's thread. Start
+// returns what start returned.
+//
+// A program that cannot be executed is therefore no error of Start: the
+// process that was to become it says why on its standard error and ends,
+// with 127 for a program that is not there and 126 otherwise. A cmd whose
+// Err is set, for a name that was not found, is not started: Start
+// returns that error. Once the launcher is closed Start returns ErrClosed.
+func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error {
+	if cmd.Err != nil {
+		return cmd.Err
+	}
+	l.rewrite(cmd)
+	return l.run(start)
+}
+
+// rewrite makes cmd the program itself, which is to replace itself with
+// the program of cmd, and gives it the user of the launcher.
+func (l *Launcher) rewrite(cmd *exec.Cmd) {
+	argv := cmd.Args
+	if len(argv) == 0 {
+		// As os/exec has it for a command without arguments.
+		argv = []string{cmd.Path}
+	}
+	args := make([]string, 0, 3+len(argv))
+	args = append(args, "great-machine", Arg, cmd.Path)
+	cmd.Path, cmd.Args = l.exe, append(args, argv...)
+	if l.user != nil {
+		if cmd.SysProcAttr == nil {
+			cmd.SysProcAttr = &syscall.SysProcAttr{}
+		}
+		// No Groups: the process has the group and no others.
+		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: l.user.UID, Gid: l.user.GID}
+	}
+}
+
+// Close ends the launcher's thread. No Start may follow: one that does
+// returns ErrClosed. Close may be called more than once.
+func (l *Launcher) Close() {
+	l.end()
+}
```

Create `launcher/launcher_linux.go`:

```go
package launcher

import (
	"runtime"
	"sync"
)

// self is the program itself. The link is to the file that is being
// executed, whatever is mounted over the path it was started from: a file
// of the image, and never one of the store that the zathras serves.
func self() (string, error) {
	return "/proc/self/exe", nil
}

// thread is the launcher's thread: a goroutine that is locked to its OS
// thread for good, and runs what it is given.
type thread struct {
	work chan func()
	quit chan struct{}
	once sync.Once
}

// begin starts the thread.
func (l *Launcher) begin() error {
	l.work = make(chan func())
	l.quit = make(chan struct{})
	go l.loop()
	return nil
}

// loop is the goroutine of the thread.
func (l *Launcher) loop() {
	// Locked, and never unlocked: the runtime runs no other goroutine on
	// a locked thread, makes no new thread from one, and ends the thread
	// with its goroutine. What this thread has given up, no other part of
	// the process has given up with it.
	runtime.LockOSThread()
	for {
		select {
		case f := <-l.work:
			f()
		case <-l.quit:
			return
		}
	}
}

// run runs start on the thread and returns what it returned.
func (l *Launcher) run(start func() error) error {
	done := make(chan error, 1)
	select {
	case l.work <- func() { done <- start() }:
		return <-done
	case <-l.quit:
		return ErrClosed
	}
}

// end ends the thread, when what it runs has returned.
func (l *Launcher) end() {
	l.once.Do(func() { close(l.quit) })
}
```

Create `launcher/launcher_other.go`:

```go
//go:build !linux

package launcher

import (
	"os"
	"sync/atomic"
)

// self is the program itself.
func self() (string, error) {
	return os.Executable()
}

// thread is nothing where a process is started from any thread: there is
// no capability to keep from it.
type thread struct {
	closed atomic.Bool
}

func (l *Launcher) begin() error { return nil }

// run runs start and returns what it returned.
func (l *Launcher) run(start func() error) error {
	if l.closed.Load() {
		return ErrClosed
	}
	return start()
}

func (l *Launcher) end() { l.closed.Store(true) }
```

- [ ] **Step 9: Run it, and see it pass**

Run: `go test ./launcher/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 10: Commit**

```bash
git add launcher/start_test.go launcher/launcher.go launcher/launcher_linux.go launcher/launcher_other.go
git commit -m 'Launcher: a program started through a launcher is started as it would have been without'
```

- [ ] **Step 11: Write the failing test: what the launcher's thread gives up, and when a launcher cannot be**

Create `launcher/caps_linux_test.go`:

```go
package launcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// userEnv tells the child of the kind "new" who its launcher is to start
// processes as: "UID:GID", or nothing for the user of the process.
const userEnv = "LAUNCHER_TEST_USER"

func init() { children["new"] = newChild }

// newChild is the test binary as a process that makes a launcher. It
// writes "refused: " and the error when New fails, and otherwise "made",
// and on the next line what a process started through the launcher
// reports.
func newChild() int {
	cfg := Config{Log: quiet}
	if text := os.Getenv(userEnv); text != "" {
		u, err := ParseUser(text)
		if err != nil {
			fmt.Println("the test gave no user:", err)
			return 1
		}
		cfg.User = &u
	}
	l, err := New(cfg)
	if err != nil {
		fmt.Println("refused:", err)
		return 0
	}
	defer l.Close()
	fmt.Println("made")
	cmd := reportingSelf()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := l.Start(cmd, cmd.Start); err != nil {
		fmt.Println("Start:", err)
		return 1
	}
	if err := cmd.Wait(); err != nil {
		fmt.Println("Wait:", err)
		return 1
	}
	return 0
}

// reportingSelf is reporting for a child that may run as another user.
// The test binary lies in a directory of the user that runs the tests,
// where another user does not reach it by its path; the link reaches the
// file whoever asks.
func reportingSelf() *exec.Cmd {
	cmd := exec.Command("/proc/self/exe")
	cmd.Env = append(os.Environ(), childEnv+"=report")
	return cmd
}

// statusOfThisThread reads the fields of /proc/thread-self/status. The
// goroutine that calls it is locked to its thread.
func statusOfThisThread(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("/proc/thread-self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	status := make(map[string]string)
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		if k, v, ok := strings.Cut(lines.Text(), ":"); ok {
			status[k] = strings.TrimSpace(v)
		}
	}
	return status
}

// set reads a set of capabilities as /proc has it.
func set(t *testing.T, status map[string]string, name string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(status[name], 16, 64)
	if err != nil {
		t.Fatalf("%s is %q in the status: %v", name, status[name], err)
	}
	return v
}

// rootWithSysAdmin skips a test in a process that is not root with
// CAP_SYS_ADMIN in its bounding set, which is what a zathras is in the
// container of a pod, and returns the bounding set.
func rootWithSysAdmin(t *testing.T) uint64 {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("needs root: scripts/test-linux.sh runs it as root, in a container")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	bounding := set(t, statusOfThisThread(t), "CapBnd")
	if bounding&(1<<capSysAdmin) == 0 {
		t.Skip("needs CAP_SYS_ADMIN, which the container of scripts/test-linux.sh has")
	}
	return bounding
}

// What a launcher starts as root has what the process has but
// CAP_SYS_ADMIN, in every set, and cannot gain it.
func TestWhatIsStartedHasNoCapSysAdmin(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	l := newLauncher(t, Config{})
	r, _, status := run(t, l, reporting())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	for _, name := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		if set(t, r.Status, name)&(1<<capSysAdmin) != 0 {
			t.Errorf("%s of the child has CAP_SYS_ADMIN: %s", name, r.Status[name])
		}
	}
	want := bounding &^ (1 << capSysAdmin)
	for _, name := range []string{"CapPrm", "CapEff", "CapBnd"} {
		if got := set(t, r.Status, name); got != want {
			t.Errorf("%s of the child is %#x, want what this process has but CAP_SYS_ADMIN, %#x", name, got, want)
		}
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
	if r.Uid != 0 || r.Gid != 0 {
		t.Errorf("the child runs as %d:%d, want root", r.Uid, r.Gid)
	}
}

// As another user a process has the user, the group and no other group,
// and no capability at all.
func TestWhatIsStartedAsAnotherUser(t *testing.T) {
	rootWithSysAdmin(t)
	l := newLauncher(t, Config{User: &User{UID: 65534, GID: 65533}})
	r, _, status := run(t, l, reportingSelf())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	if r.Uid != 65534 || r.Gid != 65533 || len(r.Groups) != 0 {
		t.Errorf("the child runs as %d:%d with the groups %v, want 65534:65533 and no other group", r.Uid, r.Gid, r.Groups)
	}
	// The real, effective, saved and file system IDs alike.
	if want := "65534\t65534\t65534\t65534"; r.Status["Uid"] != want {
		t.Errorf("Uid of the child is %q, want %q", r.Status["Uid"], want)
	}
	if want := "65533\t65533\t65533\t65533"; r.Status["Gid"] != want {
		t.Errorf("Gid of the child is %q, want %q", r.Status["Gid"], want)
	}
	for _, name := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
		if got := set(t, r.Status, name); got != 0 {
			t.Errorf("%s of the child is %#x, want none", name, got)
		}
	}
	if set(t, r.Status, "CapBnd")&(1<<capSysAdmin) != 0 {
		t.Errorf("CapBnd of the child has CAP_SYS_ADMIN: %s", r.Status["CapBnd"])
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
}

// What the launcher's thread gave up, the rest of the process has not:
// it mounts.
func TestTheRestOfTheProcessKeepsWhatItHad(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	newLauncher(t, Config{})
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status := statusOfThisThread(t)
	for _, name := range []string{"CapPrm", "CapEff", "CapBnd"} {
		if got := set(t, status, name); got != bounding {
			t.Errorf("%s of another thread is %#x after a launcher was made, want %#x as before", name, got, bounding)
		}
	}
	if status["NoNewPrivs"] != "0" {
		t.Errorf("NoNewPrivs of another thread is %q, want 0", status["NoNewPrivs"])
	}
	dir := t.TempDir()
	if err := unix.Mount("tmpfs", dir, "tmpfs", 0, ""); err != nil {
		t.Fatalf("the rest of the process cannot mount any more: %v", err)
	}
	if err := unix.Unmount(dir, 0); err != nil {
		t.Errorf("unmounting: %v", err)
	}
}

// Close ends the thread: the runtime does not give a thread that gave up
// a capability to another goroutine.
func TestCloseEndsTheThread(t *testing.T) {
	l, err := New(Config{Log: quiet})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.tid == os.Getpid() {
		// The goroutine got the first thread of the process, which cannot
		// end while the process lives: the runtime parks it for good
		// instead. No second launcher gets that thread.
		l.Close()
		if l, err = New(Config{Log: quiet}); err != nil {
			t.Fatalf("New: %v", err)
		}
	}
	task := fmt.Sprintf("/proc/self/task/%d", l.tid)
	if _, err := os.Stat(task); err != nil {
		t.Fatalf("the launcher's thread %d is no thread of this process: %v", l.tid, err)
	}
	l.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(task); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the launcher's thread %d is still there after Close", l.tid)
		}
		time.Sleep(time.Millisecond)
	}
}

// withoutSetpcap runs the test binary as a process that has no
// CAP_SETPCAP and makes a launcher for user, and returns what it wrote.
//
// It is started the way a launcher starts a process: from a thread that
// has dropped the capability from its bounding set, and is never used
// again.
func withoutSetpcap(t *testing.T, user string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"=new", userEnv+"="+user)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, unix.CAP_SETPCAP, 0, 0, 0); err != nil {
			started <- fmt.Errorf("dropping CAP_SETPCAP: %w", err)
			return
		}
		started <- cmd.Start()
	}()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the process without CAP_SETPCAP: %v: %s", err, out.String())
	}
	return out.String()
}

// A zathras that cannot drop CAP_SYS_ADMIN from what it starts does not
// start anything as root: the service would have the capability. As
// another user the service has no capability, whatever the set holds.
func TestALauncherThatCannotDropRefuses(t *testing.T) {
	rootWithSysAdmin(t)
	out := withoutSetpcap(t, "")
	if !strings.HasPrefix(out, "refused: ") {
		t.Fatalf("a launcher was made in a process without CAP_SETPCAP: %q", out)
	}
	for _, word := range []string{"CAP_SYS_ADMIN", "CAP_SETPCAP", "--user", "operation not permitted"} {
		if !strings.Contains(out, word) {
			t.Errorf("the refusal does not say %q: %s", word, out)
		}
	}

	out = withoutSetpcap(t, "65534:65534")
	made, rest, _ := strings.Cut(out, "\n")
	if made != "made" {
		t.Fatalf("with a user that is not root no launcher was made: %q", out)
	}
	var r report
	reported, _, _ := strings.Cut(rest, "\n")
	if err := json.Unmarshal([]byte(reported), &r); err != nil {
		t.Fatalf("the child reported %q: %v", rest, err)
	}
	if r.Uid != 65534 || set(t, r.Status, "CapEff") != 0 || set(t, r.Status, "CapPrm") != 0 || r.Status["NoNewPrivs"] != "1" {
		t.Errorf("the child runs as %d with CapEff %s, CapPrm %s and NoNewPrivs %s; want 65534, none, none and 1",
			r.Uid, r.Status["CapEff"], r.Status["CapPrm"], r.Status["NoNewPrivs"])
	}
}

// A launcher warns when what it starts as root keeps capabilities a
// container does not have by default, as in a privileged container, and
// names them; in the container of a pod it has nothing to warn about.
func TestTheWarningOfCapabilitiesBeyondAContainers(t *testing.T) {
	bounding := rootWithSysAdmin(t)
	var log bytes.Buffer
	newLauncher(t, Config{Log: slog.New(slog.NewTextHandler(&log, nil))})
	beyond := beyondDefault(bounding &^ (1 << capSysAdmin))
	if len(beyond) == 0 {
		if strings.Contains(log.String(), "level=WARN") {
			t.Errorf("a launcher warned in a container that has no more than its default and CAP_SYS_ADMIN: %s", log.String())
		}
	} else {
		for _, name := range append(beyond, "--user") {
			if !strings.Contains(log.String(), name) {
				t.Errorf("the warning does not name %s: %s", name, log.String())
			}
		}
	}
	// As another user there is nothing to warn about, whatever the set.
	log.Reset()
	newLauncher(t, Config{User: &User{UID: 65534, GID: 65534}, Log: slog.New(slog.NewTextHandler(&log, nil))})
	if strings.Contains(log.String(), "level=WARN") {
		t.Errorf("a launcher for a user that is not root warned: %s", log.String())
	}
}

// A zathras that is not root has no capability to drop. Its launcher is
// made all the same, and what it starts cannot gain privileges.
func TestWithoutRootNoNewPrivsAlone(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("this test is root")
	}
	l := newLauncher(t, Config{})
	r, _, status := run(t, l, reporting())
	if status != 0 {
		t.Fatalf("the child ended with %d", status)
	}
	if r.Status["NoNewPrivs"] != "1" {
		t.Errorf("NoNewPrivs of the child is %q, want 1", r.Status["NoNewPrivs"])
	}
	if got := set(t, r.Status, "CapEff"); got != 0 {
		t.Errorf("CapEff of the child is %#x, want none", got)
	}
}
```

Create `launcher/caps_test.go`:

```go
package launcher

import (
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// containerCaps is the bounding set of a container that was given nothing
// and had nothing taken: what CapBnd reads there.
const containerCaps = 0x00000000a80425fb

func TestTheCapabilitiesAContainerHasByDefault(t *testing.T) {
	if defaultCaps != containerCaps {
		t.Errorf("defaultCaps is %#x, want %#x", defaultCaps, uint64(containerCaps))
	}
	if defaultCaps&(1<<capSysAdmin) != 0 {
		t.Error("CAP_SYS_ADMIN is among the capabilities of a container's default")
	}
}

func TestBeyondDefault(t *testing.T) {
	for _, c := range []struct {
		name     string
		bounding uint64
		want     []string
	}{
		{"a container's own", containerCaps, nil},
		{"fewer than a container has", 0x00000000000004fb, nil},
		{"none", 0, nil},
		{"with CAP_SYS_ADMIN, which was not dropped", containerCaps | 1<<capSysAdmin, []string{"CAP_SYS_ADMIN"}},
		{"with two more", containerCaps | 1<<12 | 1<<19, []string{"CAP_NET_ADMIN", "CAP_SYS_PTRACE"}},
		{"a privileged container's, without CAP_SYS_ADMIN", 0x000001ffffffffff &^ (1 << capSysAdmin), nil},
		{"one the kernel has and this program has no name for", containerCaps | 1<<45, []string{"capability 45"}},
	} {
		got := beyondDefault(c.bounding)
		if c.name == "a privileged container's, without CAP_SYS_ADMIN" {
			// Every capability there is but the fourteen and CAP_SYS_ADMIN.
			if len(got) != 41-14-1 || slices.Contains(got, "CAP_SYS_ADMIN") || slices.Contains(got, "CAP_CHOWN") || !slices.Contains(got, "CAP_SYS_MODULE") {
				t.Errorf("%s: beyondDefault = %q", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: beyondDefault(%#x) = %q, want %q", c.name, c.bounding, got, c.want)
		}
	}
}

// What comes of a CAP_SYS_ADMIN that could not be dropped: a launcher
// whose processes run as root cannot be; one whose processes run as
// another user can, for they have no capability whatever the set holds.
func TestRefusal(t *testing.T) {
	if err := refusalOf(nil, true); err != nil {
		t.Errorf("dropped, as root: refused: %v", err)
	}
	if err := refusalOf(nil, false); err != nil {
		t.Errorf("dropped, as another user: refused: %v", err)
	}
	if err := refusalOf(syscall.EPERM, false); err != nil {
		t.Errorf("not dropped, as another user: refused: %v", err)
	}
	err := refusalOf(syscall.EPERM, true)
	if err == nil {
		t.Fatal("not dropped, as root: not refused")
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Errorf("the refusal does not hold why the capability was not dropped: %v", err)
	}
	for _, word := range []string{"CAP_SYS_ADMIN", "CAP_SETPCAP", "--user"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal does not name %s: %v", word, err)
		}
	}
}
```

- [ ] **Step 12: Run it, and see it fail**

Run: `go test ./launcher/`

Expected: it fails, with

```text
launcher/caps_test.go:16:5: undefined: defaultCaps
```

- [ ] **Step 13: Write the code: the launcher's thread drops CAP_SYS_ADMIN and sets no_new_privs; a launcher that cannot refuses, and warns of more than a container has**

Create `launcher/caps.go`:

```go
package launcher

import "fmt"

// capSysAdmin is CAP_SYS_ADMIN, which a zathras mounts its store with and
// which nothing it starts is to have.
const capSysAdmin = 21

// capNames are the capabilities of Linux by their numbers.
var capNames = [...]string{
	0:  "CAP_CHOWN",
	1:  "CAP_DAC_OVERRIDE",
	2:  "CAP_DAC_READ_SEARCH",
	3:  "CAP_FOWNER",
	4:  "CAP_FSETID",
	5:  "CAP_KILL",
	6:  "CAP_SETGID",
	7:  "CAP_SETUID",
	8:  "CAP_SETPCAP",
	9:  "CAP_LINUX_IMMUTABLE",
	10: "CAP_NET_BIND_SERVICE",
	11: "CAP_NET_BROADCAST",
	12: "CAP_NET_ADMIN",
	13: "CAP_NET_RAW",
	14: "CAP_IPC_LOCK",
	15: "CAP_IPC_OWNER",
	16: "CAP_SYS_MODULE",
	17: "CAP_SYS_RAWIO",
	18: "CAP_SYS_CHROOT",
	19: "CAP_SYS_PTRACE",
	20: "CAP_SYS_PACCT",
	21: "CAP_SYS_ADMIN",
	22: "CAP_SYS_BOOT",
	23: "CAP_SYS_NICE",
	24: "CAP_SYS_RESOURCE",
	25: "CAP_SYS_TIME",
	26: "CAP_SYS_TTY_CONFIG",
	27: "CAP_MKNOD",
	28: "CAP_LEASE",
	29: "CAP_AUDIT_WRITE",
	30: "CAP_AUDIT_CONTROL",
	31: "CAP_SETFCAP",
	32: "CAP_MAC_OVERRIDE",
	33: "CAP_MAC_ADMIN",
	34: "CAP_SYSLOG",
	35: "CAP_WAKE_ALARM",
	36: "CAP_BLOCK_SUSPEND",
	37: "CAP_AUDIT_READ",
	38: "CAP_PERFMON",
	39: "CAP_BPF",
	40: "CAP_CHECKPOINT_RESTORE",
}

// defaultCaps are the capabilities a container has when it was given none
// and none was taken from it, with containerd as with Docker: CHOWN,
// DAC_OVERRIDE, FOWNER, FSETID, KILL, SETGID, SETUID, SETPCAP,
// NET_BIND_SERVICE, NET_RAW, SYS_CHROOT, MKNOD, AUDIT_WRITE and SETFCAP.
const defaultCaps uint64 = 1<<0 | 1<<1 | 1<<3 | 1<<4 | 1<<5 | 1<<6 | 1<<7 | 1<<8 |
	1<<10 | 1<<13 | 1<<18 | 1<<27 | 1<<29 | 1<<31

// beyondDefault returns the names of the capabilities of a bounding set
// that a container does not have by default, by their numbers.
func beyondDefault(bounding uint64) []string {
	var names []string
	for n := range 64 {
		if bounding&^defaultCaps&(1<<n) == 0 {
			continue
		}
		if n < len(capNames) {
			names = append(names, capNames[n])
		} else {
			names = append(names, fmt.Sprintf("capability %d", n))
		}
	}
	return names
}

// refusalOf returns why a launcher cannot be when CAP_SYS_ADMIN was to be
// dropped from the bounding set of its thread and dropErr came of it, and
// nil when it can be: the capability was dropped, or the processes do not
// run as root, and then have no capability whatever the set holds.
func refusalOf(dropErr error, asRoot bool) error {
	if dropErr == nil || !asRoot {
		return nil
	}
	return fmt.Errorf("launcher: the service and the shells would have CAP_SYS_ADMIN, which this process mounts with: "+
		"it could not be dropped from what they are started with (%w), which takes CAP_SETPCAP. "+
		"Give the container CAP_SETPCAP, which it has unless it was taken away, or run them as a user that is not root, with --user", dropErr)
}
```

Change `launcher/launcher_linux.go`:

```diff
--- a/launcher/launcher_linux.go
+++ b/launcher/launcher_linux.go
@@ -1,8 +1,14 @@
 package launcher
 
 import (
+	"errors"
+	"fmt"
+	"os"
 	"runtime"
+	"strings"
 	"sync"
+
+	"golang.org/x/sys/unix"
 )
 
 // self is the program itself. The link is to the file that is being
@@ -14,27 +20,43 @@ func self() (string, error) {
 
 // thread is the launcher's thread: a goroutine that is locked to its OS
 // thread for good, and runs what it is given.
+//
+// Linux keeps the capabilities of a process, and whether it may gain
+// privileges, for every thread, and a child is a copy of the thread that
+// started it. This thread gives up what the processes of a zathras are
+// not to have, and every one of them is started from it. The other
+// threads keep what they had: the zathras goes on mounting.
 type thread struct {
 	work chan func()
 	quit chan struct{}
 	once sync.Once
+	tid  int // of the OS thread
 }
 
-// begin starts the thread.
+// begin starts the thread, and returns when the thread has given up what
+// it is to give up, or with why it could not.
 func (l *Launcher) begin() error {
 	l.work = make(chan func())
 	l.quit = make(chan struct{})
-	go l.loop()
-	return nil
+	ready := make(chan error, 1)
+	go l.loop(ready)
+	return <-ready
 }
 
 // loop is the goroutine of the thread.
-func (l *Launcher) loop() {
+func (l *Launcher) loop(ready chan<- error) {
 	// Locked, and never unlocked: the runtime runs no other goroutine on
 	// a locked thread, makes no new thread from one, and ends the thread
-	// with its goroutine. What this thread has given up, no other part of
-	// the process has given up with it.
+	// with its goroutine, or parks it for good if it is the first thread
+	// of the process, which cannot end. What this thread has given up, no
+	// other part of the process has given up with it.
 	runtime.LockOSThread()
+	l.tid = unix.Gettid()
+	err := l.restrict()
+	ready <- err
+	if err != nil {
+		return
+	}
 	for {
 		select {
 		case f := <-l.work:
@@ -60,3 +82,83 @@ func (l *Launcher) run(start func() error) error {
 func (l *Launcher) end() {
 	l.once.Do(func() { close(l.quit) })
 }
+
+// restrict takes from the calling thread, which is the launcher's, what
+// the processes it starts are not to have:
+//
+//   - CAP_SYS_ADMIN, out of the bounding set and out of the inheritable
+//     set, which together are what a program that root executes gets;
+//   - every ambient capability, which a program gets whoever executes it;
+//   - the gaining of privileges by executing a program (no_new_privs): a
+//     setuid program, or a file with capabilities.
+//
+// It fails when the capability cannot be dropped and the processes run as
+// root, and warns when they run as root with more than a container has
+// by default.
+func (l *Launcher) restrict() error {
+	asRoot := os.Geteuid() == 0
+	if l.user != nil {
+		asRoot = l.user.UID == 0
+	}
+	bounding, err := boundingSet()
+	if err != nil {
+		return fmt.Errorf("launcher: reading the bounding set: %w", err)
+	}
+	if bounding&(1<<capSysAdmin) != 0 {
+		err := unix.Prctl(unix.PR_CAPBSET_DROP, capSysAdmin, 0, 0, 0)
+		if refusal := refusalOf(err, asRoot); refusal != nil {
+			return refusal
+		}
+		if err == nil {
+			bounding &^= 1 << capSysAdmin
+		}
+	}
+
+	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
+	var sets [2]unix.CapUserData // the capabilities below 32, and those from 32 on
+	if err := unix.Capget(&header, &sets[0]); err != nil {
+		return fmt.Errorf("launcher: reading the capabilities: %w", err)
+	}
+	if sets[0].Inheritable&(1<<capSysAdmin) != 0 {
+		sets[0].Inheritable &^= 1 << capSysAdmin
+		if err := unix.Capset(&header, &sets[0]); err != nil {
+			return fmt.Errorf("launcher: taking CAP_SYS_ADMIN out of the inheritable set: %w", err)
+		}
+	}
+	// A kernel before 4.3 has no ambient set, and says so with EINVAL.
+	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil && !errors.Is(err, unix.EINVAL) {
+		return fmt.Errorf("launcher: clearing the ambient set: %w", err)
+	}
+	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
+		return fmt.Errorf("launcher: setting no_new_privs: %w", err)
+	}
+
+	if asRoot {
+		if beyond := beyondDefault(bounding); len(beyond) > 0 {
+			l.log.Warn("the service and the shells run as root with capabilities that a container does not have by default, "+
+				"as in a privileged container: --user runs them as a user that has none",
+				"capabilities", strings.Join(beyond, " "))
+		}
+	}
+	return nil
+}
+
+// boundingSet returns the bounding set of the calling thread: a bit for
+// every capability a program that it executes can have at all.
+func boundingSet() (uint64, error) {
+	var set uint64
+	for n := range 64 {
+		has, err := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(n), 0, 0, 0)
+		if errors.Is(err, unix.EINVAL) {
+			// The kernel has no capability of this number, and none above.
+			break
+		}
+		if err != nil {
+			return 0, err
+		}
+		if has == 1 {
+			set |= 1 << n
+		}
+	}
+	return set, nil
+}
```

- [ ] **Step 14: Run it, and see it pass**

Run: `go test ./launcher/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 15: Commit**

```bash
git add launcher/caps_linux_test.go launcher/caps_test.go launcher/caps.go launcher/launcher_linux.go
git commit -m 'Launcher: what the launcher'\''s thread gives up, and when a launcher cannot be'
```

- [ ] **Step 16: Write the failing test: great-machine started with exec-in-place becomes the program it is given**

Create `cmd/great-machine/main_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// program builds great-machine into a directory of the test and returns
// its path.
func program(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "great-machine")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building great-machine: %v\n%s", err, output)
	}
	return out
}

// run runs the program with args, and returns what it wrote to standard
// output and to standard error, and its exit status.
func run(t *testing.T, program string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(program, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	status := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running %s %q: %v", program, args, err)
		}
		status = exit.ExitCode()
	}
	return out.String(), errOut.String(), status
}

// A zathras starts the service and the shells as the program itself with
// exec-in-place, and the program then becomes what was meant: before it
// reads a command, a flag or the environment.
func TestTheProgramReplacesItself(t *testing.T) {
	gm := program(t)

	// The first argument of the program that was meant is the one given,
	// and its exit status is the program's.
	out, errOut, status := run(t, gm, launcher.Arg, "/bin/sh", "the-shell", "-c", `echo "$0"; exit 7`)
	if out != "the-shell\n" || errOut != "" || status != 7 {
		t.Errorf("as /bin/sh the program wrote %q and %q and ended with %d; want the shell's name, nothing and 7", out, errOut, status)
	}
	// What follows is the other program's: no flag of it is read here.
	out, errOut, status = run(t, gm, launcher.Arg, "/bin/sh", "sh", "-c", `echo "$1 $2"`, "zero", "--data", "--help")
	if out != "--data --help\n" || errOut != "" || status != 0 {
		t.Errorf("with flags for /bin/sh the program wrote %q and %q and ended with %d; want the flags, nothing and 0", out, errOut, status)
	}

	nothing := filepath.Join(t.TempDir(), "nothing")
	out, errOut, status = run(t, gm, launcher.Arg, nothing, "nothing")
	if status != 127 || out != "" || !strings.Contains(errOut, "executing "+nothing) || !strings.Contains(errOut, "no such file") {
		t.Errorf("as a program that is not there it wrote %q and %q and ended with %d; want the reason on standard error and 127", out, errOut, status)
	}

	// Without the argument it is the program it always was.
	out, _, status = run(t, gm, "version")
	if status != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("version wrote %q and ended with %d", out, status)
	}
}
```

- [ ] **Step 17: Run it, and see it fail**

Run: `go test ./cmd/great-machine/`

Expected: it fails, with

```text
main_test.go:56: as /bin/sh the program wrote "" and "great-machine: unknown command \"exec-in-place\"\n" and ended with 2; want the shell's name, nothing and 7
```

- [ ] **Step 18: Write the code: exec-in-place is looked for first in main; scripts/test-linux.sh runs the tests as root in a container that is not privileged**

Change `cmd/great-machine/main.go`:

```diff
--- a/cmd/great-machine/main.go
+++ b/cmd/great-machine/main.go
@@ -5,10 +5,18 @@ import (
 	"os"
 
 	"github.com/fables-for-robots/great-machine/cli"
+	"github.com/fables-for-robots/great-machine/launcher"
 	"github.com/fables-for-robots/great-machine/reaper"
 )
 
 func main() {
+	// A zathras starts its service and its shells as this program, which
+	// then replaces itself with the program that was meant. That comes
+	// before everything else: it is no command, and what follows it on
+	// the command line is the other program's.
+	if status, was := launcher.Replace(os.Args); was {
+		os.Exit(status)
+	}
 	// As the first process of a container the program runs itself again
 	// as a child, and here only passes signals on and waits for every
 	// child, so that none stays a zombie.
```

Create `scripts/test-linux.sh`:

```bash
#!/bin/sh
# Runs the tests on Linux as root, in a container: the ones that need root
# and the kernel, which skip themselves anywhere else (what the launcher's
# thread gives up, the store that a zathras mounts), and with them every
# other. The arguments are `go test`'s; without any, every package is
# tested.
#
#   scripts/test-linux.sh                              # everything
#   scripts/test-linux.sh -run Mount -v ./storemount/
#
# The container is not privileged. It has what the app container of a pod
# has (deploy/pod.yaml): CAP_SYS_ADMIN, which a zathras mounts its store
# with, and no AppArmor profile, because the profile Docker gives a
# container denies every mount, whatever the capabilities. That is the
# appArmorProfile "Unconfined" of the pod. Where there is no AppArmor, as
# in Docker Desktop, the option changes nothing. That the tests pass in
# such a container is part of what they show.
#
# --init, because without it `go` is process 1 of the container and waits
# for no child but its own.
#
# It needs docker, and go for the place of the module cache. The build
# cache is kept in a volume, great-machine-go-build.
set -eu
cd "$(dirname "$0")/.."
[ $# -gt 0 ] || set -- ./...
exec docker run --rm --init \
	--cap-add SYS_ADMIN --security-opt apparmor=unconfined \
	-v "$PWD:$PWD" -w "$PWD" \
	-v "$(go env GOMODCACHE):/go/pkg/mod" \
	-v great-machine-go-build:/root/.cache/go-build \
	-e GOFLAGS=-buildvcs=false \
	golang:1.27 go test "$@"
```

and make it executable:

```bash
chmod +x scripts/test-linux.sh
```

- [ ] **Step 19: Run it, and see it pass**

Run: `go test ./cmd/great-machine/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 20: Commit**

```bash
git add cmd/great-machine/main_test.go cmd/great-machine/main.go scripts/test-linux.sh
git commit -m 'Launcher: great-machine started with exec-in-place becomes the program it is given'
```

- [ ] **Step 21: Write the failing test: the working directory is the started program's own to change to; as another user it is given the pipes and the terminal it is started on**

A review found both. A working directory in the store, which os/exec changes to between the fork and the execution, stood the process still as an execution from the store did. And a service of --user could not open /dev/stdout, nor a shell its terminal: both were root's.

Change `cmd/great-machine/main_test.go`:

```diff
--- a/cmd/great-machine/main_test.go
+++ b/cmd/great-machine/main_test.go
@@ -51,18 +51,33 @@ func TestTheProgramReplacesItself(t *testing.T) {
 
 	// The first argument of the program that was meant is the one given,
 	// and its exit status is the program's.
-	out, errOut, status := run(t, gm, launcher.Arg, "/bin/sh", "the-shell", "-c", `echo "$0"; exit 7`)
+	out, errOut, status := run(t, gm, launcher.Arg, "", "/bin/sh", "the-shell", "-c", `echo "$0"; exit 7`)
 	if out != "the-shell\n" || errOut != "" || status != 7 {
 		t.Errorf("as /bin/sh the program wrote %q and %q and ended with %d; want the shell's name, nothing and 7", out, errOut, status)
 	}
 	// What follows is the other program's: no flag of it is read here.
-	out, errOut, status = run(t, gm, launcher.Arg, "/bin/sh", "sh", "-c", `echo "$1 $2"`, "zero", "--data", "--help")
+	out, errOut, status = run(t, gm, launcher.Arg, "", "/bin/sh", "sh", "-c", `echo "$1 $2"`, "zero", "--data", "--help")
 	if out != "--data --help\n" || errOut != "" || status != 0 {
 		t.Errorf("with flags for /bin/sh the program wrote %q and %q and ended with %d; want the flags, nothing and 0", out, errOut, status)
 	}
 
+	// The working directory comes first, and is changed to before the
+	// program is executed: a path of the program that is not absolute is
+	// one in that directory.
+	dir, err := filepath.EvalSymlinks(t.TempDir())
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := os.WriteFile(filepath.Join(dir, "here"), []byte("#!/bin/sh\npwd\n"), 0o755); err != nil {
+		t.Fatal(err)
+	}
+	out, errOut, status = run(t, gm, launcher.Arg, dir, "./here", "here")
+	if out != dir+"\n" || errOut != "" || status != 0 {
+		t.Errorf("in %s the program wrote %q and %q and ended with %d; want the directory, nothing and 0", dir, out, errOut, status)
+	}
+
 	nothing := filepath.Join(t.TempDir(), "nothing")
-	out, errOut, status = run(t, gm, launcher.Arg, nothing, "nothing")
+	out, errOut, status = run(t, gm, launcher.Arg, "", nothing, "nothing")
 	if status != 127 || out != "" || !strings.Contains(errOut, "executing "+nothing) || !strings.Contains(errOut, "no such file") {
 		t.Errorf("as a program that is not there it wrote %q and %q and ended with %d; want the reason on standard error and 127", out, errOut, status)
 	}
```

Change `launcher/launcher_test.go`:

```diff
--- a/launcher/launcher_test.go
+++ b/launcher/launcher_test.go
@@ -10,7 +10,7 @@ func TestReplaceIsForItsArgumentAlone(t *testing.T) {
 		nil,
 		{"great-machine"},
 		{"great-machine", "zathras"},
-		{"great-machine", "zathras", Arg, "/bin/true", "true"},
+		{"great-machine", "zathras", Arg, "", "/bin/true", "true"},
 		{Arg},
 	} {
 		if status, was := Replace(args); status != 0 || was {
@@ -24,7 +24,8 @@ func TestReplaceIsForItsArgumentAlone(t *testing.T) {
 func TestReplaceWithoutAProgram(t *testing.T) {
 	for _, args := range [][]string{
 		{"great-machine", Arg},
-		{"great-machine", Arg, "/bin/true"},
+		{"great-machine", Arg, ""},
+		{"great-machine", Arg, "", "/bin/true"},
 	} {
 		if status, was := Replace(args); status != 126 || !was {
 			t.Errorf("Replace(%q) = %d, %v; want 126, true", args, status, was)
```

Change `launcher/start_test.go`:

```diff
--- a/launcher/start_test.go
+++ b/launcher/start_test.go
@@ -203,12 +203,41 @@ func TestStartStartsTheProgramItselfAndReturnsWhatStartReturned(t *testing.T) {
 	if err != failed || calls != 1 {
 		t.Errorf("Start = %v after %d calls of start, want what start returned after one", err, calls)
 	}
-	if want := []string{"great-machine", Arg, "/bin/sh", "sh", "-c", "exit 0"}; !slices.Equal(cmd.Args, want) {
+	if want := []string{"great-machine", Arg, "", "/bin/sh", "sh", "-c", "exit 0"}; !slices.Equal(cmd.Args, want) {
 		t.Errorf("the arguments of the command are %q, want %q", cmd.Args, want)
 	}
 	if cmd.Path == "/bin/sh" || !filepath.IsAbs(cmd.Path) {
 		t.Errorf("the command's program is %q, want the program itself", cmd.Path)
 	}
+
+	// The working directory is not the command's any more: what os/exec
+	// does with it, it does between the fork and the execution, where
+	// nothing may touch the store. The program that replaces itself
+	// changes to it.
+	in := exec.Command("/bin/sh", "-c", "exit 0")
+	in.Dir = "/some/directory"
+	if err := l.Start(in, func() error { return failed }); err != failed {
+		t.Errorf("Start = %v, want what start returned", err)
+	}
+	if want := []string{"great-machine", Arg, "/some/directory", "/bin/sh", "/bin/sh", "-c", "exit 0"}; !slices.Equal(in.Args, want) || in.Dir != "" {
+		t.Errorf("a command with a working directory became %q in %q, want %q in none", in.Args, in.Dir, want)
+	}
+}
+
+// A working directory that is not there is found out by the process that
+// was to change to it, as a program that is not there is: it says so and
+// ends with 126.
+func TestAWorkingDirectoryThatIsNotThere(t *testing.T) {
+	l := newLauncher(t, Config{})
+	cmd := reporting()
+	cmd.Dir = filepath.Join(t.TempDir(), "nothing")
+	r, errOut, status := run(t, l, cmd)
+	if status != 126 || r.Pid != 0 {
+		t.Errorf("the exit status is %d and the program reported %+v, want 126 and no program", status, r)
+	}
+	if !strings.Contains(errOut, "changing to the working directory "+cmd.Args[2]) || !strings.Contains(errOut, "no such file") {
+		t.Errorf("standard error does not say which directory could not be changed to and why: %q", errOut)
+	}
 }
 
 // A program that is not there is found out by the process that was to
```

Create `launcher/stdio_linux_test.go`:

```go
package launcher

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
)

// another is a user that is not root, and has no name anywhere.
var another = &User{UID: 65534, GID: 65534}

// A service writes to pipes that the supervisor made, as root. Started as
// another user it is given them: a program that opens /dev/stdout or
// /dev/stderr anew, as many are configured to, opens what it was started
// with, whoever it runs as.
func TestWhatIsStartedCanOpenItsStandardStreams(t *testing.T) {
	rootWithSysAdmin(t)
	for name, user := range map[string]*User{"as root": nil, "as another user": another} {
		t.Run(name, func(t *testing.T) {
			l := newLauncher(t, Config{User: user})
			r, w, err := os.Pipe() // as the supervisor has it
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			cmd := exec.Command("/bin/sh", "-c", "echo to standard output > /dev/stdout; echo to standard error > /dev/stderr")
			cmd.Stdout, cmd.Stderr = w, w
			err = l.Start(cmd, cmd.Start)
			w.Close()
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			out, _ := io.ReadAll(r)
			if err := cmd.Wait(); err != nil || string(out) != "to standard output\nto standard error\n" {
				t.Errorf("the program that opens its standard streams anew wrote %q and ended with %v", out, err)
			}
		})
	}
}

// A shell is started on a terminal that this process opened, as root. As
// another user it is given the terminal: it opens it by its name, as
// whatever asks for the terminal of its session does.
func TestWhatIsStartedCanOpenItsTerminal(t *testing.T) {
	rootWithSysAdmin(t)
	for name, user := range map[string]*User{"as root": nil, "as another user": another} {
		t.Run(name, func(t *testing.T) {
			l := newLauncher(t, Config{User: user})
			master, terminal, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			cmd := exec.Command("/bin/sh", "-c", `t=$(tty); if : > "$t"; then echo "opened $t"; else echo "denied $t"; fi`)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = terminal, terminal, terminal
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			err = l.Start(cmd, cmd.Start)
			terminal.Close()
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			var out bytes.Buffer
			io.Copy(&out, master) // ends with an error when the shell has gone
			cmd.Wait()
			if !strings.Contains(out.String(), "opened /dev/pts/") {
				t.Errorf("the shell could not open its own terminal:\n%s", out.String())
			}
		})
	}
}
```

- [ ] **Step 22: Run it, and see it fail**

Run: `go test ./launcher/ ./cmd/great-machine/ && scripts/test-linux.sh -run TestWhatIsStartedCanOpen ./launcher/`

Expected: it fails, with

```text
start_test.go:207: the arguments of the command are ["great-machine" "exec-in-place" "/bin/sh" "sh" "-c" "exit 0"], want ["great-machine" "exec-in-place" "" "/bin/sh" "sh" "-c" "exit 0"]
start_test.go:234: Start: chdir .../nothing: no such file or directory
stdio_linux_test.go:41: the program that opens its standard streams anew wrote "/bin/sh: 1: cannot create /dev/stdout: Permission denied\n..." and ended with exit status 2
stdio_linux_test.go:72: the shell could not open its own terminal: /bin/sh: 1: cannot create /dev/pts/0: Permission denied
```

- [ ] **Step 23: Write the code: exec-in-place changes to the working directory itself; a launcher with a user gives it the pipes and the terminal a process is started on; what is not as without a launcher is said**

Change `launcher/launcher.go`:

```diff
--- a/launcher/launcher.go
+++ b/launcher/launcher.go
@@ -19,6 +19,15 @@
 // child. So what a Launcher starts is the program itself, which is a file
 // of the image, with Arg: started so, it replaces itself with the program
 // that was meant (Replace), as a process of its own that holds nobody.
+// The same goes for the working directory of what is started, which
+// os/exec changes to between the fork and the execution: the process
+// that replaces itself changes to it.
+//
+// What is started is started as it would have been without a launcher,
+// with one difference besides: a signal that this process was itself
+// started ignoring, other than SIGHUP and SIGINT, is not ignored by what
+// a launcher starts. The Go program in between takes every other signal
+// for itself, and the execution sets what was taken back to its default.
 package launcher
 
 import (
@@ -31,12 +40,17 @@ import (
 	"strconv"
 	"strings"
 	"syscall"
+
+	"golang.org/x/term"
 )
 
 // Arg is the first argument of the program when it is started to replace
 // itself with another:
 //
-//	great-machine exec-in-place PATH ARGV0 [ARG...]
+//	great-machine exec-in-place DIR PATH ARGV0 [ARG...]
+//
+// DIR is the working directory to change to first; empty, it stays where
+// it is.
 const Arg = "exec-in-place"
 
 // The exit status of a program that was to replace itself and could not,
@@ -48,21 +62,28 @@ const (
 
 // Replace is called first thing in main, and in the TestMain of a test
 // that starts processes through a Launcher. When args are not those of
-// Arg it returns (0, false) at once. When they are, it replaces the
-// process with the program at PATH, with the arguments from ARGV0 on and
-// the environment of the process, and does not return; when that fails it
-// says so on standard error and returns the exit status, 127 for a program
-// that is not there and 126 for any other failure, and true.
+// Arg it returns (0, false) at once. When they are, it changes to DIR,
+// unless that is empty, and replaces the process with the program at
+// PATH, with the arguments from ARGV0 on and the environment of the
+// process, and does not return; when that fails it says so on standard
+// error and returns the exit status, 127 for a program that is not there
+// and 126 for any other failure, and true.
 func Replace(args []string) (status int, was bool) {
 	if len(args) < 2 || args[1] != Arg {
 		return 0, false
 	}
-	if len(args) < 4 {
-		fmt.Fprintf(os.Stderr, "great-machine: %s is how a zathras starts a program: it takes PATH ARGV0 [ARG...]\n", Arg)
+	if len(args) < 5 {
+		fmt.Fprintf(os.Stderr, "great-machine: %s is how a zathras starts a program: it takes DIR PATH ARGV0 [ARG...]\n", Arg)
 		return statusNotExecuted, true
 	}
-	path := args[2]
-	err := syscall.Exec(path, args[3:], os.Environ())
+	dir, path := args[2], args[3]
+	if dir != "" {
+		if err := os.Chdir(dir); err != nil {
+			fmt.Fprintf(os.Stderr, "great-machine: changing to the working directory %s: %v\n", dir, errors.Unwrap(err))
+			return statusNotExecuted, true
+		}
+	}
+	err := syscall.Exec(path, args[4:], os.Environ())
 	if !errors.Is(err, syscall.ENOENT) {
 		fmt.Fprintf(os.Stderr, "great-machine: executing %s: %v\n", path, err)
 		return statusNotExecuted, true
@@ -177,17 +198,24 @@ func New(cfg Config) (*Launcher, error) {
 	return l, nil
 }
 
-// Start starts cmd with start, which is cmd.Start or what calls it
-// (pty.StartWithSize). Before that it makes cmd the program itself,
-// started with Arg to replace itself with what cmd was to run, and sets
-// the user of Config. On Linux start runs on the launcher's thread. Start
-// returns what start returned.
+// Start starts cmd with start, which is cmd.Start or what calls it.
+// Before that it makes cmd the program itself, started with Arg to
+// change to the working directory of cmd and to replace itself with what
+// cmd was to run, and sets the user of Config. On Linux start runs on the
+// launcher's thread. Start returns what start returned.
 //
-// A program that cannot be executed is therefore no error of Start: the
-// process that was to become it says why on its standard error and ends,
-// with 127 for a program that is not there and 126 otherwise. A cmd whose
-// Err is set, for a name that was not found, is not started: Start
-// returns that error. Once the launcher is closed Start returns ErrClosed.
+// With a user, the standard streams of cmd that are pipes or terminals
+// are given to that user first, so that the program can open them anew
+// by their names, /dev/stdout or the terminal of its session, as it could
+// if it had made them. They have to be the command's when Start is
+// called: a terminal that start itself opens is not seen here.
+//
+// A program that cannot be executed, and a working directory that cannot
+// be changed to, are therefore no errors of Start: the process that was
+// to become the program says why on its standard error and ends, with
+// 127 for a program that is not there and 126 otherwise. A cmd whose Err
+// is set, for a name that was not found, is not started: Start returns
+// that error. Once the launcher is closed Start returns ErrClosed.
 func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error {
 	if cmd.Err != nil {
 		return cmd.Err
@@ -196,24 +224,63 @@ func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error {
 	return l.run(start)
 }
 
-// rewrite makes cmd the program itself, which is to replace itself with
-// the program of cmd, and gives it the user of the launcher.
+// rewrite makes cmd the program itself, which is to change to the working
+// directory of cmd and to replace itself with the program of cmd, and
+// gives it the user of the launcher.
 func (l *Launcher) rewrite(cmd *exec.Cmd) {
 	argv := cmd.Args
 	if len(argv) == 0 {
 		// As os/exec has it for a command without arguments.
 		argv = []string{cmd.Path}
 	}
-	args := make([]string, 0, 3+len(argv))
-	args = append(args, "great-machine", Arg, cmd.Path)
-	cmd.Path, cmd.Args = l.exe, append(args, argv...)
+	args := make([]string, 0, 4+len(argv))
+	args = append(args, "great-machine", Arg, cmd.Dir, cmd.Path)
+	cmd.Path, cmd.Args, cmd.Dir = l.exe, append(args, argv...), ""
 	if l.user != nil {
 		if cmd.SysProcAttr == nil {
 			cmd.SysProcAttr = &syscall.SysProcAttr{}
 		}
 		// No Groups: the process has the group and no others.
 		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: l.user.UID, Gid: l.user.GID}
+		for _, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
+			if f, ok := stream.(*os.File); ok && f != nil {
+				l.give(f)
+			}
+		}
+	}
+}
+
+// give makes the user of the launcher the owner of f, when f is a pipe or
+// a terminal: what a process is started on, and nothing that is a file of
+// somebody's or a device of the system, as /dev/null is. That it could
+// not be done is no reason not to start the process, which then cannot
+// open its standard streams anew.
+func (l *Launcher) give(f *os.File) {
+	info, err := f.Stat()
+	if err != nil {
+		return
+	}
+	switch {
+	case info.Mode()&os.ModeNamedPipe != 0:
+	case info.Mode()&os.ModeCharDevice != 0 && isTerminal(f):
+	default:
+		return
+	}
+	if err := f.Chown(int(l.user.UID), -1); err != nil {
+		l.log.Debug("a standard stream was not given to the user of the process, which cannot open it anew", "user", l.user.UID, "error", err)
+	}
+}
+
+// isTerminal says whether f is a terminal, without making the file
+// blocking, as asking for its descriptor would.
+func isTerminal(f *os.File) bool {
+	raw, err := f.SyscallConn()
+	if err != nil {
+		return false
 	}
+	is := false
+	raw.Control(func(fd uintptr) { is = term.IsTerminal(int(fd)) })
+	return is
 }
 
 // Close ends the launcher's thread. No Start may follow: one that does
```

- [ ] **Step 24: Run it, and see it pass**

Run: `go test ./launcher/ ./cmd/great-machine/ && scripts/test-linux.sh -run TestWhatIsStartedCanOpen ./launcher/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 25: Commit**

```bash
git add cmd/great-machine/main_test.go launcher/launcher_test.go launcher/start_test.go launcher/stdio_linux_test.go launcher/launcher.go
git commit -m 'Launcher: the working directory is the started program'\''s own to change to; as another user it is given the pipes and the terminal it is started on'
```

- [ ] **Step 26: The whole, here and on Linux as root**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
scripts/test-linux.sh
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 33 packages that have tests, both times; nothing from `git status`. This is the first run of the script: it pulls nothing if `golang:1.27` is there, and makes the volume `great-machine-go-build`.

On Linux as root the tests of `launcher` skip two, which are for a process that is not root (`TestWithoutRootNoNewPrivsAlone`, `TestAnotherUserTakesRoot`). They are built as root and run as another user, in the same image:

```bash
docker run --rm --init -e GOFLAGS=-buildvcs=false \
  -v "$PWD:$PWD" -w "$PWD" -v "$(go env GOMODCACHE):/go/pkg/mod" -v great-machine-go-build:/root/.cache/go-build \
  golang:1.27 sh -c 'go test -c -o /tmp/launcher.test ./launcher/ && cd /tmp &&
    setpriv --reuid 65534 --regid 65534 --clear-groups ./launcher.test -test.v \
      -test.run "TestWithoutRootNoNewPrivsAlone|TestAnotherUserTakesRoot"'
```

Expected: both `PASS`.
