package zathras

import (
	"context"

	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// answer writes what answers req to s. ctx is done when draal has closed
// the stream or the connection has ended.
func (a *Agent) answer(ctx context.Context, s node.Stream, req *wire.Request) {
	switch {
	case req.Prepare != nil:
		if err := a.prepare(ctx, req.Prepare); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Prepared: &wire.Prepared{}})
	case req.Activate != nil:
		if err := a.activate(req.Activate); err != nil {
			wire.WriteError(s, err)
			return
		}
		wire.Write(s, &wire.Response{Activated: &wire.Activated{}})
	case req.Logs != nil:
		a.logs(ctx, s, req.Logs)
	default:
		wire.WriteError(s, wire.Errorf(wire.CodeBadRequest, "a zathras answers prepare, activate, logs and shell, and this request is none of them"))
	}
}
