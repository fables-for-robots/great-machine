package zathras

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/fables-for-robots/great-machine/supervisor"
	"github.com/fables-for-robots/great-machine/wire"
)

// prepare has the sidecar fetch the closure and then checks the binary.
// ctx is done when draal has withdrawn the prepare, which ends the request
// to the sidecar. The generation is in Preparing while this runs, and in
// Prepared when it has worked.
func (a *Agent) prepare(ctx context.Context, req *wire.Prepare) error {
	if req.Generation == 0 {
		return wire.Errorf(wire.CodeBadRequest, "a prepare needs a generation")
	}
	a.mu.Lock()
	a.preparing = append(a.preparing, req.Generation)
	a.mu.Unlock()
	a.stateChanged()

	err := a.fetch(ctx, req)

	a.mu.Lock()
	if i := slices.Index(a.preparing, req.Generation); i >= 0 {
		a.preparing = slices.Delete(a.preparing, i, i+1)
	}
	if err == nil {
		if i, found := slices.BinarySearch(a.prepared, req.Generation); !found {
			a.prepared = slices.Insert(a.prepared, i, req.Generation)
		}
	}
	a.mu.Unlock()
	a.stateChanged()

	if err != nil {
		a.log.Warn("prepare failed", "generation", req.Generation, "binary", req.Binary, "error", err)
		return err
	}
	a.log.Info("prepared", "generation", req.Generation, "binary", req.Binary, "paths", len(req.Closure))
	return nil
}

// fetch is the work of a prepare. Its errors are as they travel: the
// message is the reason, which for an answer of the sidecar that is not
// 200 holds what the sidecar said.
func (a *Agent) fetch(ctx context.Context, req *wire.Prepare) error {
	if err := a.cfg.Preload.Preload(ctx, req.Closure); err != nil {
		return wire.Errorf(wire.CodeInternal, "%s", reason(err))
	}
	if err := supervisor.CheckExecutable(req.Binary); err != nil {
		return wire.Errorf(wire.CodeBadRequest, "%s", reason(err))
	}
	return nil
}

// activate has the supervisor take the generation as its own. A binary
// that is no executable file is refused, and what runs is left alone.
func (a *Agent) activate(req *wire.Activate) error {
	if req.Generation == 0 {
		return wire.Errorf(wire.CodeBadRequest, "an activate needs a generation")
	}
	err := a.cfg.Supervisor.Activate(req.Generation, req.Binary, req.Args)
	switch {
	case err == nil:
		a.log.Info("activated", "generation", req.Generation, "binary", req.Binary)
		a.stateChanged()
		return nil
	case errors.Is(err, supervisor.ErrStopped):
		return wire.Errorf(wire.CodeUnavailable, "%s", reason(err))
	default:
		a.log.Warn("activate refused", "generation", req.Generation, "binary", req.Binary, "error", err)
		return wire.Errorf(wire.CodeBadRequest, "%s", reason(err))
	}
}

// reason returns the text of err as it can travel: a message whose text
// is no valid UTF-8 is refused by whoever reads it, and an error may quote
// anything.
func reason(err error) string {
	return strings.ToValidUTF8(err.Error(), "�")
}
