package authz

import (
	"slices"
	"sync/atomic"

	"github.com/fables-for-robots/great-machine/wire"
)

// The operations that stage 6 tells apart. A push, a commit, a listing
// and a deletion were one, the store's write; who is asked for the grants,
// for the page of the store and who it is are new.
const (
	// OpStorePush is the first two steps of a push, which name the
	// reference: it is of the service the name is under.
	OpStorePush Operation = "store-push"
	// OpStoreCommit is the last step of a push, which names an upload and
	// no reference. The store answers it for the endpoint that began the
	// upload, and whoever deploys anything may ask.
	OpStoreCommit Operation = "store-commit"
	OpStoreList   Operation = "store-list"
	OpStoreDelete Operation = "store-delete"
	OpStorePage   Operation = "store-page"
	// OpAccess is the grants: listing them, granting and revoking.
	OpAccess Operation = "access"
	// OpMe is a peer asking who it is, which every peer may.
	OpMe Operation = "me"
)

// kind is who an operation is for.
type kind int

const (
	forNobody   kind = iota // no such operation
	forDeployer             // a deployer of the service, or of every service
	forAnyDeployer
	forAdmin
	forEverybody
)

// what says who an operation is for, and how a refusal names it: for a
// service ("may not roll it out") and for none.
var what = map[Operation]struct {
	kind         kind
	ofIt, ofNone string
}{
	OpStore:       {forDeployer, "ask where its store is", "ask where the store is"},
	OpRollout:     {forDeployer, "roll it out", "roll out"},
	OpRollback:    {forDeployer, "roll it back", "roll back"},
	OpRollouts:    {forDeployer, "list its rollouts", "list the rollouts"},
	OpHistory:     {forDeployer, "read the samples of its zathrases", "read the samples of a zathras"},
	OpLogs:        {forDeployer, "read the logs of its zathrases", "read the logs of a zathras"},
	OpShell:       {forDeployer, "open a shell in it", "open a shell"},
	OpStorePull:   {forDeployer, "pull its store paths", "pull from the store"},
	OpStorePush:   {forDeployer, "push its store paths", "push to the store"},
	OpStoreList:   {forDeployer, "list its store paths", "list the store"},
	OpWatch:       {forAnyDeployer, "", "watch the fleet"},
	OpStoreCommit: {forAnyDeployer, "", "push to the store"},
	OpStoreDelete: {forAdmin, "", "delete from the store"},
	OpStorePage:   {forAdmin, "", "read the page of the store"},
	OpAccess:      {forAdmin, "", "manage the grants"},
	OpMe:          {forEverybody, "", ""},
}

// Config is what an Authority is made of besides the grants.
type Config struct {
	// Admins are the admins of draal's command line: admins whatever the
	// grants say. An empty ID is skipped, and one that is given twice is
	// one admin.
	Admins []wire.ID
	// Registered says which service a peer is registered as a zathras of,
	// and false for a peer that is none: such a zathras pulls the store
	// paths of its service. nil: nobody is registered. It can be set
	// later, with SetRegistered.
	Registered func(peer wire.ID) (service string, ok bool)
}

// Authority decides who may do what, by the grants: a copy in memory of
// what the database holds, which Replace puts a new one in the place of.
// Nothing it is asked reads anything but memory. Its methods may be
// called from any goroutine.
type Authority struct {
	admins     []wire.ID // of the command line, in the order given
	fixed      map[wire.ID]bool
	grants     atomic.Pointer[table]
	registered atomic.Pointer[func(wire.ID) (string, bool)]
}

// table is the grants at one time. It is never changed.
type table struct {
	all  []wire.Grant // as the database lists them
	byID map[wire.ID][]wire.Grant
}

// New returns an authority with the grants given, as the database lists
// them.
func New(cfg Config, grants []wire.Grant) *Authority {
	a := &Authority{fixed: make(map[wire.ID]bool)}
	for _, id := range cfg.Admins {
		if id == "" || a.fixed[id] {
			continue
		}
		a.fixed[id] = true
		a.admins = append(a.admins, id)
	}
	a.Replace(grants)
	if cfg.Registered != nil {
		a.SetRegistered(cfg.Registered)
	}
	return a
}

// Replace puts grants in the place of what the authority decided by. It
// is called when the database was written, with what the database holds
// then: the next question is answered by them.
func (a *Authority) Replace(grants []wire.Grant) {
	t := &table{all: slices.Clone(grants), byID: make(map[wire.ID][]wire.Grant)}
	for _, g := range t.all {
		t.byID[g.ID] = append(t.byID[g.ID], g)
	}
	a.grants.Store(t)
}

// SetRegistered says who is asked which service a peer is registered as
// a zathras of.
func (a *Authority) SetRegistered(registered func(peer wire.ID) (service string, ok bool)) {
	a.registered.Store(&registered)
}

// has reports whether peer holds the role for the service: by a grant
// for it, or by one for every service.
func (t *table) has(peer wire.ID, role wire.Role, service string) bool {
	for _, g := range t.byID[peer] {
		if g.Role == role && (g.Service == "" || g.Service == service) {
			return true
		}
	}
	return false
}

// any reports whether peer holds the role for any service at all.
func (t *table) any(peer wire.ID, role wire.Role) bool {
	for _, g := range t.byID[peer] {
		if g.Role == role {
			return true
		}
	}
	return false
}

// isAdmin reports whether peer is an admin: of the command line, or by a
// grant.
func (a *Authority) isAdmin(t *table, peer wire.ID) bool {
	return peer != "" && (a.fixed[peer] || t.any(peer, wire.RoleAdmin))
}

// Authorize returns nil when peer may do op on service, and otherwise a
// *wire.Error with CodeUnauthorized that says which role it takes.
// service is "" for what is of no service in particular: such a thing is
// an admin's, and a deployer's of every service where a deployer may do
// it at all.
//
// A pull is granted besides to a peer that is registered as a zathras of
// the service.
func (a *Authority) Authorize(peer wire.ID, op Operation, service string) error {
	w := what[op]
	if w.kind == forEverybody {
		return nil
	}
	t := a.grants.Load()
	if w.kind != forNobody && a.isAdmin(t, peer) {
		return nil
	}
	switch w.kind {
	case forDeployer:
		if t.has(peer, wire.RoleDeployer, service) {
			return nil
		}
		if op == OpStorePull && service != "" {
			if registered := a.registered.Load(); registered != nil {
				if of, ok := (*registered)(peer); ok && of == service {
					return nil
				}
			}
		}
		if service == "" {
			return wire.Errorf(wire.CodeUnauthorized, "%s is no deployer of every service and may not %s", peer, w.ofNone)
		}
		return wire.Errorf(wire.CodeUnauthorized, "%s is no deployer of %s and may not %s", peer, service, w.ofIt)
	case forAnyDeployer:
		if t.any(peer, wire.RoleDeployer) {
			return nil
		}
		return wire.Errorf(wire.CodeUnauthorized, "%s is no deployer of any service and may not %s", peer, w.ofNone)
	case forAdmin:
		return wire.Errorf(wire.CodeUnauthorized, "%s is no admin and may not %s", peer, w.ofNone)
	}
	return wire.Errorf(wire.CodeUnauthorized, "%s may not %s: there is no such operation", peer, op)
}

// Signer returns nil when by is a role key that signs for a zathras of
// the service: it holds zathras for the service, or for every service.
// The refusal names the role key and the service, and the command that
// grants what is missing: it is read in the log of a pod.
func (a *Authority) Signer(by wire.ID, service string) error {
	if a.grants.Load().has(by, wire.RoleZathras, service) {
		return nil
	}
	return wire.Errorf(wire.CodeUnauthorized,
		"the role key %s is no zathras of %s: an admin grants it with `great-machine access grant zathras %s --service %s`",
		by, service, by, service)
}

// Sees returns the services a key may watch: every one, for an admin and
// for a deployer of every service, and otherwise those it is a deployer
// of, by name.
func (a *Authority) Sees(peer wire.ID) (every bool, services []string) {
	t := a.grants.Load()
	if a.isAdmin(t, peer) {
		return true, nil
	}
	for _, g := range t.byID[peer] {
		if g.Role != wire.RoleDeployer {
			continue
		}
		if g.Service == "" {
			return true, nil
		}
		services = append(services, g.Service)
	}
	slices.Sort(services)
	return false, slices.Compact(services)
}

// Holds returns what a key was granted: for an admin of the command line
// a grant that says so, and then the grants of the table.
func (a *Authority) Holds(peer wire.ID) []wire.Grant {
	var out []wire.Grant
	if peer != "" && a.fixed[peer] {
		out = append(out, wire.Grant{ID: peer, Role: wire.RoleAdmin, Fixed: true})
	}
	return append(out, a.grants.Load().byID[peer]...)
}

// All returns every grant: the admins of the command line first, in the
// order they were given, and then the table as the database lists it.
func (a *Authority) All() []wire.Grant {
	t := a.grants.Load()
	out := make([]wire.Grant, 0, len(a.admins)+len(t.all))
	for _, id := range a.admins {
		out = append(out, wire.Grant{ID: id, Role: wire.RoleAdmin, Fixed: true})
	}
	return append(out, t.all...)
}

// Fixed reports whether peer is an admin of the command line, which no
// request revokes.
func (a *Authority) Fixed(peer wire.ID) bool {
	return peer != "" && a.fixed[peer]
}

// Admins reports whether there is an admin at all: without one nobody
// can grant anything.
func (a *Authority) Admins() bool {
	if len(a.admins) > 0 {
		return true
	}
	for _, g := range a.grants.Load().all {
		if g.Role == wire.RoleAdmin {
			return true
		}
	}
	return false
}
