package booking_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/booking"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestFailedCrossVenueReschedulePreservesCapacityLedgerAndBookings(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "reschedule-atomicity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 9, 0, 0, 0, time.UTC)

	createUser := func(email string, role domain.Role, birth *time.Time) domain.User {
		user, createErr := store.CreateUser(ctx, domain.User{Email: email, PasswordHash: "hash", Name: email,
			Role: role, BirthDate: birth, AbilityLevel: 3, Active: true, CreatedAt: now})
		if createErr != nil {
			t.Fatalf("create %s: %v", role, createErr)
		}
		return user
	}
	birth := time.Date(2012, time.June, 15, 0, 0, 0, 0, time.UTC)
	guardian := createUser("atomicity-guardian@example.test", domain.RoleGuardian, nil)
	student := createUser("atomicity-student@example.test", domain.RoleStudent, &birth)
	coachUser := createUser("atomicity-coach@example.test", domain.RoleCoach, nil)
	coachID, err := store.CreateCoach(ctx, coachUser.ID, "youth-multi-sport")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{GuardianID: guardian.ID,
		StudentID: student.ID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	createSlot := func(name string, startsAt time.Time) domain.Slot {
		venue, createErr := store.CreateVenue(ctx, domain.Venue{Name: name, District: "Central", Timezone: "UTC", Active: true})
		if createErr != nil {
			t.Fatalf("create venue: %v", createErr)
		}
		slot, createErr := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "swimming",
			StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour), Capacity: 3, MinimumAge: 8,
			MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
		if createErr != nil {
			t.Fatalf("create slot: %v", createErr)
		}
		if createErr = store.AssignCoach(ctx, coachID, slot.ID, now); createErr != nil {
			t.Fatalf("assign coach: %v", createErr)
		}
		return slot
	}
	sourceSlot := createSlot("Atomicity Source Venue", now.Add(48*time.Hour))
	destinationSlot := createSlot("Atomicity Destination Venue", now.Add(52*time.Hour))
	service := booking.New(store, func() time.Time { return now }, 15*time.Minute)
	actor := domain.Actor{UserID: guardian.ID, Role: domain.RoleGuardian, RequestID: "failed-reschedule"}
	sourceBooking, err := service.Create(ctx, actor, booking.CreateRequest{StudentID: student.ID,
		GuardianID: guardian.ID, SlotID: sourceSlot.ID, IdempotencyKey: "source-booking"})
	if err != nil {
		t.Fatalf("create source booking: %v", err)
	}
	destinationBooking, err := service.Create(ctx, actor, booking.CreateRequest{StudentID: student.ID,
		GuardianID: guardian.ID, SlotID: destinationSlot.ID, IdempotencyKey: "destination-booking"})
	if err != nil {
		t.Fatalf("create destination booking: %v", err)
	}

	_, err = service.Reschedule(ctx, actor, sourceBooking.ID, sourceBooking.Version, destinationSlot.ID)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Reschedule() error = %v, want conflict", err)
	}
	gotSourceBooking, err := store.BookingByID(ctx, sourceBooking.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotDestinationBooking, err := store.BookingByID(ctx, destinationBooking.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotSourceSlot, err := store.SlotByID(ctx, sourceSlot.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotDestinationSlot, err := store.SlotByID(ctx, destinationSlot.ID)
	if err != nil {
		t.Fatal(err)
	}
	sourceBalance, err := store.LedgerBalance(ctx, sourceSlot.ID)
	if err != nil {
		t.Fatal(err)
	}
	destinationBalance, err := store.LedgerBalance(ctx, destinationSlot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSourceBooking.SlotID != sourceSlot.ID || gotDestinationBooking.SlotID != destinationSlot.ID ||
		gotSourceSlot.Reserved != 1 || gotDestinationSlot.Reserved != 1 ||
		sourceBalance != 1 || destinationBalance != 1 ||
		gotSourceSlot.Reserved != sourceBalance || gotDestinationSlot.Reserved != destinationBalance {
		t.Fatalf("failed reschedule changed state: bookings=%d/%d slots=%d/%d ledger=%d/%d",
			gotSourceBooking.SlotID, gotDestinationBooking.SlotID, gotSourceSlot.Reserved,
			gotDestinationSlot.Reserved, sourceBalance, destinationBalance)
	}
}
