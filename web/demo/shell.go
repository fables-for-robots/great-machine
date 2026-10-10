package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fables-for-robots/great-machine/draal"
	"github.com/fables-for-robots/great-machine/ptyshell"
	"github.com/fables-for-robots/great-machine/wire"
)

// shell is a real shell on this machine, as the draal.Shell of a made-up
// zathras. It ends as a session of draal does when its zathras goes:
// with unavailable.
type shell struct {
	zathras wire.ID
	session *ptyshell.Session
	stop    chan struct{} // closed by Close
	gone    atomic.Bool   // the zathras went while the shell ran
	once    sync.Once
}

// A shell is a draal.Shell.
var _ draal.Shell = (*shell)(nil)

// startShell starts a shell on this machine for the zathras. It is ended
// when left is closed: the zathras has gone.
func startShell(zathras wire.ID, req wire.ShellRequest, left <-chan struct{}) (*shell, error) {
	session, err := ptyshell.Start(ptyshell.Config{Term: req.Term, Cols: req.Cols, Rows: req.Rows})
	if err != nil {
		return nil, wire.Errorf(wire.CodeInternal, "starting a shell: %v", err)
	}
	sh := &shell{zathras: zathras, session: session, stop: make(chan struct{})}
	go func() {
		select {
		case <-left:
			sh.gone.Store(true)
			session.Close()
		case <-sh.stop:
		}
	}()
	return sh, nil
}

// Read returns what the shell printed, io.EOF when it has ended, and
// unavailable when the zathras went.
func (sh *shell) Read(p []byte) (int, error) {
	n, err := sh.session.Read(p)
	if errors.Is(err, io.EOF) && sh.gone.Load() {
		return n, wire.Errorf(wire.CodeUnavailable, "the connection of zathras %s failed: the zathras is gone", sh.zathras)
	}
	return n, err
}

// Write writes what was typed.
func (sh *shell) Write(p []byte) (int, error) {
	return sh.session.Write(p)
}

// Resize tells the shell the size of the terminal.
func (sh *shell) Resize(cols, rows uint16) error {
	return sh.session.Resize(cols, rows)
}

// Wait returns the exit status of the shell once it has ended.
func (sh *shell) Wait(ctx context.Context) (int, error) {
	status := make(chan int, 1)
	go func() { status <- sh.session.Wait() }()
	select {
	case s := <-status:
		return s, nil
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	}
}

// Close ends the shell.
func (sh *shell) Close() error {
	sh.once.Do(func() { close(sh.stop) })
	return sh.session.Close()
}
