package httpapi

import (
	"net/http"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/closure"
)

func (s *Server) createClosure(w http.ResponseWriter, r *http.Request) {
	var request closure.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	created, err := s.deps.Closures.CreateAndApply(r.Context(), actor, request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) reopenClosure(w http.ResponseWriter, r *http.Request) {
	id, version, ok := mutationIdentity(w, r)
	if !ok {
		return
	}
	actor, _ := Actor(r.Context())
	reopened, err := s.deps.Closures.Reopen(r.Context(), actor, id, version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reopened)
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	actor, _ := Actor(r.Context())
	limit, offset := pagination(r, 200)
	events, err := s.deps.Audit.List(r.Context(), actor, r.URL.Query().Get("object_type"), r.URL.Query().Get("object_id"), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events, "limit": limit, "offset": offset})
}
