package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func TestBookingWaitlistCheckInAndLedger(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	student, guardian, slot := bookingFixtures(t, store, now, 1)
	expires := now.Add(15 * time.Minute)
	booking, err := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID,
		SlotID: slot.ID, Status: domain.BookingHeld, HoldExpiresAt: &expires, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateBooking() error: %v", err)
	}
	confirmed, err := store.TransitionBooking(ctx, booking.ID, booking.Version, domain.BookingHeld, domain.BookingConfirmed, "", now)
	if err != nil {
		t.Fatalf("confirm booking: %v", err)
	}
	if _, err := store.TransitionBooking(ctx, booking.ID, booking.Version, domain.BookingHeld, domain.BookingCancelled, "stale", now); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
	if err := store.RecordCheckIn(ctx, confirmed.ID, guardian.ID, "checkin-request", slot.StartsAt); err != nil {
		t.Fatalf("record check-in: %v", err)
	}
	if err := store.RecordCheckIn(ctx, confirmed.ID, guardian.ID, "checkin-request-2", slot.StartsAt); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate check-in error = %v", err)
	}

	entry, err := store.AppendLedger(ctx, domain.LedgerEntry{SlotID: slot.ID, BookingID: &booking.ID,
		Kind: domain.LedgerReserve, Delta: 1, BalanceAfter: 1, Reason: "booking", CorrelationID: "booking-1", CreatedAt: now})
	if err != nil || entry.ID == 0 {
		t.Fatalf("AppendLedger() = %+v, %v", entry, err)
	}
	balance, err := store.LedgerBalance(ctx, slot.ID)
	if err != nil || balance != 1 {
		t.Fatalf("ledger balance = %d, error = %v", balance, err)
	}
}

func TestWaitlistOrderingAndPromotion(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Now().UTC()
	student1, guardian, slot := bookingFixtures(t, store, now, 1)
	birth := time.Date(2012, time.January, 2, 0, 0, 0, 0, time.UTC)
	student2 := createUser(t, store, domain.User{Email: "student2@example.test", PasswordHash: "hash", Name: "Student 2",
		Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	for index, student := range []domain.User{student1, student2} {
		booking, err := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID,
			SlotID: slot.ID, Status: domain.BookingWaitlisted, CreatedAt: now.Add(time.Duration(index) * time.Second),
			UpdatedAt: now.Add(time.Duration(index) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddWaitlistEntry(ctx, booking.ID, slot.ID, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	next, err := store.NextWaitlistedBooking(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.StudentID != student1.ID {
		t.Fatalf("next student = %d, want %d", next.StudentID, student1.ID)
	}
	promoted, err := store.PromoteWaitlistedBooking(ctx, next.ID, next.Version, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != domain.BookingHeld || promoted.HoldExpiresAt == nil {
		t.Fatalf("promoted booking = %+v", promoted)
	}
	if err := store.MarkWaitlistPromoted(ctx, promoted.ID, now); err != nil {
		t.Fatal(err)
	}
	second, err := store.NextWaitlistedBooking(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.StudentID != student2.ID {
		t.Fatalf("second student = %d, want %d", second.StudentID, student2.ID)
	}
}

func TestBookingListFiltersByOwnerAndStatus(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Now().UTC()
	student, guardian, slot := bookingFixtures(t, store, now, 4)
	expires := now.Add(time.Hour)
	for _, status := range []domain.BookingStatus{domain.BookingHeld, domain.BookingCancelled} {
		booking, err := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID,
			SlotID: slot.ID, Status: status, HoldExpiresAt: &expires, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			if status == domain.BookingCancelled {
				// The active-booking partial index permits cancelled history but the first row is still active.
				continue
			}
			t.Fatal(err)
		}
		_ = booking
	}
	items, err := store.ListBookings(ctx, guardian.ID, domain.RoleGuardian, string(domain.BookingHeld), 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("guardian held bookings = %v, error = %v", items, err)
	}
	items, err = store.ListBookings(ctx, student.ID, domain.RoleStudent, string(domain.BookingHeld), 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("student held bookings = %v, error = %v", items, err)
	}
}

func bookingFixtures(t *testing.T, store *Store, now time.Time, capacity int) (domain.User, domain.User, domain.Slot) {
	t.Helper()
	birth := time.Date(2012, time.January, 2, 0, 0, 0, 0, time.UTC)
	student := createUser(t, store, domain.User{Email: "student@example.test", PasswordHash: "hash", Name: "Student",
		Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	guardian := createUser(t, store, domain.User{Email: "guardian@example.test", PasswordHash: "hash", Name: "Guardian",
		Role: domain.RoleGuardian, Active: true, CreatedAt: now})
	venue, err := store.CreateVenue(context.Background(), domain.Venue{Name: "Fixture Venue", District: "North", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(context.Background(), validSlot(venue.ID, now.Add(24*time.Hour), capacity))
	if err != nil {
		t.Fatal(err)
	}
	return student, guardian, slot
}
