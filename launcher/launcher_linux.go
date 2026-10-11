package launcher

import (
	"runtime"
	"sync"
)

// self is the program itself. The link is to the file that is being
// executed, whatever is mounted over the path it was started from: a file
// of the image, and never one of the store that the zathras serves.
func self() (string, error) {
	return "/proc/self/exe", nil
}

// thread is the launcher's thread: a goroutine that is locked to its OS
// thread for good, and runs what it is given.
type thread struct {
	work chan func()
	quit chan struct{}
	once sync.Once
}

// begin starts the thread.
func (l *Launcher) begin() error {
	l.work = make(chan func())
	l.quit = make(chan struct{})
	go l.loop()
	return nil
}

// loop is the goroutine of the thread.
func (l *Launcher) loop() {
	// Locked, and never unlocked: the runtime runs no other goroutine on
	// a locked thread, makes no new thread from one, and ends the thread
	// with its goroutine. What this thread has given up, no other part of
	// the process has given up with it.
	runtime.LockOSThread()
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
