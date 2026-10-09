### Task 5: supervisor, and the test service

**Files:**
- Create: `testservice/main.go` (the program the tests run as a service)
- Create: `testservice/build/build.go` (`build.Binary`: builds it into a test's temporary directory)
- Test: `testservice/build/build_test.go`
- Create: `supervisor/executable.go` (`CheckExecutable`, `ErrNotExecutable`)
- Test: `supervisor/executable_test.go`
- Create: `supervisor/supervisor.go`
- Test: `supervisor/supervisor_test.go`

**Interfaces:**
- Consumes: from `wire` (Task 2):

  ```go
  type Phase string

  const (
  	PhaseIdle     Phase = "idle"
  	PhaseRunning  Phase = "running"
  	PhaseStopping Phase = "stopping"
  	PhaseBackoff  Phase = "backoff"
  )

  // Exit is how a service ended.
  type Exit struct {
  	Generation uint64        `json:"generation"`
  	Status     int           `json:"status"`           // the exit status; -1 when a signal ended it or it never started
  	Signal     string        `json:"signal,omitempty"` // such as "SIGKILL"
  	Error      string        `json:"error,omitempty"`  // why the binary could not be started
  	At         time.Time     `json:"at"`
  	Ran        time.Duration `json:"ran"`
  }

  // State is what a zathras reports.
  type State struct {
  	Generation uint64        `json:"generation"` // 0 for none
  	Binary     string        `json:"binary,omitempty"`
  	Args       []string      `json:"args,omitempty"`
  	Phase      Phase         `json:"phase"`
  	PID        int           `json:"pid,omitempty"`         // while running
  	RunningFor time.Duration `json:"running_for,omitempty"` // while running
  	RestartAt  time.Time     `json:"restart_at"`            // while in backoff; the zero time otherwise
  	LastExit   *Exit         `json:"last_exit,omitempty"`
  	Restarts   uint64        `json:"restarts"`
  	Prepared   []uint64      `json:"prepared,omitempty"` // ascending
  	Preparing  uint64        `json:"preparing,omitempty"`
  	Sample     *Sample       `json:"sample,omitempty"`
  }
  ```

- Consumes: from `logring` (Task 4):

  ```go
  const (
  	Stdout  = "stdout"
  	Stderr  = "stderr"
  	Zathras = "zathras"
  )

  func New(size, maxLine int) *Ring
  func (r *Ring) Add(stream, text string)       // adds text as one line of stream
  func (r *Ring) Writer(stream string) *Writer  // cuts what is written to it into lines of stream
  func (r *Ring) Lines() []wire.LogLine         // oldest first
  func (w *Writer) Write(p []byte) (int, error) // never fails
  func (w *Writer) Flush()                      // adds what has no line feed yet as a line
  ```

- Produces, exactly as the plan's Interfaces section has it:

  ```go
  package supervisor

  type Config struct {
  	Ring           *logring.Ring // required: the service's lines and the supervisor's own
  	Stdout, Stderr io.Writer     // copies of the service's output; nil for none
  	Env            []string      // nil: os.Environ()
  	Dir            string        // "": the directory of this process
  	StopTimeout    time.Duration // 0: 2 minutes
  	QuickExit      time.Duration // 0: 30 seconds
  	BackoffMin     time.Duration // 0: 1 second
  	BackoffMax     time.Duration // 0: 2 minutes
  	Log            *slog.Logger  // nil: slog.Default()
  }

  type Supervisor struct{ /* unexported */ }

  func New(cfg Config) *Supervisor
  func (s *Supervisor) Run(ctx context.Context) error
  var ErrNotExecutable = errors.New("not an executable file")
  var ErrStopped = errors.New("the supervisor has stopped")
  func (s *Supervisor) Activate(generation uint64, binary string, args []string) error
  func (s *Supervisor) State() wire.State
  func (s *Supervisor) Changes() <-chan struct{}
  func CheckExecutable(path string) error
  ```

  ```
  testservice [--stdout TEXT] [--stderr TEXT] [--exit-after D] [--exit-code N] [--ignore-term] [--spawn]
  ```

  ```go
  package build // testservice/build

  // Binary builds testservice into tb.TempDir() and returns its path.
  func Binary(tb testing.TB) string
  ```

  What the contract leaves open, and this task settles. The tests pin each of these:
  - **`Activate` returns when `Run` has taken it**, not before. When it returns nil, `State` shows the new generation, binary and args, and a phase that is true of the new generation: `running` (with the new PID) when nothing had to be stopped, `backoff` when the binary could not be started, `stopping` (no PID) while the service of the generation before has not ended. There is no moment at which `State` shows the new generation with the phase, PID or `RunningFor` of the old service.
  - An `Activate` **before `Run` was called** does not wait: the generation is taken, the phase stays `idle`, and `Run` begins with it. Code that does `go s.Run(ctx)` and then `s.Activate(…)` at once may be this case.
  - `Activate` returns `ErrStopped` from the moment the context of `Run` is done, also while `Run` still waits for the service to end.
  - **After `Run` has returned the phase is `stopping`** when there was a generation (and `idle` when there never was), with no PID and the zero `RestartAt`. The service is down for good, and `stopping` is the phase that makes nobody send it anything.
  - `RestartAt` is exactly `LastExit.At` plus the delay.
  - `Restarts` counts every start that followed an end the supervisor did not ask for, also a start that fails. A start after `Activate` is not counted.
  - `LastExit` of a binary that could not be started has `Status` -1, `Error` set and `Ran` 0. `Signal` is a name such as `SIGKILL`, or `signal 42` for a signal without one.
  - The supervisor's lines in the ring, to the letter (durations as Go prints them, the time a service ran rounded to milliseconds below a second and to tenths of a second above):

    ```
    started generation 7: /nix/store/abc-web/bin/web (pid 123)
    generation 7 exited with status 1 after 3.2s; starting it again in 4s
    generation 7 exited with status 0 after 41.5s; starting it again
    generation 7 was ended by SIGKILL after 417ms; starting it again in 1s
    generation 7 could not be started: fork/exec /nix/store/abc-web/bin/web: exec format error; starting it again in 2s
    generation 7 exited with status 0 after 12.3s
    ```

    The last form, with nothing behind the time, is the end of a service the supervisor stopped: for the next generation, or because `Run` ends.
  - `testservice` writes the text of `--stdout` and `--stderr` **after** it has arranged how it takes SIGTERM, and after the child of `--spawn` ignores SIGTERM. A test that has read the text knows that SIGTERM now does what the flags say; a SIGTERM before that ends the program as it ends any program, with the signal. An empty TEXT is written as an empty line. With `--exit-after`, a SIGTERM before D still ends it with 0, unless `--ignore-term` is given.
  - `build.Binary` runs `go build` on the import path of `testservice`, so it works from the directory of any package of the module. It takes about a second.

This is Spec section 7.1, the part of 7.2 about stopping, and what 5.3 says of the fields of the state.

**How it is built.** One goroutine, `Run`, owns everything that changes. It reacts to four things: an `Activate` (handed over on a channel), the end of the service, a timer (the backoff has passed; the stop timeout has passed) and the end of its context. The state that `State` returns is behind a mutex and is changed in one step for every event, so a reader never sees half a change.

The service is started with `os/exec`, in a process group of its own (`Setpgid`), with `/dev/null` as standard input (what `os/exec` gives a command whose `Stdin` is nil). Signals go to the group: `syscall.Kill(-pid, sig)`. Process 1's duties are not this package's concern: Task 10 sees to it that the program never is process 1 where it runs a service.

The output needs care. If `cmd.Stdout` were an ordinary writer, `os/exec` would make the pipe and `cmd.Wait` would not return before every process that holds the pipe has ended — a child the service left behind would hide the service's end (for `cmd.WaitDelay`, if that is set, or for ever). So this package makes the pipes itself and gives the command their write ends as files. `cmd.Wait` then returns when the service itself has ended. Then the group is killed, which ends whoever else held the pipes; the output is read to its end (for at most a second, in case a process left the group and still holds a pipe, after which the pipes are closed here); the ring's writers are flushed, so that a last line without a line feed is not lost; and only then does `Run` hear of the end. `cmd.WaitDelay` is not set, as with files for output it has nothing to bound.

The tests run real processes, with every duration made short, and take about ten seconds. They are in the package itself, because the test harness has to know when `Run` has begun.

- [ ] **Step 1: Write the failing test of the test service**

Create `testservice/build/build_test.go`:

```go
package build_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fables-for-robots/great-machine/testservice/build"
)

// run runs the program to its end and returns what it wrote and its exit
// status.
func run(t *testing.T, bin string, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		status = exit.ExitCode()
	default:
		t.Fatalf("running testservice %q: %v", args, err)
	}
	return out.String(), errOut.String(), status
}

func TestBinaryWritesAndExits(t *testing.T) {
	bin := build.Binary(t)
	if filepath.Dir(filepath.Dir(bin)) != filepath.Dir(t.TempDir()) {
		t.Errorf("the binary is at %s, outside the test's temporary directory", bin)
	}
	tests := []struct {
		name           string
		args           []string
		stdout, stderr string
		status         int
	}{
		{"exits with 0", []string{"--exit-after", "0"}, "", "", 0},
		{"exit code", []string{"--exit-after", "10ms", "--exit-code", "3"}, "", "", 3},
		{"stdout", []string{"--stdout", "hello there", "--exit-after", "0"}, "hello there\n", "", 0},
		{"stderr", []string{"--stderr", "oh no", "--exit-after", "0", "--exit-code", "1"}, "", "oh no\n", 1},
		{"an empty text is a line", []string{"--stdout", "", "--stderr", "x", "--exit-after", "0"}, "\n", "x\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, status := run(t, bin, tt.args...)
			if stdout != tt.stdout || stderr != tt.stderr || status != tt.status {
				t.Errorf("got stdout %q, stderr %q, status %d; want %q, %q, %d",
					stdout, stderr, status, tt.stdout, tt.stderr, tt.status)
			}
		})
	}
}

// started starts the program and returns it when it has written the line
// "ready", with the lines it wrote before.
func started(t *testing.T, bin string, args ...string) (*exec.Cmd, []string) {
	t.Helper()
	cmd := exec.Command(bin, append(args, "--stdout", "ready")...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	var lines []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if sc.Text() == "ready" {
			return cmd, lines
		}
		lines = append(lines, sc.Text())
	}
	t.Fatalf("testservice %q ended before it was ready", args)
	return nil, nil
}

func TestBinaryEndsOnSIGTERM(t *testing.T) {
	bin := build.Binary(t)
	cmd, _ := started(t, bin)
	cmd.Process.Signal(syscall.SIGTERM)
	state, err := cmd.Process.Wait()
	if err != nil || state.ExitCode() != 0 {
		t.Errorf("after SIGTERM: %v, %v; want exit status 0", state, err)
	}
}

func TestBinaryIgnoresSIGTERM(t *testing.T) {
	bin := build.Binary(t)
	cmd, _ := started(t, bin, "--ignore-term", "--exit-after", "300ms", "--exit-code", "5")
	cmd.Process.Signal(syscall.SIGTERM)
	state, err := cmd.Process.Wait()
	if err != nil || state.ExitCode() != 5 {
		t.Errorf("after SIGTERM: %v, %v; want it ignored and exit status 5", state, err)
	}
}

func TestBinarySpawnsAChildInItsGroup(t *testing.T) {
	bin := build.Binary(t)
	cmd, lines := started(t, bin, "--spawn")
	var child int
	if len(lines) != 1 {
		t.Fatalf("got the lines %q before ready, want one: child <pid>", lines)
	}
	if _, err := fmt.Sscanf(lines[0], "child %d", &child); err != nil {
		t.Fatalf("got the line %q, want child <pid>", lines[0])
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })

	pgid, err := syscall.Getpgid(child)
	if err != nil {
		t.Fatalf("the child %d: %v", child, err)
	}
	want, _ := syscall.Getpgid(cmd.Process.Pid)
	if pgid != want {
		t.Errorf("the child is in process group %d, want %d, that of testservice", pgid, want)
	}

	// The child ignores SIGTERM: it is there when testservice has ended
	// by one.
	syscall.Kill(child, syscall.SIGTERM)
	cmd.Process.Signal(syscall.SIGTERM)
	if state, err := cmd.Process.Wait(); err != nil || state.ExitCode() != 0 {
		t.Errorf("after SIGTERM: %v, %v; want exit status 0", state, err)
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Errorf("the child is gone after SIGTERM: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./testservice/build/`
Expected: FAIL — `no non-test Go files in …/testservice/build`

- [ ] **Step 3: Write the test service and its builder**

Create `testservice/main.go`:

```go
// Command testservice is a service for tests: it writes what it is told
// to, and exits, hangs or ignores SIGTERM on request.
//
//	testservice [--stdout TEXT] [--stderr TEXT] [--exit-after D] [--exit-code N] [--ignore-term] [--spawn]
//
// It writes TEXT and a line feed to the stream named, once, at its start,
// and after it has arranged how it takes SIGTERM: a test that has read
// the text knows that SIGTERM does what the flags say. With --exit-after
// it exits after D with --exit-code (0 unless given). Without, it runs
// until SIGTERM and exits with 0, or with --ignore-term until it is
// killed. --spawn starts a child in its process group that ignores
// SIGTERM and writes its process ID to standard output as "child <pid>".
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// childEnv is set in the environment of the child that --spawn starts.
const childEnv = "TESTSERVICE_CHILD"

func main() {
	if os.Getenv(childEnv) != "" {
		child()
	}
	stdout := flag.String("stdout", "", "write `TEXT` and a line feed to standard output at the start")
	stderr := flag.String("stderr", "", "write `TEXT` and a line feed to standard error at the start")
	exitAfter := flag.Duration("exit-after", 0, "exit after `D`")
	exitCode := flag.Int("exit-code", 0, "the exit status of --exit-after")
	ignoreTerm := flag.Bool("ignore-term", false, "ignore SIGTERM")
	spawn := flag.Bool("spawn", false, "start a child in this process group that ignores SIGTERM")
	flag.Parse()
	given := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { given[f.Name] = true })

	term := make(chan os.Signal, 1)
	if *ignoreTerm {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(term, syscall.SIGTERM)
	}
	if *spawn {
		pid, err := startChild()
		if err != nil {
			fmt.Fprintln(os.Stderr, "testservice:", err)
			os.Exit(2)
		}
		fmt.Printf("child %d\n", pid)
	}
	if given["stdout"] {
		fmt.Fprintln(os.Stdout, *stdout)
	}
	if given["stderr"] {
		fmt.Fprintln(os.Stderr, *stderr)
	}

	var after <-chan time.Time
	if given["exit-after"] {
		after = time.After(*exitAfter)
	}
	// The ticker keeps the select from being the last thing that could
	// ever happen, which the Go runtime would take for a deadlock.
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-after:
			os.Exit(*exitCode)
		case <-term:
			os.Exit(0)
		case <-tick.C:
		}
	}
}

// startChild starts this program again as the child of --spawn, and
// returns its process ID when the child ignores SIGTERM.
func startChild() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	defer r.Close()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{w} // file descriptor 3 of the child
	err = cmd.Start()
	w.Close()
	if err != nil {
		return 0, err
	}
	// The child closes its end when it is ready.
	io.Copy(io.Discard, r)
	return cmd.Process.Pid, nil
}

// child is what --spawn starts: it ignores SIGTERM, says so by closing
// file descriptor 3, and stays until it is killed. It keeps the standard
// output and standard error of its parent open.
func child() {
	signal.Ignore(syscall.SIGTERM)
	os.NewFile(3, "ready").Close()
	for {
		time.Sleep(time.Hour)
	}
}
```

Create `testservice/build/build.go`:

```go
// Package build builds the program testservice for the tests that need a
// real service.
package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Binary builds testservice into tb.TempDir() and returns its path.
func Binary(tb testing.TB) string {
	tb.Helper()
	out := filepath.Join(tb.TempDir(), "testservice")
	cmd := exec.Command("go", "build", "-o", out, "github.com/fables-for-robots/great-machine/testservice")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("building testservice: %v\n%s", err, output)
	}
	return out
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go vet ./testservice/... && go test -race ./testservice/...`
Expected: PASS for `testservice/build`, and `[no test files]` for `testservice`. `git status` shows no binary: it was built into the test's temporary directory.

- [ ] **Step 5: Commit**

```bash
git add testservice/
git commit -m "Testservice: a service for tests, and its builder"
```

- [ ] **Step 6: Write the failing test of CheckExecutable**

Create `supervisor/executable_test.go`:

```go
package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckExecutable(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	write := func(name string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path(name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		// WriteFile's mode passes the umask; this one does not.
		if err := os.Chmod(path(name), mode); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, path(name)); err != nil {
			t.Fatal(err)
		}
	}
	write("program", 0o755)
	write("owner-only", 0o700)
	write("others-only", 0o601)
	write("data", 0o644)
	if err := os.Mkdir(path("directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(path("program"), "link-to-program")
	link("program", "relative-link")
	link(path("link-to-program"), "link-to-link")
	link(path("data"), "link-to-data")
	link(path("directory"), "link-to-directory")
	link(path("nothing"), "dangling-link")

	tests := []struct {
		name string
		ok   bool
	}{
		{"program", true},
		{"owner-only", true},
		{"others-only", true},
		{"link-to-program", true},
		{"relative-link", true},
		{"link-to-link", true},
		{"data", false},
		{"link-to-data", false},
		{"directory", false},
		{"link-to-directory", false},
		{"dangling-link", false},
		{"missing", false},
		{"program/below-a-file", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckExecutable(path(tt.name))
			if tt.ok {
				if err != nil {
					t.Errorf("got %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrNotExecutable) {
				t.Fatalf("got %v, want ErrNotExecutable in its chain", err)
			}
			if !strings.Contains(err.Error(), path(tt.name)) {
				t.Errorf("%q does not name the path", err)
			}
		})
	}
}
```

- [ ] **Step 7: Run the test to see it fail**

Run: `go test ./supervisor/`
Expected: FAIL — the package does not build: `undefined: CheckExecutable`, `undefined: ErrNotExecutable`

- [ ] **Step 8: Write CheckExecutable**

Create `supervisor/executable.go`:

```go
package supervisor

import (
	"errors"
	"fmt"
	"os"
)

// ErrNotExecutable is in the chain of what Activate and CheckExecutable
// return for a binary that is no file this process may execute.
var ErrNotExecutable = errors.New("not an executable file")

// CheckExecutable returns nil when path, through its symbolic links, is a
// regular file with an execute bit.
func CheckExecutable(path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		// os.Stat names the path in its error.
		return fmt.Errorf("%w: %w", ErrNotExecutable, err)
	case info.IsDir():
		return fmt.Errorf("%w: %s is a directory", ErrNotExecutable, path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is no regular file", ErrNotExecutable, path)
	case info.Mode().Perm()&0o111 == 0:
		return fmt.Errorf("%w: %s has no execute bit", ErrNotExecutable, path)
	}
	return nil
}
```

- [ ] **Step 9: Run the test to see it pass**

Run: `go test ./supervisor/`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add supervisor/
git commit -m "Supervisor: CheckExecutable"
```

- [ ] **Step 11: Write the failing tests of the supervisor**

Create `supervisor/supervisor_test.go`:

```go
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// eventually polls cond until it is true, and fails the test when it is
// not within 20 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not within 20 seconds: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// harness is a supervisor that runs, and its ring.
type harness struct {
	*Supervisor
	t      *testing.T
	ring   *logring.Ring
	cancel context.CancelFunc
	done   chan error // what Run returned
}

// run starts a supervisor with cfg, in which durations that are zero are
// made short: a service that ends within 300 ms waits 20 ms, 40 ms and
// then 80 ms. The stop timeout is long, so that only a test that sets it
// sees a SIGKILL. The supervisor is stopped when the test ends.
func run(t *testing.T, cfg Config) *harness {
	t.Helper()
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 30 * time.Second
	}
	if cfg.QuickExit == 0 {
		cfg.QuickExit = 300 * time.Millisecond
	}
	if cfg.BackoffMin == 0 {
		cfg.BackoffMin = 20 * time.Millisecond
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 80 * time.Millisecond
	}
	h := &harness{t: t, ring: logring.New(400, 16<<10), done: make(chan error, 1)}
	cfg.Ring = h.ring
	cfg.Log = slog.New(slog.DiscardHandler)
	h.Supervisor = New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- h.Run(ctx) }()
	t.Cleanup(func() { h.stop() })
	// An Activate before Run has begun is kept for Run, and the tests
	// look at what an Activate does that Run takes.
	eventually(t, "Run has begun", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.began
	})
	return h
}

// stop ends Run and returns what it returned.
func (h *harness) stop() error {
	h.t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(60 * time.Second):
		h.t.Fatal("Run did not return within 60 seconds of the end of its context")
	}
	return nil
}

// lines returns the texts of the ring's lines of stream.
func (h *harness) lines(stream string) []string {
	var out []string
	for _, l := range h.ring.Lines() {
		if l.Stream == stream {
			out = append(out, l.Text)
		}
	}
	return out
}

// matching returns the supervisor's own lines that match re.
func (h *harness) matching(re string) []string {
	var out []string
	exp := regexp.MustCompile(re)
	for _, text := range h.lines(logring.Zathras) {
		if exp.MatchString(text) {
			out = append(out, text)
		}
	}
	return out
}

// wrote waits for the service to write text as a line of stream.
func (h *harness) wrote(stream, text string) {
	h.t.Helper()
	eventually(h.t, fmt.Sprintf("the line %q on %s", text, stream), func() bool {
		return slices.Contains(h.lines(stream), text)
	})
}

// phase waits for the phase and returns the state that has it.
func (h *harness) phase(want wire.Phase) wire.State {
	h.t.Helper()
	var st wire.State
	eventually(h.t, "phase "+string(want), func() bool {
		st = h.State()
		return st.Phase == want
	})
	return st
}

// activate fails the test when Activate does.
func (h *harness) activate(generation uint64, binary string, args ...string) {
	h.t.Helper()
	if err := h.Activate(generation, binary, args); err != nil {
		h.t.Fatalf("Activate(%d, %s, %q): %v", generation, binary, args, err)
	}
}

// alive says whether the process is there. A process that has ended and
// that nobody has waited for yet is not.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// On Linux a zombie still takes signals. Its state is the field
	// behind the name, which is in parentheses.
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	rest := stat[bytes.LastIndexByte(stat, ')')+1:]
	return !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("Z"))
}

// script writes a shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// buffer is a bytes.Buffer for more than one goroutine.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestIdleUntilActivateThenRunning(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})

	st := h.State()
	if st.Phase != wire.PhaseIdle || st.Generation != 0 || st.Binary != "" || st.PID != 0 ||
		st.RunningFor != 0 || !st.RestartAt.IsZero() || st.LastExit != nil || st.Restarts != 0 {
		t.Fatalf("before Activate: %+v, want idle and nothing else", st)
	}

	h.activate(7, bin, "--stdout", "ready")
	st = h.State()
	if st.Phase != wire.PhaseRunning {
		t.Fatalf("when Activate has returned the phase is %s, want running", st.Phase)
	}
	if st.Generation != 7 || st.Binary != bin || !slices.Equal(st.Args, []string{"--stdout", "ready"}) {
		t.Errorf("got generation %d, binary %s, args %q", st.Generation, st.Binary, st.Args)
	}
	if st.PID <= 0 || !alive(st.PID) {
		t.Errorf("PID %d is no process that runs", st.PID)
	}
	if !st.RestartAt.IsZero() || st.LastExit != nil || st.Restarts != 0 {
		t.Errorf("got RestartAt %v, LastExit %+v, Restarts %d; want none", st.RestartAt, st.LastExit, st.Restarts)
	}
	if st.Prepared != nil || st.Preparing != 0 || st.Sample != nil {
		t.Errorf("fields that are not the supervisor's are set: %+v", st)
	}
	eventually(t, "RunningFor grows", func() bool {
		later := h.State()
		return later.RunningFor > st.RunningFor && later.PID == st.PID
	})

	// What State returns is the caller's.
	st.Args[0] = "changed"
	if got := h.State().Args[0]; got != "--stdout" {
		t.Errorf("the state's args were changed through what State returned: %q", got)
	}

	want := fmt.Sprintf("started generation 7: %s (pid %d)", bin, st.PID)
	if got := h.lines(logring.Zathras); !slices.Equal(got, []string{want}) {
		t.Errorf("the supervisor's lines are %q, want %q", got, want)
	}
}

func TestActivateBeforeRun(t *testing.T) {
	bin := build.Binary(t)
	ring := logring.New(400, 16<<10)
	s := New(Config{Ring: ring, Log: slog.New(slog.DiscardHandler)})
	if err := s.Activate(3, bin, []string{"--stdout", "ready"}); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Generation != 3 || st.Binary != bin || st.Phase != wire.PhaseIdle {
		t.Errorf("before Run: %+v, want generation 3, idle", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	eventually(t, "running", func() bool { return s.State().Phase == wire.PhaseRunning })
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run returned %v", err)
	}
}

func TestArgumentsReachTheProgramVerbatim(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})
	texts := []string{
		"two words",
		`"double" and 'single' quotes`,
		`back\slash $HOME * ~ ; | &`,
		"  blanks at both ends  ",
		"zażółć gęślą jaźń 日本語 😀",
		"--exit-code",
	}
	for i, text := range texts {
		h.activate(uint64(i+1), bin, "--stdout", text)
		h.wrote(logring.Stdout, text)
	}
	// An empty argument: were it lost, --stderr would be taken for the
	// text of --stdout.
	h.activate(100, bin, "--stdout", "", "--stderr", "after the empty one")
	h.wrote(logring.Stderr, "after the empty one")
	h.wrote(logring.Stdout, "")
	if got := h.State().Args; !slices.Equal(got, []string{"--stdout", "", "--stderr", "after the empty one"}) {
		t.Errorf("the state has the args %q", got)
	}
}

func TestOutputGoesToTheRingAndToTheCopies(t *testing.T) {
	bin := build.Binary(t)
	last := script(t, "printf 'no line feed'\nprintf 'none here either' >&2\n")
	var stdout, stderr buffer
	h := run(t, Config{Stdout: &stdout, Stderr: &stderr, QuickExit: time.Hour})

	h.activate(1, bin, "--stdout", "to stdout", "--stderr", "to stderr")
	h.wrote(logring.Stdout, "to stdout")
	h.wrote(logring.Stderr, "to stderr")
	eventually(t, "the copies", func() bool {
		return stdout.String() == "to stdout\n" && stderr.String() == "to stderr\n"
	})
	if slices.Contains(h.lines(logring.Stderr), "to stdout") || slices.Contains(h.lines(logring.Stdout), "to stderr") {
		t.Errorf("the streams are mixed: stdout %q, stderr %q", h.lines(logring.Stdout), h.lines(logring.Stderr))
	}
	pid := h.State().PID

	// A last line without a line feed is in the ring when the service
	// has ended.
	h.activate(2, last)
	h.wrote(logring.Stdout, "no line feed")
	h.wrote(logring.Stderr, "none here either")
	eventually(t, "the copies of the last lines", func() bool {
		return stdout.String() == "to stdout\nno line feed" && stderr.String() == "to stderr\nnone here either"
	})

	// The supervisor's own lines, in the contract's words.
	eventually(t, "the line of the exit of generation 2", func() bool {
		return len(h.matching(`^generation 2 exited`)) > 0
	})
	got := h.lines(logring.Zathras)
	if len(got) < 4 {
		t.Fatalf("the supervisor's lines are %q, want four at least", got)
	}
	want := []string{
		fmt.Sprintf(`^started generation 1: %s \(pid %d\)$`, regexp.QuoteMeta(bin), pid),
		`^generation 1 exited with status 0 after [0-9.]+m?s$`,
		fmt.Sprintf(`^started generation 2: %s \(pid [0-9]+\)$`, regexp.QuoteMeta(last)),
		`^generation 2 exited with status 0 after [0-9.]+m?s; starting it again in 20ms$`,
	}
	for i, re := range want {
		if !regexp.MustCompile(re).MatchString(got[i]) {
			t.Errorf("line %d of the supervisor is %q, want it to match %s", i, got[i], re)
		}
	}
	for _, l := range h.ring.Lines() {
		if l.At.IsZero() {
			t.Errorf("a line without a time: %+v", l)
		}
	}
}

func TestServiceThatRanLongIsStartedAgainAtOnce(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: 100 * time.Millisecond})

	// Every phase the supervisor says it is in, as far as the test gets
	// to see them.
	var mu sync.Mutex
	var phases []wire.Phase
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		for {
			select {
			case <-h.Changes():
				mu.Lock()
				phases = append(phases, h.State().Phase)
				mu.Unlock()
			case <-watching:
				return
			}
		}
	}()

	// Exit status 0 is no reason to leave a service down.
	h.activate(1, bin, "--exit-after", "600ms")
	first := h.State().PID
	var st wire.State
	eventually(t, "the second start", func() bool {
		st = h.State()
		return st.Restarts == 1 && st.Phase == wire.PhaseRunning
	})
	if st.PID == first || st.PID <= 0 {
		t.Errorf("the PID is %d after the restart, and was %d before", st.PID, first)
	}
	if st.LastExit == nil || st.LastExit.Status != 0 || st.LastExit.Signal != "" || st.LastExit.Generation != 1 {
		t.Fatalf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if st.LastExit.Ran < 100*time.Millisecond {
		t.Errorf("it ran for %s, want more than QuickExit", st.LastExit.Ran)
	}
	if !st.RestartAt.IsZero() {
		t.Errorf("RestartAt is %v, want the zero time", st.RestartAt)
	}
	exits := h.matching(`^generation 1 exited`)
	if len(exits) == 0 || !regexp.MustCompile(`^generation 1 exited with status 0 after [0-9.]+m?s; starting it again$`).MatchString(exits[0]) {
		t.Errorf("the lines of the exit are %q, want one that ends with: starting it again", exits)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Contains(phases, wire.PhaseBackoff) {
		t.Errorf("the phases were %v, want no backoff", phases)
	}
}

// delays returns, for every exit of the generation that the supervisor
// has written a line of, what follows "starting it again": " in 20ms", or
// "" for at once.
func (h *harness) delays(generation uint64) []string {
	exp := regexp.MustCompile(fmt.Sprintf(`^generation %d .*; starting it again(.*)$`, generation))
	var out []string
	for _, text := range h.lines(logring.Zathras) {
		if m := exp.FindStringSubmatch(text); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func TestServiceThatEndsSoonWaitsTwiceAsLongEachTime(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(3, bin, "--exit-after", "0", "--exit-code", "7")

	st := h.phase(wire.PhaseBackoff)
	if st.PID != 0 || st.RunningFor != 0 {
		t.Errorf("in backoff: PID %d, RunningFor %s; want none", st.PID, st.RunningFor)
	}
	if st.Generation != 3 || st.Binary != bin {
		t.Errorf("in backoff: generation %d, binary %s", st.Generation, st.Binary)
	}
	exit := st.LastExit
	if exit == nil || exit.Status != 7 || exit.Signal != "" || exit.Error != "" || exit.Generation != 3 {
		t.Fatalf("LastExit is %+v, want status 7 of generation 3", exit)
	}
	if exit.Ran <= 0 || exit.Ran > time.Minute || exit.At.IsZero() || time.Since(exit.At) > time.Minute {
		t.Errorf("LastExit is %+v, want the time it ended and how long it ran", exit)
	}
	// The delay is what lies between the end and RestartAt.
	delay := st.RestartAt.Sub(exit.At)
	if !slices.Contains([]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond}, delay) {
		t.Errorf("RestartAt is %s after the end, want 20ms, 40ms or 80ms", delay)
	}

	eventually(t, "six exits", func() bool { return len(h.delays(3)) >= 6 })
	want := []string{" in 20ms", " in 40ms", " in 80ms", " in 80ms", " in 80ms", " in 80ms"}
	if got := h.delays(3)[:6]; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
	eventually(t, "five restarts", func() bool { return h.State().Restarts >= 5 })
	if line := h.matching(`^generation 3 exited`)[0]; !regexp.MustCompile(`^generation 3 exited with status 7 after [0-9.]+m?s; starting it again in 20ms$`).MatchString(line) {
		t.Errorf("the line of the first exit is %q", line)
	}

	// The gaps between the starts are the delays at least.
	var starts []time.Time
	for _, l := range h.ring.Lines() {
		if l.Stream == logring.Zathras && strings.HasPrefix(l.Text, "started generation 3") {
			starts = append(starts, l.At)
		}
	}
	for i, min := range []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 80 * time.Millisecond} {
		if gap := starts[i+1].Sub(starts[i]); gap < min {
			t.Errorf("start %d came %s after the one before, want %s at least", i+1, gap, min)
		}
	}
}

func TestLongRunMakesTheNextDelayTheFirstAgain(t *testing.T) {
	// The fourth run is a long one, every other ends at once.
	service := script(t, `n=$(cat "$0.runs" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$0.runs"
if [ "$n" -eq 4 ]; then sleep 2; fi
exit 1
`)
	h := run(t, Config{QuickExit: time.Second})
	h.activate(1, service)
	eventually(t, "six exits", func() bool { return len(h.delays(1)) >= 6 })
	want := []string{" in 20ms", " in 40ms", " in 80ms", "", " in 20ms", " in 40ms"}
	if got := h.delays(1)[:6]; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
}

func TestServiceEndedBySignal(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	var st wire.State
	eventually(t, "the exit", func() bool {
		st = h.State()
		return st.LastExit != nil
	})
	if st.LastExit.Status != -1 || st.LastExit.Signal != "SIGKILL" || st.LastExit.Error != "" {
		t.Errorf("LastExit is %+v, want status -1 and SIGKILL", st.LastExit)
	}
	if got := h.matching(`^generation 1 was ended by SIGKILL after [0-9.]+m?s; starting it again in 20ms$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the end by SIGKILL", h.lines(logring.Zathras))
	}
	eventually(t, "the restart", func() bool {
		st = h.State()
		return st.Phase == wire.PhaseRunning && st.Restarts == 1 && st.PID != pid
	})
}

func TestBinaryThatCannotBeStarted(t *testing.T) {
	// A file with an execute bit that is no program.
	noProgram := filepath.Join(t.TempDir(), "no-program")
	if err := os.WriteFile(noProgram, []byte("this is no program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{})
	h.activate(1, noProgram)

	st := h.State()
	if st.Phase != wire.PhaseBackoff || st.Generation != 1 || st.PID != 0 {
		t.Fatalf("when Activate has returned: %+v, want backoff of generation 1", st)
	}
	exit := st.LastExit
	if exit == nil || exit.Status != -1 || exit.Error == "" || exit.Signal != "" || exit.Generation != 1 || exit.Ran != 0 {
		t.Fatalf("LastExit is %+v, want status -1 and the error", exit)
	}
	if !strings.Contains(exit.Error, "exec format error") {
		t.Errorf("the error is %q, want it to say: exec format error", exit.Error)
	}
	if delay := st.RestartAt.Sub(exit.At); delay != 20*time.Millisecond {
		t.Errorf("RestartAt is %s after the end, want 20ms", delay)
	}
	eventually(t, "three tries", func() bool { return len(h.delays(1)) >= 3 })
	if got, want := h.delays(1)[:3], []string{" in 20ms", " in 40ms", " in 80ms"}; !slices.Equal(got, want) {
		t.Errorf("the delays were %q, want %q", got, want)
	}
	line := h.matching(`^generation 1 could not be started`)[0]
	if !regexp.MustCompile(`^generation 1 could not be started: .*exec format error; starting it again in 20ms$`).MatchString(line) {
		t.Errorf("the line is %q", line)
	}
	if got := h.matching(`^started`); len(got) != 0 {
		t.Errorf("lines of starts that never were: %q", got)
	}
}

func TestActivateRefusesWhatIsNoExecutableFile(t *testing.T) {
	bin := build.Binary(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	if err := os.WriteFile(data, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{})

	// With nothing running, and with a service running.
	for _, running := range []bool{false, true} {
		if running {
			h.activate(1, bin, "--stdout", "ready")
			h.wrote(logring.Stdout, "ready")
		}
		before := h.State()
		for name, path := range map[string]string{
			"a missing file":                 filepath.Join(dir, "missing"),
			"a directory":                    dir,
			"a file without the execute bit": data,
		} {
			err := h.Activate(2, path, []string{"x"})
			if !errors.Is(err, ErrNotExecutable) {
				t.Errorf("running %v, %s: Activate returned %v, want ErrNotExecutable", running, name, err)
			}
			after := h.State()
			if after.Generation != before.Generation || after.Binary != before.Binary || after.PID != before.PID ||
				after.Phase != before.Phase || !slices.Equal(after.Args, before.Args) {
				t.Errorf("running %v, %s: the state went from %+v to %+v", running, name, before, after)
			}
		}
		if running && !alive(before.PID) {
			t.Errorf("the service was stopped")
		}
	}
	if got := h.matching(`generation 2`); len(got) != 0 {
		t.Errorf("the supervisor wrote of the generation it refused: %q", got)
	}
}

func TestActivateReplacesTheServiceThatRuns(t *testing.T) {
	bin := build.Binary(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.Link(bin, other); err != nil {
		t.Fatal(err)
	}
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--stdout", "one")
	h.wrote(logring.Stdout, "one")
	old := h.State().PID

	h.activate(2, other, "--stdout", "two")
	// At once: the generation and the binary are the new ones, and the
	// phase is not that of the old service.
	st := h.State()
	if st.Generation != 2 || st.Binary != other || !slices.Equal(st.Args, []string{"--stdout", "two"}) {
		t.Errorf("when Activate has returned: generation %d, binary %s, args %q", st.Generation, st.Binary, st.Args)
	}
	if st.PID == old || (st.Phase != wire.PhaseStopping && st.Phase != wire.PhaseRunning) {
		t.Errorf("when Activate has returned: phase %s, PID %d (the old one is %d)", st.Phase, st.PID, old)
	}

	st = h.phase(wire.PhaseRunning)
	h.wrote(logring.Stdout, "two")
	if st.PID == old || st.PID <= 0 || st.Generation != 2 {
		t.Errorf("running: %+v, the old PID is %d", st, old)
	}
	// The old one was sent SIGTERM, which ends testservice with 0.
	if st.LastExit == nil || st.LastExit.Generation != 1 || st.LastExit.Status != 0 || st.LastExit.Signal != "" {
		t.Errorf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if st.Restarts != 0 {
		t.Errorf("Restarts is %d: a start after Activate is no restart", st.Restarts)
	}
	if alive(old) {
		t.Errorf("the old service, %d, is still there", old)
	}
	if got := h.delays(1); len(got) != 0 {
		t.Errorf("the supervisor wrote that it starts generation 1 again: %q", h.lines(logring.Zathras))
	}
}

func TestServiceThatIgnoresSIGTERMIsKilledAfterStopTimeout(t *testing.T) {
	bin := build.Binary(t)
	const stopTimeout = 400 * time.Millisecond
	h := run(t, Config{StopTimeout: stopTimeout, QuickExit: time.Hour})
	h.activate(1, bin, "--ignore-term", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	old := h.State().PID

	asked := time.Now()
	h.activate(2, bin, "--stdout", "two")
	st := h.State()
	if st.Phase != wire.PhaseStopping || st.Generation != 2 || st.PID != 0 || st.RunningFor != 0 {
		t.Errorf("when Activate has returned: %+v, want stopping, generation 2, no PID", st)
	}
	if !alive(old) {
		t.Errorf("the service that ignores SIGTERM is gone before the stop timeout")
	}

	st = h.phase(wire.PhaseRunning)
	if waited := time.Since(asked); waited < stopTimeout {
		t.Errorf("generation 2 runs after %s, want the stop timeout of %s to pass first", waited, stopTimeout)
	}
	if st.LastExit == nil || st.LastExit.Generation != 1 || st.LastExit.Status != -1 || st.LastExit.Signal != "SIGKILL" {
		t.Errorf("LastExit is %+v, want SIGKILL of generation 1", st.LastExit)
	}
	if st.LastExit != nil && st.LastExit.Ran < stopTimeout {
		t.Errorf("it ran for %s, want the stop timeout at least", st.LastExit.Ran)
	}
	if got := h.matching(`^generation 1 was ended by SIGKILL after [0-9.]+m?s$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the end by SIGKILL", h.lines(logring.Zathras))
	}
	h.wrote(logring.Stdout, "two")
}

func TestWhatTheServiceLeftInItsGroupIsKilled(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})
	h.activate(1, bin, "--spawn", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	var child int
	for _, text := range h.lines(logring.Stdout) {
		fmt.Sscanf(text, "child %d", &child)
	}
	if child == 0 {
		t.Fatalf("no line child <pid> in %q", h.lines(logring.Stdout))
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if !alive(child) {
		t.Fatalf("the child %d does not run", child)
	}

	// The service ends at SIGTERM. Its child ignores it, and holds the
	// output of the service open.
	h.activate(2, bin, "--stdout", "two")
	st := h.phase(wire.PhaseRunning)
	if st.LastExit == nil || st.LastExit.Status != 0 {
		t.Errorf("LastExit is %+v, want status 0", st.LastExit)
	}
	eventually(t, "the child is gone", func() bool { return !alive(child) })
}

func TestActivateDuringBackoffStartsAtOnce(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour, BackoffMin: 20 * time.Millisecond, BackoffMax: time.Hour})
	h.activate(1, bin, "--exit-after", "0", "--exit-code", "1")
	// 20 ms, 40 ms, 80 ms, 160 ms, 320 ms, and then 640 ms and more to
	// be in backoff in.
	eventually(t, "six exits of generation 1", func() bool { return len(h.delays(1)) >= 6 })
	st := h.phase(wire.PhaseBackoff)
	if st.RestartAt.IsZero() {
		t.Errorf("in backoff without RestartAt")
	}

	h.activate(2, bin, "--exit-after", "0", "--exit-code", "2")
	if got := h.matching(`^started generation 2`); len(got) != 1 {
		t.Fatalf("when Activate has returned, generation 2 was not started: %q", h.lines(logring.Zathras))
	}
	eventually(t, "two exits of generation 2", func() bool { return len(h.delays(2)) >= 2 })
	if got, want := h.delays(2)[:2], []string{" in 20ms", " in 40ms"}; !slices.Equal(got, want) {
		t.Errorf("the delays of generation 2 were %q, want %q: the backoff was not forgotten", got, want)
	}
	if got := len(h.matching(`^started generation 1`)); got != 6 {
		t.Errorf("generation 1 was started %d times, want 6: it was started after Activate", got)
	}
}

func TestEndOfContextStopsTheService(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID

	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if alive(pid) {
		t.Errorf("the service %d runs after Run has returned", pid)
	}
	st := h.State()
	if st.Phase != wire.PhaseStopping || st.PID != 0 || st.Generation != 1 {
		t.Errorf("after Run: %+v, want stopping, no PID, generation 1", st)
	}
	if st.LastExit == nil || st.LastExit.Status != 0 || st.LastExit.Generation != 1 {
		t.Errorf("LastExit is %+v, want status 0 of generation 1", st.LastExit)
	}
	if err := h.Activate(2, bin, nil); !errors.Is(err, ErrStopped) {
		t.Errorf("Activate after Run returned %v, want ErrStopped", err)
	}
	if st := h.State(); st.Generation != 1 {
		t.Errorf("the refused Activate changed the generation to %d", st.Generation)
	}
	if got := h.matching(`^started`); len(got) != 1 {
		t.Errorf("starts: %q, want the one", got)
	}
	if got := h.matching(`^generation 1 exited with status 0 after [0-9.]+m?s$`); len(got) != 1 {
		t.Errorf("the supervisor's lines are %q, want one of the exit, with nothing of a start behind it", h.lines(logring.Zathras))
	}
}

func TestEndOfContextKillsAfterStopTimeout(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{StopTimeout: 300 * time.Millisecond})
	h.activate(1, bin, "--ignore-term", "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	pid := h.State().PID

	asked := time.Now()
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if waited := time.Since(asked); waited < 300*time.Millisecond {
		t.Errorf("Run returned after %s, want the stop timeout to pass first", waited)
	}
	if alive(pid) {
		t.Errorf("the service %d runs after Run has returned", pid)
	}
	if exit := h.State().LastExit; exit == nil || exit.Signal != "SIGKILL" {
		t.Errorf("LastExit is %+v, want SIGKILL", exit)
	}
}

func TestEndOfContextDuringBackoffStartsNothing(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour, BackoffMin: time.Hour, BackoffMax: time.Hour})
	h.activate(1, bin, "--exit-after", "0")
	h.phase(wire.PhaseBackoff)
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	st := h.State()
	if st.Phase != wire.PhaseStopping || !st.RestartAt.IsZero() {
		t.Errorf("after Run: phase %s, RestartAt %v; want stopping and the zero time", st.Phase, st.RestartAt)
	}
	if got := h.matching(`^started`); len(got) != 1 {
		t.Errorf("starts: %q, want the one", got)
	}
}

func TestEndOfContextWhileIdle(t *testing.T) {
	h := run(t, Config{})
	if err := h.stop(); err != nil {
		t.Errorf("Run returned %v, want nil", err)
	}
	if st := h.State(); st.Phase != wire.PhaseIdle {
		t.Errorf("after Run: phase %s, want idle", st.Phase)
	}
	if err := h.Activate(1, "/bin/sh", nil); !errors.Is(err, ErrStopped) {
		t.Errorf("Activate after Run returned %v, want ErrStopped", err)
	}
}

func TestChanges(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{QuickExit: time.Hour})

	// changed waits for a value, and for the state that cond describes.
	changed := func(what string, cond func(wire.State) bool) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case <-h.Changes():
				if cond(h.State()) {
					return
				}
			case <-deadline:
				t.Fatalf("no value on Changes for: %s", what)
			}
		}
	}
	select {
	case <-h.Changes():
		t.Fatal("a value on Changes before anything changed")
	default:
	}

	h.activate(1, bin, "--stdout", "ready")
	changed("the start", func(st wire.State) bool { return st.Phase == wire.PhaseRunning && st.Generation == 1 })
	h.wrote(logring.Stdout, "ready")

	h.activate(2, bin, "--exit-after", "0")
	changed("the next generation", func(st wire.State) bool { return st.Generation == 2 })
	changed("the backoff", func(st wire.State) bool { return st.Phase == wire.PhaseBackoff })
	changed("the restart", func(st wire.State) bool { return st.Restarts >= 1 })

	// Nobody reads Changes from here on, and the supervisor goes on.
	before := h.State().Restarts
	eventually(t, "five more restarts", func() bool { return h.State().Restarts >= before+5 })
	if n := len(h.Changes()); n != 1 {
		t.Errorf("Changes holds %d values, want the one it has room for", n)
	}
}
```

- [ ] **Step 12: Run the tests to see them fail**

Run: `go test ./supervisor/`
Expected: FAIL — the package does not build: `undefined: Supervisor`, `undefined: Config`, `undefined: New`, and more of the kind.

- [ ] **Step 13: Write the supervisor**

Create `supervisor/supervisor.go`:

```go
// Package supervisor runs the service of a zathras: it starts the binary
// it is told to, stops it for the next one, and starts it again when it
// has ended, after a delay that grows while the service ends soon.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

// Config is what a Supervisor is made of.
type Config struct {
	Ring           *logring.Ring // required: the service's lines and the supervisor's own
	Stdout, Stderr io.Writer     // copies of the service's output; nil for none
	Env            []string      // nil: os.Environ()
	Dir            string        // "": the directory of this process
	StopTimeout    time.Duration // 0: 2 minutes
	QuickExit      time.Duration // 0: 30 seconds
	BackoffMin     time.Duration // 0: 1 second
	BackoffMax     time.Duration // 0: 2 minutes
	Log            *slog.Logger  // nil: slog.Default()
}

// drainTimeout is how long the output of a service that has ended is
// waited for. Its process group was killed by then, so the wait ends at
// once unless a process of the service left the group and kept the
// output open.
var drainTimeout = time.Second

// ErrStopped is what Activate returns once Run has ended.
var ErrStopped = errors.New("the supervisor has stopped")

// Supervisor runs one service. Its methods may be called from any
// goroutine.
type Supervisor struct {
	cfg     Config
	acts    chan *activation // from Activate to Run
	quit    chan struct{}    // closed when Run begins to stop
	changes chan struct{}

	mu        sync.Mutex
	state     wire.State  // without RunningFor
	startedAt time.Time   // of the service, while it runs
	began     bool        // Run was called
	first     *activation // of an Activate before Run
}

// activation is one call of Activate.
type activation struct {
	generation uint64
	binary     string
	args       []string
	taken      chan struct{} // closed when Run has taken it
}

// New returns a supervisor that is idle. Run makes it work.
func New(cfg Config) *Supervisor {
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = 2 * time.Minute
	}
	if cfg.QuickExit == 0 {
		cfg.QuickExit = 30 * time.Second
	}
	if cfg.BackoffMin == 0 {
		cfg.BackoffMin = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 2 * time.Minute
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Supervisor{
		cfg:     cfg,
		acts:    make(chan *activation),
		quit:    make(chan struct{}),
		changes: make(chan struct{}, 1),
		state:   wire.State{Phase: wire.PhaseIdle},
	}
}

// Activate checks the binary, and refuses with ErrNotExecutable leaving
// what runs alone. Otherwise it takes the generation as its own and
// returns; the service that runs is stopped and binary is started after
// that, by Run.
//
// When Activate returns nil, State shows the generation, and a phase that
// is true of it: running when nothing had to be stopped, stopping while
// the service of the generation before has not ended. An Activate before
// Run is kept for Run to begin with, and the phase stays idle until then.
func (s *Supervisor) Activate(generation uint64, binary string, args []string) error {
	select {
	case <-s.quit:
		return ErrStopped
	default:
	}
	if err := CheckExecutable(binary); err != nil {
		return err
	}
	a := &activation{
		generation: generation,
		binary:     binary,
		args:       slices.Clone(args),
		taken:      make(chan struct{}),
	}

	s.mu.Lock()
	if !s.began {
		s.first = a
		s.state.Generation, s.state.Binary, s.state.Args = a.generation, a.binary, a.args
		s.mu.Unlock()
		s.changed()
		return nil
	}
	s.mu.Unlock()

	select {
	case s.acts <- a:
		<-a.taken
		return nil
	case <-s.quit:
		return ErrStopped
	}
}

// State returns Generation, Binary, Args, Phase, PID, RunningFor,
// RestartAt, LastExit and Restarts. The other fields are the zero value.
func (s *Supervisor) State() wire.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state
	st.Args = slices.Clone(st.Args)
	if st.LastExit != nil {
		exit := *st.LastExit
		st.LastExit = &exit
	}
	if st.Phase == wire.PhaseRunning {
		st.RunningFor = time.Since(s.startedAt)
	}
	return st
}

// Changes has room for one value and gets one after every change of what
// State returns but RunningFor.
func (s *Supervisor) Changes() <-chan struct{} {
	return s.changes
}

// update changes the state and tells Changes.
func (s *Supervisor) update(change func(st *wire.State)) {
	s.mu.Lock()
	change(&s.state)
	s.mu.Unlock()
	s.changed()
}

func (s *Supervisor) changed() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

// say adds a line of the supervisor's own to the ring, and logs it.
func (s *Supervisor) say(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	s.cfg.Ring.Add(logring.Zathras, text)
	s.cfg.Log.Info(text)
}

// Run does the work until ctx is done, then stops the service as the
// spec's 7.1 says and returns nil. Call it once.
//
// When Run has returned the phase is stopping, for good, if there was a
// generation, and idle otherwise.
func (s *Supervisor) Run(ctx context.Context) error {
	r := &runner{s: s}
	s.mu.Lock()
	s.began = true
	r.target, s.first = s.first, nil
	s.mu.Unlock()

	if r.target != nil {
		r.start(nil)
	}
	for {
		var ended chan ending
		if r.proc != nil {
			ended = r.proc.ended
		}
		select {
		case <-ctx.Done():
			r.shutdown()
			return nil
		case a := <-s.acts:
			r.activate(a)
		case e := <-ended:
			r.exited(e)
		case <-r.restartC:
			r.restartC = nil
			r.start(func(st *wire.State) { st.Restarts++ })
		case <-r.killC:
			r.killC = nil
			r.signal(syscall.SIGKILL)
		}
	}
}

// runner is what Run knows and nobody else: only Run's goroutine touches
// it.
type runner struct {
	s        *Supervisor
	target   *activation   // what is to run; nil before the first Activate
	proc     *process      // the service, from its start until Run has heard of its end
	stopping bool          // proc was sent SIGTERM
	delay    time.Duration // the delay of the backoff before; 0 when the next is the first
	restartC <-chan time.Time
	restartT *time.Timer
	killC    <-chan time.Time
	killT    *time.Timer
}

// process is a service that was started.
type process struct {
	pid        int
	generation uint64
	started    time.Time
	ended      chan ending // gets one value, when the service has ended and its output was read
}

// ending is what Wait said of a service, and when.
type ending struct {
	err error
	at  time.Time
}

// activate takes a as the target: it stops the service if one runs, and
// starts the target at once if none does. Any backoff is forgotten.
func (r *runner) activate(a *activation) {
	defer close(a.taken)
	r.target = a
	r.delay = 0
	r.cancelRestart()
	take := func(st *wire.State) {
		st.Generation, st.Binary, st.Args = a.generation, a.binary, a.args
	}
	if r.proc == nil {
		r.start(take)
		return
	}
	r.stop()
	r.s.update(func(st *wire.State) {
		take(st)
		st.Phase = wire.PhaseStopping
		st.PID = 0
	})
}

// stop sends the service SIGTERM, and SIGKILL after StopTimeout, unless
// it was sent SIGTERM already.
func (r *runner) stop() {
	if r.stopping {
		return
	}
	r.stopping = true
	r.signal(syscall.SIGTERM)
	r.killT = time.NewTimer(r.s.cfg.StopTimeout)
	r.killC = r.killT.C
}

// signal sends sig to the process group of the service.
func (r *runner) signal(sig syscall.Signal) {
	if r.proc == nil {
		return
	}
	if err := syscall.Kill(-r.proc.pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		r.s.cfg.Log.Warn("signalling the service", "pid", r.proc.pid, "signal", signalName(sig), "error", err)
	}
}

func (r *runner) cancelRestart() {
	if r.restartT != nil {
		r.restartT.Stop()
	}
	r.restartT, r.restartC = nil, nil
}

func (r *runner) cancelKill() {
	if r.killT != nil {
		r.killT.Stop()
	}
	r.killT, r.killC = nil, nil
}

// start starts the target. The state changes once: before to the state of
// a service that runs, or that waits to be started again when the binary
// could not be started, with the changes of before applied first.
func (r *runner) start(before func(st *wire.State)) {
	if before == nil {
		before = func(*wire.State) {}
	}
	s, t := r.s, r.target
	p, err := s.spawn(t)
	if err != nil {
		// A binary that cannot be started counts as a service that
		// ended at once.
		r.backoff(wire.Exit{Generation: t.generation, Status: -1, Error: err.Error(), At: time.Now()}, before)
		return
	}
	r.proc = p
	s.update(func(st *wire.State) {
		before(st)
		st.Phase = wire.PhaseRunning
		st.PID = p.pid
		st.RestartAt = time.Time{}
		s.startedAt = p.started
	})
	s.say("started generation %d: %s (pid %d)", t.generation, t.binary, p.pid)
}

// exited is called when the service has ended.
func (r *runner) exited(e ending) {
	exit := r.proc.exit(e)
	r.proc = nil
	r.cancelKill()
	if r.stopping {
		// It was stopped for the target, which starts at once.
		r.stopping = false
		r.s.say("%s", describe(exit))
		r.start(func(st *wire.State) { st.LastExit = &exit })
		return
	}
	if exit.Ran >= r.s.cfg.QuickExit {
		r.delay = 0
		r.s.say("%s; starting it again", describe(exit))
		r.start(func(st *wire.State) {
			st.LastExit = &exit
			st.Restarts++
		})
		return
	}
	r.backoff(exit, nil)
}

// backoff records an end that came within QuickExit of the start, and
// waits before the next start: BackoffMin, then twice as long each time,
// up to BackoffMax.
func (r *runner) backoff(exit wire.Exit, before func(st *wire.State)) {
	s := r.s
	switch {
	case r.delay == 0:
		r.delay = s.cfg.BackoffMin
	default:
		r.delay = min(2*r.delay, s.cfg.BackoffMax)
	}
	at := exit.At.Add(r.delay)
	s.update(func(st *wire.State) {
		if before != nil {
			before(st)
		}
		st.Phase = wire.PhaseBackoff
		st.PID = 0
		st.LastExit = &exit
		st.RestartAt = at
	})
	s.say("%s; starting it again in %s", describe(exit), r.delay)
	r.restartT = time.NewTimer(time.Until(at))
	r.restartC = r.restartT.C
}

// shutdown stops the service and waits for its end. Nothing is started.
func (r *runner) shutdown() {
	s := r.s
	close(s.quit)
	r.cancelRestart()
	var last *wire.Exit
	if r.proc != nil {
		r.stop()
		s.update(func(st *wire.State) {
			st.Phase = wire.PhaseStopping
			st.PID = 0
		})
		var e ending
		select {
		case e = <-r.proc.ended:
		case <-r.killC:
			r.signal(syscall.SIGKILL)
			e = <-r.proc.ended
		}
		exit := r.proc.exit(e)
		last = &exit
		r.proc = nil
		r.cancelKill()
		s.say("%s", describe(exit))
	}
	s.update(func(st *wire.State) {
		if last != nil {
			st.LastExit = last
		}
		if st.Generation != 0 {
			st.Phase = wire.PhaseStopping
		}
		st.PID = 0
		st.RestartAt = time.Time{}
	})
}

// spawn starts the binary of t with no standard input, in a process group
// of its own, its output going to the ring and to the copies.
func (s *Supervisor) spawn(t *activation) (*process, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}

	cmd := exec.Command(t.binary, t.args...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.Dir
	// The service writes to pipes of this package's own, not to pipes
	// that os/exec makes: Wait then returns when the service has ended,
	// and not only when the last process that holds its output has.
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	started := time.Now()
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, err
	}

	p := &process{
		pid:        cmd.Process.Pid,
		generation: t.generation,
		started:    started,
		ended:      make(chan ending, 1),
	}
	outLines, errLines := s.cfg.Ring.Writer(logring.Stdout), s.cfg.Ring.Writer(logring.Stderr)
	var copies sync.WaitGroup
	copies.Go(func() { io.Copy(tee{outLines, s.cfg.Stdout}, outR) })
	copies.Go(func() { io.Copy(tee{errLines, s.cfg.Stderr}, errR) })
	go func() {
		err := cmd.Wait()
		at := time.Now()
		// Whatever is left of the process group goes with the service.
		// That closes the output as well, unless a process left the
		// group; then the output is closed here.
		syscall.Kill(-p.pid, syscall.SIGKILL)
		read := make(chan struct{})
		go func() {
			copies.Wait()
			close(read)
		}()
		select {
		case <-read:
		case <-time.After(drainTimeout):
			outR.Close()
			errR.Close()
			<-read
		}
		outR.Close()
		errR.Close()
		// A last line without a line feed.
		outLines.Flush()
		errLines.Flush()
		p.ended <- ending{err: err, at: at}
	}()
	return p, nil
}

// tee writes to the ring and to the copy, if there is one. It never
// fails: a copy that cannot be written to does not stop the service's
// output from being read.
type tee struct {
	lines *logring.Writer
	copy  io.Writer
}

func (t tee) Write(p []byte) (int, error) {
	t.lines.Write(p)
	if t.copy != nil {
		t.copy.Write(p)
	}
	return len(p), nil
}

// exit makes of what Wait said how the service ended.
func (p *process) exit(e ending) wire.Exit {
	exit := wire.Exit{Generation: p.generation, At: e.at, Ran: e.at.Sub(p.started)}
	var exitErr *exec.ExitError
	switch {
	case e.err == nil:
	case errors.As(e.err, &exitErr):
		exit.Status = exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			exit.Status = -1
			exit.Signal = signalName(ws.Signal())
		}
	default:
		// Wait itself failed: nothing is known of the end.
		exit.Status = -1
	}
	return exit
}

// describe says how a service ended, as the ring has it.
func describe(exit wire.Exit) string {
	switch {
	case exit.Error != "":
		return fmt.Sprintf("generation %d could not be started: %s", exit.Generation, exit.Error)
	case exit.Signal != "":
		return fmt.Sprintf("generation %d was ended by %s after %s", exit.Generation, exit.Signal, short(exit.Ran))
	default:
		return fmt.Sprintf("generation %d exited with status %d after %s", exit.Generation, exit.Status, short(exit.Ran))
	}
}

// short rounds d for a person: 3.2s, 417ms.
func short(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(100 * time.Millisecond)
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGABRT: "SIGABRT",
	syscall.SIGALRM: "SIGALRM",
	syscall.SIGBUS:  "SIGBUS",
	syscall.SIGFPE:  "SIGFPE",
	syscall.SIGHUP:  "SIGHUP",
	syscall.SIGILL:  "SIGILL",
	syscall.SIGINT:  "SIGINT",
	syscall.SIGKILL: "SIGKILL",
	syscall.SIGPIPE: "SIGPIPE",
	syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGSYS:  "SIGSYS",
	syscall.SIGTERM: "SIGTERM",
	syscall.SIGTRAP: "SIGTRAP",
	syscall.SIGUSR1: "SIGUSR1",
	syscall.SIGUSR2: "SIGUSR2",
	syscall.SIGXCPU: "SIGXCPU",
	syscall.SIGXFSZ: "SIGXFSZ",
}

// signalName returns the name of sig, such as SIGKILL, or "signal 42" for
// one that has none here.
func signalName(sig syscall.Signal) string {
	if name, ok := signalNames[sig]; ok {
		return name
	}
	return fmt.Sprintf("signal %d", int(sig))
}
```

- [ ] **Step 14: Run the tests to see them pass**

Run: `go vet ./supervisor/ && go test -race -count=5 ./supervisor/`
Expected: PASS, after about a minute (ten seconds a round). The five rounds are there to show a test that only fails now and then; if one does, it is a fault to find, not a run to repeat. When a round is over no `testservice` runs any more: `pgrep testservice` prints nothing.

- [ ] **Step 15: Commit**

```bash
git add supervisor/
git commit -m "Supervisor: start, stop and restart the service, with backoff"
```
