package draal

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/fleet"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// Serve accepts connections from l and runs the reconciler until ctx is
// done, and then returns nil, having closed every connection. It returns
// the error of l when l fails before that. Call it once.
func (d *Draal) Serve(ctx context.Context, l node.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { d.reap(ctx) })
	wg.Go(func() { d.reconcile(ctx) })
	var err error
	for {
		var conn node.Conn
		conn, err = l.Accept(ctx)
		if err != nil {
			break
		}
		wg.Go(func() { d.serveConn(ctx, conn) })
	}
	stopped := ctx.Err() != nil
	cancel()
	wg.Wait()
	if stopped {
		return nil
	}
	return fmt.Errorf("draal: accepting a connection: %w", err)
}

// serveConn answers the streams of one connection, each a request, until
// the connection ends or ctx is done, and closes the connection.
//
// At most MaxStreams streams of the connection are served at a time; one
// beyond them is closed at once, without an answer. Only the streams the
// peer opens are counted: those draal opens to a zathras are not served
// here.
func (d *Draal) serveConn(ctx context.Context, conn node.Conn) {
	var wg sync.WaitGroup
	places := make(chan struct{}, d.cfg.MaxStreams) // one for every stream served
	warned := false
	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			break
		}
		select {
		case places <- struct{}{}:
		default:
			if !warned {
				warned = true
				d.log.Warn("a peer opens more streams than draal serves at a time: closing those beyond", "peer", conn.RemoteID(), "most", d.cfg.MaxStreams)
			}
			s.Close()
			continue
		}
		wg.Go(func() {
			defer func() { <-places }()
			d.serveStream(ctx, conn, s)
		})
	}
	conn.Close()
	wg.Wait()
}

// serveStream reads the request of a stream and answers it. The request
// has RequestWait to arrive, or the stream is closed.
func (d *Draal) serveStream(ctx context.Context, conn node.Conn, s node.Stream) {
	defer s.Close()
	// A stream has no deadline: a timer closes it, which ends the Read.
	// When the timer has fired the stream is closed, whatever the Read
	// returned.
	timer := time.AfterFunc(d.cfg.RequestWait, func() { s.Close() })
	var req wire.Request
	err := wire.Read(s, &req)
	if !timer.Stop() {
		d.log.Debug("no request within RequestWait", "peer", conn.RemoteID(), "after", d.cfg.RequestWait)
		return
	}
	if err != nil {
		d.log.Debug("reading a request", "peer", conn.RemoteID(), "error", err)
		return
	}
	// One that asks for more than one thing is refused before anything of
	// it is looked at: authorized for one, it might do another.
	if fields := asked(&req); len(fields) > 1 {
		d.refuse(s, wire.Errorf(wire.CodeBadRequest, "a request asks for one thing, and this one asks for %s", strings.Join(fields, " and ")))
		return
	}
	if req.Register != nil {
		d.register(ctx, conn, s, req.Register)
		return
	}
	d.operate(ctx, conn.RemoteID(), s, &req)
}

// refuse answers a request with err, and waits for the peer to end the
// stream so that the answer is not lost with it: RefuseWait at most, and
// then closes the stream.
func (d *Draal) refuse(s node.Stream, err error) {
	wire.WriteError(s, err)
	s.CloseWrite()
	timer := time.AfterFunc(d.cfg.RefuseWait, func() { s.Close() })
	defer timer.Stop()
	io.Copy(io.Discard, s)
}

// register is the session of a zathras: it joins the fleet, its reports
// are taken until the stream or the connection ends, and then it leaves.
func (d *Draal) register(ctx context.Context, conn node.Conn, s node.Stream, reg *wire.Register) {
	id := conn.RemoteID()
	if err := d.cfg.Authz.Authorize(id, authz.OpRegister, reg.Service); err != nil {
		d.log.Warn("refused", "peer", id, "operation", authz.OpRegister, "service", reg.Service)
		d.refuse(s, err)
		return
	}
	if err := validRegister(reg); err != nil {
		d.refuse(s, err)
		return
	}
	// What a zathras reports is bounded before it is kept or shown. That
	// it had to be is logged once for the connection.
	warned := false
	bound := func(st wire.State) wire.State {
		st, cut := bounded(st)
		if cut && !warned {
			warned = true
			d.log.Warn("a zathras reports more than draal keeps, or control characters: what it reports is cut and cleaned",
				"zathras", id, "name", reg.Name)
		}
		return st
	}
	state := bound(reg.State)
	now := d.cfg.Now()
	sess := &session{id: id, service: reg.Service, name: reg.Name, conn: conn}
	// The fleet and the sessions change together, so that whoever sees
	// the member finds its connection.
	d.mu.Lock()
	epoch := d.fleet.Join(fleet.Member{
		ID: id, Service: reg.Service, Name: reg.Name,
		Connected: now, State: state, Reported: now,
	})
	replaced := d.sessions[id]
	d.sessions[id] = sess
	d.mu.Unlock()
	if replaced != nil && replaced.conn != conn {
		d.log.Info("a zathras connected again: closing its connection of before", "zathras", id, "name", reg.Name)
		replaced.conn.Close()
	}
	d.log.Info("zathras registered", "zathras", id, "service", reg.Service, "name", reg.Name, "version", reg.Version)

	// A connection that ends is to end the reading, whatever the stream does.
	left := make(chan struct{})
	defer close(left)
	go func() {
		select {
		case <-conn.Done():
			s.Close()
		case <-left:
		}
	}()

	last := state
	if err := wire.Write(s, &wire.Response{Registered: &wire.Registered{}}); err == nil {
		for {
			var st wire.State
			if err := wire.Read(s, &st); err != nil {
				break
			}
			if !knownPhase(st.Phase) {
				d.log.Warn("a zathras reported a phase draal does not know, and does not speak the protocol: closing its connection",
					"zathras", id, "name", reg.Name, "phase", clean(string(st.Phase), maxSignal))
				conn.Close()
				break
			}
			st = bound(st)
			d.fleet.Report(id, epoch, st, d.cfg.Now())
			last = st
		}
	}

	// The reconciler is told who left, with its last report, together with
	// the fleet: whoever finds the member gone finds the leave as well.
	at := d.cfg.Now()
	d.mu.Lock()
	if d.fleet.Leave(id, epoch) {
		d.left = append(d.left, leave{id: id, service: reg.Service, state: last, at: at})
	}
	current := d.sessions[id] == sess
	if current {
		delete(d.sessions, id)
	}
	d.mu.Unlock()
	if current {
		// What is left of the connection is closed, which ends the
		// commands, logs and shells under way on it.
		conn.Close()
		d.log.Info("zathras gone", "zathras", id, "service", reg.Service, "name", reg.Name)
	}
}

// validRegister returns the bad-request a registration is refused with,
// or nil: a service that is a DNS label, a name and a version as
// validName and validVersion have them, and a phase draal knows.
func validRegister(reg *wire.Register) error {
	if err := wire.ValidService(reg.Service); err != nil {
		return err
	}
	if err := validName(reg.Name); err != nil {
		return err
	}
	if err := validVersion(reg.Version); err != nil {
		return err
	}
	if !knownPhase(reg.State.Phase) {
		return wire.Errorf(wire.CodeBadRequest, "%q is no phase of a zathras", clean(string(reg.State.Phase), maxSignal))
	}
	return nil
}

// reap closes the connection of every zathras that has not reported for
// GoneAfter, which makes it leave.
func (d *Draal) reap(ctx context.Context) {
	tick := time.NewTicker(d.cfg.GoneAfter / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		for _, id := range d.fleet.Stale(d.cfg.Now(), d.cfg.GoneAfter) {
			if sess := d.session(id); sess != nil {
				d.log.Info("no report of a zathras: closing its connection", "zathras", id, "after", d.cfg.GoneAfter)
				sess.conn.Close()
			}
		}
	}
}
