package draal

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/wire"
)

// rolloutsListed is how many rollouts of a service Rollouts returns.
const rolloutsListed = 20

// Store returns the jaccard-store server draal was given and the prefix
// of the service. A service draal has not heard of has a prefix like any
// other: the first rollout of a service pushes before draal knows it.
func (d *Draal) Store(ctx context.Context, service string) (wire.StoreInfo, error) {
	if err := wire.ValidService(service); err != nil {
		return wire.StoreInfo{}, err
	}
	return wire.StoreInfo{Server: d.cfg.Store.Server, Prefix: wire.StorePrefix(service)}, nil
}

// ServiceOf returns the service of a connected zathras.
func (d *Draal) ServiceOf(zathras wire.ID) (string, bool) {
	m, ok := d.fleet.Get(zathras)
	return m.Service, ok
}

// History returns the samples kept of a connected zathras, oldest first,
// and unavailable for a zathras that is not connected.
func (d *Draal) History(ctx context.Context, zathras wire.ID) ([]wire.Sample, error) {
	if _, ok := d.fleet.Get(zathras); !ok {
		return nil, notConnected(zathras)
	}
	return d.fleet.History(zathras), nil
}

// Rollouts returns the last 20 rollouts of a service, newest first. A
// service that never had one has none.
func (d *Draal) Rollouts(ctx context.Context, service string) ([]wire.RolloutInfo, error) {
	if err := wire.ValidService(service); err != nil {
		return nil, err
	}
	list, err := d.cfg.DB.Rollouts(ctx, service, rolloutsListed)
	if err != nil {
		return nil, wire.Errorf(wire.CodeInternal, "reading the rollouts of %s: %v", service, err)
	}
	return list, nil
}

// Rollout checks the request, records a new generation and the rollout to
// it, and returns the rollout. The reconciler carries it out.
func (d *Draal) Rollout(ctx context.Context, by wire.ID, req wire.RolloutRequest) (wire.RolloutInfo, error) {
	if err := d.checkRollout(req); err != nil {
		return wire.RolloutInfo{}, err
	}
	info, err := d.cfg.DB.NewRollout(ctx, req, by, d.cfg.Now())
	if err != nil {
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeInternal, "recording the rollout of %s: %v", req.Service, err)
	}
	d.log.Info("rollout recorded", "service", info.Service, "rollout", info.ID, "generation", info.Generation, "by", by)
	d.changed()
	return info, nil
}

// Rollback checks the request and records a rollout to the stable
// generation of the service.
func (d *Draal) Rollback(ctx context.Context, by wire.ID, req wire.RollbackRequest) (wire.RolloutInfo, error) {
	if err := wire.ValidService(req.Service); err != nil {
		return wire.RolloutInfo{}, err
	}
	if req.Parallel < 0 {
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "parallel is %d, and cannot be below 0", req.Parallel)
	}
	info, err := d.cfg.DB.NewRollback(ctx, req, by, d.cfg.Now())
	switch {
	case errors.Is(err, db.ErrNotFound):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeNotFound, "no service %q", req.Service)
	case errors.Is(err, db.ErrNoStable):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "service %q has no stable generation to roll back to", req.Service)
	case errors.Is(err, db.ErrNothingToRollBack):
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeBadRequest, "the last rollout of service %q completed: there is nothing to roll back", req.Service)
	case err != nil:
		return wire.RolloutInfo{}, wire.Errorf(wire.CodeInternal, "recording the rollback of %s: %v", req.Service, err)
	}
	d.log.Info("rollback recorded", "service", info.Service, "rollout", info.ID, "generation", info.Generation, "by", by)
	d.changed()
	return info, nil
}

// checkRollout returns the bad-request a rollout is refused with, or nil.
func (d *Draal) checkRollout(req wire.RolloutRequest) error {
	if err := wire.ValidService(req.Service); err != nil {
		return err
	}
	name, err := storeName(d.cfg.StoreDir, req.Binary)
	if err != nil {
		return err
	}
	if len(req.Closure) == 0 {
		return wire.Errorf(wire.CodeBadRequest, "the closure is empty")
	}
	for _, n := range req.Closure {
		if n == "" || n == "." || n == ".." || strings.Contains(n, "/") {
			return wire.Errorf(wire.CodeBadRequest, "%q in the closure is no name of a store path", n)
		}
	}
	if !slices.Contains(req.Closure, name) {
		return wire.Errorf(wire.CodeBadRequest, "the closure does not hold %s, the store path of the binary", name)
	}
	if req.Parallel < 0 {
		return wire.Errorf(wire.CodeBadRequest, "parallel is %d, and cannot be below 0", req.Parallel)
	}
	return nil
}

// storeName returns the name of the store path that binary is in. It
// refuses a binary that is not an absolute path under storeDir with a
// file name behind the store path.
func storeName(storeDir, binary string) (string, error) {
	rest, ok := strings.CutPrefix(binary, storeDir+"/")
	if !ok || path.Clean(binary) != binary {
		return "", wire.Errorf(wire.CodeBadRequest, "the binary %q is no path of a file under %s/", binary, storeDir)
	}
	name, file, _ := strings.Cut(rest, "/")
	if name == "" || file == "" {
		return "", wire.Errorf(wire.CodeBadRequest, "the binary %q names no file inside a store path", binary)
	}
	return name, nil
}

// notConnected is the answer for a zathras draal has no connection of.
func notConnected(zathras wire.ID) *wire.Error {
	return wire.Errorf(wire.CodeUnavailable, "zathras %s is not connected", zathras)
}
