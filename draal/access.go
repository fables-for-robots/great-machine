package draal

import (
	"context"
	"errors"
	"slices"

	"github.com/fables-for-robots/great-machine/db"
	"github.com/fables-for-robots/great-machine/node"
	"github.com/fables-for-robots/great-machine/wire"
)

// maxNote is how many bytes of the note of a grant are kept.
const maxNote = 200

// said returns what an error says without its code: the message of a
// refusal, as a log line has it.
func said(err error) string {
	var e *wire.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// seen returns the fleet as the key may watch it: all of it for a key
// that sees every service, and otherwise the services it was granted.
func (d *Draal) seen(peer wire.ID, f wire.Fleet) wire.Fleet {
	every, services := d.cfg.Authz.Sees(peer)
	if every {
		return f
	}
	out := wire.Fleet{Services: make([]wire.Service, 0, len(services))}
	for _, s := range f.Services {
		if slices.Contains(services, s.Name) {
			out.Services = append(out.Services, s)
		}
	}
	return out
}

// Me returns who a key is to draal: its endpoint ID and what it was
// granted. A key that was granted nothing is answered too.
func (d *Draal) Me(peer wire.ID) wire.Identity {
	return wire.Identity{ID: peer, Grants: d.cfg.Authz.Holds(peer)}
}

// Grants returns every grant: the admins of the command line, and then
// the grants of the database.
func (d *Draal) Grants() []wire.Grant {
	return d.cfg.Authz.All()
}

// grantOf returns the ID, the role and the service of a grant as they are
// kept, or the bad-request they are refused with: the ID is an endpoint
// ID, the role one of the three, and the service a name of a service or
// empty, which an admin's always is.
func grantOf(id wire.ID, role wire.Role, service string) (wire.ID, error) {
	parsed, err := node.ParseID(string(id))
	if err != nil {
		return "", wire.Errorf(wire.CodeBadRequest, "%q is no endpoint ID, as `great-machine id` prints one", clean(string(id), 80))
	}
	if err := wire.ValidRole(role); err != nil {
		return "", err
	}
	if service != "" {
		if role == wire.RoleAdmin {
			return "", wire.Errorf(wire.CodeBadRequest, "an admin is of every service, and is granted without one")
		}
		if err := wire.ValidService(service); err != nil {
			return "", err
		}
	}
	return parsed, nil
}

// reload gives the authority the grants as the database holds them. It is
// called with access held, when the database was written.
func (d *Draal) reload(ctx context.Context) error {
	grants, err := d.cfg.DB.Grants(ctx)
	if err != nil {
		return wire.Errorf(wire.CodeInternal, "reading the grants: %v", err)
	}
	d.cfg.Authz.Replace(grants)
	return nil
}

// Grant gives a key a role, for a service or for every service, by the
// admin by. A grant that is there already is there with the new note.
// The next request of the key is decided with it.
func (d *Draal) Grant(ctx context.Context, by wire.ID, req wire.GrantRequest) error {
	id, err := grantOf(req.ID, req.Role, req.Service)
	if err != nil {
		return err
	}
	grant := wire.Grant{ID: id, Role: req.Role, Service: req.Service, Note: clean(req.Note, maxNote), By: by, At: d.cfg.Now().UTC()}
	d.access.Lock()
	defer d.access.Unlock()
	if err := d.cfg.DB.PutGrant(ctx, grant); err != nil {
		return wire.Errorf(wire.CodeInternal, "recording the grant: %v", err)
	}
	if err := d.reload(ctx); err != nil {
		return err
	}
	d.log.Info("granted", "key", id, "role", req.Role, "service", req.Service, "by", by)
	return nil
}

// Revoke takes a grant away, and ends what it was used for before it
// returns: every connection of the key, of either protocol, and the
// session of every zathras the key signed for, with the connections of
// those zathrases to the store. They dial again and find what is still
// granted. asked is the connection the revocation was asked on, which is
// an admin's and stays.
//
// An admin of the command line is not revoked, and an admin does not
// revoke its own admin grant: once there is an admin, there is always
// one.
func (d *Draal) Revoke(ctx context.Context, by wire.ID, req wire.RevokeRequest, asked node.Conn) error {
	id, err := grantOf(req.ID, req.Role, req.Service)
	if err != nil {
		return err
	}
	if req.Role == wire.RoleAdmin && id == by {
		return wire.Errorf(wire.CodeBadRequest, "an admin does not revoke its own admin grant: another admin does, so that there is always one")
	}
	d.access.Lock()
	defer d.access.Unlock()
	err = d.cfg.DB.DeleteGrant(ctx, id, req.Role, req.Service)
	switch {
	case errors.Is(err, db.ErrNotFound) && req.Role == wire.RoleAdmin && d.cfg.Authz.Fixed(id):
		return wire.Errorf(wire.CodeBadRequest, "%s is an admin by draal's command line, which no request revokes", id)
	case errors.Is(err, db.ErrNotFound) && req.Service == "":
		return wire.Errorf(wire.CodeNotFound, "%s holds no grant of %s for every service", id, req.Role)
	case errors.Is(err, db.ErrNotFound):
		return wire.Errorf(wire.CodeNotFound, "%s holds no grant of %s for %s", id, req.Role, req.Service)
	case err != nil:
		return wire.Errorf(wire.CodeInternal, "removing the grant: %v", err)
	}
	if err := d.reload(ctx); err != nil {
		return err
	}
	d.log.Info("revoked", "key", id, "role", req.Role, "service", req.Service, "by", by)
	d.drop(id, asked)
	return nil
}

// drop ends what a key was let in for: its connections but for the one
// given, the sessions of the zathrases it signed for, and the connections
// of both to the store.
func (d *Draal) drop(key wire.ID, but node.Conn) {
	var conns []node.Conn
	peers := []wire.ID{key}
	d.mu.Lock()
	for conn := range d.conns {
		if conn != but && conn.RemoteID() == key {
			conns = append(conns, conn)
		}
	}
	for id, sess := range d.sessions {
		if sess.by == key {
			conns = append(conns, sess.conn)
			peers = append(peers, id)
		}
	}
	d.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
	if d.cfg.Store != nil {
		for _, peer := range peers {
			d.cfg.Store.Drop(peer)
		}
	}
}

// StorePage returns what the page of the store answers a GET of path
// with, and not-found for a draal that has no store.
func (d *Draal) StorePage(path string) (wire.StorePage, error) {
	if d.cfg.Store == nil {
		return wire.StorePage{}, wire.Errorf(wire.CodeNotFound, "this draal has no page of the store")
	}
	return d.cfg.Store.Page(path), nil
}
