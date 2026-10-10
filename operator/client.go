// Package operator is the client of draal: what the commands and the
// terminal UI use to ask it. Every method of Client is one request of the
// protocol on a stream of its own. What draal refuses comes back as a
// *wire.Error, which errors.As finds.
package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// ErrClosed is the error of what is asked of a client after its Close, and
// of what its Close ended. errors.Is finds it.
var ErrClosed = errors.New("the client is closed")

// Client talks to draal on one connection. It is safe for concurrent use.
type Client struct {
	conn node.Conn

	// done is closed by Close.
	done  chan struct{}
	close sync.Once

	mu     sync.Mutex
	closed bool // Close was called: no goroutine is begun any more
	// following counts the goroutines of Watch and Logs. It is added to
	// under mu while closed is not set, so that Close waits for all.
	following sync.WaitGroup

	// ending, when a test has set it, is called by a goroutine of Watch
	// or Logs when it has ended all but itself.
	ending func()
}

// New returns a client on a connection to draal. The client owns the
// connection from then on.
func New(conn node.Conn) *Client {
	return &Client{conn: conn, done: make(chan struct{})}
}

// Close ends everything the client started, and closes the connection.
// Every request under way fails. The channel of every Watch and of every
// Logs is closed and their goroutines have ended when Close returns,
// whatever their contexts say and whether or not anybody receives from
// the channels. A Shell's Read, Write, Resize and Wait return with
// ErrClosed.
//
// Close may be called more than once and from several goroutines, while
// requests are under way: each call returns when all that is done. The
// first returns what closing the connection returned, the others nil.
func (c *Client) Close() error {
	var err error
	c.close.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		// This ends a goroutine that waits for somebody to receive from
		// its channel, and closing the connection one that reads its
		// stream: Close of a node.Conn ends every stream of it.
		close(c.done)
		err = c.conn.Close()
		c.following.Wait()
	})
	return err
}

// follow runs f in a goroutine that Close waits for. It returns false,
// and runs nothing, when the client is closed.
func (c *Client) follow(f func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.following.Add(1)
	go func() {
		defer c.following.Done()
		f()
		if c.ending != nil {
			c.ending()
		}
	}()
	return true
}

// failed makes the error of a request: what draal answered as it is,
// ErrClosed when the client is closed, the error of ctx when that ended
// the request, and any other with what was asked in front.
func (c *Client) failed(ctx context.Context, what string, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
	}
	select {
	case <-c.done:
		return fmt.Errorf("operator: %s: %w", what, ErrClosed)
	default:
	}
	if ctx.Err() != nil {
		return fmt.Errorf("operator: %s: %w", what, context.Cause(ctx))
	}
	return fmt.Errorf("operator: %s: %w", what, err)
}

// open opens a stream, writes req and reads the first answer. The stream
// is closed when ctx is done; stop takes that back.
func (c *Client) open(ctx context.Context, what string, req *wire.Request) (s node.Stream, resp *wire.Response, stop func() bool, err error) {
	s, err = c.conn.OpenStream(ctx)
	if err != nil {
		return nil, nil, nil, c.failed(ctx, what, err)
	}
	stop = context.AfterFunc(ctx, func() { s.Close() })
	resp, err = wire.Call(s, req)
	if err != nil {
		stop()
		s.Close()
		return nil, nil, nil, c.failed(ctx, what, err)
	}
	return s, resp, stop, nil
}

// call does a request that has one answer.
func (c *Client) call(ctx context.Context, what string, req *wire.Request) (*wire.Response, error) {
	s, resp, stop, err := c.open(ctx, what, req)
	if err != nil {
		return nil, err
	}
	stop()
	s.Close()
	return resp, nil
}

// unexpected is the error for an answer of another kind than was asked for.
func unexpected(what string) error {
	return fmt.Errorf("operator: %s: draal answered with something else", what)
}

// Store asks for the jaccard-store server and the prefix to push to.
func (c *Client) Store(ctx context.Context) (wire.StoreInfo, error) {
	resp, err := c.call(ctx, "store", &wire.Request{Store: &wire.StoreRequest{}})
	if err != nil {
		return wire.StoreInfo{}, err
	}
	if resp.Store == nil {
		return wire.StoreInfo{}, unexpected("store")
	}
	return *resp.Store, nil
}

// Rollout asks for a rollout, and returns it as draal recorded it.
func (c *Client) Rollout(ctx context.Context, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollout", &wire.Request{Rollout: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollout")
	}
	return *resp.Rollout, nil
}

// Rollback asks for a rollout to the stable generation of a service.
func (c *Client) Rollback(ctx context.Context, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollback", &wire.Request{Rollback: &req})
	if err != nil {
		return wire.RolloutInfo{}, err
	}
	if resp.Rollout == nil {
		return wire.RolloutInfo{}, unexpected("rollback")
	}
	return *resp.Rollout, nil
}

// Rollouts returns the last rollouts of a service, newest first.
func (c *Client) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	resp, err := c.call(ctx, "rollouts", &wire.Request{Rollouts: &wire.RolloutsRequest{Service: service}})
	if err != nil {
		return nil, err
	}
	if resp.Rollouts == nil {
		return nil, unexpected("rollouts")
	}
	return resp.Rollouts.Rollouts, nil
}

// History returns the samples draal has of the zathras, oldest first.
func (c *Client) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	resp, err := c.call(ctx, "history", &wire.Request{History: &wire.HistoryRequest{Zathras: zathras}})
	if err != nil {
		return nil, err
	}
	if resp.History == nil {
		return nil, unexpected("history")
	}
	return resp.History.Samples, nil
}

// Watch returns a channel of the fleet: as it is now, and again whenever
// it changed. The channel is closed when ctx is done, which ends the
// request, when the connection has ended, or when the client is closed.
// Watch returns when the first fleet has come, or with what draal
// refused. A channel that is closed while ctx is not done and the client
// is not closed says that draal has gone.
//
// Nobody has to receive from the channel for it to be closed: the
// goroutine that sends on it ends with ctx and with the client.
//
// Receive from the channel, or end ctx: what draal sends and nobody
// receives stays in the connection, which takes about 768 KiB of it over
// all its streams together, and then no request of this client is
// answered any more, until somebody receives or the stream is ended.
func (c *Client) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	s, resp, stop, err := c.open(ctx, "watch", &wire.Request{Watch: &wire.WatchRequest{}})
	if err != nil {
		return nil, err
	}
	if resp.Fleet == nil {
		stop()
		s.Close()
		return nil, unexpected("watch")
	}
	out := make(chan wire.Fleet)
	started := c.follow(func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			select {
			case out <- *resp.Fleet:
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
			for {
				var err error
				if resp, err = wire.ReadResponse(s); err != nil {
					return
				}
				if resp.Fleet != nil {
					break
				}
			}
		}
	})
	if !started {
		stop()
		s.Close()
		return nil, c.failed(ctx, "watch", ErrClosed)
	}
	return out, nil
}

// Logs returns a channel of the lines of the ring of a zathras, and with
// follow of those that follow. The channel is closed at the end of the
// stream, when ctx is done, which ends the request, or when the client is
// closed; nobody has to receive from it for that.
//
// Receive from the channel, or end ctx: as with Watch, lines that nobody
// receives stay in the connection until it takes no more, and then no
// request of this client is answered any more. A followed log of a
// service that writes much comes there soon.
//
// The protocol answers logs with lines alone, so Logs returns when the
// first line has come, when the stream has ended without one, or with
// what draal refused. With follow and an empty ring that is when the
// first line is written, or when ctx is done. A channel of a followed log
// that is closed while ctx is not done and the client is not closed says
// that draal or the zathras has gone.
func (c *Client) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	s, resp, stop, err := c.open(ctx, "logs", &wire.Request{Logs: &wire.LogsRequest{Zathras: zathras, Follow: follow}})
	if errors.Is(err, io.EOF) && ctx.Err() == nil {
		// The ring is empty.
		out := make(chan wire.LogLine)
		close(out)
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out := make(chan wire.LogLine)
	started := c.follow(func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			if resp.Line != nil {
				select {
				case out <- *resp.Line:
				case <-ctx.Done():
					return
				case <-c.done:
					return
				}
			}
			var err error
			if resp, err = wire.ReadResponse(s); err != nil {
				return
			}
		}
	})
	if !started {
		stop()
		s.Close()
		return nil, c.failed(ctx, "logs", ErrClosed)
	}
	return out, nil
}
