package httpapi

import (
	"net/http"
	"strconv"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func (s *Server) createBooking(w http.ResponseWriter, r *http.Request) {
	var request booking.CreateRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	request.IdempotencyKey = r.Header.Get("Idempotency-Key")
	actor, _ := Actor(r.Context())
	created, err := s.deps.Bookings.Create(r.Context(), actor, request)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listBookings(w http.ResponseWriter, r *http.Request) {
	actor, _ := Actor(r.Context())
	limit, offset := pagination(r, 100)
	items, err := s.deps.Bookings.List(r.Context(), actor, r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func (s *Server) confirmBooking(w http.ResponseWriter, r *http.Request) {
	bookingID, version, ok := mutationIdentity(w, r)
	if !ok {
		return
	}
	actor, _ := Actor(r.Context())
	result, err := s.deps.Bookings.Confirm(r.Context(), actor, bookingID, version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) cancelBooking(w http.ResponseWriter, r *http.Request) {
	bookingID, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request struct {
		Version int64  `json:"version"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	result, err := s.deps.Bookings.Cancel(r.Context(), actor, bookingID, request.Version, request.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) rescheduleBooking(w http.ResponseWriter, r *http.Request) {
	bookingID, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request struct {
		Version           int64 `json:"version"`
		DestinationSlotID int64 `json:"destination_slot_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	actor, _ := Actor(r.Context())
	result, err := s.deps.Bookings.Reschedule(r.Context(), actor, bookingID, request.Version, request.DestinationSlotID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) checkInBooking(w http.ResponseWriter, r *http.Request) {
	bookingID, version, ok := mutationIdentity(w, r)
	if !ok {
		return
	}
	actor, _ := Actor(r.Context())
	result, err := s.deps.CheckIn.Redeem(r.Context(), actor, bookingID, version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func mutationIdentity(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return 0, 0, false
	}
	var request struct {
		Version int64 `json:"version"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return 0, 0, false
	}
	if request.Version < 1 {
		writeError(w, r, domain.ErrValidation)
		return 0, 0, false
	}
	return id, request.Version, true
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.ErrValidation
	}
	return id, nil
}
