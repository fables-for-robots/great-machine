// Package nodetest is a network in memory with the Conn, Stream and
// Listener of package node, for the tests of what speaks the protocol. It
// needs no iroh, no socket and no time.
//
// It behaves as the real thing does wherever a protocol could tell the
// difference:
//
//   - Both sides of a connection open streams and accept them.
//   - A stream is not seen by the peer's AcceptStream until its opener has
//     written to it or ended it.
//   - A Write returns before the peer has read, until 512 KiB wait to be
//     read; then it waits, as a stream of QUIC does.
//   - CloseWrite ends one direction: the peer reads io.EOF after what was
//     written, and can still write.
//   - Close of a stream delivers what was written and then io.EOF, fails
//     the peer's Write, and fails this side's Read. When a Write of this
//     side has not returned, Close breaks the stream off instead: that
//     Write fails, what the peer has not read is dropped, and the peer
//     reads an error.
//   - Close of a connection closes Done on both sides and fails every
//     call that waits on it or on one of its streams.
package nodetest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// window is how many bytes of a stream may wait to be read before a Write
// waits: the window a stream of go-iroh begins with.
const window = 512 << 10

// backlog is how many connections may wait for Accept before Dial waits.
const backlog = 64

var (
	errConnEnded     = errors.New("nodetest: the connection has ended")
	errStreamClosed  = errors.New("nodetest: the stream was closed on this side")
	errPeerClosed    = errors.New("nodetest: the peer closed the stream")
	errPeerReset     = errors.New("nodetest: the peer broke the stream off")
	errListenerEnded = errors.New("nodetest: the listener is closed")
)

// Net is a network in memory. Its streams are buffered: a Write of a
// frame returns before the peer has read it.
type Net struct {
	mu        sync.Mutex
	listeners map[wire.ID]*listener
}

// New returns a network on which nobody listens.
func New() *Net {
	return &Net{listeners: make(map[wire.ID]*listener)}
}

// ID returns an endpoint ID for tests: 64 hexadecimal characters made of n.
func ID(n int) wire.ID {
	return wire.ID(fmt.Sprintf("%064x", n))
}

// Listen makes id reachable. A second Listen for an id that listens fails
// the test through panic. An id whose listener was closed may listen again.
func (n *Net) Listen(id wire.ID) node.Listener {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.listeners[id]; ok {
		panic(fmt.Sprintf("nodetest: %s listens already", id))
	}
	l := &listener{
		net:      n,
		id:       id,
		incoming: make(chan *conn, backlog),
		done:     make(chan struct{}),
		conns:    make(map[*link]struct{}),
	}
	n.listeners[id] = l
	return l
}

// Dial connects from to to, and fails when nobody listens as to. It
// returns when the connection is made, before the listener's Accept has
// returned it.
func (n *Net) Dial(ctx context.Context, from, to wire.ID) (node.Conn, error) {
	n.mu.Lock()
	l := n.listeners[to]
	n.mu.Unlock()
	if l == nil {
		return nil, fmt.Errorf("nodetest: dial %s: nobody listens as that", to)
	}
	lk := &link{done: make(chan struct{})}
	client := &conn{link: lk, remote: to, accept: newQueue()}
	server := &conn{link: lk, remote: from, accept: newQueue()}
	client.peer, server.peer = server, client
	if !l.track(lk) {
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, errListenerEnded)
	}
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.done:
		lk.end()
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, errListenerEnded)
	case <-ctx.Done():
		lk.end()
		return nil, fmt.Errorf("nodetest: dial %s: %w", to, ctx.Err())
	}
}

// listener is the Listener of one ID.
type listener struct {
	net      *Net
	id       wire.ID
	incoming chan *conn
	done     chan struct{}

	mu     sync.Mutex
	closed bool
	conns  map[*link]struct{}
}

func (l *listener) ID() wire.ID { return l.id }

// LocalAddr returns the zero value: a listener in memory has no address.
func (l *listener) LocalAddr() netip.AddrPort { return netip.AddrPort{} }

func (l *listener) Accept(ctx context.Context) (node.Conn, error) {
	select {
	case <-l.done:
		return nil, errListenerEnded
	default:
	}
	select {
	case c := <-l.incoming:
		return c, nil
	case <-l.done:
		return nil, errListenerEnded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// track notes a connection to the listener, to end it when the listener
// is closed. It returns false when the listener is closed already.
func (l *listener) track(lk *link) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[lk] = struct{}{}
	return true
}

// Close ends Accept, frees the ID and ends every connection to the
// listener, as closing the endpoint of the real thing does.
func (l *listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()

	close(l.done)
	l.net.mu.Lock()
	delete(l.net.listeners, l.id)
	l.net.mu.Unlock()
	for lk := range conns {
		lk.end()
	}
	return nil
}

// link is what the two sides of a connection share: its end, and the
// streams to fail when it comes.
type link struct {
	mu      sync.Mutex
	ended   bool
	done    chan struct{}
	streams []*pipe
}

// add notes the two directions of a stream. It returns false when the
// connection has ended.
func (lk *link) add(a, b *pipe) bool {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	if lk.ended {
		return false
	}
	lk.streams = append(lk.streams, a, b)
	return true
}

// end ends the connection for both sides.
func (lk *link) end() {
	lk.mu.Lock()
	if lk.ended {
		lk.mu.Unlock()
		return
	}
	lk.ended = true
	streams := lk.streams
	lk.streams = nil
	lk.mu.Unlock()

	close(lk.done)
	for _, p := range streams {
		p.fail()
	}
}

// conn is one side of a connection.
type conn struct {
	link   *link
	remote wire.ID
	peer   *conn
	// accept holds the streams the peer opened and wrote to.
	accept *queue
}

func (c *conn) RemoteID() wire.ID { return c.remote }

// OpenStream opens a stream. As with the real thing, the peer's
// AcceptStream does not return it until this side has written to it or
// ended it.
func (c *conn) OpenStream(ctx context.Context) (node.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	there, back := newPipe(), newPipe()
	if !c.link.add(there, back) {
		return nil, errConnEnded
	}
	mine := &stream{in: back, out: there}
	theirs := &stream{in: there, out: back}
	mine.announce = func() { c.peer.accept.push(theirs) }
	return mine, nil
}

func (c *conn) AcceptStream(ctx context.Context) (node.Stream, error) {
	return c.accept.pop(ctx, c.link.done)
}

func (c *conn) Done() <-chan struct{} { return c.link.done }

func (c *conn) Close() error {
	c.link.end()
	return nil
}

// queue is the streams that wait for AcceptStream, without a limit.
type queue struct {
	mu    sync.Mutex
	items []*stream
	ready chan struct{} // has room for one value: there may be items
}

func newQueue() *queue {
	return &queue{ready: make(chan struct{}, 1)}
}

func (q *queue) push(s *stream) {
	q.mu.Lock()
	q.items = append(q.items, s)
	q.mu.Unlock()
	q.wake()
}

func (q *queue) wake() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// pop returns the oldest stream, and fails when done is closed, even if
// streams wait, or when ctx is done.
func (q *queue) pop(ctx context.Context, done <-chan struct{}) (*stream, error) {
	for {
		select {
		case <-done:
			return nil, errConnEnded
		default:
		}
		q.mu.Lock()
		if len(q.items) > 0 {
			s := q.items[0]
			q.items = q.items[1:]
			if len(q.items) > 0 {
				q.wake()
			}
			q.mu.Unlock()
			return s, nil
		}
		q.mu.Unlock()
		select {
		case <-q.ready:
		case <-done:
			return nil, errConnEnded
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// stream is one side of a stream: it reads one pipe and writes the other.
type stream struct {
	in, out *pipe
	// announce shows the stream to the peer's AcceptStream. It is nil on
	// the side that accepted the stream.
	announce func()
	once     sync.Once
}

// show makes the peer see the stream, the first time it is called.
func (s *stream) show() {
	if s.announce != nil {
		s.once.Do(s.announce)
	}
}

func (s *stream) Read(p []byte) (int, error) { return s.in.read(p) }

func (s *stream) Write(p []byte) (int, error) {
	s.show()
	return s.out.write(p)
}

func (s *stream) CloseWrite() error {
	s.show()
	s.out.closeWrite()
	return nil
}

func (s *stream) Close() error {
	s.show()
	s.out.closeOrReset()
	s.in.closeRead()
	return nil
}

// pipe is one direction of a stream: bytes that were written and wait to
// be read.
type pipe struct {
	mu      sync.Mutex
	changed *sync.Cond
	buf     []byte
	writing int  // the writes that have not returned
	eof     bool // the writer ended the direction
	reset   bool // the writer broke the direction off
	gone    bool // the reader closed the stream
	failed  bool // the connection has ended
}

func newPipe() *pipe {
	p := &pipe{}
	p.changed = sync.NewCond(&p.mu)
	return p
}

func (p *pipe) write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writing++
	defer func() { p.writing-- }()
	written := 0
	for {
		switch {
		case p.failed:
			return written, errConnEnded
		case p.eof, p.reset:
			return written, errStreamClosed
		case p.gone:
			return written, errPeerClosed
		}
		if len(b) == 0 {
			return written, nil
		}
		if room := window - len(p.buf); room > 0 {
			n := min(room, len(b))
			p.buf = append(p.buf, b[:n]...)
			b = b[n:]
			written += n
			p.changed.Broadcast()
			continue
		}
		p.changed.Wait()
	}
}

func (p *pipe) read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		switch {
		case p.failed:
			return 0, errConnEnded
		case p.gone:
			return 0, errStreamClosed
		case p.reset:
			return 0, errPeerReset
		case len(p.buf) > 0:
			n := copy(b, p.buf)
			p.buf = p.buf[n:]
			p.changed.Broadcast()
			return n, nil
		case p.eof:
			return 0, io.EOF
		}
		if len(b) == 0 {
			return 0, nil
		}
		p.changed.Wait()
	}
}

// closeWrite ends the direction from the side that writes: the reader
// gets what was written and then io.EOF.
func (p *pipe) closeWrite() {
	p.mu.Lock()
	p.eof = true
	p.mu.Unlock()
	p.changed.Broadcast()
}

// closeOrReset ends the direction from the side that writes, for Close of
// a stream: as closeWrite does when no write is under way, and otherwise
// by breaking it off, which fails the write that waits, drops what was
// not read and fails the reader. A direction that has ended stays as it
// ended.
func (p *pipe) closeOrReset() {
	p.mu.Lock()
	switch {
	case p.eof || p.reset:
	case p.writing > 0:
		p.reset = true
		p.buf = nil
	default:
		p.eof = true
	}
	p.mu.Unlock()
	p.changed.Broadcast()
}

// closeRead ends the direction from the side that reads: what waits is
// dropped, and a Write fails.
func (p *pipe) closeRead() {
	p.mu.Lock()
	p.gone = true
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}

// fail ends the direction because the connection has ended.
func (p *pipe) fail() {
	p.mu.Lock()
	p.failed = true
	p.buf = nil
	p.mu.Unlock()
	p.changed.Broadcast()
}
