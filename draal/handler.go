package draal

import (
	"context"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/authz"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// admit maps a request of an operator to its operation and its service
// and asks the authority whether the peer may. A refusal is logged.
//
// What is asked about a zathras is of the service the zathras is of. A
// key that may ask it of some service and not of that one is answered as
// for a zathras that is not connected: it does not learn that there is
// one. A key that may ask it of no service is refused, in the same words
// whether the zathras is there or not, and they name no service.
func (d *Draal) admit(peer wire.ID, req *wire.Request) error {
	var (
		op      authz.Operation
		service string
		zathras *wire.ID
	)
	switch {
	case req.Store != nil:
		op, service = authz.OpStore, req.Store.Service
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
	case req.Me != nil:
		op = authz.OpMe
	case req.Grants != nil, req.Grant != nil, req.Revoke != nil:
		op = authz.OpAccess
	case req.StorePage != nil:
		op = authz.OpStorePage
	default:
		return wire.Errorf(wire.CodeBadRequest, "draal answers no such request")
	}
	if zathras != nil {
		service, connected := d.ServiceOf(*zathras)
		if connected && d.cfg.Authz.Authorize(peer, op, service) == nil {
			return nil
		}
		if err := d.cfg.Authz.Any(peer, op); err != nil {
			d.log.Warn("refused", "peer", peer, "operation", op)
			return err
		}
		if connected {
			d.log.Warn("refused", "peer", peer, "operation", op, "service", service)
		}
		return notConnected(*zathras)
	}
	if err := d.cfg.Authz.Authorize(peer, op, service); err != nil {
		d.log.Warn("refused", "peer", peer, "operation", op, "service", service)
		return err
	}
	return nil
}

// operate answers one request of an operator through the methods of
// Controller, and returns when the peer has ended the stream, or, after a
// refusal, when RefuseWait has passed.
func (d *Draal) operate(ctx context.Context, conn node.Conn, s node.Stream, req *wire.Request) {
	peer := conn.RemoteID()
	if err := d.admit(peer, req); err != nil {
		d.refuse(s, err)
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if req.Shell != nil {
		d.serveShell(ctx, cancel, s, *req.Shell)
		return
	}
	// The peer writes nothing more: the end of what it sends, or an
	// error, is the end of the request.
	go func() {
		io.Copy(io.Discard, s)
		cancel()
	}()
	// An answer that cannot be written for another reason than the
	// stream, such as one of more than wire.MaxFrame, is logged: the
	// operator sees only that the answer ended.
	out := &streamWriter{w: s}
	unwritten := func(err error) {
		if out.err == nil {
			d.log.Error("an answer to an operator could not be written", "peer", peer, "request", strings.Join(asked(req), " "), "error", err)
		}
	}
	// send writes one response, and ends the request when it cannot.
	send := func(resp *wire.Response) bool {
		if err := wire.Write(out, resp); err != nil {
			unwritten(err)
			cancel()
			return false
		}
		return true
	}
	if err := d.respond(ctx, conn, req, send); err != nil {
		if werr := wire.WriteError(out, err); werr != nil {
			unwritten(werr)
		}
		s.CloseWrite()
		// A refusal is not held for the peer: it has RefuseWait to end
		// the stream.
		wait := time.NewTimer(d.cfg.RefuseWait)
		defer wait.Stop()
		select {
		case <-ctx.Done():
		case <-wait.C:
		}
		return
	}
	s.CloseWrite()
	<-ctx.Done()
}

// streamWriter is a writer over a stream that keeps the error of the
// stream, which tells a peer that has gone from an answer that could not
// be made.
type streamWriter struct {
	w   io.Writer
	err error
}

func (sw *streamWriter) Write(p []byte) (int, error) {
	n, err := sw.w.Write(p)
	if err != nil {
		sw.err = err
	}
	return n, err
}

// asked returns the names of the fields of req that are set, as the
// protocol names them.
func asked(req *wire.Request) []string {
	v := reflect.ValueOf(req).Elem()
	var names []string
	for i := range v.NumField() {
		if !v.Field(i).IsNil() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			names = append(names, name)
		}
	}
	return names
}

// respond does what req asks and sends what answers it. An error it
// returns is the answer.
func (d *Draal) respond(ctx context.Context, conn node.Conn, req *wire.Request, send func(*wire.Response) bool) error {
	peer := conn.RemoteID()
	switch {
	case req.Store != nil:
		info, err := d.Store(ctx, req.Store.Service)
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
			// What the key may see, by the grants as they are now.
			f = d.seen(peer, f)
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
	case req.Me != nil:
		me := d.Me(peer)
		send(&wire.Response{Me: &me})
	case req.Grants != nil:
		send(&wire.Response{Grants: &wire.GrantList{Grants: d.Grants()}})
	case req.Grant != nil:
		if err := d.Grant(ctx, peer, *req.Grant); err != nil {
			return err
		}
		send(&wire.Response{Granted: &wire.Granted{}})
	case req.Revoke != nil:
		// The connection the revocation is asked on is an admin's and
		// stays one: it is not among those that are ended.
		if err := d.Revoke(ctx, peer, *req.Revoke, conn); err != nil {
			return err
		}
		send(&wire.Response{Revoked: &wire.Revoked{}})
	case req.StorePage != nil:
		page, err := d.StorePage(req.StorePage.Path)
		if err != nil {
			return err
		}
		send(&wire.Response{StorePage: &page})
	}
	return nil
}

// serveShell relays a shell session between the stream of an operator and
// the Shell of the Controller. The session ends when the shell does, and
// when the operator closes the stream, whatever waits then.
func (d *Draal) serveShell(ctx context.Context, cancel context.CancelFunc, s node.Stream, req wire.ShellRequest) {
	sh, err := d.Shell(ctx, req.Zathras, req)
	if err != nil {
		d.refuse(s, err)
		return
	}
	defer sh.Close()
	if err := wire.Write(s, &wire.Response{Shell: &wire.ShellOutput{}}); err != nil {
		return
	}
	// An operator that closes the stream is not always seen by the
	// reading of the input: that waits in a Write to a zathras whose
	// shell takes no input, while the shell prints nothing that could be
	// written to the operator. Done of the stream says it then. Done is
	// closed as well when draal ends what it sends, behind the exit
	// status or an error: answered is closed before that, and from then
	// on the session is over and the input is read to the operator's end
	// of the stream.
	answered := make(chan struct{})
	go func() {
		select {
		case <-s.Done():
			select {
			case <-answered:
			default:
				sh.Close()
				cancel()
			}
		case <-ctx.Done():
		}
	}()
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
			sh.Close() // ends a Write of input that waits for the zathras
			wire.WriteError(s, err)
			close(answered)
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
	close(answered)
	s.CloseWrite()
	<-ctx.Done()
}
