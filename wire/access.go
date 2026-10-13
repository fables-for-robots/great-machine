package wire

import (
	"strings"
	"time"
)

// Role is what a key was granted.
type Role string

// The roles. An admin does everything. A deployer pushes and rolls out,
// and looks at what it rolled out. A zathras fetches and reports: its
// grant is held by a role key, which signs for the zathrases of a
// service and connects to nothing.
const (
	RoleAdmin    Role = "admin"
	RoleDeployer Role = "deployer"
	RoleZathras  Role = "zathras"
)

// ValidRole returns nil for one of the three roles, and a bad-request
// *Error that quotes the role otherwise.
func ValidRole(r Role) error {
	switch r {
	case RoleAdmin, RoleDeployer, RoleZathras:
		return nil
	}
	return Errorf(CodeBadRequest, "%q is no role: there are admin, deployer and zathras", string(r))
}

// Grant is a role that a key was given, for a service or for every
// service. A key has as many grants as it was given.
type Grant struct {
	ID   ID   `json:"id"`
	Role Role `json:"role"`
	// Service is the service the role is of. Empty: every service, which
	// an admin always is of.
	Service string `json:"service,omitempty"`
	// Note is what a person wrote beside the grant.
	Note string `json:"note,omitempty"`
	// By is the admin who granted it, and At when. Neither is set for an
	// admin of draal's command line.
	By ID        `json:"by,omitempty"`
	At time.Time `json:"at"`
	// Fixed says that this is an admin of draal's command line, which no
	// request revokes.
	Fixed bool `json:"fixed,omitempty"`
}

// GrantsRequest asks for every grant. It is an admin's.
type GrantsRequest struct{}

// GrantList answers GrantsRequest: the admins of the command line first,
// then the grants by role, service and ID.
type GrantList struct {
	Grants []Grant `json:"grants"`
}

// GrantRequest grants a role. A grant that is there already is there
// with the new note.
type GrantRequest struct {
	ID      ID     `json:"id"`
	Role    Role   `json:"role"`
	Service string `json:"service,omitempty"` // empty: every service
	Note    string `json:"note,omitempty"`
}

// Granted answers GrantRequest.
type Granted struct{}

// RevokeRequest takes a grant away, and ends what it was used for.
type RevokeRequest struct {
	ID      ID     `json:"id"`
	Role    Role   `json:"role"`
	Service string `json:"service,omitempty"`
}

// Revoked answers RevokeRequest.
type Revoked struct{}

// MeRequest asks who the peer is to draal. Every peer may ask.
type MeRequest struct{}

// Identity answers MeRequest: the endpoint ID of the peer, as the
// handshake proved it, and what it was granted.
type Identity struct {
	ID     ID      `json:"id"`
	Grants []Grant `json:"grants"`
}

// StorePageRequest asks for a path of the page of the store, which is
// jaccard-store's admin page over the database of the store. It is an
// admin's.
type StorePageRequest struct {
	Path string `json:"path"` // with its query, as a browser asks for it: "/api/stats"
}

// StorePage answers StorePageRequest with what a GET of the path is
// answered with.
type StorePage struct {
	Status int    `json:"status"`
	Type   string `json:"type,omitempty"` // the Content-Type
	Body   []byte `json:"body,omitempty"`
}

// Statement is what a zathras registers with: the endpoint ID of a role
// key, and that key's signature over StatementText of draal and the
// zathras.
type Statement struct {
	By        ID     `json:"by"`
	Signature []byte `json:"signature"`
}

// StatementText returns what a role key signs for a zathras: that at the
// draal, the zathras acts for it. It is of one draal and one zathras, so
// that a statement is worth nothing to another endpoint and at another
// draal.
func StatementText(draal, zathras ID) []byte {
	return []byte("great-machine zathras statement 1\n" + string(draal) + "\n" + string(zathras) + "\n")
}

// ServiceOfStore returns the service whose prefix in the store a name, or
// the prefix of a listing, is under: what follows StorePrefix of a
// service. It returns false for what is under no service's prefix, one
// that is shorter than a prefix among it: "/services/web" is under none,
// since "/services/webshop/nix/store/" begins with it too.
func ServiceOfStore(name string) (service string, ok bool) {
	rest, ok := strings.CutPrefix(name, "/services/")
	if !ok {
		return "", false
	}
	service, _, ok = strings.Cut(rest, "/")
	if !ok || ValidService(service) != nil || !strings.HasPrefix(name, StorePrefix(service)) {
		return "", false
	}
	return service, true
}
