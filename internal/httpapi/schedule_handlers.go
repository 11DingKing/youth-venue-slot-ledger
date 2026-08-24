package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Server) listVenues(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 50)
	venues, err := s.deps.Schedule.ListVenues(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": venues, "limit": limit, "offset": offset})
}

func (s *Server) createVenue(w http.ResponseWriter, r *http.Request) {
	var venue domain.Venue
	if err := decodeJSON(w, r, &venue); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	created, err := s.deps.Schedule.CreateVenue(r.Context(), actor, venue)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listSlots(w http.ResponseWriter, r *http.Request) {
	venueID, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	now := time.Now().UTC()
	from, err := queryTime(r, "from", now)
	if err != nil {
		writeError(w, r, err)
		return
	}
	until, err := queryTime(r, "until", from.Add(31*24*time.Hour))
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, offset := pagination(r, 100)
	slots, err := s.deps.Schedule.ListSlots(r.Context(), venueID, from, until, limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": slots, "limit": limit, "offset": offset})
}

func (s *Server) createSlot(w http.ResponseWriter, r *http.Request) {
	var request struct {
		domain.Slot
		CoachID int64 `json:"coach_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	created, err := s.deps.Schedule.CreateSlot(r.Context(), actor, request.Slot, request.CoachID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) adjustCapacity(w http.ResponseWriter, r *http.Request) {
	slotID, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request struct {
		Version  int64  `json:"version"`
		Capacity int    `json:"capacity"`
		Reason   string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	adjusted, err := s.deps.Schedule.AdjustCapacity(r.Context(), actor, slotID, request.Version, request.Capacity, request.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, adjusted)
}

func pagination(r *http.Request, maximum int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 || limit > maximum {
		limit = maximum
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func queryTime(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, domain.ErrValidation
	}
	return parsed, nil
}
