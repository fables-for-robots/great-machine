// Package ptyshell runs a shell on a pseudo-terminal: what an operator
// types is written to it, what the shell prints is read from it, and the
// size of the terminal can be changed while it runs.
package ptyshell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// killAfter is how long a shell has to end after SIGHUP before it is
// killed. A variable, so that the tests make it short.
var killAfter = 2 * time.Second

// drainFor is how long after the end of the shell its output can still be
// read while something else holds the terminal open (a job the shell left
// in the background). A variable, so that the tests make it short.
var drainFor = time.Second

// Config says what shell to start, and on what terminal.
type Config struct {
	Shell string   // the program; "": /bin/sh
	Env   []string // nil: os.Environ(); TERM is set from Term
	Dir   string   // the working directory; "": that of this process
	Term  string   // "": xterm
	Cols  uint16   // 0: 80
	Rows  uint16   // 0: 24
}

// Session is a shell that runs on a pseudo-terminal. Read, Write, Resize,
// Wait and Close may be called from different goroutines.
type Session struct {
	pty  *os.File // the master side of the terminal
	cmd  *exec.Cmd
	done chan struct{} // closed when the shell has ended
	// status is the exit status of the shell, once done is closed.
	status int

	closeOnce sync.Once
	closeErr  error
}

// Start starts the shell on a pseudo-terminal, in a session of its own:
// the terminal is its controlling terminal, and the shell leads a process
// group of its own.
func Start(cfg Config) (*Session, error) {
	shell := cfg.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	term := cfg.Term
	if term == "" {
		term = "xterm"
	}
	size := pty.Winsize{Cols: cfg.Cols, Rows: cfg.Rows}
	if size.Cols == 0 {
		size.Cols = 80
	}
	if size.Rows == 0 {
		size.Rows = 24
	}
	base := cfg.Env
	if base == nil {
		base = os.Environ()
	}
	env := make([]string, 0, len(base)+1)
	for _, v := range base {
		if !strings.HasPrefix(v, "TERM=") {
			env = append(env, v)
		}
	}
	env = append(env, "TERM="+term)

	cmd := exec.Command(shell)
	cmd.Env = env
	cmd.Dir = cfg.Dir
	// StartWithSize makes the shell the leader of a new session (Setsid)
	// with the terminal as its controlling terminal (Setctty).
	started, err := pty.StartWithSize(cmd, &size)
	if err != nil {
		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
	}
	master, err := pollable(started)
	if err != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		return nil, fmt.Errorf("ptyshell: starting %s: %w", shell, err)
	}
	s := &Session{pty: master, cmd: cmd, done: make(chan struct{})}
	go s.wait()
	return s, nil
}

// pollable returns the terminal that f is as a file whose Read ends when
// the file is closed or its deadline has passed, and closes f.
//
// The file of pty.StartWithSize is no such file: the package pty has
// called its Fd method, which makes a file blocking for good, and a Read
// of it that waits is ended by nothing but the terminal. Hence a second
// descriptor of the terminal, made non-blocking before the package os
// sees it.
func pollable(f *os.File) (*os.File, error) {
	defer f.Close()
	fd, err := unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("a second descriptor of the terminal: %w", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("making the terminal non-blocking: %w", err)
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

// wait waits for the shell and keeps its exit status.
func (s *Session) wait() {
	s.cmd.Wait()
	// ExitCode is -1 for a shell that a signal ended.
	s.status = s.cmd.ProcessState.ExitCode()
	// What the shell wrote is in the terminal and can be read at once. A
	// Read that waits after that waits for a process the shell left
	// behind, which is not the shell: it is given drainFor and no longer.
	s.pty.SetReadDeadline(time.Now().Add(drainFor))
	close(s.done)
}

// Read reads what the shell and its children wrote. It returns io.EOF
// when the shell has ended and all was read, and after Close.
func (s *Session) Read(p []byte) (int, error) {
	n, err := s.pty.Read(p)
	if n > 0 || err == nil {
		return n, nil
	}
	switch {
	case errors.Is(err, io.EOF):
		// What macOS answers when the other side of the terminal is closed.
	case errors.Is(err, syscall.EIO):
		// What Linux answers.
	case errors.Is(err, os.ErrClosed):
		// Close was called.
	case errors.Is(err, os.ErrDeadlineExceeded):
		// The shell has ended and something else holds the terminal.
	default:
		return 0, fmt.Errorf("ptyshell: reading the terminal: %w", err)
	}
	return 0, io.EOF
}

// Write writes p to the terminal, as if it had been typed.
func (s *Session) Write(p []byte) (int, error) {
	n, err := s.pty.Write(p)
	if err != nil {
		return n, fmt.Errorf("ptyshell: writing to the terminal: %w", err)
	}
	return n, nil
}

// Resize tells the terminal, and through it the shell, its new size.
func (s *Session) Resize(cols, rows uint16) error {
	// Not pty.Setsize, which would make the file blocking: see pollable.
	raw, err := s.pty.SyscallConn()
	if err != nil {
		return fmt.Errorf("ptyshell: resizing the terminal: %w", err)
	}
	var ioctlErr error
	err = raw.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	})
	if err = errors.Join(err, ioctlErr); err != nil {
		return fmt.Errorf("ptyshell: resizing the terminal: %w", err)
	}
	return nil
}

// Wait returns the exit status when the shell has ended: -1 when a signal
// ended it.
func (s *Session) Wait() int {
	<-s.done
	return s.status
}

// Close ends the shell if it runs, with SIGHUP to its process group and
// SIGKILL two seconds later, and frees the terminal. It returns when the
// shell has ended. It may be called more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		// From here on nobody may be reading what the shell prints, and on
		// macOS a process cannot end while output of its terminal waits to
		// be read. So what is left is read here and dropped, until the
		// terminal is closed.
		go io.Copy(io.Discard, s.pty)
		select {
		case <-s.done:
		default:
			s.signal(syscall.SIGHUP)
			timer := time.NewTimer(killAfter)
			defer timer.Stop()
			select {
			case <-s.done:
			case <-timer.C:
				s.signal(syscall.SIGKILL)
				<-s.done
			}
		}
		// Closing the terminal hangs up whatever still has it.
		if err := s.pty.Close(); err != nil {
			s.closeErr = fmt.Errorf("ptyshell: closing the terminal: %w", err)
		}
	})
	return s.closeErr
}

// signal sends sig to the process group of the shell and to the one in
// the foreground of the terminal, which is another one while the shell
// runs a command with job control.
func (s *Session) signal(sig syscall.Signal) {
	shell := s.cmd.Process.Pid // the shell leads its group
	syscall.Kill(-shell, sig)
	if fg := s.foreground(); fg > 1 && fg != shell {
		syscall.Kill(-fg, sig)
	}
}

// foreground returns the process group in the foreground of the terminal,
// or 0 when it cannot be told.
func (s *Session) foreground() int {
	raw, err := s.pty.SyscallConn()
	if err != nil {
		return 0
	}
	var group int
	var ioctlErr error
	err = raw.Control(func(fd uintptr) {
		group, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	})
	if err != nil || ioctlErr != nil {
		return 0
	}
	return group
}
