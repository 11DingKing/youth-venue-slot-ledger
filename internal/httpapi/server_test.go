package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/audit"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/checkin"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/closure"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/identity"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/schedule"
)

func TestHealthReadinessAndRequestID(t *testing.T) {
	handler, closeDB := testHandler(t)
	defer closeDB()

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", "client-request-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("X-Request-ID"); got != "client-request-123" {
		t.Fatalf("request ID = %q", got)
	}
	var health map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health["status"] != "ok" {
		t.Fatalf("health response = %v", health)
	}

	request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready"`) {
		t.Fatalf("ready response = %d %s", response.Code, response.Body.String())
	}
	if len(response.Header().Get("X-Request-ID")) < 16 {
		t.Fatalf("generated request ID = %q", response.Header().Get("X-Request-ID"))
	}
}

func TestLoginMeLogoutLifecycleOverHTTP(t *testing.T) {
	handler, closeDB := testHandler(t)
	defer closeDB()

	loginBody := `{"email":"operator@example.test","password":"change-me-now"}`
	login := performRequest(handler, http.MethodPost, "/v1/auth/login", loginBody, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	var loginResult struct {
		Token string `json:"token"`
		User  struct {
			Role domain.Role `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginResult); err != nil {
		t.Fatal(err)
	}
	if loginResult.Token == "" || loginResult.User.Role != domain.RoleOperator {
		t.Fatalf("login result = %+v", loginResult)
	}

	me := performRequest(handler, http.MethodGet, "/v1/auth/me", "", loginResult.Token)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"operator"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}
	logout := performRequest(handler, http.MethodPost, "/v1/auth/logout", "", loginResult.Token)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", logout.Code, logout.Body.String())
	}
	me = performRequest(handler, http.MethodGet, "/v1/auth/me", "", loginResult.Token)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d %s", me.Code, me.Body.String())
	}
	assertAPIError(t, me, "unauthorized")
}

func TestProtectedRoutesAndMalformedJSON(t *testing.T) {
	handler, closeDB := testHandler(t)
	defer closeDB()

	unauthorized := performRequest(handler, http.MethodGet, "/v1/venues", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized route = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	assertAPIError(t, unauthorized, "unauthorized")

	wrongPassword := performRequest(handler, http.MethodPost, "/v1/auth/login",
		`{"email":"operator@example.test","password":"wrong-password"}`, "")
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d %s", wrongPassword.Code, wrongPassword.Body.String())
	}
	assertAPIError(t, wrongPassword, "unauthorized")

	unknownField := performRequest(handler, http.MethodPost, "/v1/auth/login",
		`{"email":"operator@example.test","password":"change-me-now","admin":true}`, "")
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", unknownField.Code, unknownField.Body.String())
	}
	assertAPIError(t, unknownField, "validation_error")

	multipleObjects := performRequest(handler, http.MethodPost, "/v1/auth/login",
		`{"email":"operator@example.test","password":"change-me-now"} {}`, "")
	if multipleObjects.Code != http.StatusBadRequest {
		t.Fatalf("multiple objects = %d %s", multipleObjects.Code, multipleObjects.Body.String())
	}
}

func TestErrorClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "unauthorized", err: domain.ErrUnauthorized, status: 401, code: "unauthorized"},
		{name: "forbidden", err: domain.ErrForbidden, status: 403, code: "forbidden"},
		{name: "not found", err: domain.ErrNotFound, status: 404, code: "not_found"},
		{name: "capacity", err: domain.ErrCapacity, status: 409, code: "capacity_unavailable"},
		{name: "version", err: domain.ErrVersionConflict, status: 409, code: "version_conflict"},
		{name: "conflict", err: domain.ErrConflict, status: 409, code: "conflict"},
		{name: "state", err: domain.ErrInvalidState, status: 409, code: "invalid_state"},
		{name: "expired", err: domain.ErrExpired, status: 409, code: "expired"},
		{name: "guardian", err: domain.ErrGuardianRequired, status: 422, code: "guardian_authorization_required"},
		{name: "eligibility", err: domain.ErrEligibility, status: 422, code: "student_not_eligible"},
		{name: "coach", err: domain.ErrCoachCoverage, status: 422, code: "coach_coverage_required"},
		{name: "validation", err: domain.ErrValidation, status: 400, code: "validation_error"},
		{name: "wrapped", err: errors.Join(errors.New("detail"), domain.ErrConflict), status: 409, code: "conflict"},
		{name: "internal", err: errors.New("database unavailable"), status: 500, code: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			status, code, message := classifyError(test.err)
			if status != test.status || code != test.code || message == "" {
				t.Fatalf("classifyError() = %d, %q, %q", status, code, message)
			}
		})
	}
}

func testHandler(t *testing.T) (http.Handler, func()) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	authService := auth.New(store, time.Hour, func() time.Time { return now })
	if err := authService.EnsureBootstrapOperator(context.Background(), "operator@example.test", "change-me-now"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	discardLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(Dependencies{
		DB: db, Logger: discardLogger, Auth: authService, Identity: identity.New(store, func() time.Time { return now }),
		Schedule: schedule.New(store, func() time.Time { return now }),
		Bookings: booking.New(store, func() time.Time { return now }, 15*time.Minute),
		CheckIn:  checkin.New(store, func() time.Time { return now }),
		Closures: closure.New(store, func() time.Time { return now }, 3), Audit: audit.New(store),
	})
	return handler, func() { _ = db.Close() }
}

func performRequest(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, expectedCode string) {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body = %s", err, response.Body.String())
	}
	if envelope.Error.Code != expectedCode || envelope.Error.Message == "" || envelope.Error.RequestID == "" {
		t.Fatalf("error envelope = %+v", envelope)
	}
}
