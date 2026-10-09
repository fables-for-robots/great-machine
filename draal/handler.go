package draal

import (
	"context"
	"io"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// admit maps a request of an operator to its operation and its service
// and asks Authorize. A refusal is logged. A zathras that is not connected
// is unavailable, for a peer that may ask at all.
func (d *Draal) admit(peer wire.ID, req *wire.Request) error {
	var (
		op      authz.Operation
		service string
		zathras *wire.ID
	)
	switch {
	case req.Store != nil:
		op = authz.OpStore
	case req.Rollout != nil:
		op, service = authz.OpRollout, req.Rollout.Service
	case req.Rollback != nil:
		op, service = authz.OpRollback, req.Rollback.Service
	case req.Watch != nil:
		op = authz.OpWatch
	case req.Rollouts != nil:
		op, service = authz.OpRollouts, req.Rollouts.Service
	case req.History != nil:
		op, zathras = authz.OpHistory, &req.History.Zathras
	case req.Logs != nil:
		op, zathras = authz.OpLogs, &req.Logs.Zathras
	case req.Shell != nil:
		op, zathras = authz.OpShell, &req.Shell.Zathras
	default:
		return wire.Errorf(wire.CodeBadRequest, "draal answers no such request")
	}
	connected := true
	if zathras != nil {
		service, connected = d.ServiceOf(*zathras)
	}
	if err := d.cfg.Authz.Authorize(peer, op, service); err != nil {
		d.log.Warn("refused", "peer", peer, "operation", op, "service", service)
		return err
	}
	if !connected {
		return notConnected(*zathras)
	}
	return nil
}

// operate answers one request of an operator through the methods of
// Controller, and returns when the peer has ended the stream.
func (d *Draal) operate(ctx context.Context, peer wire.ID, s node.Stream, req *wire.Request) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	err := d.admit(peer, req)
	// The peer writes nothing more: the end of what it sends, or an
	// error, is the end of the request.
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()
	// send writes one response, and ends the request when it cannot.
	send := func(resp *wire.Response) bool {
		if err := wire.Write(s, resp); err != nil {
			cancel()
			return false
		}
		return true
	}
	if err == nil {
		err = d.respond(ctx, peer, req, send)
	}
	if err != nil {
		wire.WriteError(s, err)
	}
	s.CloseWrite()
	<-ctx.Done()
}

// respond does what req asks and sends what answers it. An error it
// returns is the answer.
func (d *Draal) respond(ctx context.Context, peer wire.ID, req *wire.Request, send func(*wire.Response) bool) error {
	switch {
	case req.Store != nil:
		info, err := d.Store(ctx)
		if err != nil {
			return err
		}
		send(&wire.Response{Store: &info})
	case req.Rollout != nil:
		info, err := d.Rollout(ctx, peer, *req.Rollout)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Rollback != nil:
		info, err := d.Rollback(ctx, peer, *req.Rollback)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollout: &info})
	case req.Watch != nil:
		fleets, err := d.Watch(ctx)
		if err != nil {
			return err
		}
		for f := range fleets {
			if !send(&wire.Response{Fleet: &f}) {
				break
			}
		}
	case req.Rollouts != nil:
		list, err := d.Rollouts(ctx, req.Rollouts.Service)
		if err != nil {
			return err
		}
		send(&wire.Response{Rollouts: &wire.RolloutList{Rollouts: list}})
	case req.History != nil:
		samples, err := d.History(ctx, req.History.Zathras)
		if err != nil {
			return err
		}
		send(&wire.Response{History: &wire.SampleList{Samples: samples}})
	}
	return nil
}
