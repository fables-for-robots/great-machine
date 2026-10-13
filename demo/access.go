package demo

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/fables-for-robots/great-machine/wire"
)

// The made-up grants: who looks at the demo is an admin of the command
// line, and beside it there are the keys a fleet of this kind has.
func (d *Demo) seedGrants() {
	day := 24 * time.Hour
	d.grants = []wire.Grant{
		{ID: operator, Role: wire.RoleAdmin, Fixed: true},
		{ID: idOf("another admin"), Role: wire.RoleAdmin, Note: "the laptop of a colleague", By: operator, At: d.start.Add(-40 * day)},
		{ID: idOf("ci"), Role: wire.RoleDeployer, Service: "web", Note: "ci", By: operator, At: d.start.Add(-30 * day)},
		{ID: idOf("ci"), Role: wire.RoleDeployer, Service: "worker", Note: "ci", By: operator, At: d.start.Add(-30 * day)},
		{ID: idOf("the role key of web"), Role: wire.RoleZathras, Service: "web", Note: "the pods of web", By: operator, At: d.start.Add(-30 * day)},
		{ID: idOf("the role key of worker"), Role: wire.RoleZathras, Service: "worker", Note: "the pods of worker", By: operator, At: d.start.Add(-29 * day)},
	}
}

// Me answers as the admin who looks at the demo.
func (d *Demo) Me(ctx context.Context) (wire.Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	me := wire.Identity{ID: operator}
	for _, g := range d.grants {
		if g.ID == operator {
			me.Grants = append(me.Grants, g)
		}
	}
	return me, nil
}

// Grants returns the made-up grants, and those that were granted since.
func (d *Demo) Grants(ctx context.Context) ([]wire.Grant, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.grants), nil
}

// Grant grants as draal does, in memory: it refuses what is no role and
// no service, and a grant that is there is there with the new note.
func (d *Demo) Grant(ctx context.Context, req wire.GrantRequest) error {
	if err := wire.ValidRole(req.Role); err != nil {
		return err
	}
	if len(req.ID) != 64 || strings.Trim(string(req.ID), "0123456789abcdef") != "" {
		return wire.Errorf(wire.CodeBadRequest, "%q is no endpoint ID", string(req.ID))
	}
	if req.Role == wire.RoleAdmin && req.Service != "" {
		return wire.Errorf(wire.CodeBadRequest, "an admin is of every service, and is granted none")
	}
	if req.Service != "" {
		if err := wire.ValidService(req.Service); err != nil {
			return err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, g := range d.grants {
		if g.ID == req.ID && g.Role == req.Role && g.Service == req.Service && !g.Fixed {
			d.grants[i].Note = req.Note
			return nil
		}
	}
	d.grants = append(d.grants, wire.Grant{ID: req.ID, Role: req.Role, Service: req.Service, Note: req.Note, By: operator, At: time.Now().UTC()})
	return nil
}

// Revoke takes a grant away as draal does: not one of the command line,
// and not the admin's own.
func (d *Demo) Revoke(ctx context.Context, req wire.RevokeRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, g := range d.grants {
		if g.ID != req.ID || g.Role != req.Role || g.Service != req.Service {
			continue
		}
		if g.Fixed {
			return wire.Errorf(wire.CodeBadRequest, "%s is an admin by draal's command line, which no request revokes", req.ID)
		}
		d.grants = slices.Delete(d.grants, i, i+1)
		return nil
	}
	return wire.Errorf(wire.CodeNotFound, "%s holds no such grant", req.ID)
}

// StorePage answers that the demo has no store.
func (d *Demo) StorePage(ctx context.Context, path string) (wire.StorePage, error) {
	return wire.StorePage{}, wire.Errorf(wire.CodeNotFound, "the demo has no store")
}
