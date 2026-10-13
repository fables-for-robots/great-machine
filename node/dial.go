package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/iroh/mdns"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
	"github.com/tmc/go-iroh/relay"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// idleTimeout is how long a connection lives without hearing from its
	// peer. The two sides of a connection each name a time and the shorter
	// one holds for both, so the side that dials sets it for the side that
	// listens too, which jaccard-store binds with go-iroh's 30 seconds.
	// The side that listens takes no less than 5 seconds from its peer.
	//
	// A peer that vanished without a word is noticed later than this by
	// up to one keep-alive: the first keep-alive that is sent into the
	// silence starts the time again, once. That is at most 8 seconds for
	// the side that dialed and 9 for the side that listens, whose
	// keep-alive is half the idle timeout. Measured with a peer that was
	// killed, twelve times each: 6.0 to 7.9 seconds on the side that
	// dialed, 7.1 to 8.8 on the side that listens.
	idleTimeout = 6 * time.Second
	// keepAlive is how often a connection with nothing to send says that
	// it is alive, so that a quiet connection is not taken for a dead one.
	keepAlive = 2 * time.Second
	// mdnsLookupTimeout is how long a dial waits for an answer on the
	// local link.
	mdnsLookupTimeout = time.Second
	// lookupBudget is how long a dial waits for the lookups together.
	lookupBudget = 10 * time.Second
)

// DialConfig configures the connection to a server.
type DialConfig struct {
	// Key is the identity of this side. The server sees its ID as the
	// RemoteID of the connection.
	Key Key
	// Server is the endpoint ID of the server.
	Server wire.ID
	// Addrs is for tests: dial these addresses, use no relay, look
	// nothing up.
	Addrs []netip.AddrPort
	// Log receives what the dial has to say, at debug level. Nil means
	// slog.Default().
	Log *slog.Logger

	// idleTimeout and keepAlive replace the constants of those names, for
	// the tests of a peer that vanishes.
	idleTimeout time.Duration
	keepAlive   time.Duration
}

// Dial connects to the server for wire.ALPN. ctx bounds the dial alone.
// Close ends the connection and the endpoint that was bound for it.
//
// Without cfg.Addrs the server is looked up by its ID through mDNS, pkarr
// and DNS, and a connection is started to every address any of them
// answers with, the relay among them. The first that completes is the
// connection.
func Dial(ctx context.Context, cfg DialConfig) (Conn, error) {
	if cfg.Key.secret.IsZero() {
		return nil, errors.New("dial: no key")
	}
	if cfg.Server == "" {
		return nil, errors.New("dial: no server ID")
	}
	server, err := key.ParseEndpointID(string(cfg.Server))
	if err != nil {
		return nil, fmt.Errorf("dial: %q is no endpoint ID: %w", cfg.Server, err)
	}
	if server == cfg.Key.secret.Public().EndpointID() {
		return nil, fmt.Errorf("dial: %s is the ID of this side's own key", cfg.Server)
	}
	if cfg.idleTimeout == 0 {
		cfg.idleTimeout = idleTimeout
	}
	if cfg.keepAlive == 0 {
		cfg.keepAlive = keepAlive
	}
	log := logger(cfg.Log)

	// What is started for the lookups lives as long as the connection:
	// the endpoint may look the server up again.
	lookups, stop := context.WithCancel(context.Background())
	var (
		ep    *iroh.Endpoint
		cands []netaddr.TransportAddr
	)
	if len(cfg.Addrs) > 0 {
		ep, cands, err = bindDirect(ctx, cfg)
	} else {
		ep, cands, err = bindAndResolve(ctx, lookups, cfg, server, log)
	}
	if err != nil {
		stop()
		return nil, fmt.Errorf("dial %s: %w", cfg.Server, err)
	}

	log.Debug("connecting", "server", cfg.Server, "candidates", cands)
	c, err := raceConnect(ctx, ep, server, wire.ALPN, cands)
	if err != nil {
		stop()
		ep.Shutdown(context.Background())
		return nil, fmt.Errorf("dial %s: connect: %w", cfg.Server, err)
	}
	log.Debug("connected", "server", cfg.Server, "addr", c.RemoteAddr())
	return &conn{
		c: c,
		release: func() error {
			stop()
			return ep.Shutdown(context.Background())
		},
		// To the addresses that were found for this dial: the peer is
		// where it was, or the connection that is there would have ended.
		again: func(ctx context.Context, alpn string) (*iroh.Conn, error) {
			return raceConnect(ctx, ep, server, alpn, cands)
		},
	}, nil
}

// Opener is a connection of another protocol than great-machine's, which
// is that protocol's to speak: a stream of it is opened for every request.
// It is what jaccard-store's client is made over.
type Opener interface {
	OpenStreamConn(ctx context.Context) (net.Conn, error)
	// Close ends the connection. The endpoint it was dialed from is not
	// its own, and stays.
	Close() error
}

// Again dials the peer of c once more, for the protocol alpn, from the
// endpoint c was dialed from: the peer sees the same endpoint ID on both
// connections. A key has one endpoint at a time, and two endpoints with
// one key evict each other at the relay, so this is how one key speaks
// two protocols to one peer. ctx bounds the dial alone.
//
// The connection it returns lives no longer than c: when c is closed its
// endpoint is shut down, and with it everything that was dialed from it.
// Only a connection that Dial returned has an endpoint to dial from.
func Again(ctx context.Context, c Conn, alpn string) (Opener, error) {
	dialed, ok := c.(*conn)
	if !ok || dialed.again == nil {
		return nil, errors.New("node: only a connection that was dialed here dials its peer again")
	}
	if alpn == "" || alpn == wire.ALPN {
		return nil, fmt.Errorf("node: dialing again for %q: it is for another protocol than great-machine's", alpn)
	}
	other, err := dialed.again(ctx, alpn)
	if err != nil {
		return nil, fmt.Errorf("dialing %s again for %s: %w", c.RemoteID(), alpn, err)
	}
	return other, nil
}

// options returns what every endpoint of a dial is bound with: the key,
// and the times after which a silent peer is given up.
func (cfg DialConfig) options() []iroh.Option {
	return []iroh.Option{
		iroh.WithSecretKey(cfg.Key.secret),
		iroh.WithTransportConfig(&iroh.QUICTransportConfig{
			MaxIdleTimeout:  cfg.idleTimeout,
			KeepAlivePeriod: cfg.keepAlive,
		}),
	}
}

// bindDirect binds an endpoint that reaches the addresses of cfg and
// nothing else: no relay and no lookups.
//
// go-iroh binds one socket for both families unless it is told an address,
// and macOS may give such a socket a port that an IPv4 socket of another
// process holds already; what arrives for that port over IPv4 then goes to
// the other socket, and the endpoint hears no answer. An IPv4 socket is
// given no port that another one has, so that is what is bound when there
// are IPv4 addresses alone to reach.
func bindDirect(ctx context.Context, cfg DialConfig) (*iroh.Endpoint, []netaddr.TransportAddr, error) {
	opts := append(cfg.options(), iroh.WithRelayMode(relay.ModeDisabled()))
	if ipv4Alone(cfg.Addrs) {
		opts = append(opts, iroh.WithBindAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), 0)))
	}
	ep, err := iroh.Bind(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("bind: %w", err)
	}
	cands := make([]netaddr.TransportAddr, len(cfg.Addrs))
	for i, ap := range cfg.Addrs {
		cands[i] = netaddr.IPAddr{Addr: ap}
	}
	return ep, cands, nil
}

// ipv4Alone reports whether there are addresses and every one of them is
// an IPv4 address.
func ipv4Alone(addrs []netip.AddrPort) bool {
	for _, ap := range addrs {
		if !ap.Addr().Is4() {
			return false
		}
	}
	return len(addrs) > 0
}

// bindAndResolve binds an endpoint with the default relays and looks the
// server up through mDNS, pkarr and DNS. The mDNS listener runs until
// lookups is done; it asks on the local link and announces nothing of
// this side.
//
// It returns the union of what the lookups answered. One answer may be
// half of the picture: mDNS knows direct addresses and no relay, and any
// record may list addresses that cannot be reached from here. With all of
// them as candidates the relay stays among them.
func bindAndResolve(ctx, lookups context.Context, cfg DialConfig, server key.EndpointID, log *slog.Logger) (*iroh.Endpoint, []netaddr.TransportAddr, error) {
	pkarr, err := iroh.N0PkarrResolver(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("pkarr resolver: %w", err)
	}
	local := mdns.New(cfg.Key.secret.Public().EndpointID(),
		mdns.WithPassive(true), mdns.WithLookupTimeout(mdnsLookupTimeout), mdns.WithLogger(log))
	go func() {
		if err := local.Start(lookups); err != nil && lookups.Err() == nil {
			log.Debug("mDNS is unavailable; looking the server up through pkarr and DNS only", "error", err)
		}
	}()
	var services iroh.AddressLookupServices
	services.AddResolver(local)
	services.AddResolver(pkarr)
	services.AddResolver(iroh.N0DNSAddressLookup(nil))

	opts := append(cfg.options(), iroh.WithAddressLookup(&services), iroh.WithRelayMode(relay.ModeDefault()))
	ep, err := iroh.Bind(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("bind: %w", err)
	}
	cands, err := lookup(ctx, &services, server, log)
	if err != nil {
		ep.Shutdown(context.Background())
		return nil, nil, err
	}
	return ep, cands, nil
}

// lookup asks every service for the addresses of id and returns the union
// of the answers. It waits until all of them have answered or failed, and
// no longer than lookupBudget: a service that has not answered by then is
// left out, so that one that hangs does not use up the time the dial has.
func lookup(ctx context.Context, services *iroh.AddressLookupServices, id key.EndpointID, log *slog.Logger) ([]netaddr.TransportAddr, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupBudget)
	defer cancel()

	addr := netaddr.NewEndpointAddr(id)
	var lastErr error
	for item, err := range services.Resolve(ctx, id) {
		if err != nil {
			lastErr = err
			continue
		}
		log.Debug("server looked up", "source", item.Provenance(), "addrs", item.Addr().Addrs())
		addr = addr.WithAddrs(item.Addr().Addrs()...)
	}
	if addr.IsEmpty() {
		if lastErr != nil {
			return nil, fmt.Errorf("no address found: %w", lastErr)
		}
		return nil, errors.New("no address found")
	}
	return addr.Addrs(), nil
}

// raceConnect starts a connection for the protocol alpn to every
// candidate at once and returns the first that completes. The attempts that lose are canceled, and one
// that completes after the winner is closed.
//
// A record may list addresses that are dead from here, the address of a
// container bridge or of another network. Dialed one after the other they
// use up the time the dial has before a live one is tried; dialed
// together, a live one answers in milliseconds.
func raceConnect(ctx context.Context, ep *iroh.Endpoint, id key.EndpointID, alpn string, cands []netaddr.TransportAddr) (*iroh.Conn, error) {
	if len(cands) == 0 {
		return nil, errors.New("no candidate addresses")
	}
	type result struct {
		conn *iroh.Conn
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	results := make(chan result, len(cands))
	for _, ta := range cands {
		go func() {
			c, err := ep.Connect(ctx, netaddr.NewEndpointAddr(id, ta), alpn)
			if err != nil {
				err = fmt.Errorf("%s: %w", ta, err)
			}
			results <- result{c, err}
		}()
	}
	var errs []error
	for pending := len(cands); pending > 0; pending-- {
		r := <-results
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		cancel()
		go func(late int) {
			for ; late > 0; late-- {
				if r := <-results; r.conn != nil {
					r.conn.Close()
				}
			}
		}(pending - 1)
		return r.conn, nil
	}
	cancel()
	return nil, errors.Join(errs...)
}
