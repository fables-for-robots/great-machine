package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fables-for-robots/great-machine/wire"
)

// refusal is the JSON of an answer that refuses.
type refusal struct {
	Error *wire.Error `json:"error"`
}

// rolloutList is the answer of the rollouts of a service.
type rolloutList struct {
	Rollouts []wire.RolloutInfo `json:"rollouts"` // newest first
}

// sampleList is the answer of the history of a zathras.
type sampleList struct {
	Samples []wire.Sample `json:"samples"` // oldest first
}

// statusOf returns the HTTP status of a refusal with the code.
func statusOf(code wire.Code) int {
	switch code {
	case wire.CodeBadRequest:
		return http.StatusBadRequest
	case wire.CodeUnauthorized:
		return http.StatusForbidden
	case wire.CodeNotFound:
		return http.StatusNotFound
	case wire.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// writeJSON answers with v as JSON. Nothing of the API is to be kept by
// a browser: it is the state of the fleet at one time.
func (h *handler) writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.log.Error("encoding an answer", "error", err)
		status, data = http.StatusInternalServerError, []byte(`{"error":{"code":"internal","message":"draal could not encode its answer"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}

// refusalOf makes of an error of the controller what the browser is told.
// A refusal is passed on as it is when its code is one that has an HTTP
// status of its own (statusOf). An internal one, one with a code draal
// does not know, and every other error is logged, and the page is told
// that draal failed and no more: draal makes such errors of what its
// database said, a zathras can answer one with any text, and neither is
// the business of a page. A code draal does not know can only have come
// from a zathras.
func (h *handler) refusalOf(r *http.Request, err error) *wire.Error {
	var e *wire.Error
	if errors.As(err, &e) && statusOf(e.Code) != http.StatusInternalServerError {
		return e
	}
	if r.Context().Err() != nil {
		// Who asked has gone, which is why it failed.
		h.log.Debug("a request ended with who asked", "path", r.URL.Path, "error", err)
	} else {
		h.log.Error("a request failed", "path", r.URL.Path, "error", err)
	}
	return wire.Errorf(wire.CodeInternal, "draal failed to answer; its log says why")
}

// refuse answers with err as a refusal.
func (h *handler) refuse(w http.ResponseWriter, r *http.Request, err error) {
	e := h.refusalOf(r, err)
	h.writeJSON(w, statusOf(e.Code), refusal{Error: e})
}

// get returns next for GET alone: every path of the API is read, and
// none changes anything.
func (h *handler) get(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			h.writeJSON(w, http.StatusMethodNotAllowed, refusal{Error: wire.Errorf(wire.CodeBadRequest,
				"%s is asked for with GET, not with %s", r.URL.Path, r.Method)})
			return
		}
		next(w, r)
	}
}

// noSuchPath answers every path under /api/ that is none of the API.
func (h *handler) noSuchPath(w http.ResponseWriter, r *http.Request) {
	h.refuse(w, r, wire.Errorf(wire.CodeNotFound, "the API has no %s", r.URL.Path))
}

// zathrasID returns the endpoint ID in the path of a request, or a
// bad-request for what is not 64 hexadecimal characters in lower case.
// Capitals are refused and not folded: an ID is compared exactly
// everywhere, and the page has it from draal, which writes lower case.
func zathrasID(r *http.Request) (wire.ID, error) {
	id := r.PathValue("id")
	valid := len(id) == 64
	for i := 0; valid && i < len(id); i++ {
		c := id[i]
		valid = c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
	}
	if !valid {
		return "", wire.Errorf(wire.CodeBadRequest,
			"%q is no endpoint ID: one is 64 hexadecimal characters in lower case", id)
	}
	return wire.ID(id), nil
}

// rollouts answers with the last rollouts of a service.
func (h *handler) rollouts(w http.ResponseWriter, r *http.Request) {
	service := r.PathValue("service")
	if err := wire.ValidService(service); err != nil {
		h.refuse(w, r, err)
		return
	}
	list, err := h.cfg.Controller.Rollouts(r.Context(), service)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if list == nil {
		list = []wire.RolloutInfo{} // [] and not null
	}
	h.writeJSON(w, http.StatusOK, rolloutList{Rollouts: list})
}

// history answers with the samples kept of a zathras.
func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	id, err := zathrasID(r)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	samples, err := h.cfg.Controller.History(r.Context(), id)
	if err != nil {
		h.refuse(w, r, err)
		return
	}
	if samples == nil {
		samples = []wire.Sample{}
	}
	h.writeJSON(w, http.StatusOK, sampleList{Samples: samples})
}
