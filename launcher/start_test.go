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
	if want := []string{"great-machine", Arg, "", "/bin/sh", "sh", "-c", "exit 0"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("the arguments of the command are %q, want %q", cmd.Args, want)
	}
	if cmd.Path == "/bin/sh" || !filepath.IsAbs(cmd.Path) {
		t.Errorf("the command's program is %q, want the program itself", cmd.Path)
	}

	// The working directory is not the command's any more: what os/exec
	// does with it, it does between the fork and the execution, where
	// nothing may touch the store. The program that replaces itself
	// changes to it.
	in := exec.Command("/bin/sh", "-c", "exit 0")
	in.Dir = "/some/directory"
	if err := l.Start(in, func() error { return failed }); err != failed {
		t.Errorf("Start = %v, want what start returned", err)
	}
	if want := []string{"great-machine", Arg, "/some/directory", "/bin/sh", "/bin/sh", "-c", "exit 0"}; !slices.Equal(in.Args, want) || in.Dir != "" {
		t.Errorf("a command with a working directory became %q in %q, want %q in none", in.Args, in.Dir, want)
	}
}

// A working directory that is not there is found out by the process that
// was to change to it, as a program that is not there is: it says so and
// ends with 126.
func TestAWorkingDirectoryThatIsNotThere(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := reporting()
	cmd.Dir = filepath.Join(t.TempDir(), "nothing")
	r, errOut, status := run(t, l, cmd)
	if status != 126 || r.Pid != 0 {
		t.Errorf("the exit status is %d and the program reported %+v, want 126 and no program", status, r)
	}
	if !strings.Contains(errOut, "changing to the working directory "+cmd.Args[2]) || !strings.Contains(errOut, "no such file") {
		t.Errorf("standard error does not say which directory could not be changed to and why: %q", errOut)
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

// A command without an environment of its own is started with that of
// this process, and with PWD naming its working directory, as os/exec
// has it: the directory is changed to by the program that replaces
// itself, and the variable is not left naming another.
func TestAProgramWithoutAnEnvironmentOfItsOwnHasItsWorkingDirectoryInPWD(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(childEnv, "report")
	t.Setenv("PWD", "/somewhere/else")
	l := newLauncher(t, Config{})
	cmd := exec.Command(os.Args[0])
	cmd.Dir = dir
	r, errOut, status := run(t, l, cmd)
	if status != 0 {
		t.Fatalf("the child ended with %d: %s", status, errOut)
	}
	if r.Dir != dir {
		t.Errorf("the working directory is %q, want %q", r.Dir, dir)
	}
	var pwd []string
	for _, kv := range r.Environ {
		if strings.HasPrefix(kv, "PWD=") {
			pwd = append(pwd, kv)
		}
	}
	if len(pwd) != 1 || pwd[0] != "PWD="+dir {
		t.Errorf("PWD of the child is %q, want %q, once", pwd, "PWD="+dir)
	}
}
