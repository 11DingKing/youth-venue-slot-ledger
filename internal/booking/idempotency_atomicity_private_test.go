package booking

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestIdempotencyFailureRollsBackBookingAndCanReplay(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "booking-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	guardian := fixtureUser(t, store, "idempotent-guardian@example.test", domain.RoleGuardian, now, nil)
	student := fixtureUser(t, store, "idempotent-student@example.test", domain.RoleStudent, now, studentBirth())
	coach := fixtureUser(t, store, "idempotent-coach@example.test", domain.RoleCoach, now, nil)
	coachID, err := store.CreateCoach(ctx, coach.ID, "youth-swimming")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{GuardianID: guardian.ID, StudentID: student.ID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(30 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Idempotency Aquatic Center", District: "East", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, fixtureSlot(venue.ID, now.Add(48*time.Hour), 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignCoach(ctx, coachID, slot.ID, now); err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now }, 15*time.Minute)
	actor := domain.Actor{UserID: guardian.ID, Role: domain.RoleGuardian, RequestID: "idempotency-retry"}
	request := CreateRequest{StudentID: student.ID, GuardianID: guardian.ID, SlotID: slot.ID, IdempotencyKey: "booking-retry-key"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_idempotency_key
		BEFORE INSERT ON idempotency_keys BEGIN SELECT RAISE(FAIL, 'idempotency storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, actor, request); err == nil {
		t.Fatal("Create() error = nil, want idempotency persistence failure")
	}
	var bookings, ledgers, audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bookings WHERE student_id = ? AND slot_id = ?`, student.ID, slot.ID).Scan(&bookings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE slot_id = ?`, slot.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'booking.create'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	failedSlot, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bookings != 0 || failedSlot.Reserved != 0 || ledgers != 0 || audits != 0 {
		t.Fatalf("failed create bookings=%d reserved=%d ledgers=%d audits=%d, want 0/0/0/0", bookings, failedSlot.Reserved, ledgers, audits)
	}

	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_idempotency_key`); err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, actor, request)
	if err != nil {
		t.Fatalf("retry Create() error: %v", err)
	}
	replayed, err := service.Create(ctx, actor, request)
	if err != nil {
		t.Fatalf("replay Create() error: %v", err)
	}
	finalSlot, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID || finalSlot.Reserved != 1 {
		t.Fatalf("replay booking=%d created=%d reserved=%d, want same ID and one seat", replayed.ID, created.ID, finalSlot.Reserved)
	}
}
