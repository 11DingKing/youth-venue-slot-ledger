package identity

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

func TestRejectedCoachProfileLeavesNoAccountAndCanRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "coach-account.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "identity-operator@example.test", PasswordHash: "hash", Name: "Identity Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now })
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "coach-account-retry"}
	request := CreateUserRequest{Email: "summer-coach@example.test", Password: "strong-password", Name: "Summer Coach", Role: domain.RoleCoach, Qualification: "youth-swimming"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_coach_profile BEFORE INSERT ON coaches BEGIN SELECT RAISE(ABORT, 'coach profile rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(ctx, actor, request); err == nil {
		t.Fatal("CreateUser() succeeded while coach profile persistence was rejected")
	}
	if _, err := store.UserByEmail(ctx, request.Email); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("failed coach creation left account: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_coach_profile`); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateUser(ctx, actor, request)
	if err != nil {
		t.Fatalf("CreateUser() after recovery: %v", err)
	}
	var profiles int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coaches WHERE user_id = ?`, created.ID).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if created.Role != domain.RoleCoach || profiles != 1 {
		t.Fatalf("recovered coach = %+v, profiles = %d", created, profiles)
	}
}
