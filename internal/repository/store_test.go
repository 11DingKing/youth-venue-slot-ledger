package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func TestUserGuardianAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	birth := time.Date(2013, time.May, 12, 0, 0, 0, 0, time.UTC)

	guardian := createUser(t, store, domain.User{Email: "GUARDIAN@example.test", PasswordHash: "hash",
		Name: "Guardian", Role: domain.RoleGuardian, Active: true, CreatedAt: now})
	student := createUser(t, store, domain.User{Email: "student@example.test", PasswordHash: "hash",
		Name: "Student", Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	if guardian.Email != "guardian@example.test" {
		t.Fatalf("email was not normalized: %q", guardian.Email)
	}
	if student.BirthDate == nil || !student.BirthDate.Equal(birth) {
		t.Fatalf("birth date = %v, want %v", student.BirthDate, birth)
	}
	if _, err := store.CreateUser(ctx, guardian); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate email error = %v, want ErrConflict", err)
	}

	authorization, err := store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{
		GuardianID: guardian.ID, StudentID: student.ID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateGuardianAuthorization() error: %v", err)
	}
	if authorization.ID == 0 {
		t.Fatal("authorization ID not assigned")
	}
	active, err := store.HasActiveGuardianAuthorization(ctx, guardian.ID, student.ID, now)
	if err != nil || !active {
		t.Fatalf("active authorization = %v, error = %v", active, err)
	}
	active, err = store.HasActiveGuardianAuthorization(ctx, guardian.ID, student.ID, now.Add(48*time.Hour))
	if err != nil || active {
		t.Fatalf("expired authorization = %v, error = %v", active, err)
	}

	session, err := store.CreateSession(ctx, domain.Session{UserID: guardian.ID, TokenHash: "token-hash",
		ExpiresAt: now.Add(time.Hour), CreatedAt: now})
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}
	loadedSession, loadedUser, err := store.SessionUserByTokenHash(ctx, "token-hash", now)
	if err != nil {
		t.Fatalf("SessionUserByTokenHash() error: %v", err)
	}
	if loadedSession.ID != session.ID || loadedUser.ID != guardian.ID {
		t.Fatalf("loaded session/user = %d/%d, want %d/%d", loadedSession.ID, loadedUser.ID, session.ID, guardian.ID)
	}
	revoked, err := store.RevokeSession(ctx, "token-hash", now.Add(time.Minute))
	if err != nil || !revoked {
		t.Fatalf("RevokeSession() = %v, %v", revoked, err)
	}
	if _, _, err := store.SessionUserByTokenHash(ctx, "token-hash", now.Add(2*time.Minute)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked session error = %v, want ErrNotFound", err)
	}
}

func TestTransactionCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Now().UTC()

	err := store.WithTx(ctx, func(tx *Store) error {
		_, err := tx.CreateVenue(ctx, domain.Venue{Name: "Rollback Arena", District: "West", Timezone: "UTC", Active: true})
		if err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil || err.Error() != "force rollback" {
		t.Fatalf("transaction error = %v", err)
	}
	venues, err := store.ListVenues(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(venues) != 0 {
		t.Fatalf("rollback left %d venues", len(venues))
	}

	err = store.WithTx(ctx, func(tx *Store) error {
		venue, err := tx.CreateVenue(ctx, domain.Venue{Name: "Commit Arena", District: "East", Timezone: "UTC", Active: true})
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: createOperator(t, tx, now).ID,
			ActorRole: domain.RoleOperator, Action: "venue.create", ObjectType: "venue",
			ObjectID: AuditObjectID(venue.ID), Result: "success", RequestID: "commit-request", CreatedAt: now})
		return err
	})
	if err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	venues, err = store.ListVenues(ctx, 10, 0)
	if err != nil || len(venues) != 1 {
		t.Fatalf("committed venues = %v, error = %v", venues, err)
	}
}

func TestStoreRejectsNestedTransaction(t *testing.T) {
	store, db := newTestStore(t)
	defer db.Close()
	err := store.WithTx(context.Background(), func(tx *Store) error {
		return tx.WithTx(context.Background(), func(*Store) error { return nil })
	})
	if err == nil || err.Error() != "nested transactions are not supported" {
		t.Fatalf("nested transaction error = %v", err)
	}
}

func TestRepositoryReturnsNotFound(t *testing.T) {
	store, db := newTestStore(t)
	defer db.Close()
	ctx := context.Background()
	checks := []struct {
		name string
		fn   func() error
	}{
		{name: "user", fn: func() error { _, err := store.UserByID(ctx, 404); return err }},
		{name: "slot", fn: func() error { _, err := store.SlotByID(ctx, 404); return err }},
		{name: "booking", fn: func() error { _, err := store.BookingByID(ctx, 404); return err }},
		{name: "closure", fn: func() error { _, err := store.ClosureByID(ctx, 404); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.fn(); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	}
}

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "repository.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	return New(db), db
}

func createUser(t *testing.T, store *Store, user domain.User) domain.User {
	t.Helper()
	created, err := store.CreateUser(context.Background(), user)
	if err != nil {
		t.Fatalf("create user %s: %v", user.Email, err)
	}
	return created
}

func createOperator(t *testing.T, store *Store, now time.Time) domain.User {
	t.Helper()
	return createUser(t, store, domain.User{Email: fmt.Sprintf("operator-%d@example.test", now.UnixNano()),
		PasswordHash: "hash", Name: "Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
}
