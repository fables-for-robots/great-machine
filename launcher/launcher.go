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
// The same goes for the working directory of what is started, which
// os/exec changes to between the fork and the execution: the process
// that replaces itself changes to it.
//
// What is started is started as it would have been without a launcher,
// with one difference besides: a signal that this process was itself
// started ignoring, other than SIGHUP and SIGINT, is not ignored by what
// a launcher starts. The Go program in between takes every other signal
// for itself, and the execution sets what was taken back to its default.
package launcher

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Arg is the first argument of the program when it is started to replace
// itself with another:
//
//	great-machine exec-in-place DIR PATH ARGV0 [ARG...]
//
// DIR is the working directory to change to first; empty, it stays where
// it is.
const Arg = "exec-in-place"

// The exit status of a program that was to replace itself and could not,
// as a shell has them.
const (
	statusNotExecuted = 126 // the program could not be executed
	statusNotThere    = 127 // there is no such program
)

// Replace is called first thing in main, and in the TestMain of a test
// that starts processes through a Launcher. When args are not those of
// Arg it returns (0, false) at once. When they are, it changes to DIR,
// unless that is empty, and replaces the process with the program at
// PATH, with the arguments from ARGV0 on and the environment of the
// process, and does not return; when that fails it says so on standard
// error and returns the exit status, 127 for a program that is not there
// and 126 for any other failure, and true.
func Replace(args []string) (status int, was bool) {
	if len(args) < 2 || args[1] != Arg {
		return 0, false
	}
	if len(args) < 5 {
		fmt.Fprintf(os.Stderr, "great-machine: %s is how a zathras starts a program: it takes DIR PATH ARGV0 [ARG...]\n", Arg)
		return statusNotExecuted, true
	}
	dir, path := args[2], args[3]
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "great-machine: changing to the working directory %s: %v\n", dir, errors.Unwrap(err))
			return statusNotExecuted, true
		}
	}
	err := syscall.Exec(path, args[4:], os.Environ())
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

// Config is what a Launcher is made of.
type Config struct {
	// User is who the processes run as, with the group and no others.
	// nil: the user of this process, and its groups.
	User *User
	// Log is where the launcher logs. nil: slog.Default().
	Log *slog.Logger
}

// ErrClosed is what Start returns once the launcher was closed.
var ErrClosed = errors.New("launcher: closed")

// Launcher starts processes. Its methods may be called from any
// goroutine.
type Launcher struct {
	user *User
	log  *slog.Logger
	// exe is the program itself, which is what every process is started
	// as.
	exe string
	// thread is what a launcher is on Linux, and nothing elsewhere.
	thread
}

// New returns a launcher. On Linux it starts the launcher's thread, which
// drops CAP_SYS_ADMIN from its bounding set, takes it out of its
// inheritable set, clears its ambient set and sets no_new_privs.
//
// It fails when the processes are to run as another user and this
// process is not root, which alone can start them so.
func New(cfg Config) (*Launcher, error) {
	l := &Launcher{user: cfg.User, log: cfg.Log}
	if l.log == nil {
		l.log = slog.Default()
	}
	if l.user != nil && os.Geteuid() != 0 {
		own := User{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
		if *l.user != own {
			return nil, fmt.Errorf("launcher: only root starts a process as another user, and this process is user %d: it cannot start one as %d:%d",
				own.UID, l.user.UID, l.user.GID)
		}
		// Its own user is what a process has anyway, and to set it
		// would be to set its groups, which takes root as well.
		l.user = nil
	}
	exe, err := self()
	if err != nil {
		return nil, fmt.Errorf("launcher: finding the program: %w", err)
	}
	l.exe = exe
	if err := l.begin(); err != nil {
		return nil, err
	}
	return l, nil
}

// Start starts cmd with start, which is cmd.Start or what calls it.
// Before that it makes cmd the program itself, started with Arg to
// change to the working directory of cmd and to replace itself with what
// cmd was to run, and sets the user of Config. On Linux start runs on the
// launcher's thread. Start returns what start returned.
//
// With a user, the standard streams of cmd that are pipes or terminals
// are given to that user first, so that the program can open them anew
// by their names, /dev/stdout or the terminal of its session, as it could
// if it had made them. They have to be the command's when Start is
// called: a terminal that start itself opens is not seen here.
//
// A program that cannot be executed, and a working directory that cannot
// be changed to, are therefore no errors of Start: the process that was
// to become the program says why on its standard error and ends, with
// 127 for a program that is not there and 126 otherwise. A cmd whose Err
// is set, for a name that was not found, is not started: Start returns
// that error. Once the launcher is closed Start returns ErrClosed.
func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error {
	if cmd.Err != nil {
		return cmd.Err
	}
	l.rewrite(cmd)
	return l.run(start)
}

// rewrite makes cmd the program itself, which is to change to the working
// directory of cmd and to replace itself with the program of cmd, and
// gives it the user of the launcher.
func (l *Launcher) rewrite(cmd *exec.Cmd) {
	argv := cmd.Args
	if len(argv) == 0 {
		// As os/exec has it for a command without arguments.
		argv = []string{cmd.Path}
	}
	args := make([]string, 0, 4+len(argv))
	args = append(args, "great-machine", Arg, cmd.Dir, cmd.Path)
	cmd.Path, cmd.Args, cmd.Dir = l.exe, append(args, argv...), ""
	if l.user != nil {
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		// No Groups: the process has the group and no others.
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: l.user.UID, Gid: l.user.GID}
		for _, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
			if f, ok := stream.(*os.File); ok && f != nil {
				l.give(f)
			}
		}
	}
}

// give makes the user of the launcher the owner of f, when f is a pipe or
// a terminal: what a process is started on, and nothing that is a file of
// somebody's or a device of the system, as /dev/null is. That it could
// not be done is no reason not to start the process, which then cannot
// open its standard streams anew.
func (l *Launcher) give(f *os.File) {
	info, err := f.Stat()
	if err != nil {
		return
	}
	switch {
	case info.Mode()&os.ModeNamedPipe != 0:
	case info.Mode()&os.ModeCharDevice != 0 && isTerminal(f):
	default:
		return
	}
	if err := f.Chown(int(l.user.UID), -1); err != nil {
		l.log.Debug("a standard stream was not given to the user of the process, which cannot open it anew", "user", l.user.UID, "error", err)
	}
}

// isTerminal says whether f is a terminal, without making the file
// blocking, as asking for its descriptor would.
func isTerminal(f *os.File) bool {
	raw, err := f.SyscallConn()
	if err != nil {
		return false
	}
	is := false
	raw.Control(func(fd uintptr) { is = term.IsTerminal(int(fd)) })
	return is
}

// Close ends the launcher's thread. No Start may follow: one that does
// returns ErrClosed. Close may be called more than once.
func (l *Launcher) Close() {
	l.end()
}
