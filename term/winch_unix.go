//go:build unix

package term

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize returns a channel that gets a value whenever the size of
// the terminal of this process has changed (SIGWINCH), and a function that
// ends that and closes the channel.
func notifyResize() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}
