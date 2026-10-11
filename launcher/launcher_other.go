//go:build !linux

package launcher

import (
	"os"
	"sync/atomic"
)

// self is the program itself.
func self() (string, error) {
	return os.Executable()
}

// thread is nothing where a process is started from any thread: there is
// no capability to keep from it.
type thread struct {
	closed atomic.Bool
}

func (l *Launcher) begin() error { return nil }

// run runs start and returns what it returned.
func (l *Launcher) run(start func() error) error {
	if l.closed.Load() {
		return ErrClosed
	}
	return start()
}

func (l *Launcher) end() { l.closed.Store(true) }
