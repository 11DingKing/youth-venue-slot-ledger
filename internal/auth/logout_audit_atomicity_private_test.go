package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestLogoutAuditFailurePreservesSessionForRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "logout-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 9, 0, 0, 0, time.UTC)
	hash, err := HashPassword("secure-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateUser(ctx, domain.User{Email: "logout-operator@example.test", PasswordHash: hash,
		Name: "Logout Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, time.Hour, func() time.Time { return now })
	login, err := service.Login(ctx, "logout-operator@example.test", "secure-password")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TRIGGER reject_logout_audit
		BEFORE INSERT ON audit_events WHEN NEW.action = 'session.logout'
		BEGIN SELECT RAISE(FAIL, 'audit storage unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, login.Token); err == nil {
		t.Fatal("Logout() error = nil, want audit failure")
	}
	if _, err := service.Authenticate(ctx, login.Token); err != nil {
		t.Fatalf("session after failed logout = %v, want active for retry", err)
	}

	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_logout_audit`); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, login.Token); err != nil {
		t.Fatalf("retry Logout() error: %v", err)
	}
	if _, err := service.Authenticate(ctx, login.Token); err == nil {
		t.Fatal("session remained active after successful logout")
	}
	var audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'session.logout'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("logout audits = %d, want 1", audits)
	}
}
