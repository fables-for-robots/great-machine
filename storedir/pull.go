package storedir

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/amber-store/core/key"
	"github.com/amber-store/core/packstore"
	"github.com/amber-store/jaccard-store/client"

	"github.com/fables-for-robots/great-machine/node"
)

// conn is a connection to the store: jaccard-store's client, over a
// connection that was dialed for it.
type conn interface {
	// Pull imports the objects of the reference name into objects, and
	// downloads for it what objects lacks: nothing, the pack of the
	// reference, or that pack and its parent.
	Pull(ctx context.Context, objects *packstore.Store, name string, opts client.PullOptions) (client.PullResult, error)
	Close() error
}

// dialed is jaccard-store's client with the connection it was made over.
type dialed struct {
	*client.Client
	c node.Opener
}

func (d *dialed) Close() error { return d.c.Close() }

// dialer returns what makes a connection to the store of what cfg dials
// it with: jaccard-store's client over it.
func dialer(cfg Config) func(context.Context) (conn, error) {
	return func(ctx context.Context) (conn, error) {
		o, err := cfg.Dial(ctx)
		if err != nil {
			return nil, err
		}
		if o == nil {
			return nil, errors.New("no connection and no error")
		}
		return &dialed{Client: client.New(o, nil), c: o}, nil
	}
}

// unwritable is the refusal of a reference whose root cannot be written
// out, which a pull is ended with before anything is downloaded.
type unwritable struct{ err error }

func (u *unwritable) Error() string { return u.err.Error() }

// pull makes the pack cache hold the reference of the store path of name,
// and returns what the pull did, with the root of the reference.
//
// A reference the store does not have is a *NotFoundError, and one whose
// root cannot be written out an error that says so: neither is tried
// again. Any other failure is: after RetryWait, then after twice as long
// each time up to RetryMax, until PullTimeout has passed since the first
// failure. It ends with ctx.
func (s *Store) pull(ctx context.Context, name string) (client.PullResult, error) {
	dest := filepath.Join(s.dir, name)
	var failedAt time.Time // of the first failure
	wait := s.cfg.RetryWait
	for attempt := 1; ; attempt++ {
		res, err := s.attempt(ctx, name)
		if err == nil {
			return res, nil
		}
		var refused *unwritable
		switch {
		case errors.Is(err, client.ErrNotFound):
			return client.PullResult{}, &NotFoundError{Path: dest, Service: s.cfg.Service}
		case errors.As(err, &refused):
			return client.PullResult{}, fmt.Errorf("%s cannot be written out: %w", dest, refused.err)
		case ctx.Err() != nil:
			return client.PullResult{}, ctx.Err()
		}
		now := time.Now()
		if failedAt.IsZero() {
			failedAt = now
		}
		// An attempt that would begin after the time is up is not waited
		// for.
		if now.Add(wait).Sub(failedAt) >= s.cfg.PullTimeout {
			s.log.Error("a store path could not be fetched, and is given up", "path", dest, "attempts", attempt, "error", err)
			return client.PullResult{}, fmt.Errorf("%s could not be fetched from the store: %w", dest, err)
		}
		s.log.Warn("a store path could not be fetched, and will be tried again", "path", dest, "attempt", attempt, "in", wait, "error", err)
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return client.PullResult{}, ctx.Err()
		}
		wait = min(2*wait, s.cfg.RetryMax)
	}
}

// attempt pulls the reference of name once, in AttemptTimeout at the
// most. A failure costs the connection it happened on, unless it is an
// answer of the store, that it has no such reference or one that cannot
// be written out, or the end of ctx: an attempt that took too long has
// failed like any other.
func (s *Store) attempt(ctx context.Context, name string) (client.PullResult, error) {
	c, gen, err := s.connect(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return client.PullResult{}, ctx.Err()
		}
		return client.PullResult{}, fmt.Errorf("dialing the store: %w", err)
	}
	limited, cancel := context.WithTimeout(ctx, s.cfg.AttemptTimeout)
	defer cancel()
	res, err := c.Pull(limited, s.objects, s.prefix+name, client.PullOptions{
		// Asked before anything is downloaded.
		Accept: func(root key.Key) error {
			if err := accept(root); err != nil {
				return &unwritable{err}
			}
			return nil
		},
	})
	var refused *unwritable
	if err != nil && !errors.Is(err, client.ErrNotFound) && !errors.As(err, &refused) && ctx.Err() == nil {
		s.drop(gen)
	}
	return res, err
}

// dial is one dial of the store, which everybody who needs the connection
// meanwhile waits for: its failure is theirs as its connection is.
type dial struct {
	done chan struct{} // closed when the dial has ended
	conn conn          // read after done
	gen  uint64
	err  error
}

// connect returns the connection to the store, and which of the
// connections that were dialed it is. When there is none it waits for the
// dial that is under way, which it begins if none is, and gives up with
// ctx; the dial goes on for the others.
func (s *Store) connect(ctx context.Context) (conn, uint64, error) {
	s.mu.Lock()
	if s.conn != nil {
		c, gen := s.conn, s.connGen
		s.mu.Unlock()
		return c, gen, nil
	}
	d := s.dialing
	if d == nil {
		d = &dial{done: make(chan struct{})}
		s.dialing = d
		// Counted with the fetches, of which the caller is one: Close
		// waits for it.
		s.running.Add(1)
		go s.dialOnce(d)
	}
	s.mu.Unlock()
	select {
	case <-d.done:
		return d.conn, d.gen, d.err
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}
}

// dialOnce dials the store for d, in DialTimeout at the most. The dial
// ends with the store and with nothing else: one fetch that is ended does
// not end the dial the others wait for.
func (s *Store) dialOnce(d *dial) {
	defer s.running.Done()
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.DialTimeout)
	defer cancel()
	c, err := s.dial(ctx)
	if err == nil && c == nil {
		err = errors.New("no connection and no error")
	}
	s.mu.Lock()
	s.dialing = nil
	// Every fetch that waited has given up, or the store is closed:
	// nobody is left to use the connection, or to close it.
	unwanted := err == nil && (len(s.fetches) == 0 || s.closed)
	if err == nil && !unwanted {
		s.connGen++
		s.conn, d.conn, d.gen = c, c, s.connGen
	}
	s.mu.Unlock()
	if unwanted {
		c.Close()
		err = errClosed
	}
	d.err = err
	close(d.done)
}

// drop closes the connection a pull failed on, unless it is gone already:
// of the pulls that fail on one connection the first drops it, and none
// of them closes the one that was dialed since.
func (s *Store) drop(gen uint64) {
	s.mu.Lock()
	var c conn
	if s.conn != nil && s.connGen == gen {
		c, s.conn = s.conn, nil
	}
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}
