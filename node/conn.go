package node

import (
	"context"
	"errors"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// errClosed is what a Write returns after Close.
var errClosed = errors.New("node: the stream was closed on this side")

// stream is a Stream over a stream of go-iroh.
type stream struct {
	s *iroh.Stream

	mu      sync.Mutex
	writing int  // the Writes that have not returned
	closed  bool // Close was called
}

func (s *stream) Read(p []byte) (int, error) { return s.s.Read(p) }

// Write counts itself, so that Close knows of a Write that may wait.
func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errClosed
	}
	s.writing++
	s.mu.Unlock()
	n, err := s.s.Write(p)
	s.mu.Lock()
	s.writing--
	s.mu.Unlock()
	return n, err
}

// CloseWrite sends the end of the stream after what was written.
func (s *stream) CloseWrite() error {
	return s.s.CloseWrite()
}

// Close tells the peer to stop sending, which fails its Write and closes
// its Done, and ends what this side sends: with the end of the stream
// after what was written, which the peer reads as io.EOF, or, when a
// Write has not returned, with a reset.
//
// Close of go-iroh's stream ends one direction only, sends the end behind
// all that was written, and does not wake a Write that waits for the peer
// to read: such a Write would wait until the connection ends. A reset
// (CancelWrite) wakes it at once, and drops what the peer has not read,
// even what has arrived there. Hence the one where no Write is under way
// and the other where one is. No Write begins between the look at the
// count and the end of the direction: both are under the lock that a
// Write takes to begin.
//
// The error of ending a direction that has ended before is of no use to
// anybody, so Close returns nil.
func (s *stream) Close() error {
	s.s.CancelRead(0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.writing > 0 {
		s.s.CancelWrite(0)
	} else {
		_ = s.s.Close()
	}
	return nil
}

// Done is the end of the context of go-iroh's stream, which is that of
// the direction this side sends in: go-iroh cancels it when the peer
// tells this side to stop sending, which its Close does, when this side
// ends or resets what it sends, and when the connection ends.
func (s *stream) Done() <-chan struct{} {
	return s.s.Context().Done()
}

// conn is a Conn over a connection of go-iroh. release, when it is set,
// frees what was bound for the connection alone: the endpoint of a dial.
type conn struct {
	c       *iroh.Conn
	release func() error
	// again is what a connection that was dialed dials its peer again
	// with, for another protocol and from its own endpoint. nil for one
	// that was accepted.
	again func(ctx context.Context, alpn string) (*iroh.Conn, error)

	close    sync.Once
	closeErr error
}

func (c *conn) RemoteID() wire.ID {
	return idOf(c.c.RemoteID())
}

func (c *conn) OpenStream(ctx context.Context) (Stream, error) {
	s, err := c.c.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return &stream{s: s}, nil
}

func (c *conn) AcceptStream(ctx context.Context) (Stream, error) {
	s, err := c.c.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return &stream{s: s}, nil
}

func (c *conn) Done() <-chan struct{} {
	return c.c.Context().Done()
}

func (c *conn) Close() error {
	c.close.Do(func() {
		c.closeErr = c.c.Close()
		if c.release != nil {
			if err := c.release(); c.closeErr == nil {
				c.closeErr = err
			}
		}
	})
	return c.closeErr
}
