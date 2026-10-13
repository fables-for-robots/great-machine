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

// The test binary is the program of these tests twice over: it is the
// tests, and started with childEnv set it is the program that a test
// started, and says what it finds itself to be.
const (
	childEnv = "LAUNCHER_TEST_CHILD"
	valueEnv = "LAUNCHER_TEST_VALUE"
	exitEnv  = "LAUNCHER_TEST_EXIT"
)

func TestMain(m *testing.M) {
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

// newLauncher returns a launcher that logs nowhere, unless cfg says where.
func newLauncher(t *testing.T, cfg Config) *Launcher {
	t.Helper()
	if cfg.Log == nil {
		cfg.Log = quiet
	}
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

// A program started through a launcher that was given no user is started
// as it is: its arguments, the first among them, its environment, its
// working directory, its process group, its standard streams, its exit
// status and its user are those of the command.
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

// Without a user a launcher changes nothing of a command: the program,
// the arguments and the working directory are the command's own, and it
// is started with what Start was given to start it with, whose error is
// what Start returns.
func TestWithoutAUserStartChangesNothingOfTheCommand(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.Args[0] = "sh"
	cmd.Dir = "/some/directory"
	failed := errors.New("it was not started")
	calls := 0
	err := l.Start(cmd, func() error {
		calls++
		return failed
	})
	if err != failed || calls != 1 {
		t.Errorf("Start = %v after %d calls of start, want what start returned after one", err, calls)
	}
	if want := []string{"sh", "-c", "exit 0"}; cmd.Path != "/bin/sh" || !slices.Equal(cmd.Args, want) || cmd.Dir != "/some/directory" {
		t.Errorf("the command became %s %q in %q, want /bin/sh %q in /some/directory", cmd.Path, cmd.Args, cmd.Dir, want)
	}
	if cmd.Env != nil || cmd.SysProcAttr != nil {
		t.Errorf("the command was given the environment %q and the attributes %+v, and had none", cmd.Env, cmd.SysProcAttr)
	}
}

// A program that is not there is a start that failed: Start returns the
// error, which says what is not there, and no process stands in for the
// program to say so and end.
func TestAProgramThatIsNotThere(t *testing.T) {
	l := newLauncher(t, Config{})
	path := filepath.Join(t.TempDir(), "nothing")
	cmd := exec.Command(path)
	err := l.Start(cmd, cmd.Start)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), path) {
		t.Errorf("Start = %v, want an error that names %s and is os.ErrNotExist", err, path)
	}
	if cmd.Process != nil {
		t.Errorf("process %d was started for a program that is not there", cmd.Process.Pid)
	}
}

// So is a file that is there and is no program.
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
		cmd := exec.Command(path)
		err := l.Start(cmd, cmd.Start)
		if err == nil {
			cmd.Wait()
			t.Errorf("%s: Start = nil, want an error", filepath.Base(path))
			continue
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), why) {
			t.Errorf("%s: Start = %v, want an error that names the file and says %q", filepath.Base(path), err, why)
		}
	}
}

// And so is a working directory that is not there.
func TestAWorkingDirectoryThatIsNotThere(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := reporting()
	cmd.Dir = filepath.Join(t.TempDir(), "nothing")
	err := l.Start(cmd, cmd.Start)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), cmd.Dir) {
		t.Errorf("Start = %v, want an error that names %s and is os.ErrNotExist", err, cmd.Dir)
	}
}

// A command for a name that was not found has its error already, which
// is what starting it returns.
func TestACommandThatWasNotFound(t *testing.T) {
	l := newLauncher(t, Config{})
	cmd := exec.Command("no-program-of-this-name-anywhere")
	if cmd.Err == nil {
		t.Skip("there is a program of that name")
	}
	if err := l.Start(cmd, cmd.Start); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Start = %v, want the error of the command", err)
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

// Only root starts a process as another user. A launcher that could not
// says so when it is made, and not when the service is to be started.
func TestAnotherUserTakesRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("this test is root")
	}
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())
	if _, err := New(Config{User: &User{UID: uid + 1, GID: gid}, Log: quiet}); err == nil {
		t.Error("New with another user succeeded, and this process is not root")
	} else if !strings.Contains(err.Error(), "root") {
		t.Errorf("the error does not say that it takes root: %v", err)
	}
	// Its own user is no other, and nothing is set for it: to set the
	// user of a process is to set its groups, which takes root as well.
	l := newLauncher(t, Config{User: &User{UID: uid, GID: gid}})
	cmd := reporting()
	r, _, status := run(t, l, cmd)
	if status != 0 || r.Uid != int(uid) || r.Gid != int(gid) {
		t.Errorf("as its own user the program ran as %d:%d and ended with %d", r.Uid, r.Gid, status)
	}
	if cmd.SysProcAttr != nil {
		t.Errorf("the command of a process's own user was given the attributes %+v, and needs none", cmd.SysProcAttr)
	}
}
