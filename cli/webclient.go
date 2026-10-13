package cli

import (
	"context"
	"errors"
	"sync"

	"github.com/fables-for-robots/great-machine/operator"
	"github.com/fables-for-robots/great-machine/web"
	"github.com/fables-for-robots/great-machine/wire"
)

// pageClient is the client of an operator as the web page asks for one.
// It dials draal when it is first asked for something, keeps the one
// connection, and dials again when that was lost: the command that
// serves the page starts, and the page loads, whether draal is there or
// not. What is asked while draal cannot be reached is refused as
// unavailable, with what the dial said.
//
// The key of an operator has one endpoint at a time, so there is one
// connection, and a connection that was lost is closed before draal is
// dialed again.
type pageClient struct {
	// dial dials draal, and returns a client and what is closed when the
	// connection of that client has ended.
	dial func(ctx context.Context) (*operator.Client, <-chan struct{}, error)

	// ctx is done when the client is closed: it ends a dial.
	ctx  context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	closed  bool
	conn    *pageConn      // the connection there is; nil: none
	dialing *pageDial      // the dial under way; nil: none
	dials   sync.WaitGroup // the goroutines that dial
}

// pageConn is a connection to draal.
type pageConn struct {
	client *operator.Client
	gone   <-chan struct{} // closed when the connection has ended
}

// pageDial is one dial of draal, which everybody who asks meanwhile
// waits for: its failure is theirs as its connection is.
type pageDial struct {
	done chan struct{} // closed when the dial has ended
	conn *pageConn     // read after done
	err  error
}

// A pageClient is a web.Client.
var _ web.Client = (*pageClient)(nil)

// newPageClient returns a client that dials with dial. It dials nothing
// before it is asked for something.
func newPageClient(dial func(ctx context.Context) (*operator.Client, <-chan struct{}, error)) *pageClient {
	p := &pageClient{dial: dial}
	p.ctx, p.stop = context.WithCancel(context.Background())
	return p
}

// errEnding is what is asked of a client that is closed.
var errEnding = wire.Errorf(wire.CodeUnavailable, "great-machine web is ending")

// get returns a client that is connected to draal: the one there is, or
// one of a dial it waits for, which it begins when none is under way. It
// gives up with ctx; the dial goes on for the others.
func (p *pageClient) get(ctx context.Context) (*operator.Client, error) {
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, errEnding
		}
		c := p.conn
		if c == nil {
			break
		}
		select {
		case <-c.gone:
		default:
			p.mu.Unlock()
			return c.client, nil
		}
		// The connection was lost. It is closed, which frees its
		// endpoint, before draal is dialed with the same key again.
		p.conn = nil
		p.mu.Unlock()
		c.client.Close()
		p.mu.Lock()
	}
	d := p.dialing
	if d == nil {
		d = &pageDial{done: make(chan struct{})}
		p.dialing = d
		p.dials.Add(1)
		go p.dialOnce(d)
	}
	p.mu.Unlock()
	select {
	case <-d.done:
		if d.err != nil {
			return nil, d.err
		}
		return d.conn.client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// dialOnce dials draal for d, in dialTimeout at the most. The dial ends
// with the client and with nothing else: one request that is given up
// does not end the dial the others wait for.
func (p *pageClient) dialOnce(d *pageDial) {
	defer p.dials.Done()
	ctx, cancel := context.WithTimeout(p.ctx, dialTimeout)
	defer cancel()
	client, gone, err := p.dial(ctx)
	p.mu.Lock()
	p.dialing = nil
	switch {
	case err != nil && p.closed:
		d.err = errEnding
	case err != nil:
		d.err = wire.Errorf(wire.CodeUnavailable, "draal cannot be reached: %v", err)
	case p.closed:
		// Nobody is to use the connection any more.
		d.err = errEnding
	default:
		d.conn = &pageConn{client: client, gone: gone}
		p.conn = d.conn
	}
	p.mu.Unlock()
	if err == nil && d.conn == nil {
		client.Close()
	}
	close(d.done)
}

// Close ends a dial that is under way and closes the connection, which
// ends everything that was asked over it. What is asked afterwards is
// refused. Close may be called more than once.
func (p *pageClient) Close() {
	p.mu.Lock()
	p.closed = true
	c := p.conn
	p.conn = nil
	p.mu.Unlock()
	p.stop()
	if c != nil {
		c.client.Close()
	}
	p.dials.Wait()
}

// failed makes of what the client of an operator returned what the page
// is told. A refusal of draal is passed on as it is, and so is the end
// of who asked. Everything else is a connection that failed: the page is
// told that draal cannot be asked, and asks again.
func failed(ctx context.Context, err error) error {
	var refusal *wire.Error
	if err == nil || errors.As(err, &refusal) || ctx.Err() != nil {
		return err
	}
	return wire.Errorf(wire.CodeUnavailable, "draal could not be asked: %v", err)
}

func (p *pageClient) Watch(ctx context.Context) (<-chan wire.Fleet, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	fleets, err := c.Watch(ctx)
	return fleets, failed(ctx, err)
}

func (p *pageClient) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	list, err := c.Rollouts(ctx, service)
	return list, failed(ctx, err)
}

func (p *pageClient) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	samples, err := c.History(ctx, zathras)
	return samples, failed(ctx, err)
}

func (p *pageClient) Logs(ctx context.Context, zathras wire.ID, follow bool) (<-chan wire.LogLine, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	lines, err := c.Logs(ctx, zathras, follow)
	return lines, failed(ctx, err)
}

// Shell asks for a shell in the container of a zathras. A shell that was
// refused is no session at all: not one that holds a nil pointer.
func (p *pageClient) Shell(ctx context.Context, zathras wire.ID, req wire.ShellRequest) (web.Shell, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	sh, err := c.Shell(ctx, zathras, req)
	if err != nil {
		return nil, failed(ctx, err)
	}
	return sh, nil
}

func (p *pageClient) Me(ctx context.Context) (wire.Identity, error) {
	c, err := p.get(ctx)
	if err != nil {
		return wire.Identity{}, err
	}
	me, err := c.Me(ctx)
	return me, failed(ctx, err)
}

func (p *pageClient) Grants(ctx context.Context) ([]wire.Grant, error) {
	c, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	grants, err := c.Grants(ctx)
	return grants, failed(ctx, err)
}

func (p *pageClient) Grant(ctx context.Context, req wire.GrantRequest) error {
	c, err := p.get(ctx)
	if err != nil {
		return err
	}
	return failed(ctx, c.Grant(ctx, req))
}

func (p *pageClient) Revoke(ctx context.Context, req wire.RevokeRequest) error {
	c, err := p.get(ctx)
	if err != nil {
		return err
	}
	return failed(ctx, c.Revoke(ctx, req))
}

func (p *pageClient) StorePage(ctx context.Context, path string) (wire.StorePage, error) {
	c, err := p.get(ctx)
	if err != nil {
		return wire.StorePage{}, err
	}
	page, err := c.StorePage(ctx, path)
	return page, failed(ctx, err)
}
