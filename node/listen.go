package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
}

// Listen binds an endpoint that accepts connections for wire.ALPN.
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
	if !binding() {
		// ctx was done while the endpoint was bound, and took the
		// announcing with it.
		stop()
		ep.Shutdown(context.Background())
		return nil, fmt.Errorf("listen: %w", context.Cause(ctx))
	}
	return &listener{ep: ep, stop: stop, log: logger(cfg.Log), conns: make(map[*conn]struct{})}, nil
}

// listener is a Listener over an endpoint of go-iroh.
type listener struct {
	ep   *iroh.Endpoint
	stop context.CancelFunc
	log  *slog.Logger

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

// Accept returns the next connection whose handshake has completed. A
// handshake that fails is not reported: Accept goes on to the next, and
// returns an error only when ctx is done or the listener is closed.
func (l *listener) Accept(ctx context.Context) (Conn, error) {
	for {
		ic, err := l.ep.Accept(ctx)
		if err == nil {
			c := &conn{c: ic}
			if !l.track(c) {
				c.Close()
				return nil, errors.New("accept: the listener is closed")
			}
			return c, nil
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
