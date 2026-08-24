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

func TestCreateCoachUserIsAtomicAndRetriable(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	service := New(store, func() time.Time { return now })
	operator := domain.Actor{UserID: 1, Role: domain.RoleOperator, RequestID: "create-coach"}
	request := CreateUserRequest{
		Email: "summer-coach@example.test", Password: "change-me-now",
		Name: "Summer Coach", Role: domain.RoleCoach, Qualification: "youth-swimming-level-2",
	}

	// Simulate a coach profile save failure by temporarily dropping the coaches
	// table. The INSERT INTO coaches issued by CreateCoach then fails, modelling
	// "教练资质档案保存失败".
	if _, err := db.ExecContext(ctx, `DROP TABLE coach_assignments`); err != nil {
		t.Fatalf("drop coach_assignments: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE coaches`); err != nil {
		t.Fatalf("drop coaches: %v", err)
	}

	if _, err := service.CreateUser(ctx, operator, request); err == nil {
		t.Fatal("expected coach creation to fail while coaches table is unavailable")
	}

	// No partial user record may survive the failed attempt: the email must stay
	// free so the original request can be retried once the fault clears.
	if _, err := store.UserByEmail(ctx, request.Email); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("orphaned user record after failed coach creation: %v", err)
	}

	// Fault clears: restore the coaches table and replay the identical request.
	if _, err := db.ExecContext(ctx, `CREATE TABLE coaches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL UNIQUE REFERENCES users(id),
		qualification TEXT NOT NULL,
		active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1))
	)`); err != nil {
		t.Fatalf("restore coaches table: %v", err)
	}

	created, err := service.CreateUser(ctx, operator, request)
	if err != nil {
		t.Fatalf("retry CreateUser() error = %v", err)
	}
	if created.Email != request.Email || created.Role != domain.RoleCoach {
		t.Fatalf("created user = %+v", created)
	}
}
