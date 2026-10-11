### Task 9: The supervisor and a shell start through what they are given

The supervisor starts the service and `ptyshell` starts a shell, each with `exec.Cmd`. Both get a field that takes the starting out of their hands, and neither comes to know the package `launcher`: the field is a function, and `(*launcher.Launcher).Start` fits it. Without it both do what they did.

Through a launcher the process that is started is the program itself, which becomes the service only afterwards. So **a binary that is not there is no failure to start any more**: it is a service that ended at once with 127, its reason on its standard error and so in the ring, and the supervisor backs off as for any other. For a shell that would be a session that says one line and ends, which is not what an operator who mistyped `--shell` should get, so `ptyshell` looks at the file first and fails as before.

**The last cycle is what a review found.** A shell of `--user` could not open its own terminal: the terminal was opened by what started the shell (`pty.StartWithSize`), where the launcher, which gives a user what a process is started on, did not see it. `ptyshell` now opens the terminal first, makes it the standard streams of the command, and then starts the command.

**Files:**
- Modify: `ptyshell/ptyshell.go`, `supervisor/supervisor.go`
- Test: `ptyshell/start_test.go`, `supervisor/start_test.go`, `supervisor/user_linux_test.go`, `ptyshell/user_linux_test.go`

**Interfaces:**
- Consumes: in the tests, `launcher.New`, `(*Launcher).Start` and `launcher.Replace` (Task 8), the last in a `TestMain` of each of the two test packages.
- Produces:
  - `supervisor.Config.Start func(cmd *exec.Cmd, start func() error) error`: nil, the process is started as it is.
  - `ptyshell.Config.Start`, the same. With it, `ptyshell.Start` fails for a shell that is no executable file before anything is started. The command it is given has the terminal as its standard streams already.

- [ ] **Step 1: Write the failing test: the supervisor and a shell session start their process with what they are given to start it with**

Create `ptyshell/start_test.go`:

```go
package ptyshell

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The tests that start a shell through a launcher start this binary,
// which then replaces itself with the shell, as great-machine does.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}

// Start, if set, is what starts the shell: it is given the command of the
// shell and what starts it on its terminal.
func TestStartIsGivenTheCommandAndWhatStartsIt(t *testing.T) {
	var paths []string
	var args [][]string
	var term bool
	s, out := start(t, Config{Term: "vt100", Start: func(cmd *exec.Cmd, begin func() error) error {
		paths, args = append(paths, cmd.Path), append(args, slices.Clone(cmd.Args))
		term = slices.Contains(cmd.Env, "TERM=vt100")
		return begin()
	}})
	typeIn(t, s, "echo hello-$((6*7))")
	out.waitFor(t, "hello-42")
	if len(paths) != 1 || paths[0] != "/bin/sh" || !slices.Equal(args[0], []string{"/bin/sh"}) || !term {
		t.Errorf("Start was given the programs %q with the arguments %q, with TERM: %v; want once /bin/sh, as its own first argument, with TERM", paths, args, term)
	}
}

// What Start returns is what came of the start.
func TestWhatStartReturnsIsTheErrorOfTheStart(t *testing.T) {
	failed := errors.New("the launcher would not")
	s, err := Start(Config{Start: func(*exec.Cmd, func() error) error { return failed }})
	if err == nil {
		s.Close()
		t.Fatal("Start() = nil, want the error of the start")
	}
	if !errors.Is(err, failed) || !strings.Contains(err.Error(), "/bin/sh") {
		t.Errorf("Start() = %v, want one that names the shell and has the error of the start in its chain", err)
	}
}

// throughALauncher returns cfg with a real launcher as its Start.
func throughALauncher(t *testing.T, cfg Config) Config {
	t.Helper()
	l, err := launcher.New(launcher.Config{Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)
	cfg.Start = l.Start
	return cfg
}

// Through a launcher the shell is the program itself first, which then
// becomes the shell: on the terminal, in its session, with its
// environment, its directory and its exit status, and ended as before.
func TestThroughALauncherTheShellRunsAsBefore(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, out := start(t, throughALauncher(t, Config{Term: "vt100", Dir: dir, Cols: 132, Rows: 43}))
	typeIn(t, s, "echo term=$TERM. in=$(pwd -P). size=$(stty size).")
	out.waitFor(t, "term=vt100. in="+dir+". size=43 132.")
	// The terminal is the shell's controlling terminal: it has a name.
	typeIn(t, s, "echo tty=$(tty).")
	out.waitFor(t, "tty=/dev/")
	if err := s.Resize(101, 37); err != nil {
		t.Fatalf("Resize() = %v", err)
	}
	typeIn(t, s, "stty size")
	out.waitFor(t, "37 101")
	typeIn(t, s, "exit 3")
	if got := wait(t, s); got != 3 {
		t.Fatalf("Wait() = %d, want 3", got)
	}
	out.waitEOF(t)
}

// Close ends a shell that was started through a launcher as any other:
// the hangup reaches it, and what it runs.
func TestThroughALauncherCloseEndsTheShell(t *testing.T) {
	s, out := start(t, throughALauncher(t, Config{}))
	typeIn(t, s, "echo ready-$((6*7)); sleep 1000")
	out.waitFor(t, "ready-42")
	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	wait(t, s)
	out.waitEOF(t)
}

// A shell that is not there is an error of Start, with a launcher too:
// whoever asked for a shell is told, and is not given a session whose
// only line is the error.
func TestThroughALauncherAShellThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	s, err := Start(throughALauncher(t, Config{Shell: missing}))
	if err == nil {
		s.Close()
		t.Fatal("Start() = nil, want an error")
	}
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("Start() = %v, want one that names the shell and has os.ErrNotExist in its chain", err)
	}
	noProgram := filepath.Join(t.TempDir(), "no-execute-bit")
	if err := os.WriteFile(noProgram, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = Start(throughALauncher(t, Config{Shell: noProgram}))
	if err == nil {
		s.Close()
		t.Fatal("Start() of a file without an execute bit = nil, want an error")
	}
	if !strings.Contains(err.Error(), noProgram) {
		t.Fatalf("Start() = %v, want one that names the shell", err)
	}
}
```

Create `supervisor/start_test.go`:

```go
package supervisor

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/testservice/build"
	"github.com/fables-for-robots/great-machine/wire"
)

// The tests that start the service through a launcher start this binary,
// which then replaces itself with the service, as great-machine does.
func TestMain(m *testing.M) {
	if status, was := launcher.Replace(os.Args); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}

// Start, if set, is what starts the service: it is given the command as
// the supervisor made it, and what starts it.
func TestStartIsGivenTheCommandAndWhatStartsIt(t *testing.T) {
	bin := build.Binary(t)
	type start struct {
		path    string
		args    []string
		ownPgid bool
	}
	var mu sync.Mutex
	var starts []start
	h := run(t, Config{Start: func(cmd *exec.Cmd, begin func() error) error {
		mu.Lock()
		starts = append(starts, start{cmd.Path, slices.Clone(cmd.Args), cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid})
		mu.Unlock()
		return begin()
	}})
	h.activate(1, bin, "--stdout", "ready")
	h.wrote(logring.Stdout, "ready")
	st := h.phase(wire.PhaseRunning)

	mu.Lock()
	defer mu.Unlock()
	want := start{bin, []string{bin, "--stdout", "ready"}, true}
	if len(starts) != 1 || starts[0].path != want.path || !slices.Equal(starts[0].args, want.args) || !starts[0].ownPgid {
		t.Fatalf("Start was given %+v, want once %+v", starts, want)
	}
	if !alive(st.PID) {
		t.Errorf("the process %d of the state does not run", st.PID)
	}
}

// What Start returns is what came of the start: its error is that of a
// binary that could not be started.
func TestWhatStartReturnsIsWhatCameOfTheStart(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, Config{Start: func(*exec.Cmd, func() error) error {
		return errors.New("the launcher would not")
	}})
	h.activate(1, bin)

	st := h.State()
	if st.Phase != wire.PhaseBackoff || st.PID != 0 {
		t.Fatalf("when Activate has returned: %+v, want backoff", st)
	}
	if exit := st.LastExit; exit == nil || exit.Status != -1 || exit.Error != "the launcher would not" {
		t.Fatalf("LastExit is %+v, want status -1 and the error of Start", exit)
	}
	eventually(t, "three tries", func() bool { return len(h.delays(1)) >= 3 })
	if line := h.matching(`^generation 1 could not be started`)[0]; line != "generation 1 could not be started: the launcher would not; starting it again in 20ms" {
		t.Errorf("the line is %q", line)
	}
	if got := h.matching(`^started`); len(got) != 0 {
		t.Errorf("lines of starts that never were: %q", got)
	}
}

// throughALauncher returns cfg with a real launcher as its Start.
func throughALauncher(t *testing.T, cfg Config) Config {
	t.Helper()
	l, err := launcher.New(launcher.Config{Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)
	cfg.Start = l.Start
	return cfg
}

// Through a launcher the service is the program itself first, which then
// becomes the service. To the supervisor nothing has changed: the
// arguments, the output, the process and its group, how it is stopped
// and what it exited with.
func TestThroughALauncherTheServiceRunsAsBefore(t *testing.T) {
	bin := build.Binary(t)
	h := run(t, throughALauncher(t, Config{QuickExit: time.Hour}))
	h.activate(1, bin, "--stdout", "", "--stderr", "two words", "--spawn")
	h.wrote(logring.Stderr, "two words")
	h.wrote(logring.Stdout, "")
	st := h.phase(wire.PhaseRunning)

	// The process of the state is the service, and leads its own group.
	if pgid, err := syscall.Getpgid(st.PID); err != nil || pgid != st.PID {
		t.Errorf("the process group of the service %d is %d (%v), want its own", st.PID, pgid, err)
	}
	var child int
	for _, text := range h.lines(logring.Stdout) {
		fmt.Sscanf(text, "child %d", &child)
	}
	if child == 0 {
		t.Fatalf("no line child <pid> in %q", h.lines(logring.Stdout))
	}
	t.Cleanup(func() { syscall.Kill(child, syscall.SIGKILL) })
	if pgid, err := syscall.Getpgid(child); err != nil || pgid != st.PID {
		t.Errorf("the process group of the service's child is %d (%v), want the service's, %d", pgid, err, st.PID)
	}

	// SIGTERM stops it, and what it left in its group goes with it.
	h.activate(2, bin, "--exit-after", "0", "--exit-code", "3")
	eventually(t, "the first generation has ended", func() bool {
		return len(h.matching(`^generation 1 exited with status 0 after`)) == 1
	})
	eventually(t, "the child is gone", func() bool { return !alive(child) })

	// And an exit status is the service's own.
	eventually(t, "the second generation has exited with 3", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Generation == 2 && exit.Status == 3 && exit.Error == ""
	})
}

// A binary that is gone when it is started was a file the supervisor took
// (Activate has looked). Through a launcher it is the process that was to
// become it that finds out: it says so, where the service writes, and
// ends with 127, and the supervisor has a service that ended at once.
func TestThroughALauncherABinaryThatIsGone(t *testing.T) {
	h := run(t, throughALauncher(t, Config{}))
	path := script(t, "exit 0\n")
	h.activate(1, path)
	eventually(t, "the service has run and ended", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == 0
	})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a start that found no program", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == 127
	})
	exit := h.State().LastExit
	if exit.Error != "" || exit.Signal != "" || exit.Generation != 1 {
		t.Errorf("LastExit is %+v, want an exit with status 127 and nothing else", exit)
	}
	said := false
	for _, text := range h.lines(logring.Stderr) {
		said = said || strings.Contains(text, "executing "+path) && strings.Contains(text, "no such file")
	}
	if !said {
		t.Errorf("standard error of the service does not say what could not be executed: %q", h.lines(logring.Stderr))
	}
	// It is started again like any service that ended at once: later
	// each time, up to the most.
	eventually(t, "a wait of the longest delay after a start that found no program", func() bool {
		lines := h.matching(`^generation 1 exited with status 127 after .*; starting it again in 80ms$`)
		return len(lines) >= 2
	})
	if got := h.matching(`could not be started`); len(got) != 0 {
		t.Errorf("lines of a binary that could not be started, and the program itself was: %q", got)
	}
}

// A file that is no program ends with 126 and the reason.
func TestThroughALauncherAFileThatIsNoProgram(t *testing.T) {
	noProgram := filepath.Join(t.TempDir(), "no-program")
	if err := os.WriteFile(noProgram, []byte("this is no program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := run(t, throughALauncher(t, Config{}))
	h.activate(1, noProgram)
	eventually(t, "a start that could not execute the file", func() bool {
		exit := h.State().LastExit
		return exit != nil && exit.Status == 126
	})
	if !regexp.MustCompile(`executing \S+no-program: exec format error`).MatchString(strings.Join(h.lines(logring.Stderr), "\n")) {
		t.Errorf("standard error of the service does not say why: %q", h.lines(logring.Stderr))
	}
}
```

- [ ] **Step 2: Run it, and see it fail**

Run: `go test ./supervisor/ ./ptyshell/`

Expected: it fails, with

```text
supervisor/start_test.go:44:21: unknown field Start in struct literal of type Config
```

- [ ] **Step 3: Write the code: supervisor.Config.Start and ptyshell.Config.Start; a shell that is not there stays an error of the start**

Change `ptyshell/ptyshell.go`:

```diff
--- a/ptyshell/ptyshell.go
+++ b/ptyshell/ptyshell.go
@@ -35,6 +35,11 @@ type Config struct {
 	Term  string   // "": xterm
 	Cols  uint16   // 0: 80
 	Rows  uint16   // 0: 24
+	// Start, if set, starts a process in the place of cmd.Start: it is
+	// given the command and what starts it on its terminal, and returns
+	// what that returned. It is (*launcher.Launcher).Start. nil: the
+	// process is started as it is.
+	Start func(cmd *exec.Cmd, start func() error) error
 }
 
 // Session is a shell that runs on a pseudo-terminal. Read, Write, Resize,
@@ -86,7 +91,24 @@ func Start(cfg Config) (*Session, error) {
 	cmd.Dir = cfg.Dir
 	// StartWithSize makes the shell the leader of a new session (Setsid)
 	// with the terminal as its controlling terminal (Setctty).
-	started, err := pty.StartWithSize(cmd, &size)
+	var started *os.File
+	begin := func() (err error) {
+		started, err = pty.StartWithSize(cmd, &size)
+		return err
+	}
+	var err error
+	if cfg.Start != nil {
+		// What Start starts may be another program that becomes the
+		// shell only afterwards, as a launcher has it: a shell that is
+		// not there would then be a session that says so and ends. It
+		// is to be an error here, as it is without, so it is looked at
+		// first.
+		if err = executable(cmd.Path); err == nil {
+			err = cfg.Start(cmd, begin)
+		}
+	} else {
+		err = begin()
+	}
 	if err != nil {
 		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
 	}
@@ -101,6 +123,22 @@ func Start(cfg Config) (*Session, error) {
 	return s, nil
 }
 
+// executable returns nil for a regular file with an execute bit, through
+// its symbolic links, and why path is none otherwise. The error of a file
+// that is not there names it and is os.ErrNotExist.
+func executable(path string) error {
+	info, err := os.Stat(path)
+	switch {
+	case err != nil:
+		return err
+	case !info.Mode().IsRegular():
+		return fmt.Errorf("%s is no regular file", path)
+	case info.Mode().Perm()&0o111 == 0:
+		return fmt.Errorf("%s has no execute bit", path)
+	}
+	return nil
+}
+
 // pollable returns the terminal that f is as a file whose Read ends when
 // the file is closed or its deadline has passed, and closes f.
 //
```

Change `supervisor/supervisor.go`:

```diff
--- a/supervisor/supervisor.go
+++ b/supervisor/supervisor.go
@@ -31,6 +31,15 @@ type Config struct {
 	BackoffMin     time.Duration // 0: 1 second
 	BackoffMax     time.Duration // 0: 2 minutes
 	Log            *slog.Logger  // nil: slog.Default()
+	// Start, if set, starts a process in the place of cmd.Start: it is
+	// given the command and what starts it, and returns what that
+	// returned. It is (*launcher.Launcher).Start. nil: the process is
+	// started as it is.
+	//
+	// Through a launcher a binary that cannot be executed is no failure
+	// to start: the process that was to become it ends with 127 or 126,
+	// having said why where the service writes.
+	Start func(cmd *exec.Cmd, start func() error) error
 }
 
 // drainTimeout is how long the output of a service that has ended is
@@ -440,7 +449,11 @@ func (s *Supervisor) spawn(t *activation) (*process, error) {
 	cmd.Stdout = outW
 	cmd.Stderr = errW
 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
-	err = cmd.Start()
+	if s.cfg.Start != nil {
+		err = s.cfg.Start(cmd, cmd.Start)
+	} else {
+		err = cmd.Start()
+	}
 	started := time.Now()
 	outW.Close()
 	errW.Close()
```

- [ ] **Step 4: Run it, and see it pass**

Run: `go test ./supervisor/ ./ptyshell/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add ptyshell/start_test.go supervisor/start_test.go ptyshell/ptyshell.go supervisor/supervisor.go
git commit -m 'Supervisor: the supervisor and a shell session start their process with what they are given to start it with'
```

- [ ] **Step 6: A test that holds as it is written: a service of another user opens its standard streams anew, and writes into the ring**

Create `supervisor/user_linux_test.go`:

```go
package supervisor

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/fables-for-robots/great-machine/launcher"
	"github.com/fables-for-robots/great-machine/logring"
	"github.com/fables-for-robots/great-machine/wire"
)

// A service of another user writes to pipes that the supervisor made, as
// root. The launcher gives them to the user, so a service that opens
// /dev/stdout or /dev/stderr anew, as many are configured to, writes
// where it would have written anyway: into the ring.
func TestAServiceOfAnotherUserCanOpenItsStandardStreams(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("another user takes root: scripts/test-linux.sh runs this as root, in a container")
	}
	l, err := launcher.New(launcher.Config{User: &launcher.User{UID: 65534, GID: 65534}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)
	h := run(t, Config{QuickExit: time.Hour, Start: l.Start})
	h.activate(1, "/bin/sh", "-c", `echo "to standard output, as $(id -u)" > /dev/stdout; echo to standard error > /dev/stderr; sleep 1000`)
	h.wrote(logring.Stdout, "to standard output, as 65534")
	h.wrote(logring.Stderr, "to standard error")
	h.phase(wire.PhaseRunning)
}
```

- [ ] **Step 7: Run it, and see it pass**

Run: `scripts/test-linux.sh -run TestAServiceOfAnotherUserCanOpenItsStandardStreams -v ./supervisor/`

Expected: it passes: the pipes of the service are the command's when the launcher is asked to start it, and the launcher of Task 8 gives them to the user.

- [ ] **Step 8: Commit**

```bash
git add supervisor/user_linux_test.go
git commit -m 'Supervisor: a service of another user opens its standard streams anew, and writes into the ring'
```

- [ ] **Step 9: Write the failing test: the shell of another user can open its own terminal**

Create `ptyshell/user_linux_test.go`:

```go
package ptyshell

import (
	"log/slog"
	"os"
	"testing"

	"github.com/fables-for-robots/great-machine/launcher"
)

// The shell of another user is started on a terminal that this process
// opened, as root. It is the shell's own all the same: the launcher gives
// it to the user, which it can because the terminal is the command's when
// the launcher is asked to start it. The shell opens it by its name, as
// whatever asks for the terminal of its session does.
func TestAShellOfAnotherUserCanOpenItsTerminal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("another user takes root: scripts/test-linux.sh runs this as root, in a container")
	}
	l, err := launcher.New(launcher.Config{User: &launcher.User{UID: 65534, GID: 65534}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("launcher.New: %v", err)
	}
	t.Cleanup(l.Close)
	s, out := start(t, Config{Start: l.Start})
	typeIn(t, s, `t=$(tty); if : > "$t"; then echo "opened-$((6*7)) by $(id -u)"; else echo "denied-$((6*7)) to $(id -u)"; fi`)
	out.waitFor(t, "opened-42 by 65534")
}
```

- [ ] **Step 10: Run it, and see it fail**

Run: `scripts/test-linux.sh -run TestAShellOfAnotherUserCanOpenItsTerminal ./ptyshell/`

Expected: it fails, with

```text
user_linux_test.go:27: the output has no "opened-42 by 65534": ... /bin/sh: 1: cannot create /dev/pts/0: Permission denied
```

- [ ] **Step 11: Write the code: the terminal of a shell is opened before the shell is started, and is the command's by then: a launcher with a user gives it to the user**

Change `ptyshell/ptyshell.go`:

```diff
--- a/ptyshell/ptyshell.go
+++ b/ptyshell/ptyshell.go
@@ -36,9 +36,9 @@ type Config struct {
 	Cols  uint16   // 0: 80
 	Rows  uint16   // 0: 24
 	// Start, if set, starts a process in the place of cmd.Start: it is
-	// given the command and what starts it on its terminal, and returns
-	// what that returned. It is (*launcher.Launcher).Start. nil: the
-	// process is started as it is.
+	// given the command, which has the terminal as its standard streams
+	// already, and what starts it, and returns what that returned. It is
+	// (*launcher.Launcher).Start. nil: the process is started as it is.
 	Start func(cmd *exec.Cmd, start func() error) error
 }
 
@@ -89,14 +89,25 @@ func Start(cfg Config) (*Session, error) {
 	cmd := exec.Command(shell)
 	cmd.Env = env
 	cmd.Dir = cfg.Dir
-	// StartWithSize makes the shell the leader of a new session (Setsid)
-	// with the terminal as its controlling terminal (Setctty).
-	var started *os.File
-	begin := func() (err error) {
-		started, err = pty.StartWithSize(cmd, &size)
-		return err
+	// The terminal is opened here, and is the command's before anything
+	// starts it: what pty.StartWithSize does in one step is done in two.
+	// A Start that runs the shell as another user gives that user what
+	// the command is started on, and has to find the terminal there, or
+	// the shell could not open its own terminal by its name.
+	started, terminal, err := pty.Open()
+	if err != nil {
+		return nil, fmt.Errorf("ptyshell: opening a terminal for %s: %w", shell, err)
+	}
+	// The shell has the terminal as its own: this side keeps the master.
+	defer terminal.Close()
+	if err := pty.Setsize(started, &size); err != nil {
+		started.Close()
+		return nil, fmt.Errorf("ptyshell: the size of the terminal for %s: %w", shell, err)
 	}
-	var err error
+	cmd.Stdin, cmd.Stdout, cmd.Stderr = terminal, terminal, terminal
+	// The shell leads a new session (Setsid), with the terminal, which is
+	// its standard input, as its controlling terminal (Setctty).
+	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
 	if cfg.Start != nil {
 		// What Start starts may be another program that becomes the
 		// shell only afterwards, as a launcher has it: a shell that is
@@ -104,12 +115,13 @@ func Start(cfg Config) (*Session, error) {
 		// is to be an error here, as it is without, so it is looked at
 		// first.
 		if err = executable(cmd.Path); err == nil {
-			err = cfg.Start(cmd, begin)
+			err = cfg.Start(cmd, cmd.Start)
 		}
 	} else {
-		err = begin()
+		err = cmd.Start()
 	}
 	if err != nil {
+		started.Close()
 		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
 	}
 	master, err := pollable(started)
```

- [ ] **Step 12: Run it, and see it pass**

Run: `scripts/test-linux.sh -run TestAShellOfAnotherUserCanOpenItsTerminal ./ptyshell/`

Expected: `ok` for every package named. `gofmt -l .` and `go vet ./...` print nothing.

- [ ] **Step 13: Commit**

```bash
git add ptyshell/user_linux_test.go ptyshell/ptyshell.go
git commit -m 'Shell: the shell of another user can open its own terminal'
```

- [ ] **Step 14: The whole, here and on Linux as root**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test ./...'
scripts/test-linux.sh
git status --short
```

Expected: nothing from `gofmt` and `go vet`; `ok` for the 33 packages that have tests, both times; and nothing from `git status`. The run on Linux is the one that counts for what this task changed: on a Mac the tests that mount, and those of what the launcher's thread gives up, skip themselves.
