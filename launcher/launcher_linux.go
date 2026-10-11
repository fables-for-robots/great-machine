package launcher

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// self is the program itself. The link is to the file that is being
// executed, whatever is mounted over the path it was started from: a file
// of the image, and never one of the store that the zathras serves.
func self() (string, error) {
	return "/proc/self/exe", nil
}

// thread is the launcher's thread: a goroutine that is locked to its OS
// thread for good, and runs what it is given.
//
// Linux keeps the capabilities of a process, and whether it may gain
// privileges, for every thread, and a child is a copy of the thread that
// started it. This thread gives up what the processes of a zathras are
// not to have, and every one of them is started from it. The other
// threads keep what they had: the zathras goes on mounting.
type thread struct {
	work chan func()
	quit chan struct{}
	once sync.Once
	tid  int // of the OS thread
}

// begin starts the thread, and returns when the thread has given up what
// it is to give up, or with why it could not.
func (l *Launcher) begin() error {
	l.work = make(chan func())
	l.quit = make(chan struct{})
	ready := make(chan error, 1)
	go l.loop(ready)
	return <-ready
}

// loop is the goroutine of the thread.
func (l *Launcher) loop(ready chan<- error) {
	// Locked, and never unlocked: the runtime runs no other goroutine on
	// a locked thread, makes no new thread from one, and ends the thread
	// with its goroutine, or parks it for good if it is the first thread
	// of the process, which cannot end. What this thread has given up, no
	// other part of the process has given up with it.
	runtime.LockOSThread()
	l.tid = unix.Gettid()
	err := l.restrict()
	ready <- err
	if err != nil {
		return
	}
	for {
		select {
		case f := <-l.work:
			f()
		case <-l.quit:
			return
		}
	}
}

// run runs start on the thread and returns what it returned.
func (l *Launcher) run(start func() error) error {
	done := make(chan error, 1)
	select {
	case l.work <- func() { done <- start() }:
		return <-done
	case <-l.quit:
		return ErrClosed
	}
}

// end ends the thread, when what it runs has returned.
func (l *Launcher) end() {
	l.once.Do(func() { close(l.quit) })
}

// restrict takes from the calling thread, which is the launcher's, what
// the processes it starts are not to have:
//
//   - CAP_SYS_ADMIN, out of the bounding set and out of the inheritable
//     set, which together are what a program that root executes gets;
//   - every ambient capability, which a program gets whoever executes it;
//   - the gaining of privileges by executing a program (no_new_privs): a
//     setuid program, or a file with capabilities.
//
// It fails when the capability cannot be dropped and the processes run as
// root, and warns when they run as root with more than a container has
// by default.
func (l *Launcher) restrict() error {
	asRoot := os.Geteuid() == 0
	if l.user != nil {
		asRoot = l.user.UID == 0
	}
	bounding, err := boundingSet()
	if err != nil {
		return fmt.Errorf("launcher: reading the bounding set: %w", err)
	}
	if bounding&(1<<capSysAdmin) != 0 {
		err := unix.Prctl(unix.PR_CAPBSET_DROP, capSysAdmin, 0, 0, 0)
		if refusal := refusalOf(err, asRoot); refusal != nil {
			return refusal
		}
		if err == nil {
			bounding &^= 1 << capSysAdmin
		}
	}

	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var sets [2]unix.CapUserData // the capabilities below 32, and those from 32 on
	if err := unix.Capget(&header, &sets[0]); err != nil {
		return fmt.Errorf("launcher: reading the capabilities: %w", err)
	}
	if sets[0].Inheritable&(1<<capSysAdmin) != 0 {
		sets[0].Inheritable &^= 1 << capSysAdmin
		if err := unix.Capset(&header, &sets[0]); err != nil {
			return fmt.Errorf("launcher: taking CAP_SYS_ADMIN out of the inheritable set: %w", err)
		}
	}
	// A kernel before 4.3 has no ambient set, and says so with EINVAL.
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil && !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("launcher: clearing the ambient set: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("launcher: setting no_new_privs: %w", err)
	}

	if asRoot {
		if beyond := beyondDefault(bounding); len(beyond) > 0 {
			l.log.Warn("the service and the shells run as root with capabilities that a container does not have by default, "+
				"as in a privileged container: --user runs them as a user that has none",
				"capabilities", strings.Join(beyond, " "))
		}
	}
	return nil
}

// boundingSet returns the bounding set of the calling thread: a bit for
// every capability a program that it executes can have at all.
func boundingSet() (uint64, error) {
	var set uint64
	for n := range 64 {
		has, err := unix.PrctlRetInt(unix.PR_CAPBSET_READ, uintptr(n), 0, 0, 0)
		if errors.Is(err, unix.EINVAL) {
			// The kernel has no capability of this number, and none above.
			break
		}
		if err != nil {
			return 0, err
		}
		if has == 1 {
			set |= 1 << n
		}
	}
	return set, nil
}
