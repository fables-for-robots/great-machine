package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/fables-for-robots/great-machine/wire"
)

const (
	// readLimit is the most bytes one message of a page may have: plenty
	// for what is typed, pasted in pieces, and for a change of size.
	readLimit = 64 << 10
	// closeWait is the most time the close of a socket takes: for the
	// close frame to be written and the page to answer it. A page that
	// does not, in that time, has its connection cut.
	closeWait = time.Second
)

// socket is a WebSocket of a page, with the two goroutines that every one
// has: one that reads what the page sends, and one that pings while
// nothing is written.
//
// The handler that opened it is its one writer of messages.
type socket struct {
	h    *handler
	r    *http.Request
	conn *websocket.Conn
	raw  net.Conn // the connection under conn, to cut it

	// ctx is done when the page has closed the socket, when the socket has
	// failed, or when the server ends. Whatever is done for the page is
	// done under it.
	ctx    context.Context
	cancel context.CancelFunc

	wrote atomic.Int64 // when the last message was written, in nanoseconds of Unix time
	// busy is set while the reader hands a message on, which for a shell
	// may be a wait of WriteWait for room; read is when it last began to
	// read again, in nanoseconds of Unix time. While it is busy it reads
	// nothing, no pong either.
	busy atomic.Bool
	read atomic.Int64

	stopRead context.CancelFunc
	running  sync.WaitGroup
	shutting sync.Once
}

// taken is a ResponseWriter that keeps the connection the WebSocket
// library takes over from the server, which the library does not hand
// out: the socket needs it to end without waiting for the page.
type taken struct {
	http.ResponseWriter
	conn net.Conn
}

// Hijack takes the connection over, as the library asks, without the
// deadline its answer had as an answer.
func (t *taken) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(t.ResponseWriter).Hijack()
	if err == nil {
		conn.SetDeadline(time.Time{})
		t.conn = conn
	}
	return conn, rw, err
}

// sameOrigin says whether a request may open a socket: it has no Origin,
// as a program that is no browser, or its Origin has the host the request
// was sent to.
//
// A browser sends the Origin of the page with every WebSocket, and lets
// every page open one to every host: the same-origin policy does not
// hold for them. Without this, any page the operator has open could ask
// 127.0.0.1 for a shell.
//
// The Host is not looked at beyond this: a page under any name is
// draal's own when it was loaded from the host it asks. So this does not
// keep out a site that points a name of its own at draal's address (DNS
// rebinding), which is known and was decided so (the spec's section 4).
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// open makes a socket of a request, or answers the request with why not
// and returns nil. onMessage is called, by the one goroutine that reads,
// with every message the page sends; nil drops them. It may wait, for no
// longer than WriteWait and for nothing that the end of the socket's
// handler does not end: while it runs, the close of the page and its
// pongs are not seen.
//
// Who has opened a socket calls its done.
func (h *handler) open(w http.ResponseWriter, r *http.Request, onMessage func(websocket.MessageType, []byte)) *socket {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		h.refuse(w, r, wire.Errorf(wire.CodeBadRequest, "%s is a WebSocket", r.URL.Path))
		return nil
	}
	if !sameOrigin(r) {
		h.log.Warn("a WebSocket of another origin was refused", "origin", r.Header.Get("Origin"), "host", r.Host, "path", r.URL.Path)
		h.refuse(w, r, wire.Errorf(wire.CodeUnauthorized,
			"a WebSocket of %s is opened by its own page alone, not by one of %s", r.Host, r.Header.Get("Origin")))
		return nil
	}
	// A socket is counted from here to the end of its done.
	if open := h.sockets.Add(1); open > int64(h.cfg.MaxSockets) {
		h.sockets.Add(-1)
		h.log.Warn("a WebSocket more than may be open was refused", "open", open-1, "path", r.URL.Path)
		h.refuse(w, r, wire.Errorf(wire.CodeUnavailable, "%d WebSockets are open, and no more may be: close a page", open-1))
		return nil
	}
	// Accept checks the Origin as sameOrigin does. No compression: what
	// is sent is small or is the output of a shell, and a context of
	// compression for every socket is memory a flood should not get.
	under := &taken{ResponseWriter: w}
	conn, err := websocket.Accept(under, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		h.sockets.Add(-1)
		h.log.Debug("a WebSocket was not opened", "path", r.URL.Path, "error", err)
		return nil // Accept has answered
	}
	conn.SetReadLimit(readLimit)

	s := &socket{h: h, r: r, conn: conn, raw: under.conn}
	s.ctx, s.cancel = context.WithCancel(r.Context())
	s.wrote.Store(time.Now().UnixNano())
	// The reader has a context of its own: the library cuts a socket
	// whose Read is cancelled, and one that ends because the server ends
	// is to be told so first.
	readCtx, stopRead := context.WithCancel(context.Background())
	s.stopRead = stopRead
	s.running.Go(func() { s.reader(readCtx, onMessage) })
	s.running.Go(s.ping)
	return s
}

// read reads what the page sends until the socket ends, which is also
// how the close and the pongs of the page are seen, and then ends
// everything that is done for the page.
func (s *socket) reader(ctx context.Context, onMessage func(websocket.MessageType, []byte)) {
	defer s.cancel()
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return
		}
		if onMessage != nil {
			s.busy.Store(true)
			onMessage(typ, data)
			s.read.Store(time.Now().UnixNano())
			s.busy.Store(false)
		}
	}
}

// ping pings the page whenever nothing was written for PingEvery, and
// ends the socket of a page that does not answer within WriteWait. A
// pong that was not read because the reader waited for a shell to take
// its input is not held against the page: that wait is bounded by
// WriteWait too, and the ping after it finds a page that has gone.
func (s *socket) ping() {
	every := s.h.cfg.PingEvery
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(time.Unix(0, s.wrote.Load())) < every {
			continue
		}
		pinged := time.Now()
		ctx, cancel := context.WithTimeout(s.ctx, s.h.cfg.WriteWait)
		err := s.conn.Ping(ctx)
		cancel()
		switch {
		case err == nil:
		case s.ctx.Err() != nil:
			return
		case errors.Is(err, context.DeadlineExceeded) && (s.busy.Load() || s.read.Load() > pinged.UnixNano()):
			// The reader did not read for some of the time the pong had.
			// If it was the ping that could not be written, the library
			// has cut the socket, and the next ping says so.
		default:
			s.h.log.Debug("a page did not answer a ping", "path", s.r.URL.Path, "error", err)
			s.cancel()
			return
		}
	}
}

// write sends one message. A page that does not take it within WriteWait
// loses its socket: the library closes a socket whose write ran out of
// time, as half a message may be on its way.
//
// For the same reason the library cuts the connection of a write whose
// context has ended, at once when it has ended already. So nothing is
// written when the socket's context is done: a handler that picked a
// line or a piece that was ready over the end of its context is told
// that the socket has ended, and goes to done, which says why with the
// close. (A write that is under way when the context ends is cut, as
// before.)
func (s *socket) write(typ websocket.MessageType, data []byte) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.h.cfg.WriteWait)
	defer cancel()
	if err := s.conn.Write(ctx, typ, data); err != nil {
		if s.ctx.Err() == nil {
			s.h.log.Debug("a page did not take a message", "path", s.r.URL.Path, "error", err)
		}
		s.cancel()
		return err
	}
	s.wrote.Store(time.Now().UnixNano())
	return nil
}

// writeJSON sends v as a text message.
func (s *socket) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		s.h.log.Error("encoding a message", "path", s.r.URL.Path, "error", err)
		s.cancel()
		return err
	}
	return s.write(websocket.MessageText, data)
}

// shut closes the socket with a code and a reason, once, and returns
// within closeWait, or within WriteWait if that is shorter: the close
// frame is written, the page has the rest of the time to answer it, and
// then the connection is cut. A page that is there has answered long
// before; one that is not is not waited for, as the library would for
// ten seconds.
//
// The wait for the answer is what gets the code to a page that is
// there: a connection that is closed while the page still sends is
// reset, and what was written to it can be lost with that.
func (s *socket) shut(code websocket.StatusCode, reason string) {
	s.shutting.Do(func() {
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			s.conn.Close(code, reason)
		}()
		wait := time.NewTimer(min(closeWait, s.h.cfg.WriteWait))
		defer wait.Stop()
		select {
		case <-closed:
		case <-wait.C:
		}
		// With the connection cut, Close of the library returns at once.
		s.raw.Close()
		<-closed
	})
}

// fail tells the page why the socket ends, as the last message, and
// closes the socket. A page that has gone is told nothing.
func (s *socket) fail(err error) {
	if s.ctx.Err() != nil {
		return
	}
	if s.writeJSON(refusal{Error: s.h.refusalOf(s.r, err)}) == nil {
		s.shut(websocket.StatusNormalClosure, "")
	}
}

// end closes the socket in the normal way: everything was sent.
func (s *socket) end() {
	if s.ctx.Err() == nil {
		s.shut(websocket.StatusNormalClosure, "")
	}
}

// done ends the socket and returns when its goroutines have ended, which
// they have within closeWait whatever the page does. A socket that is
// open still because the server ends is closed as going away, so that
// the page knows to come back.
func (s *socket) done() {
	if s.r.Context().Err() != nil {
		s.shut(websocket.StatusGoingAway, "draal is shutting down")
	}
	s.cancel()
	s.stopRead()
	s.raw.Close()
	s.conn.CloseNow()
	s.running.Wait()
	s.h.sockets.Add(-1)
}

// watch sends the fleet, at once and whenever it changes.
func (h *handler) watch(w http.ResponseWriter, r *http.Request) {
	s := h.open(w, r, nil)
	if s == nil {
		return
	}
	defer s.done()
	fleets, err := h.cfg.Controller.Watch(s.ctx)
	if err != nil {
		s.fail(err)
		return
	}
	// The controller keeps the newest fleet for a watcher that is slow
	// and drops the ones between, so there is no queue here: while a
	// message is written, nothing is taken from the channel.
	for {
		select {
		case fleet, ok := <-fleets:
			if !ok {
				s.fail(wire.Errorf(wire.CodeUnavailable, "draal has stopped sending the fleet"))
				return
			}
			if s.writeJSON(fleet) != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// logs sends the ring of a zathras, and with follow what follows.
func (h *handler) logs(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	follow, err := truth(r, "follow")
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	s := h.open(w, r, nil)
	if s == nil {
		return
	}
	defer s.done()
	lines, err := h.cfg.Controller.Logs(s.ctx, id, follow)
	if err != nil {
		s.fail(err)
		return
	}
	// A line is taken from the controller when the one before is written:
	// a page that reads slowly holds up its own log and nothing else, and
	// what it misses, the ring of the zathras counts as lost.
	for {
		select {
		case line, ok := <-lines:
			if ok {
				if s.writeJSON(line) != nil {
					return
				}
				continue
			}
			// The channel is closed for a log that has ended and for one
			// that broke off, and the Controller says no more than that.
			// So a ring that broke off half way, as when its zathras went
			// while it was sent, ends as a whole one does.
			if !follow {
				s.end()
				return
			}
			// Review Focus 2: a log that is followed does not end in
			// silence. The channel closes when the zathras has gone and
			// also when only the stream of its log broke, so the page is
			// told what holds for both: that the log has ended. Whether
			// the zathras is connected, the page has from the fleet.
			s.fail(wire.Errorf(wire.CodeUnavailable, "the log of zathras %s has ended", id))
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// truth returns a parameter of the query that is a truth value: absent or
// empty is false.
func truth(r *http.Request, name string) (bool, error) {
	switch v := r.URL.Query().Get(name); v {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	default:
		return false, wire.Errorf(wire.CodeBadRequest, "%s is %q, and can be 1 or 0", name, v)
	}
}
