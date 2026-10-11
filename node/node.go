// Package node is where great-machine meets iroh: the keys, the endpoint
// draal listens on, and the dial of a zathras or an operator.
//
// A peer is known by its endpoint ID, the public half of its key, which
// the handshake proves. Every connection that is dialed here is for
// wire.ALPN. Both sides of a connection open streams and accept them: a
// zathras dials draal, and draal opens the streams on which it sends its
// commands.
//
// The endpoint draal listens on can accept other protocols as well, each
// given to a handler (ListenConfig.Handlers): that is how draal answers
// jaccard-store's protocol under its own endpoint ID. Two endpoints with
// one key evict each other at the relay, so a second protocol under the
// same ID is the same endpoint or none.
//
// Listen is over the node package of jaccard-store, which binds the
// endpoint and announces it. The dial is this package's own, modelled on
// that of jaccard-store, because the side that dials has to accept streams
// and to set how soon a peer that vanished is noticed.
//
// The rest of great-machine uses the interfaces Conn, Stream and Listener
// and nothing of iroh, so that it is tested over node/nodetest.
package node

import (
	"context"
	"io"
	"net/netip"

	"github.com/fables-for-robots/great-machine/wire"
)

// Stream is one stream of a connection, in both directions.
//
// One goroutine may read while another writes. Two that write at once
// have to take turns themselves.
type Stream interface {
	io.Reader
	io.Writer
	// CloseWrite ends what this side sends. The peer reads io.EOF after
	// what was written. This side can still read.
	CloseWrite() error
	// Close ends the stream in both directions, and returns at once.
	//
	// When no Write of this side is under way, the end is orderly: what
	// this side wrote before is still delivered, and the peer reads
	// io.EOF after it.
	//
	// When a Write of this side has not returned, because the peer does
	// not read, the stream is broken off instead: that Write fails, what
	// this side wrote and the peer has not read may be lost, and the peer
	// reads an error and not io.EOF. A side that wants its last words
	// read lets its Write return before it calls Close.
	//
	// Either way a Write of the peer fails, at once or when it next
	// waits, a Read of this side fails, every later Write of this side
	// fails, and Done is closed on both sides. Close may be called after
	// CloseWrite and more than once.
	Close() error
	// Done is closed when this side can send no more on the stream: the
	// peer has closed it with Close, this side has ended what it sends
	// with CloseWrite or Close, or the connection has ended. It is how a
	// side that neither reads nor writes at the moment learns that the
	// peer has closed the stream: nothing else tells it.
	Done() <-chan struct{}
}

// Conn is a connection to a peer. Both sides open streams and accept them.
type Conn interface {
	// RemoteID returns the endpoint ID of the peer, as the handshake
	// proved it.
	RemoteID() wire.ID
	// OpenStream opens a stream.
	//
	// THE PEER DOES NOT SEE THE STREAM UNTIL SOMETHING IS WRITTEN TO IT:
	// its AcceptStream returns only when the first bytes, or the end of
	// the stream, have arrived. Whoever opens a stream writes first. Every
	// protocol of great-machine begins with a Request of the side that
	// opened the stream, so this holds; a protocol in which the side that
	// accepts speaks first would wait for ever.
	OpenStream(ctx context.Context) (Stream, error)
	// AcceptStream waits for a stream the peer opens. It fails when the
	// connection has ended.
	AcceptStream(ctx context.Context) (Stream, error)
	// Done is closed when the connection has ended, whoever ended it.
	Done() <-chan struct{}
	// Close ends the connection, and every stream of it on both sides.
	// What was written and has not arrived is lost: a side that wants its
	// last message read waits for the peer to end the stream or the
	// connection. Close has to be called for a connection that was dialed
	// even when Done is closed already, to free its endpoint. It may be
	// called more than once.
	Close() error
}

// Listener accepts the connections of an endpoint.
type Listener interface {
	// ID returns the endpoint ID that peers dial.
	ID() wire.ID
	// LocalAddr is the UDP address the endpoint is bound to.
	LocalAddr() netip.AddrPort
	// Accept waits for a connection whose handshake has completed.
	Accept(ctx context.Context) (Conn, error)
	// Close ends Accept and the endpoint, and with it every connection
	// that was accepted.
	Close() error
}
