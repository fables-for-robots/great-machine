package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"sync"

	jnode "github.com/amber-store/jaccard-store/node"
	"github.com/tmc/go-iroh/iroh"

	"github.com/fables-for-robots/great-machine/wire"
)

// ListenConfig configures the endpoint that accepts connections.
type ListenConfig struct {
	// Key is the identity of the endpoint. Its ID is what peers dial.
	Key Key
	// Bind is the UDP address to bind. The zero value: every address, a
	// port the system picks.
	Bind netip.AddrPort
	// Local is for tests: no relay, nothing announced, bound to 127.0.0.1
	// unless Bind says otherwise.
	Local bool
	// Log receives what binding and announcing have to say. Nil means
	// slog.Default().
	Log *slog.Logger
	// Handlers are for the protocols the endpoint accepts besides
	// great-machine's own: by its ALPN, what is given every connection
	// that is accepted for it. A handler is called in a goroutine of its
	// own and returns when it is done with the connection, which is then
	// closed: it need not close it. Close of the listener closes the
	// connections of handlers too, and does not wait for the handlers:
	// a handler returns when its connection is done, may still be
	// running when Close has returned, and can be started on a
	// connection that Close has closed already.
	//
	// Such a connection is handed over by Accept, which goes on to the
	// next: it is taken only while Accept is called.
	Handlers map[string]func(Conn)
}

// Listen binds an endpoint that accepts connections for wire.ALPN, and
// for the ALPN of every handler of cfg.
//
// Unless cfg.Local, the endpoint uses the default relays and is announced
// through pkarr and mDNS, as jaccard-store announces its server, so that
// its ID is all a peer needs. Listen waits for the relay, 15 seconds at
// most. ctx bounds Listen alone: the endpoint and its announcing live
// until the Listener is closed.
func Listen(ctx context.Context, cfg ListenConfig) (Listener, error) {
	if cfg.Key.secret.IsZero() {
		return nil, errors.New("listen: no key")
	}
	alpns := []string{wire.ALPN}
	for alpn, handler := range cfg.Handlers {
		switch {
		case alpn == "":
			return nil, errors.New("listen: a handler for no ALPN")
		case alpn == wire.ALPN:
			return nil, fmt.Errorf("listen: a handler for %s, whose connections are Accept's", alpn)
		case handler == nil:
			return nil, fmt.Errorf("listen: no handler for %s", alpn)
		}
		alpns = append(alpns, alpn)
	}
	// The announcing of jaccard-store runs until its context is done. It
	// gets one that ends with the listener, and with ctx only while
	// Listen runs.
	run, stop := context.WithCancel(context.WithoutCancel(ctx))
	binding := context.AfterFunc(ctx, stop)
	ep, err := jnode.Bind(run, jnode.ServerConfig{
		Key:   cfg.Key.secret,
		ALPN:  wire.ALPN,
		Bind:  cfg.Bind,
		Local: cfg.Local,
		Log:   cfg.Log,
	})
	if err != nil {
		stop()
		return nil, fmt.Errorf("listen: %w", err)
	}
	// jaccard-store binds for one ALPN. The others are added here, which
	// go-iroh lets be done until the first Accept.
	if len(alpns) > 1 {
		if err := ep.SetALPNs(alpns); err != nil {
			stop()
			ep.Shutdown(context.Background())
			return nil, fmt.Errorf("listen: accepting %v: %w", alpns, err)
		}
	}
	if !binding() {
		// ctx was done while the endpoint was bound, and took the
		// announcing with it.
		stop()
		ep.Shutdown(context.Background())
		return nil, fmt.Errorf("listen: %w", context.Cause(ctx))
	}
	return &listener{ep: ep, stop: stop, log: logger(cfg.Log), handlers: maps.Clone(cfg.Handlers), turn: make(chan struct{}, 1), conns: make(map[*conn]struct{})}, nil
}

// listener is a Listener over an endpoint of go-iroh.
type listener struct {
	ep   *iroh.Endpoint
	stop context.CancelFunc
	log  *slog.Logger
	// handlers are given the connections of other protocols, by ALPN.
	handlers map[string]func(Conn)
	// turn is a lock with a slot for one: go-iroh lets one call of Accept
	// wait at a time and fails the others at once.
	turn chan struct{}

	mu     sync.Mutex
	closed bool
	// conns are the connections that were accepted and have not ended.
	// Shutting the endpoint down says nothing to their peers, which would
	// take the idle timeout to notice, so Close closes each of them first.
	conns map[*conn]struct{}

	close    sync.Once
	closeErr error
}

func (l *listener) ID() wire.ID {
	return idOf(l.ep.ID())
}

func (l *listener) LocalAddr() netip.AddrPort {
	return l.ep.LocalAddr()
}

// Accept returns the next connection for wire.ALPN whose handshake has
// completed. A handshake that fails is not reported: Accept goes on to the
// next, and returns an error only when ctx is done or the listener is
// closed. A connection for the ALPN of a handler is given to the handler,
// and Accept goes on to the next.
//
// Calls may be concurrent: they take turns, and one that waits for its
// turn gives up with its ctx or when the listener is closed.
func (l *listener) Accept(ctx context.Context) (Conn, error) {
	select {
	case l.turn <- struct{}{}:
		defer func() { <-l.turn }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.ep.Closed():
		return nil, errors.New("accept: the listener is closed")
	}
	for {
		ic, err := l.ep.Accept(ctx)
		if err == nil {
			c := &conn{c: ic}
			if !l.track(c) {
				c.Close()
				return nil, errors.New("accept: the listener is closed")
			}
			alpn := ic.ALPN()
			if alpn == wire.ALPN {
				return c, nil
			}
			handler, ok := l.handlers[alpn]
			if !ok {
				// The endpoint accepts no ALPN but these, so this is
				// not reached; a connection nobody answers is closed.
				l.log.Warn("a connection for a protocol nobody answers: closing it", "alpn", alpn, "peer", c.RemoteID())
				c.Close()
				continue
			}
			go func() {
				defer c.Close()
				handler(c)
			}()
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		select {
		case <-l.ep.Closed():
			return nil, fmt.Errorf("accept: the listener is closed: %w", err)
		default:
		}
		l.log.Debug("a connection was not accepted", "error", err)
	}
}

// track notes c until it has ended. It returns false when the listener is
// closed already.
func (l *listener) track(c *conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[c] = struct{}{}
	go func() {
		<-c.Done()
		l.mu.Lock()
		delete(l.conns, c)
		l.mu.Unlock()
	}()
	return true
}

func (l *listener) Close() error {
	l.close.Do(func() {
		l.mu.Lock()
		l.closed = true
		conns := l.conns
		l.conns = nil
		l.mu.Unlock()
		for c := range conns {
			c.Close()
		}
		l.stop()
		l.closeErr = l.ep.Shutdown(context.Background())
	})
	return l.closeErr
}

// logger returns l, or the default logger when l is nil.
func logger(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	return l
}
