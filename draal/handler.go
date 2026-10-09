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
		if zathras != nil {
			// The answer is the same whether the zathras is connected or
			// not, and names no service.
			return wire.Errorf(wire.CodeUnauthorized, "%s is no operator and may not %s", peer, op)
		}
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
	if err == nil && req.Shell != nil {
		d.serveShell(ctx, cancel, s, *req.Shell)
		return
	}
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
	case req.Logs != nil:
		lines, err := d.Logs(ctx, req.Logs.Zathras, req.Logs.Follow)
		if err != nil {
			return err
		}
		for line := range lines {
			if !send(&wire.Response{Line: &line}) {
				break
			}
		}
	}
	return nil
}

// serveShell relays a shell session between the stream of an operator and
// the Shell of the Controller.
func (d *Draal) serveShell(ctx context.Context, cancel context.CancelFunc, s node.Stream, req wire.ShellRequest) {
	sh, err := d.Shell(ctx, req.Zathras, req)
	if err != nil {
		refuse(s, err)
		return
	}
	defer sh.Close()
	if err := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}}); err != nil {
		return
	}
	// Input, until the operator ends the stream, which ends the session.
	go func() {
		defer cancel()
		defer sh.Close()
		for {
			var in wire.ShellInput
			if err := wire.Read(s, &in); err != nil {
				return
			}
			if in.Resize != nil {
				if err := sh.Resize(in.Resize.Cols, in.Resize.Rows); err != nil {
					return
				}
			}
			if len(in.Data) > 0 {
				if _, err := sh.Write(in.Data); err != nil {
					return
				}
			}
		}
	}()
	// Output, and behind it the exit status.
	buf := make([]byte, 16<<10)
	for {
		n, err := sh.Read(buf)
		if n > 0 {
			if werr := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Data: buf[:n]}}); werr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			wire.WriteError(s, err)
			s.CloseWrite()
			<-ctx.Done()
			return
		}
	}
	status, err := sh.Wait(ctx)
	sh.Close() // the zathras need not wait for the operator
	if err != nil {
		wire.WriteError(s, err)
	} else {
		wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{Exit: &status}})
	}
	s.CloseWrite()
	<-ctx.Done()
}
