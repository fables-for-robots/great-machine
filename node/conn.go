package node

import (
	"context"
	"sync"

	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// stream is a Stream over a stream of go-iroh.
type stream struct {
	s *iroh.Stream
}

func (s stream) Read(p []byte) (int, error)  { return s.s.Read(p) }
func (s stream) Write(p []byte) (int, error) { return s.s.Write(p) }

// CloseWrite sends the end of the stream after what was written.
func (s stream) CloseWrite() error {
	return s.s.CloseWrite()
}

// Close sends the end of the stream after what was written, which the
// peer reads as io.EOF, and tells the peer to stop sending, which fails
// its Write. Close of go-iroh's stream alone ends one direction only.
//
// The error of ending a direction that has ended before is of no use to
// anybody, so Close returns nil.
func (s stream) Close() error {
	s.s.CancelRead(0)
	_ = s.s.Close()
	return nil
}

// conn is a Conn over a connection of go-iroh. release, when it is set,
// frees what was bound for the connection alone: the endpoint of a dial.
type conn struct {
	c       *iroh.Conn
	release func() error

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
	return stream{s}, nil
}

func (c *conn) AcceptStream(ctx context.Context) (Stream, error) {
	s, err := c.c.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	return stream{s}, nil
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
