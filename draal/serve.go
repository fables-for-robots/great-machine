package draal

import (
	"context"
	"fmt"
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
func (d *Draal) serveConn(ctx context.Context, conn node.Conn) {
	var wg sync.WaitGroup
	for {
		s, err := conn.AcceptStream(ctx)
		if err != nil {
			break
		}
		wg.Go(func() { d.serveStream(ctx, conn, s) })
	}
	conn.Close()
	wg.Wait()
}

// serveStream reads the request of a stream and answers it.
func (d *Draal) serveStream(ctx context.Context, conn node.Conn, s node.Stream) {
	defer s.Close()
	var req wire.Request
	if err := wire.Read(s, &req); err != nil {
		d.log.Debug("reading a request", "peer", conn.RemoteID(), "error", err)
		return
	}
	if req.Register != nil {
		d.register(ctx, conn, s, req.Register)
		return
	}
	d.operate(ctx, conn.RemoteID(), s, &req)
}

// refuse answers a request with err, and waits for the peer to end the
// stream so that the answer is not lost with it.
func refuse(s node.Stream, err error) {
	wire.WriteError(s, err)
	s.CloseWrite()
	var rest [512]byte
	for {
		if _, err := s.Read(rest[:]); err != nil {
			return
		}
	}
}

// register is the session of a zathras: it joins the fleet, its reports
// are taken until the stream or the connection ends, and then it leaves.
func (d *Draal) register(ctx context.Context, conn node.Conn, s node.Stream, reg *wire.Register) {
	id := conn.RemoteID()
	if err := d.cfg.Authz.Authorize(id, authz.OpRegister, reg.Service); err != nil {
		d.log.Warn("refused", "peer", id, "operation", authz.OpRegister, "service", reg.Service)
		refuse(s, err)
		return
	}
	if err := wire.ValidService(reg.Service); err != nil {
		refuse(s, err)
		return
	}
	now := d.cfg.Now()
	sess := &session{id: id, service: reg.Service, conn: conn}
	// The fleet and the sessions change together, so that whoever sees
	// the member finds its connection.
	d.mu.Lock()
	epoch := d.fleet.Join(fleet.Member{
		ID: id, Service: reg.Service, Name: reg.Name,
		Connected: now, State: reg.State, Reported: now,
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

	if err := wire.Write(s, &wire.Response{Registered: &wire.Registered{}}); err == nil {
		for {
			var st wire.State
			if err := wire.Read(s, &st); err != nil {
				break
			}
			d.fleet.Report(id, epoch, st, d.cfg.Now())
		}
	}

	d.fleet.Leave(id, epoch)
	d.mu.Lock()
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
