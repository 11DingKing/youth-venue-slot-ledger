package waitlist

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestFailedWaitlistPromotionDoesNotConsumeSeat(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "waitlist-seat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	birth := time.Date(2012, time.January, 1, 0, 0, 0, 0, time.UTC)
	student, err := store.CreateUser(ctx, domain.User{Email: "wait-student@example.test", PasswordHash: "hash", Name: "Wait Student", Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	guardian, err := store.CreateUser(ctx, domain.User{Email: "wait-guardian@example.test", PasswordHash: "hash", Name: "Wait Guardian", Role: domain.RoleGuardian, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	operator, err := store.CreateUser(ctx, domain.User{Email: "wait-operator@example.test", PasswordHash: "hash", Name: "Wait Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Waitlist Center", District: "East", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "swimming", StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(25 * time.Hour), Capacity: 2, MinimumAge: 8, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
	if err != nil {
		t.Fatal(err)
	}
	booking, err := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID, SlotID: slot.ID, Status: domain.BookingWaitlisted, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddWaitlistEntry(ctx, booking.ID, slot.ID, now); err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now }, 15*time.Minute)
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "waitlist-seat-owner"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_waitlist_promotion BEFORE UPDATE OF status ON bookings WHEN NEW.status = 'held' BEGIN SELECT RAISE(ABORT, 'promotion rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.PromoteNext(ctx, actor, slot.ID); err == nil {
		t.Fatal("PromoteNext() succeeded while booking promotion was rejected")
	}
	afterFailure, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	stillWaiting, err := store.BookingByID(ctx, booking.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Reserved != 0 || afterFailure.Version != slot.Version || stillWaiting.Status != domain.BookingWaitlisted {
		t.Fatalf("failed promotion left slot reserved %d version %d and booking %s", afterFailure.Reserved, afterFailure.Version, stillWaiting.Status)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_waitlist_promotion`); err != nil {
		t.Fatal(err)
	}
	promoted, ok, err := service.PromoteNext(ctx, actor, slot.ID)
	if err != nil || !ok {
		t.Fatalf("PromoteNext() after recovery = %v, %v", ok, err)
	}
	if promoted.Status != domain.BookingHeld {
		t.Fatalf("promoted booking status = %s", promoted.Status)
	}
}
