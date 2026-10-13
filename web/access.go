package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/fables-for-robots/great-machine/wire"
)

// maxGrant is the most a grant may be as JSON. An ID, a role, the name
// of a service and a note, which draal cuts at 200 bytes, fit in it many
// times over.
const maxGrant = 64 << 10

// grantList is the answer of the grants.
type grantList struct {
	Grants []wire.Grant `json:"grants"`
}

// me answers with the endpoint ID of the operator's key and its grants.
// A key with no grant has an identity too: its ID is what an admin needs
// to grant it one.
func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	me, err := h.cfg.Client.Me(r.Context())
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if me.Grants == nil {
		me.Grants = []wire.Grant{} // [] and not null
	}
	h.writeJSON(w, http.StatusOK, me)
}

// grants is the grants as the page has them: GET answers with every
// grant, POST grants and DELETE revokes. Who may do any of it, draal
// says.
func (h *handler) grants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listGrants(w, r)
	case http.MethodPost:
		if h.fromThePage(w, r) {
			h.grant(w, r)
		}
	case http.MethodDelete:
		if h.fromThePage(w, r) {
			h.revoke(w, r)
		}
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		h.writeJSON(w, http.StatusMethodNotAllowed, refusal{Error: wire.Errorf(wire.CodeBadRequest,
			"%s is asked for with GET, granted to with POST and revoked from with DELETE, not asked with %s", r.URL.Path, r.Method)})
	}
}

// fromThePage says whether a request that changes something comes from
// the page itself, and has refused it when not. A browser sends the
// Origin of the page with every such request, and a cookie to every port
// of a host: a page of another program on the same machine would
// otherwise act with it. A program that is no browser sends no Origin.
func (h *handler) fromThePage(w http.ResponseWriter, r *http.Request) bool {
	if sameOrigin(r) {
		return true
	}
	h.log.Warn("a request of another origin was refused", "origin", r.Header.Get("Origin"), "host", r.Host, "path", r.URL.Path)
	h.refuse(w, r, wire.Errorf(wire.CodeUnauthorized,
		"what changes something at %s is asked by its own page alone, not by one of %s", r.Host, r.Header.Get("Origin")))
	return false
}

// done answers a change that was made: with nothing.
func done(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listGrants(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Client.Grants(r.Context())
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if list == nil {
		list = []wire.Grant{}
	}
	h.writeJSON(w, http.StatusOK, grantList{Grants: list})
}

// grant grants what the body says, which is one wire.GrantRequest as
// JSON and nothing else. Whether it is a grant, draal says: an ID that
// is none, a role that is none.
func (h *handler) grant(w http.ResponseWriter, r *http.Request) {
	var req wire.GrantRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGrant))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.refuse(w, r, wire.Errorf(wire.CodeBadRequest, "what was sent is no grant as JSON, with id, role, service and note: %v", err))
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		h.refuse(w, r, wire.Errorf(wire.CodeBadRequest, "what was sent is more than one grant as JSON"))
		return
	}
	if err := h.cfg.Client.Grant(r.Context(), req); err != nil {
		h.refuse(w, r, err)
		return
	}
	done(w)
}

// revoke revokes the grant the query names: id, role and service, the
// last empty or absent for a grant of every service.
func (h *handler) revoke(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := wire.RevokeRequest{ID: wire.ID(q.Get("id")), Role: wire.Role(q.Get("role")), Service: q.Get("service")}
	if err := h.cfg.Client.Revoke(r.Context(), req); err != nil {
		h.refuse(w, r, err)
		return
	}
	done(w)
}

// store answers a path under /store/ with what draal answers for the
// page of its store: jaccard-store's admin page, which reads the
// database of the store, and so is asked of draal. The path is asked as
// the browser asks it, with /store taken off and with its query. Of the
// answer the status, the type and the body are passed on, and the
// headers are this handler's own.
func (h *handler) store(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/store")
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	page, err := h.cfg.Client.StorePage(r.Context(), path)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if page.Status < http.StatusOK || page.Status > 599 {
		h.refuse(w, r, wire.Errorf(wire.CodeInternal, "the page of the store was answered with the status %d, which is none", page.Status))
		return
	}
	ctype := page.Type
	if ctype == "" {
		// Not for the browser to guess, and not for this server either.
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	// What the store holds changes with every rollout.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(page.Status)
	w.Write(page.Body)
}
