package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestHashPasswordPolicyAndVerification(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("short password error = %v, want ErrValidation", err)
	}
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword() error: %v", err)
	}
	if hash == "correct-horse-battery-staple" || hash == "" {
		t.Fatalf("password hash was not generated: %q", hash)
	}
	second, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if second == hash {
		t.Fatal("bcrypt hashes should use independent salts")
	}
}

func TestBootstrapLoginAuthenticateLogout(t *testing.T) {
	ctx := context.Background()
	store, closeDB := authTestStore(t)
	defer closeDB()
	now := time.Date(2026, time.August, 24, 8, 30, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	service := New(store, 2*time.Hour, clock)

	if err := service.EnsureBootstrapOperator(ctx, "operator@example.test", "change-me-now"); err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}
	if err := service.EnsureBootstrapOperator(ctx, "operator@example.test", "different-password"); err != nil {
		t.Fatalf("idempotent bootstrap: %v", err)
	}
	if _, err := service.Login(ctx, "operator@example.test", "wrong-password"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("wrong password error = %v", err)
	}
	result, err := service.Login(ctx, " OPERATOR@EXAMPLE.TEST ", "change-me-now")
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	if result.Token == "" || len(result.Token) < 40 {
		t.Fatalf("token is unexpectedly short: %q", result.Token)
	}
	if result.User.PasswordHash != "" {
		// PasswordHash is omitted from JSON but still exists on the domain object. It must never equal the password.
		if result.User.PasswordHash == "change-me-now" {
			t.Fatal("plaintext password leaked")
		}
	}
	if !result.ExpiresAt.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("expires at = %v", result.ExpiresAt)
	}
	user, err := service.Authenticate(ctx, result.Token)
	if err != nil || user.Role != domain.RoleOperator {
		t.Fatalf("Authenticate() = %+v, %v", user, err)
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}
	if _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked token error = %v", err)
	}
	if err := service.Logout(ctx, result.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("second logout error = %v", err)
	}
}

func TestSessionExpiryAndMaintenance(t *testing.T) {
	ctx := context.Background()
	store, closeDB := authTestStore(t)
	defer closeDB()
	now := time.Date(2026, time.August, 24, 9, 0, 0, 0, time.UTC)
	current := now
	service := New(store, time.Minute, func() time.Time { return current })
	if err := service.EnsureBootstrapOperator(ctx, "expiry@example.test", "change-me-now"); err != nil {
		t.Fatal(err)
	}
	first, err := service.Login(ctx, "expiry@example.test", "change-me-now")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "expiry@example.test", "change-me-now")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("independent logins returned the same token")
	}
	current = now.Add(2 * time.Minute)
	if _, err := service.Authenticate(ctx, first.Token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expired authentication error = %v", err)
	}
	count, err := service.RevokeExpired(ctx)
	if err != nil {
		t.Fatalf("RevokeExpired() error: %v", err)
	}
	if count != 2 {
		t.Fatalf("revoked sessions = %d, want 2", count)
	}
	count, err = service.RevokeExpired(ctx)
	if err != nil || count != 0 {
		t.Fatalf("second cleanup = %d, %v", count, err)
	}
}

func TestAuthenticateRejectsMissingAndUnknownTokens(t *testing.T) {
	store, closeDB := authTestStore(t)
	defer closeDB()
	service := New(store, time.Hour, time.Now)
	for _, token := range []string{"", "unknown-token", "Bearer something"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("token %q error = %v", token, err)
		}
	}
}

func TestTokenHashIsDeterministicAndDoesNotExposeToken(t *testing.T) {
	token := "a-sensitive-session-token"
	first := TokenHash(token)
	second := TokenHash(token)
	if first != second {
		t.Fatalf("TokenHash() is not deterministic: %q != %q", first, second)
	}
	if first == token || len(first) != 64 {
		t.Fatalf("unexpected token hash %q", first)
	}
}

func authTestStore(t *testing.T) (*repository.Store, func()) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	return repository.New(db), func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}
}
