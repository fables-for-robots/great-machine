// Package reaper does what the first process of a container has to do,
// so that the rest of the program need not: it runs the program again as
// a child, and itself only passes signals on and waits for every child
// there is, the orphans the kernel hands to process 1 among them.
package reaper

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Init returns false at once in a process that is not process 1. In
// process 1 it starts the program again as its child, with the same
// arguments, environment and standard streams, passes SIGTERM, SIGINT and
// SIGHUP on to it, waits for every child there is, and returns true and
// the exit status of that child when it has ended.
//
// The status of a child that a signal ended is 128 plus the number of the
// signal, as a shell has it.
func Init() (status int, was bool) {
	if os.Getpid() != 1 {
		return 0, false
	}
	return run(), true
}

// run is Init in process 1.
func run() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "reaper: finding the program:", err)
		return 1
	}

	// The signals are asked for before the child is started, so that
	// the SIGCHLD of a child that ends at once is not missed.
	signals := make(chan os.Signal, 64)
	signal.Notify(signals, syscall.SIGCHLD, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Not os/exec, whose Wait would wait for this one child: the loop
	// below is what waits, for this child and every other.
	child, err := syscall.ForkExec(exe, os.Args, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reaper: starting %s: %v\n", exe, err)
		return 1
	}

	for sig := range signals {
		if sig != syscall.SIGCHLD {
			syscall.Kill(child, sig.(syscall.Signal))
			continue
		}
		// One SIGCHLD may stand for several children: take every one
		// that has ended.
		for {
			var ws syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil || pid <= 0 {
				break
			}
			if pid != child {
				continue
			}
			switch {
			case ws.Exited():
				return ws.ExitStatus()
			case ws.Signaled():
				return 128 + int(ws.Signal())
			}
		}
	}
	return 1
}
