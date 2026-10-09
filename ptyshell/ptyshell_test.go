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
