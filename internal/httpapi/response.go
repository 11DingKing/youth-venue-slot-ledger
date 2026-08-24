package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := classifyError(err)
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message, RequestID: RequestID(r.Context())}})
}

func classifyError(err error) (int, string, string) {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized", "Authentication is required or the session has expired."
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden", "The current role cannot perform this operation."
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found", "The requested resource was not found."
	case errors.Is(err, domain.ErrCapacity):
		return http.StatusConflict, "capacity_unavailable", "The slot no longer has available capacity."
	case errors.Is(err, domain.ErrVersionConflict):
		return http.StatusConflict, "version_conflict", "The resource changed; reload it before retrying."
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrIdempotencyReuse):
		return http.StatusConflict, "conflict", "The request conflicts with current state."
	case errors.Is(err, domain.ErrInvalidState):
		return http.StatusConflict, "invalid_state", "The requested state transition is not allowed."
	case errors.Is(err, domain.ErrExpired):
		return http.StatusConflict, "expired", "The operation window has expired."
	case errors.Is(err, domain.ErrGuardianRequired):
		return http.StatusUnprocessableEntity, "guardian_authorization_required", "An active guardian authorization is required."
	case errors.Is(err, domain.ErrEligibility):
		return http.StatusUnprocessableEntity, "student_not_eligible", "The student does not match this slot's group."
	case errors.Is(err, domain.ErrCoachCoverage):
		return http.StatusUnprocessableEntity, "coach_coverage_required", "The slot has no active qualified coach coverage."
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation_error", "The request contains invalid fields."
	default:
		return http.StatusInternalServerError, "internal_error", "The operation could not be completed."
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.Join(domain.ErrValidation, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.Join(domain.ErrValidation, errors.New("request must contain one JSON object"))
	}
	return nil
}
