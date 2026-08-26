package httpapi

import (
	"net/http"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/identity"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := s.deps.Auth.Login(r.Context(), request.Email, request.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Auth.Logout(r.Context(), Token(r.Context())); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	actor, ok := Actor(r.Context())
	if !ok {
		writeError(w, r, domain.ErrUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": actor.UserID, "role": actor.Role})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var request identity.CreateUserRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	user, err := s.deps.Identity.CreateUser(r.Context(), actor, request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) authorizeGuardian(w http.ResponseWriter, r *http.Request) {
	var request domain.GuardianAuthorization
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	authorization, err := s.deps.Identity.AuthorizeGuardian(r.Context(), actor, request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, authorization)
}
