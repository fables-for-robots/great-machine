// Package launcher starts the processes of a zathras: the service and the
// shells.
//
// What it does to a process is --user. A Launcher that was given a user
// starts every process as that user, with the group and no other, and
// gives the pipes and the terminal the process is started on to that
// user. One that was given none starts a process as it is.
//
// A zathras mounts nothing and its container is given no capability, so
// there is nothing to keep from what it starts: a process is started
// from whichever thread starts it, and is the program that was meant
// from the first. Whether it can gain privileges by executing a program
// is the container's to say (allowPrivilegeEscalation), not the
// launcher's. What a launcher still does about capabilities is warn: a
// service that is root has every capability the container was given.
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

// Launcher starts processes. Its methods may be called from any
// goroutine.
type Launcher struct {
	user *User
	log  *slog.Logger
}

// New returns a launcher.
//
// It fails when the processes are to run as another user and this
// process is not root, which alone can start them so.
//
// It warns when the processes run as root and this process has
// capabilities that a container does not have by default: they have
// them too.
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
	if asRoot(l.user, os.Geteuid()) {
		bounding, err := boundingSet()
		if err != nil {
			// The warning is all the set is read for: a launcher that
			// cannot warn starts processes all the same.
			l.log.Warn("the capabilities of this process could not be read, so there is no saying whether the service and the shells, which run as root, have more than a container has by default", "error", err)
		} else {
			l.warn(bounding)
		}
	}
	return l, nil
}

// asRoot says whether the processes of a launcher with user run as root,
// in a process whose own user is euid.
func asRoot(user *User, euid int) bool {
	if user != nil {
		return user.UID == 0
	}
	return euid == 0
}

// warn says which capabilities of bounding, the bounding set of this
// process, a container does not have by default, when there are any: a
// process that root starts has them. It is for a launcher whose
// processes run as root.
func (l *Launcher) warn(bounding uint64) {
	if beyond := beyondDefault(bounding); len(beyond) > 0 {
		l.log.Warn("the service and the shells run as root with capabilities that a container does not have by default, "+
			"as in a privileged container: --user runs them as a user that has none",
			"capabilities", strings.Join(beyond, " "))
	}
}

// Start starts cmd with start, which is cmd.Start or what calls it, and
// returns what start returned.
//
// With a user, cmd is first given that user and the group, and no other
// group, and the standard streams of cmd that are pipes or terminals are
// given to that user, so that the program can open them anew by their
// names, /dev/stdout or the terminal of its session, as it could if it
// had made them. They have to be the command's when Start is called: a
// terminal that start itself opens is not seen here.
//
// Without a user nothing of cmd is changed.
func (l *Launcher) Start(cmd *exec.Cmd, start func() error) error {
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
	return start()
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
		l.log.Warn("a standard stream was not given to the user of the process, which cannot open it anew", "user", l.user.UID, "error", err)
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
