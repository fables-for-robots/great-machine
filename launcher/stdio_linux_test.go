package launcher

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// another is a user that is not root, and has no name anywhere.
var another = &User{UID: 65534, GID: 65534}

// A service writes to pipes that the supervisor made, as root. Started as
// another user it is given them: a program that opens /dev/stdout or
// /dev/stderr anew, as many are configured to, opens what it was started
// with, whoever it runs as.
func TestWhatIsStartedCanOpenItsStandardStreams(t *testing.T) {
	root(t)
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
	root(t)
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

// What is given to another user is what a process is started on, a pipe
// or a terminal, and nothing else: a file that a process writes to, and
// a device of the system, as /dev/null is, keep their owner.
func TestWhatIsNoPipeAndNoTerminalKeepsItsOwner(t *testing.T) {
	root(t)
	l := newLauncher(t, Config{User: another})
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// A device of the test's own, which is /dev/null by its numbers: were
	// it given away, the machine's /dev/null would not be.
	path := filepath.Join(t.TempDir(), "null")
	if err := unix.Mknod(path, unix.S_IFCHR|0o666, int(unix.Mkdev(1, 3))); err != nil {
		t.Fatalf("making a device for the test: %v", err)
	}
	null, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	owner := func(f *os.File) uint32 {
		t.Helper()
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return info.Sys().(*syscall.Stat_t).Uid
	}
	before := map[*os.File]uint32{file: owner(file), null: owner(null)}
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, file, file
	if err := l.Start(cmd, cmd.Start); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	for f, uid := range before {
		if got := owner(f); got != uid {
			t.Errorf("%s belongs to %d after a process was started on it as another user, want %d as before", f.Name(), got, uid)
		}
	}
}
