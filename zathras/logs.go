package zathras

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/wire"
)

// followBuffer is how many lines may wait for a follower of the log that
// reads slowly before it loses the oldest of them.
const followBuffer = 1024

// logs writes the lines of the ring to w, and with follow every line that
// comes after them, until ctx is done or a write fails.
func (a *Agent) logs(ctx context.Context, w io.Writer, req *wire.LogsRequest) {
	if !req.Follow {
		for _, line := range a.cfg.Ring.Lines() {
			if wire.Write(w, &wire.Response{Line: &line}) != nil {
				return
			}
		}
		return
	}
	// Follow ends the following, and closes the channel, when ctx is done.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, more := a.cfg.Ring.Follow(ctx, followBuffer)
	for _, line := range lines {
		if wire.Write(w, &wire.Response{Line: &line}) != nil {
			return
		}
	}
	for line := range more {
		if wire.Write(w, &wire.Response{Line: &line}) != nil {
			return
		}
	}
}
