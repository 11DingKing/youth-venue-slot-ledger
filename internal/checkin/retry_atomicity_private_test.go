package checkin

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestFailedCheckInLeavesNoReceiptAndCanRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "checkin-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	birth := time.Date(2012, time.January, 1, 0, 0, 0, 0, time.UTC)
	student, err := store.CreateUser(ctx, domain.User{Email: "retry-student@example.test", PasswordHash: "hash", Name: "Retry Student", Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	guardian, err := store.CreateUser(ctx, domain.User{Email: "retry-guardian@example.test", PasswordHash: "hash", Name: "Retry Guardian", Role: domain.RoleGuardian, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	coach, err := store.CreateUser(ctx, domain.User{Email: "retry-coach@example.test", PasswordHash: "hash", Name: "Retry Coach", Role: domain.RoleCoach, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Retry Check-in Center", District: "Central", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "badminton", StartsAt: now, EndsAt: now.Add(time.Hour), Capacity: 4, MinimumAge: 8, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveSeat(ctx, slot.ID, slot.Version); err != nil {
		t.Fatal(err)
	}
	booking, err := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID, SlotID: slot.ID, Status: domain.BookingConfirmed, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now })
	actor := domain.Actor{UserID: coach.ID, Role: domain.RoleCoach, RequestID: "retryable-check-in"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_checkin_transition BEFORE UPDATE OF status ON bookings WHEN OLD.id = `+strconv.FormatInt(booking.ID, 10)+` BEGIN SELECT RAISE(ABORT, 'booking transition rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, actor, booking.ID, booking.Version); err == nil {
		t.Fatal("Redeem() succeeded while booking persistence rejected the transition")
	}
	persisted, err := store.BookingByID(ctx, booking.ID)
	if err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM checkins WHERE booking_id = ?`, booking.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.BookingConfirmed || persisted.Version != booking.Version || receipts != 0 {
		t.Fatalf("failed check-in left booking %s version %d and %d receipts", persisted.Status, persisted.Version, receipts)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_checkin_transition`); err != nil {
		t.Fatal(err)
	}
	checkedIn, err := service.Redeem(ctx, actor, booking.ID, booking.Version)
	if err != nil {
		t.Fatalf("Redeem() after persistence recovery: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM checkins WHERE booking_id = ?`, booking.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if checkedIn.Status != domain.BookingCheckedIn || receipts != 1 {
		t.Fatalf("recovered check-in = %+v, receipts = %d", checkedIn, receipts)
	}
}
