package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	actorKey     contextKey = "actor"
	tokenKey     contextKey = "token"
)

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func Actor(ctx context.Context) (domain.Actor, bool) {
	value, ok := ctx.Value(actorKey).(domain.Actor)
	return value, ok
}

func Token(ctx context.Context) string {
	value, _ := ctx.Value(tokenKey).(string)
	return value
}

func RequestLifecycle(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("http panic", "request_id", requestID, "panic", recovered, "stack", string(debug.Stack()))
				writeError(w, r.WithContext(ctx), context.Canceled)
			}
			logger.Info("http request", "request_id", requestID, "method", r.Method,
				"path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Authenticate(service *auth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}
		token := strings.TrimSpace(parts[1])
		user, err := service.Authenticate(r.Context(), token)
		if err != nil {
			writeError(w, r, err)
			return
		}
		actor := domain.Actor{UserID: user.ID, Role: user.Role, RequestID: RequestID(r.Context())}
		ctx := context.WithValue(r.Context(), actorKey, actor)
		ctx = context.WithValue(ctx, tokenKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(buffer)
}
