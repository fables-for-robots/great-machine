### Task 8: ptyshell, shell sessions on a pseudo-terminal

**Files:**
- Create: `ptyshell/ptyshell.go`
- Test: `ptyshell/ptyshell_test.go`
- Modify: `go.mod`, `go.sum` (`github.com/creack/pty` v1.1.24 and `golang.org/x/sys` become requirements)

**Interfaces:**
- Consumes: nothing of the other tasks. `github.com/creack/pty` v1.1.24
  (pure Go) opens the pseudo-terminal and starts the shell on it;
  `golang.org/x/sys/unix` does the ioctls.
- Produces:

  ```go
  package ptyshell

  type Config struct {
  	Shell string   // the program; "": /bin/sh
  	Env   []string // nil: os.Environ(); TERM is set from Term
  	Dir   string
  	Term  string // "": xterm
  	Cols  uint16 // 0: 80
  	Rows  uint16 // 0: 24
  }

  type Session struct{ /* unexported */ }

  // Start starts the shell on a pseudo-terminal, in a session of its own.
  func Start(cfg Config) (*Session, error)

  func (s *Session) Read(p []byte) (int, error)  // output; io.EOF when the shell has ended and all was read
  func (s *Session) Write(p []byte) (int, error) // input
  func (s *Session) Resize(cols, rows uint16) error

  // Wait returns the exit status when the shell has ended: -1 when a signal
  // ended it.
  func (s *Session) Wait() int

  // Close ends the shell if it runs, with SIGHUP to its process group and
  // SIGKILL two seconds later, and frees the terminal. It may be called
  // more than once.
  func (s *Session) Close() error
  ```

  `Close` returns when the shell has ended, so a `Wait` after it returns at
  once. `Read` returns `io.EOF` after `Close` too, and a `Read` that waits
  is ended by `Close`.

Three things about pseudo-terminals shape the code, and each has a test
(`TestEndWithAJobLeftBehind` for the first two, which fails on Linux
without them, and `TestCloseEndsAShellThatNobodyReads` for the third,
which fails on macOS without it):

- **The file that `pty.StartWithSize` returns cannot be used as it is.**
  The package `pty` calls the file's `Fd` method, which makes an `os.File`
  blocking for good: a `Read` that waits is then ended neither by `Close`
  nor by a deadline. `Start` therefore makes a second descriptor of the
  terminal, non-blocking, and `Resize` does its ioctl through
  `SyscallConn` and never through `pty.Setsize`.
- **On Linux a `Read` does not end with the shell while a job the shell
  left behind has the terminal open** (`sleep 20 & exit`). When the shell
  has ended, `Read` gets a deadline one second ahead (`drainFor`), and
  takes its passing for the end.
- **On macOS a process cannot end while output of its terminal waits to
  be read.** `Close` reads and drops what is left while it waits for the
  shell.

The two waiting times, `killAfter` (the spec's two seconds) and `drainFor`,
are unexported variables, as the contract gives `Config` no fields for
them; the tests make them short. The tests run the real `/bin/sh`, on
macOS and on Linux.

- [ ] **Step 1: Write the failing test**

`ptyshell/ptyshell_test.go`:

```go
package ptyshell

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// output collects what a session prints, in a goroutine of its own.
type output struct {
	mu   sync.Mutex
	text strings.Builder
	err  error         // what ended the reading
	done chan struct{} // closed when Read has returned an error
}

// collect starts to read s until Read fails.
func collect(s *Session) *output {
	o := &output{done: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.Read(buf)
			o.mu.Lock()
			o.text.Write(buf[:n])
			if err != nil {
				o.err = err
				o.mu.Unlock()
				close(o.done)
				return
			}
			o.mu.Unlock()
		}
	}()
	return o
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String()
}

// waitFor fails the test unless the output holds want within ten seconds.
func (o *output) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(o.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the output has no %q:\n%s", want, o.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitEOF fails the test unless the reading ends with io.EOF within ten
// seconds.
func (o *output) waitEOF(t *testing.T) {
	t.Helper()
	select {
	case <-o.done:
		if o.err != io.EOF {
			t.Fatalf("Read() failed with %v, want io.EOF", o.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Read() did not return io.EOF; the output so far:\n%s", o.String())
	}
}

// start starts a session that is closed at the end of the test, and
// collects its output. The shell gets a small environment of its own, so
// that nothing of the user's is read or written.
func start(t *testing.T, cfg Config) (*Session, *output) {
	t.Helper()
	if cfg.Env == nil {
		cfg.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PS1=$ "}
	}
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start() = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, collect(s)
}

// typeIn writes a line to the session.
func typeIn(t *testing.T, s *Session, line string) {
	t.Helper()
	if _, err := s.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("Write(%q) = %v", line, err)
	}
}

// wait returns s.Wait(), and fails the test if the shell has not ended
// within ten seconds.
func wait(t *testing.T, s *Session) int {
	t.Helper()
	status := make(chan int, 1)
	go func() { status <- s.Wait() }()
	select {
	case st := <-status:
		return st
	case <-time.After(10 * time.Second):
		t.Fatal("the shell has not ended")
		return 0
	}
}

// shortly makes the waiting times of the package short for one test.
func shortly(t *testing.T) {
	t.Helper()
	kill, drain := killAfter, drainFor
	killAfter, drainFor = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { killAfter, drainFor = kill, drain })
}

func TestInputAndOutput(t *testing.T) {
	s, out := start(t, Config{})
	// The terminal echoes what is typed, so the test looks for what only
	// the shell can have printed.
	typeIn(t, s, "echo hello-$((6*7))")
	out.waitFor(t, "hello-42")
}

func TestSize(t *testing.T) {
	s, out := start(t, Config{Cols: 132, Rows: 43})
	typeIn(t, s, "stty size")
	out.waitFor(t, "43 132")

	if err := s.Resize(101, 37); err != nil {
		t.Fatalf("Resize() = %v", err)
	}
	typeIn(t, s, "stty size")
	out.waitFor(t, "37 101")
}

func TestDefaultSize(t *testing.T) {
	s, out := start(t, Config{})
	typeIn(t, s, "stty size")
	out.waitFor(t, "24 80")
}

func TestTerm(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"as the config says", Config{Term: "vt100"}, "term=vt100."},
		{"xterm unless it says", Config{}, "term=xterm."},
		{"in place of the one of Env", Config{
			Term: "vt220",
			Env:  []string{"TERM=dumb", "PATH=" + os.Getenv("PATH"), "PS1=$ "},
		}, "term=vt220."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, out := start(t, c.cfg)
			typeIn(t, s, "echo term=$TERM.")
			out.waitFor(t, c.want)
		})
	}
}

func TestDir(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, out := start(t, Config{Dir: dir})
	typeIn(t, s, "echo in=$(pwd -P).")
	out.waitFor(t, "in="+dir+".")
}

func TestExitStatus(t *testing.T) {
	s, out := start(t, Config{})
	typeIn(t, s, "exit 3")
	if got := wait(t, s); got != 3 {
		t.Fatalf("Wait() = %d, want 3", got)
	}
	out.waitEOF(t)
	// And again, for whoever reads after the end.
	if n, err := s.Read(make([]byte, 16)); n != 0 || err != io.EOF {
		t.Fatalf("Read() after the end = %d, %v, want 0, io.EOF", n, err)
	}
	if got := s.Wait(); got != 3 {
		t.Fatalf("a second Wait() = %d, want 3", got)
	}
}

func TestOutputBeforeTheEndIsRead(t *testing.T) {
	s, out := start(t, Config{})
	typeIn(t, s, "echo last-$((6*7)); exit 0")
	if got := wait(t, s); got != 0 {
		t.Fatalf("Wait() = %d, want 0", got)
	}
	out.waitEOF(t)
	if !strings.Contains(out.String(), "last-42") {
		t.Fatalf("the output has no last-42:\n%s", out.String())
	}
}

func TestEndWithAJobLeftBehind(t *testing.T) {
	shortly(t)
	s, out := start(t, Config{})
	// The sleep keeps the terminal open after the shell has ended.
	typeIn(t, s, "sleep 20 & exit 4")
	if got := wait(t, s); got != 4 {
		t.Fatalf("Wait() = %d, want 4", got)
	}
	out.waitEOF(t)
}

func TestCloseEndsTheShell(t *testing.T) {
	s, out := start(t, Config{})
	typeIn(t, s, "echo ready-$((6*7)); sleep 1000")
	out.waitFor(t, "ready-42")

	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	wait(t, s)
	out.waitEOF(t)
	if err := s.Close(); err != nil {
		t.Fatalf("a second Close() = %v", err)
	}
	if _, err := s.Write([]byte("x")); err == nil {
		t.Fatal("Write() after Close() = nil, want an error")
	}
}

// A shell cannot end on macOS while output of its terminal waits to be
// read: it hangs in its exit, for 600 ms or for good. So Close has to read
// what nobody else does, and the shell then ends as soon as it is told to.
func TestCloseEndsAShellThatNobodyReads(t *testing.T) {
	cfg := Config{Env: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "PS1=$ "}}
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start() = %v", err)
	}
	typeIn(t, s, "echo some output that nobody reads; sleep 1000")

	began := time.Now()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close() has not returned")
	}
	if took := time.Since(began); took >= killAfter/4 {
		t.Fatalf("Close() took %v: the shell could not end while its output was not read", took)
	}
	wait(t, s)
	if n, err := s.Read(make([]byte, 16)); n != 0 || err != io.EOF {
		t.Fatalf("Read() after Close() = %d, %v, want 0, io.EOF", n, err)
	}
}

func TestCloseKillsAShellThatIgnoresSIGHUP(t *testing.T) {
	shortly(t)
	s, out := start(t, Config{})
	typeIn(t, s, "trap '' HUP; echo ready-$((6*7)); sleep 1000")
	out.waitFor(t, "ready-42")

	began := time.Now()
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if got := wait(t, s); got != -1 {
		t.Fatalf("Wait() = %d, want -1: SIGKILL ended the shell", got)
	}
	if took := time.Since(began); took < killAfter {
		t.Fatalf("Close() took %v: the shell was killed before the %v it is given", took, killAfter)
	}
	out.waitEOF(t)
}

func TestCloseAfterTheEnd(t *testing.T) {
	s, _ := start(t, Config{})
	typeIn(t, s, "exit 0")
	wait(t, s)
	for i := range 2 {
		if err := s.Close(); err != nil {
			t.Fatalf("Close() number %d = %v", i+1, err)
		}
	}
	if got := s.Wait(); got != 0 {
		t.Fatalf("Wait() = %d, want 0", got)
	}
}

func TestStartOfAShellThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	s, err := Start(Config{Shell: missing})
	if err == nil {
		s.Close()
		t.Fatal("Start() = nil, want an error")
	}
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Start() = %v, want one that names the shell and has os.ErrNotExist in its chain", err)
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

Run: `go test ./ptyshell/`
Expected: FAIL (the build fails) — `undefined: Session`, `undefined: Start`, `undefined: killAfter`

- [ ] **Step 3: Write the implementation**

`ptyshell/ptyshell.go`:

```go
// Package ptyshell runs a shell on a pseudo-terminal: what an operator
// types is written to it, what the shell prints is read from it, and the
// size of the terminal can be changed while it runs.
package ptyshell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// killAfter is how long a shell has to end after SIGHUP before it is
// killed. A variable, so that the tests make it short.
var killAfter = 2 * time.Second

// drainFor is how long after the end of the shell its output can still be
// read while something else holds the terminal open (a job the shell left
// in the background). A variable, so that the tests make it short.
var drainFor = time.Second

// Config says what shell to start, and on what terminal.
type Config struct {
	Shell string   // the program; "": /bin/sh
	Env   []string // nil: os.Environ(); TERM is set from Term
	Dir   string   // the working directory; "": that of this process
	Term  string   // "": xterm
	Cols  uint16   // 0: 80
	Rows  uint16   // 0: 24
}

// Session is a shell that runs on a pseudo-terminal. Read, Write, Resize,
// Wait and Close may be called from different goroutines.
type Session struct {
	pty  *os.File // the master side of the terminal
	cmd  *exec.Cmd
	done chan struct{} // closed when the shell has ended
	// status is the exit status of the shell, once done is closed.
	status int

	closeOnce sync.Once
	closeErr  error
}

// Start starts the shell on a pseudo-terminal, in a session of its own:
// the terminal is its controlling terminal, and the shell leads a process
// group of its own.
func Start(cfg Config) (*Session, error) {
	shell := cfg.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	term := cfg.Term
	if term == "" {
		term = "xterm"
	}
	size := pty.Winsize{Cols: cfg.Cols, Rows: cfg.Rows}
	if size.Cols == 0 {
		size.Cols = 80
	}
	if size.Rows == 0 {
		size.Rows = 24
	}
	base := cfg.Env
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+1)
	for _, v := range base {
		if !strings.HasPrefix(v, "TERM=") {
			env = append(env, v)
		}
	}
	env = append(env, "TERM="+term)

	cmd := exec.Command(shell)
	cmd.Env = env
	cmd.Dir = cfg.Dir
	// StartWithSize makes the shell the leader of a new session (Setsid)
	// with the terminal as its controlling terminal (Setctty).
	started, err := pty.StartWithSize(cmd, &size)
	if err != nil {
		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
	}
	master, err := pollable(started)
	if err != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
	}
	s := &Session{pty: master, cmd: cmd, done: make(chan struct{})}
	go s.wait()
	return s, nil
}

// pollable returns the terminal that f is as a file whose Read ends when
// the file is closed or its deadline has passed, and closes f.
//
// The file of pty.StartWithSize is no such file: the package pty has
// called its Fd method, which makes a file blocking for good, and a Read
// of it that waits is ended by nothing but the terminal. Hence a second
// descriptor of the terminal, made non-blocking before the package os
// sees it.
func pollable(f *os.File) (*os.File, error) {
	defer f.Close()
	fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("a second descriptor of the terminal: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("making the terminal non-blocking: %w", err)
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// wait waits for the shell and keeps its exit status.
func (s *Session) wait() {
	s.cmd.Wait()
	// ExitCode is -1 for a shell that a signal ended.
	s.status = s.cmd.ProcessState.ExitCode()
	// What the shell wrote is in the terminal and can be read at once. A
	// Read that waits after that waits for a process the shell left
	// behind, which is not the shell: it is given drainFor and no longer.
	s.pty.SetReadDeadline(time.Now().Add(drainFor))
	close(s.done)
}

// Read reads what the shell and its children wrote. It returns io.EOF
// when the shell has ended and all was read, and after Close.
func (s *Session) Read(p []byte) (int, error) {
	n, err := s.pty.Read(p)
	if n > 0 || err == nil {
		return n, nil
	}
	switch {
	case errors.Is(err, io.EOF):
		// What macOS answers when the other side of the terminal is closed.
	case errors.Is(err, syscall.EIO):
		// What Linux answers.
	case errors.Is(err, os.ErrClosed):
		// Close was called.
	case errors.Is(err, os.ErrDeadlineExceeded):
		// The shell has ended and something else holds the terminal.
	default:
		return 0, fmt.Errorf("ptyshell: reading the terminal: %w", err)
	}
	return 0, io.EOF
}

// Write writes p to the terminal, as if it had been typed.
func (s *Session) Write(p []byte) (int, error) {
	n, err := s.pty.Write(p)
	if err != nil {
		return n, fmt.Errorf("ptyshell: writing to the terminal: %w", err)
	}
	return n, nil
}

// Resize tells the terminal, and through it the shell, its new size.
func (s *Session) Resize(cols, rows uint16) error {
	// Not pty.Setsize, which would make the file blocking: see pollable.
	raw, err := s.pty.SyscallConn()
	if err != nil {
		return fmt.Errorf("ptyshell: resizing the terminal: %w", err)
	}
	var ioctlErr error
	err = raw.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	})
	if err = errors.Join(err, ioctlErr); err != nil {
		return fmt.Errorf("ptyshell: resizing the terminal: %w", err)
	}
	return nil
}

// Wait returns the exit status when the shell has ended: -1 when a signal
// ended it.
func (s *Session) Wait() int {
	<-s.done
	return s.status
}

// Close ends the shell if it runs, with SIGHUP to its process group and
// SIGKILL two seconds later, and frees the terminal. It returns when the
// shell has ended. It may be called more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		// From here on nobody may be reading what the shell prints, and on
		// macOS a process cannot end while output of its terminal waits to
		// be read. So what is left is read here and dropped, until the
		// terminal is closed.
		go io.Copy(io.Discard, s.pty)
		select {
		case <-s.done:
		default:
			s.signal(syscall.SIGHUP)
			timer := time.NewTimer(killAfter)
			defer timer.Stop()
			select {
			case <-s.done:
			case <-timer.C:
				s.signal(syscall.SIGKILL)
				<-s.done
			}
		}
		// Closing the terminal hangs up whatever still has it.
		if err := s.pty.Close(); err != nil {
			s.closeErr = fmt.Errorf("ptyshell: closing the terminal: %w", err)
		}
	})
	return s.closeErr
}

// signal sends sig to the process group of the shell and to the one in
// the foreground of the terminal, which is another one while the shell
// runs a command with job control.
func (s *Session) signal(sig syscall.Signal) {
	shell := s.cmd.Process.Pid // the shell leads its group
	syscall.Kill(-shell, sig)
	if fg := s.foreground(); fg > 1 && fg != shell {
		syscall.Kill(-fg, sig)
	}
}

// foreground returns the process group in the foreground of the terminal,
// or 0 when it cannot be told.
func (s *Session) foreground() int {
	raw, err := s.pty.SyscallConn()
	if err != nil {
		return 0
	}
	var group int
	var ioctlErr error
	err = raw.Control(func(fd uintptr) {
		group, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	if err != nil || ioctlErr != nil {
		return 0
	}
	return group
}
```

- [ ] **Step 4: Add the requirements**

Run:

```bash
go get github.com/creack/pty@v1.1.24
go mod tidy
```

Expected: `go.mod` requires `github.com/creack/pty v1.1.24` and `golang.org/x/sys`, neither marked `// indirect`.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go vet ./ptyshell/ && go test -race -count=3 ./ptyshell/`
Expected: `ok`, and `pgrep -fl 'sleep 1000'` prints nothing afterwards

- [ ] **Step 6: Commit**

```bash
git add ptyshell/ go.mod go.sum
git commit -m "Ptyshell: shell sessions on a pseudo-terminal"
```
