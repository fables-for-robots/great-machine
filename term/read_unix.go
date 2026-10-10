//go:build unix

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// errStopped is what Read of a reader returns once stop was called.
var errStopped = errors.New("term: the reading was stopped")

// errGone is what Read of a reader returns when the terminal it reads has
// gone: its other end is closed, as when the window is.
var errGone = errors.New("the terminal has gone")

// reader reads a file so that the reading can be ended while it waits for
// input, which a plain Read of a terminal cannot be.
//
// It waits with select(2) for the file or for a pipe of its own, and reads
// the file only when select says that there is something to read; stop
// writes to the pipe. select is used because it is the one call that does
// this for a terminal on Linux and on macOS alike: on macOS kqueue does not
// work for /dev/tty, and poll is documented not to work for every device.
// The file itself is left as it is: it is not made non-blocking, which
// would change it for every process that shares it, the shell that started
// this program among them.
//
// stop waits for the read that is under way, so that read must not wait.
// After select it does not, as long as nobody else reads the file; but
// something else may: a reader that the caller has not quite ended, or
// another program on the same terminal. It may take what select found,
// and a read would then wait for the next key, hold stop up until it is
// typed, and take it. For a terminal that is ruled out with its mode:
// noWait makes a read return at once, with nothing, when there is nothing.
// For a file that is no terminal nothing rules it out: there the reader
// has to be the only one.
//
// What select costs: it knows file descriptors below 1024 alone, so
// newReader fails in a process that has more files open than that.
type reader struct {
	fd         int  // the file
	tty        bool // the file is a terminal whose reads do not wait
	wake, woke int  // the pipe: stop writes to woke, Read waits for wake

	// found, when a test has set it, is called when select has found
	// input and before it is read.
	found func()

	// read, when a test has set it, is called in place of unix.Read to
	// read the file: through it a read fails as it does only at an instant
	// that a test cannot bring about.
	read func(fd int, p []byte) (int, error)

	stopped atomic.Bool
	mu      sync.Mutex // held while Read waits for the file or reads it
	once    sync.Once
}

// newReader returns a reader of f. It is to be stopped, which frees the
// pipe.
func newReader(f *os.File) (*reader, error) {
	var p [2]int
	// No child may get the pipe. Without pipe2, which macOS lacks, that
	// takes the lock that keeps a fork away until both ends are marked.
	syscall.ForkLock.RLock()
	err := unix.Pipe(p[:])
	if err == nil {
		unix.CloseOnExec(p[0])
		unix.CloseOnExec(p[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("term: a pipe to end the reading with: %w", err)
	}
	r := &reader{fd: int(f.Fd()), wake: p[0], woke: p[1]}
	if r.fd < 0 || r.fd >= unix.FD_SETSIZE || r.wake >= unix.FD_SETSIZE {
		unix.Close(p[0])
		unix.Close(p[1])
		return nil, fmt.Errorf("term: too many files are open to wait for input: select knows file descriptors below %d alone", unix.FD_SETSIZE)
	}
	return r, nil
}

// noWait is to be called when the file is a terminal that was made raw:
// it sets the mode of the terminal so that a read returns at once, with
// nothing, when there is nothing to read (VMIN and VTIME 0). Whoever
// restores the mode the terminal had takes that back.
func (r *reader) noWait() error {
	mode, err := unix.IoctlGetTermios(r.fd, getTermios)
	if err != nil {
		return err
	}
	mode.Cc[unix.VMIN], mode.Cc[unix.VTIME] = 0, 0
	if err := unix.IoctlSetTermios(r.fd, setTermios, mode); err != nil {
		return err
	}
	r.tty = true
	return nil
}

// Read waits for input and reads it, as a Read of the file does: it
// returns at least one byte, or an error, or nothing at once when p is
// empty. Once stop was called it returns errStopped, without having read
// anything. It is for one goroutine.
func (r *reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	empty := 0 // reads in a row that select promised and that got nothing
	for {
		if r.stopped.Load() {
			return 0, errStopped
		}
		var set unix.FdSet
		set.Set(r.fd)
		set.Set(r.wake)
		// A signal ends select, and SIGWINCH comes whenever the window
		// changes: it is asked again.
		if _, err := unix.Select(max(r.fd, r.wake)+1, &set, nil, nil, nil); err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, fmt.Errorf("select: %w", err)
		}
		if !set.IsSet(r.fd) {
			// The pipe alone: stopped is set, and the loop says so.
			continue
		}
		if r.stopped.Load() {
			// Both at once: what is there to read is left for whoever
			// reads the file next.
			return 0, errStopped
		}
		if r.found != nil {
			r.found()
		}
		read := unix.Read
		if r.read != nil {
			read = r.read
		}
		n, err := read(r.fd, p)
		switch {
		case err == unix.EINTR || err == unix.EAGAIN:
			continue
		case err == unix.EIO && r.tty && r.gone():
			// The other end of the terminal has just been closed: on
			// Linux a read fails with EIO until the kernel has hung the
			// terminal up. See gone.
			return 0, errGone
		case err != nil:
			return 0, fmt.Errorf("read: %w", err)
		case n > 0:
			return n, nil
		case !r.tty:
			return 0, io.EOF
		}
		// A terminal whose reads do not wait had nothing. Either it has
		// gone: then select says for ever that it can be read, and a read
		// returns nothing, on macOS and, once the hangup has gone
		// through, on Linux (before it a read fails with EIO, above). Or
		// another reader took what select had found: then there is
		// nothing to do but to wait again.
		if r.gone() {
			return 0, errGone
		}
		// Not to spin, should a terminal ever be gone without saying so.
		if empty++; empty > 3 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// gone says whether the other end of the terminal is closed. poll tells:
// it reports a hangup for a pseudo-terminal whose other end is closed, on
// Linux and on macOS, where nothing else does (on macOS even the requests
// for the mode and the size still succeed). A read does not tell it
// alone: on Linux a read of a pseudo-terminal whose other end has just
// been closed fails with EIO until the kernel has hung the terminal up,
// and returns nothing from then on, and poll reports the hangup in both;
// on macOS a read returns nothing. But a read fails with EIO for other
// reasons too, as one by a process that may not read the terminal, and
// returns nothing when another reader took what select had found. It is
// asked without waiting, and only once select and a read have disagreed
// or a read of the terminal has failed with EIO. A device that poll does
// not know is taken for alive, hence the pause in Read: poll reports such
// a device with nothing or, as macOS does for /dev/tty, with POLLNVAL,
// which is therefore no hangup. A descriptor that is not open at all has
// failed select before poll is asked.
func (r *reader) gone() bool {
	fds := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, 0)
		if err == unix.EINTR {
			continue
		}
		return err == nil && hungUp(fds[0].Revents)
	}
}

// hungUp says whether the events poll reported are those of a terminal
// whose other end is closed: a hangup or an error. Linux reports both
// together for a terminal it has hung up, and a hangup alone in the
// instant before, when its other end has been closed. POLLNVAL is none:
// it says that poll does not know the device.
func hungUp(revents int16) bool {
	return revents&(unix.POLLHUP|unix.POLLERR) != 0
}

// stop ends the reading: a Read that waits returns errStopped, and so does
// every later one. When stop returns, no Read is in the file and none will
// be. It may be called more than once.
func (r *reader) stop() {
	r.once.Do(func() {
		r.stopped.Store(true)
		for {
			if _, err := unix.Write(r.woke, []byte{0}); err != unix.EINTR {
				break
			}
		}
		// Waits for the Read that is under way: it is in select, which the
		// pipe has ended, or in a read of what select found, which does
		// not wait: see reader.
		r.mu.Lock()
		defer r.mu.Unlock()
		unix.Close(r.wake)
		unix.Close(r.woke)
	})
}
