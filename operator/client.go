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

// Client talks to draal on one connection. It is safe for concurrent use.
type Client struct {
	conn  node.Conn
	close sync.Once
}

// New returns a client on a connection to draal. The client owns the
// connection from then on.
func New(conn node.Conn) *Client {
	return &Client{conn: conn}
}

// Close closes the connection, which ends every request under way and
// closes the channels of Watch and Logs. It may be called more than once:
// a second Close does nothing and returns nil.
func (c *Client) Close() error {
	var err error
	c.close.Do(func() { err = c.conn.Close() })
	return err
}

// failed makes the error of a request: what draal answered as it is, the
// error of ctx when that ended the request, and any other with what was
// asked in front.
func failed(ctx context.Context, what string, err error) error {
	var refusal *wire.Error
	if errors.As(err, &refusal) {
		return refusal
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
		return nil, nil, nil, failed(ctx, what, err)
	}
	stop = context.AfterFunc(ctx, func() { s.Close() })
	resp, err = wire.Call(s, req)
	if err != nil {
		stop()
		s.Close()
		return nil, nil, nil, failed(ctx, what, err)
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
// request, or when the connection has ended. Watch returns when the first
// fleet has come, or with what draal refused. A channel that is closed
// while ctx is not done says that draal has gone.
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
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			select {
			case out <- *resp.Fleet:
			case <-ctx.Done():
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
	}()
	return out, nil
}

// Logs returns a channel of the lines of the ring of a zathras, and with
// follow of those that follow. The channel is closed at the end of the
// stream, or when ctx is done, which ends the request.
//
// The protocol answers logs with lines alone, so Logs returns when the
// first line has come, when the stream has ended without one, or with
// what draal refused. With follow and an empty ring that is when the
// first line is written, or when ctx is done. A channel of a followed log
// that is closed while ctx is not done says that draal or the zathras has
// gone.
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
	go func() {
		defer close(out)
		defer s.Close()
		defer stop()
		for {
			if resp.Line != nil {
				select {
				case out <- *resp.Line:
				case <-ctx.Done():
					return
				}
			}
			var err error
			if resp, err = wire.ReadResponse(s); err != nil {
				return
			}
		}
	}()
	return out, nil
}
