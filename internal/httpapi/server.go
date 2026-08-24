package httpapi

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/audit"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/checkin"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/closure"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/identity"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/schedule"
)

type Dependencies struct {
	DB       *sql.DB
	Logger   *slog.Logger
	Auth     *auth.Service
	Identity *identity.Service
	Schedule *schedule.Service
	Bookings *booking.Service
	CheckIn  *checkin.Service
	Closures *closure.Service
	Audit    *audit.Service
}

type Server struct {
	deps Dependencies
}

func New(deps Dependencies) http.Handler {
	server := &Server{deps: deps}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("POST /v1/auth/login", server.login)

	protected := http.NewServeMux()
	protected.HandleFunc("POST /v1/auth/logout", server.logout)
	protected.HandleFunc("GET /v1/auth/me", server.me)
	protected.HandleFunc("POST /v1/admin/users", server.createUser)
	protected.HandleFunc("POST /v1/admin/guardian-authorizations", server.authorizeGuardian)
	protected.HandleFunc("GET /v1/venues", server.listVenues)
	protected.HandleFunc("POST /v1/venues", server.createVenue)
	protected.HandleFunc("GET /v1/venues/{id}/slots", server.listSlots)
	protected.HandleFunc("POST /v1/slots", server.createSlot)
	protected.HandleFunc("POST /v1/slots/{id}/capacity-adjustments", server.adjustCapacity)
	protected.HandleFunc("POST /v1/bookings", server.createBooking)
	protected.HandleFunc("GET /v1/bookings", server.listBookings)
	protected.HandleFunc("POST /v1/bookings/{id}/confirm", server.confirmBooking)
	protected.HandleFunc("POST /v1/bookings/{id}/cancel", server.cancelBooking)
	protected.HandleFunc("POST /v1/bookings/{id}/reschedule", server.rescheduleBooking)
	protected.HandleFunc("POST /v1/bookings/{id}/check-in", server.checkInBooking)
	protected.HandleFunc("POST /v1/closures", server.createClosure)
	protected.HandleFunc("POST /v1/closures/{id}/reopen", server.reopenClosure)
	protected.HandleFunc("GET /v1/audit-events", server.listAuditEvents)
	mux.Handle("/v1/", Authenticate(deps.Auth, protected))
	return RequestLifecycle(deps.Logger, mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := database.Ready(ctx, s.deps.DB); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
