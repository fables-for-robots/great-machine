package zathras

import (
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// remoteShell is a shell session as draal has it.
type remoteShell struct {
	t      *testing.T
	l      *link
	s      node.Stream
	output strings.Builder
}

// openShell asks for a shell and reads the output that says it runs.
func (l *link) openShell(req wire.ShellRequest) *remoteShell {
	l.t.Helper()
	s := l.open(&wire.Request{Shell: &req})
	resp, err := l.read(s)
	if err != nil {
		l.t.Fatalf("shell answered %v", err)
	}
	// The first output is empty and says that the shell runs.
	if resp.Shell == nil || len(resp.Shell.Data) != 0 || resp.Shell.Exit != nil {
		l.t.Fatalf("shell answered %+v first, want an empty output", resp)
	}
	return &remoteShell{t: l.t, l: l, s: s}
}

// typeIn sends a line as it is typed.
func (r *remoteShell) typeIn(line string) {
	r.t.Helper()
	if err := wire.Write(r.s, &wire.ShellInput{Data: []byte(line + "\n")}); err != nil {
		r.t.Fatalf("writing input: %v", err)
	}
}

// expect reads output until all of it holds a match of pattern, and
// returns the match's first group. The terminal echoes what is typed, so
// a pattern has to match what only the shell can have printed.
func (r *remoteShell) expect(pattern string) string {
	r.t.Helper()
	re := regexp.MustCompile(pattern)
	for {
		if m := re.FindStringSubmatch(r.output.String()); m != nil {
			return m[len(m)-1]
		}
		resp, err := r.l.read(r.s)
		if err != nil || resp.Shell == nil {
			r.t.Fatalf("waiting for %q the shell answered %+v, %v; the output so far:\n%s", pattern, resp, err, r.output.String())
		}
		if resp.Shell.Exit != nil {
			r.t.Fatalf("waiting for %q the shell ended with %d; the output so far:\n%s", pattern, *resp.Shell.Exit, r.output.String())
		}
		r.output.Write(resp.Shell.Data)
	}
}

// pid returns the process ID of the shell.
func (r *remoteShell) pid() int {
	r.t.Helper()
	r.typeIn("echo pid=$$.")
	pid, err := strconv.Atoi(r.expect(`pid=(\d+)\.`))
	if err != nil {
		r.t.Fatal(err)
	}
	return pid
}

// gone fails the test unless the process ends.
func gone(t *testing.T, pid int) {
	t.Helper()
	eventually(t, "process "+strconv.Itoa(pid)+" to end", func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

func TestShell(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 90, Rows: 30, Term: "vt100"})

	sh.typeIn("echo hello-$((6*7))")
	sh.expect(`hello-42`)

	// The terminal is as the request says.
	sh.typeIn("echo term=$TERM.")
	sh.expect(`term=vt100\.`)
	sh.typeIn("stty size")
	sh.expect(`30 90`)

	// A change of size is applied.
	if err := wire.Write(sh.s, &wire.ShellInput{Resize: &wire.WinSize{Cols: 101, Rows: 37}}); err != nil {
		t.Fatal(err)
	}
	sh.typeIn("stty size")
	sh.expect(`37 101`)

	// The end of the shell is its exit status, and then the end of the
	// stream.
	sh.typeIn("exit 3")
	for {
		resp, err := l.read(sh.s)
		if err != nil || resp.Shell == nil {
			t.Fatalf("the shell answered %+v, %v, want output or the exit status", resp, err)
		}
		if resp.Shell.Exit != nil {
			if *resp.Shell.Exit != 3 {
				t.Fatalf("the exit status is %d, want 3", *resp.Shell.Exit)
			}
			break
		}
	}
	if resp, err := l.read(sh.s); err != io.EOF {
		t.Fatalf("after the exit status the shell answered %+v, %v, want the end of the stream", resp, err)
	}
	sh.s.Close()
	w.idle()
}

func TestShellEndsWithItsStream(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()
	sh.typeIn("sleep 1000")

	sh.s.Close()
	gone(t, pid)
	w.idle()
}

func TestShellEndsWithTheConnection(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()

	l.conn.Close()
	gone(t, pid)
	w.idle()
}

func TestShellEndsWithRun(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	sh := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	pid := sh.pid()

	w.stop() // fails the test unless Run returns nil in time
	// Run ends the shells before it returns.
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the shell, process %d, is still there after Run has returned (kill 0: %v)", pid, err)
	}
}

func TestShellThatCannotBeStarted(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	w := newWorld(t, setup{shell: missing})
	l := w.accept()
	resp, err := l.call(&wire.Request{Shell: &wire.ShellRequest{Cols: 80, Rows: 24}})
	if wire.CodeOf(err) != wire.CodeInternal || !strings.Contains(err.Error(), missing) {
		t.Fatalf("shell answered %+v, %v, want an internal error that names the shell", resp, err)
	}
	w.idle()
}

func TestTwoShellsAtOnce(t *testing.T) {
	w := newWorld(t, setup{})
	l := w.accept()
	one := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	two := l.openShell(wire.ShellRequest{Cols: 80, Rows: 24})
	if a, b := one.pid(), two.pid(); a == b {
		t.Fatalf("both sessions are process %d", a)
	}
	one.typeIn("exit 0")
	two.typeIn("echo still-$((6*7))")
	two.expect(`still-42`)
}
