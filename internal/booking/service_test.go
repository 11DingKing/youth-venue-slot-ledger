package booking

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type bookingFixture struct {
	store    *repository.Store
	service  *Service
	now      time.Time
	guardian domain.User
	student  domain.User
	coachID  int64
	venue    domain.Venue
	slot     domain.Slot
}

func TestCreateBookingPersistsReservationLedgerAuditAndIdempotency(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 2)
	defer closeDB()
	ctx := context.Background()
	actor := guardianActor(fixture, "create-request")
	request := CreateRequest{StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID,
		SlotID: fixture.slot.ID, IdempotencyKey: "booking-create-1"}
	created, err := fixture.service.Create(ctx, actor, request)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.Status != domain.BookingHeld || created.HoldExpiresAt == nil {
		t.Fatalf("created booking = %+v", created)
	}
	if !created.HoldExpiresAt.Equal(fixture.now.Add(15 * time.Minute)) {
		t.Fatalf("hold expiry = %v", created.HoldExpiresAt)
	}
	slot, err := fixture.store.SlotByID(ctx, fixture.slot.ID)
	if err != nil || slot.Reserved != 1 {
		t.Fatalf("slot after create = %+v, %v", slot, err)
	}
	balance, err := fixture.store.LedgerBalance(ctx, slot.ID)
	if err != nil || balance != 1 {
		t.Fatalf("ledger balance = %d, %v", balance, err)
	}
	events, err := fixture.store.ListAuditEvents(ctx, "booking", repository.AuditObjectID(created.ID), 10, 0)
	if err != nil || len(events) != 1 || events[0].Action != "booking.create" {
		t.Fatalf("audit events = %+v, %v", events, err)
	}

	replayed, err := fixture.service.Create(ctx, actor, request)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed ID = %d, want %d", replayed.ID, created.ID)
	}
	slot, _ = fixture.store.SlotByID(ctx, slot.ID)
	if slot.Reserved != 1 {
		t.Fatalf("idempotent replay reserved %d seats", slot.Reserved)
	}
	request.StudentID++
	if _, err := fixture.service.Create(ctx, actor, request); !errors.Is(err, domain.ErrIdempotencyReuse) {
		t.Fatalf("idempotency reuse error = %v", err)
	}
}

func TestBookingRequiresActorAuthorizationGuardianAndCoach(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 2)
	defer closeDB()
	ctx := context.Background()
	request := CreateRequest{StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID,
		SlotID: fixture.slot.ID, IdempotencyKey: "auth-check"}
	if _, err := fixture.service.Create(ctx, domain.Actor{UserID: fixture.student.ID, Role: domain.RoleStudent}, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("student actor error = %v", err)
	}
	otherGuardian := fixtureUser(t, fixture.store, "other-guardian@example.test", domain.RoleGuardian, fixture.now, nil)
	if _, err := fixture.service.Create(ctx, domain.Actor{UserID: otherGuardian.ID, Role: domain.RoleGuardian}, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("other guardian error = %v", err)
	}

	secondVenue, _ := fixture.store.CreateVenue(ctx, domain.Venue{Name: "No Coach Venue", District: "West", Timezone: "UTC", Active: true})
	uncovered := fixtureSlot(secondVenue.ID, fixture.now.Add(72*time.Hour), 2)
	uncovered, _ = fixture.store.CreateSlot(ctx, uncovered)
	request.SlotID = uncovered.ID
	request.IdempotencyKey = "no-coach"
	if _, err := fixture.service.Create(ctx, guardianActor(fixture, "no-coach"), request); !errors.Is(err, domain.ErrCoachCoverage) {
		t.Fatalf("uncovered slot error = %v", err)
	}
}

func TestFullSlotWaitlistsAndCancellationPromotesAtomically(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 1)
	defer closeDB()
	ctx := context.Background()
	first, err := fixture.service.Create(ctx, guardianActor(fixture, "first"), CreateRequest{
		StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID, IdempotencyKey: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondStudent := fixtureUser(t, fixture.store, "second-student@example.test", domain.RoleStudent, fixture.now, studentBirth())
	_, err = fixture.store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{GuardianID: fixture.guardian.ID,
		StudentID: secondStudent.ID, ValidFrom: fixture.now.Add(-time.Hour), ValidUntil: fixture.now.Add(30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Create(ctx, guardianActor(fixture, "second"), CreateRequest{
		StudentID: secondStudent.ID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID, IdempotencyKey: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != domain.BookingWaitlisted {
		t.Fatalf("second booking status = %s, want waitlisted", second.Status)
	}
	cancelled, err := fixture.service.Cancel(ctx, guardianActor(fixture, "cancel-first"), first.ID, first.Version, "family conflict")
	if err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	if cancelled.Status != domain.BookingCancelled {
		t.Fatalf("cancelled status = %s", cancelled.Status)
	}
	promoted, err := fixture.store.BookingByID(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Status != domain.BookingHeld || promoted.HoldExpiresAt == nil {
		t.Fatalf("promoted booking = %+v", promoted)
	}
	slot, _ := fixture.store.SlotByID(ctx, fixture.slot.ID)
	if slot.Reserved != 1 {
		t.Fatalf("reserved seats = %d, want 1", slot.Reserved)
	}
	balance, _ := fixture.store.LedgerBalance(ctx, slot.ID)
	if balance != slot.Reserved {
		t.Fatalf("ledger balance = %d, reserved = %d", balance, slot.Reserved)
	}
}

func TestConfirmHonorsExpiryAndVersion(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 2)
	defer closeDB()
	ctx := context.Background()
	created, err := fixture.service.Create(ctx, guardianActor(fixture, "confirm-create"), CreateRequest{
		StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID, IdempotencyKey: "confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := fixture.service.Confirm(ctx, guardianActor(fixture, "confirm"), created.ID, created.Version)
	if err != nil || confirmed.Status != domain.BookingConfirmed {
		t.Fatalf("Confirm() = %+v, %v", confirmed, err)
	}
	if _, err := fixture.service.Confirm(ctx, guardianActor(fixture, "confirm-stale"), created.ID, created.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("second confirm error = %v", err)
	}

	fixture2, closeDB2 := newBookingFixture(t, 2)
	defer closeDB2()
	expiring, err := fixture2.service.Create(ctx, guardianActor(fixture2, "expiry-create"), CreateRequest{
		StudentID: fixture2.student.ID, GuardianID: fixture2.guardian.ID, SlotID: fixture2.slot.ID, IdempotencyKey: "expiry",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture2.service.now = func() time.Time { return fixture2.now.Add(16 * time.Minute) }
	if _, err := fixture2.service.Confirm(ctx, guardianActor(fixture2, "expired"), expiring.ID, expiring.Version); !errors.Is(err, domain.ErrExpired) {
		t.Fatalf("expired confirm error = %v", err)
	}
}

func TestCrossVenueRescheduleMovesLedgerAtomically(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 2)
	defer closeDB()
	ctx := context.Background()
	created, err := fixture.service.Create(ctx, guardianActor(fixture, "move-create"), CreateRequest{
		StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID, IdempotencyKey: "move",
	})
	if err != nil {
		t.Fatal(err)
	}
	destinationVenue, _ := fixture.store.CreateVenue(ctx, domain.Venue{Name: "Destination Venue", District: "East", Timezone: "UTC", Active: true})
	destination, _ := fixture.store.CreateSlot(ctx, fixtureSlot(destinationVenue.ID, fixture.slot.StartsAt.Add(2*time.Hour), 3))
	if err := fixture.store.AssignCoach(ctx, fixture.coachID, destination.ID, fixture.now); err != nil {
		t.Fatal(err)
	}
	moved, err := fixture.service.Reschedule(ctx, guardianActor(fixture, "move-request"), created.ID, created.Version, destination.ID)
	if err != nil {
		t.Fatalf("Reschedule() error: %v", err)
	}
	if moved.SlotID != destination.ID {
		t.Fatalf("booking slot = %d, want %d", moved.SlotID, destination.ID)
	}
	source, _ := fixture.store.SlotByID(ctx, fixture.slot.ID)
	destination, _ = fixture.store.SlotByID(ctx, destination.ID)
	if source.Reserved != 0 || destination.Reserved != 1 {
		t.Fatalf("source/destination reserved = %d/%d", source.Reserved, destination.Reserved)
	}
	sourceBalance, _ := fixture.store.LedgerBalance(ctx, source.ID)
	destinationBalance, _ := fixture.store.LedgerBalance(ctx, destination.ID)
	if sourceBalance != 0 || destinationBalance != 1 {
		t.Fatalf("source/destination ledger = %d/%d", sourceBalance, destinationBalance)
	}
}

func TestCrossVenueRescheduleConflictLeavesCapacityAndLedgerConsistent(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 2)
	defer closeDB()
	ctx := context.Background()

	// A second slot at a different venue but overlapping the destination's times
	// so the student cannot hold both; the unique active-student-slot index will
	// reject the move.
	destinationVenue, _ := fixture.store.CreateVenue(ctx, domain.Venue{Name: "Destination Venue", District: "East", Timezone: "UTC", Active: true})
	destination, _ := fixture.store.CreateSlot(ctx, fixtureSlot(destinationVenue.ID, fixture.slot.StartsAt.Add(2*time.Hour), 3))
	if err := fixture.store.AssignCoach(ctx, fixture.coachID, destination.ID, fixture.now); err != nil {
		t.Fatal(err)
	}

	// Original booking in the source slot.
	sourceBooking, err := fixture.service.Create(ctx, guardianActor(fixture, "source-create"), CreateRequest{
		StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID, IdempotencyKey: "source",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-existing booking in the destination slot that will block the move.
	destBooking, err := fixture.service.Create(ctx, guardianActor(fixture, "dest-create"), CreateRequest{
		StudentID: fixture.student.ID, GuardianID: fixture.guardian.ID, SlotID: destination.ID, IdempotencyKey: "dest",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Reschedule(ctx, guardianActor(fixture, "conflict-move"), sourceBooking.ID, sourceBooking.Version, destination.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Reschedule() error = %v, want ErrConflict", err)
	}

	source, _ := fixture.store.SlotByID(ctx, fixture.slot.ID)
	destination, _ = fixture.store.SlotByID(ctx, destination.ID)
	if source.Reserved != 1 || destination.Reserved != 1 {
		t.Fatalf("source/destination reserved = %d/%d, want 1/1", source.Reserved, destination.Reserved)
	}
	if balance, _ := fixture.store.LedgerBalance(ctx, source.ID); balance != source.Reserved {
		t.Fatalf("source ledger = %d, reserved = %d", balance, source.Reserved)
	}
	if balance, _ := fixture.store.LedgerBalance(ctx, destination.ID); balance != destination.Reserved {
		t.Fatalf("destination ledger = %d, reserved = %d", balance, destination.Reserved)
	}

	sourceAfter, _ := fixture.store.BookingByID(ctx, sourceBooking.ID)
	destAfter, _ := fixture.store.BookingByID(ctx, destBooking.ID)
	if sourceAfter.Status != domain.BookingHeld || sourceAfter.SlotID != fixture.slot.ID || sourceAfter.Version != sourceBooking.Version {
		t.Fatalf("source booking mutated = %+v", sourceAfter)
	}
	if destAfter.Status != domain.BookingHeld || destAfter.SlotID != destination.ID || destAfter.Version != destBooking.Version {
		t.Fatalf("destination booking mutated = %+v", destAfter)
	}
}

func TestConcurrentBookingDoesNotOversell(t *testing.T) {
	fixture, closeDB := newBookingFixture(t, 1)
	defer closeDB()
	ctx := context.Background()
	secondStudent := fixtureUser(t, fixture.store, "concurrent-student@example.test", domain.RoleStudent, fixture.now, studentBirth())
	_, _ = fixture.store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{GuardianID: fixture.guardian.ID,
		StudentID: secondStudent.ID, ValidFrom: fixture.now.Add(-time.Hour), ValidUntil: fixture.now.Add(24 * time.Hour)})
	students := []int64{fixture.student.ID, secondStudent.ID}
	start := make(chan struct{})
	results := make(chan domain.Booking, 2)
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	for index, studentID := range students {
		group.Add(1)
		go func(index int, studentID int64) {
			defer group.Done()
			<-start
			booking, err := fixture.service.Create(ctx, guardianActor(fixture, fmt.Sprintf("concurrent-%d", index)), CreateRequest{
				StudentID: studentID, GuardianID: fixture.guardian.ID, SlotID: fixture.slot.ID,
				IdempotencyKey: fmt.Sprintf("concurrent-%d", index),
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- booking
		}(index, studentID)
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent create error: %v", err)
	}
	held, waitlisted := 0, 0
	for booking := range results {
		switch booking.Status {
		case domain.BookingHeld:
			held++
		case domain.BookingWaitlisted:
			waitlisted++
		default:
			t.Fatalf("unexpected status %s", booking.Status)
		}
	}
	if held != 1 || waitlisted != 1 {
		t.Fatalf("held/waitlisted = %d/%d, want 1/1", held, waitlisted)
	}
	slot, _ := fixture.store.SlotByID(ctx, fixture.slot.ID)
	if slot.Reserved != 1 {
		t.Fatalf("concurrent reserved = %d, want 1", slot.Reserved)
	}
}

func newBookingFixture(t *testing.T, capacity int) (bookingFixture, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "booking.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	guardian := fixtureUser(t, store, "guardian@example.test", domain.RoleGuardian, now, nil)
	student := fixtureUser(t, store, "student@example.test", domain.RoleStudent, now, studentBirth())
	coach := fixtureUser(t, store, "coach@example.test", domain.RoleCoach, now, nil)
	coachID, err := store.CreateCoach(ctx, coach.ID, "multi-sport-youth")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateGuardianAuthorization(ctx, domain.GuardianAuthorization{GuardianID: guardian.ID,
		StudentID: student.ID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Booking Venue", District: "North", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, fixtureSlot(venue.ID, now.Add(48*time.Hour), capacity))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AssignCoach(ctx, coachID, slot.ID, now); err != nil {
		t.Fatal(err)
	}
	fixture := bookingFixture{store: store, service: New(store, func() time.Time { return now }, 15*time.Minute),
		now: now, guardian: guardian, student: student, coachID: coachID, venue: venue, slot: slot}
	return fixture, func() { _ = db.Close() }
}

func fixtureUser(t *testing.T, store *repository.Store, email string, role domain.Role, now time.Time, birth *time.Time) domain.User {
	t.Helper()
	user, err := store.CreateUser(context.Background(), domain.User{Email: email, PasswordHash: "hash", Name: email,
		Role: role, BirthDate: birth, AbilityLevel: 3, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func studentBirth() *time.Time {
	value := time.Date(2012, time.June, 15, 0, 0, 0, 0, time.UTC)
	return &value
}

func fixtureSlot(venueID int64, starts time.Time, capacity int) domain.Slot {
	return domain.Slot{VenueID: venueID, Sport: "swimming", StartsAt: starts, EndsAt: starts.Add(time.Hour),
		Capacity: capacity, MinimumAge: 8, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10,
		Status: domain.SlotOpen}
}

func guardianActor(fixture bookingFixture, requestID string) domain.Actor {
	return domain.Actor{UserID: fixture.guardian.ID, Role: domain.RoleGuardian, RequestID: requestID}
}
