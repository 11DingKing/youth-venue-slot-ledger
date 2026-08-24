package checkin

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

func TestRedeemCheckInCommitsBookingCheckInAndAudit(t *testing.T) {
	fixture, closeDB := newCheckInFixture(t)
	defer closeDB()
	actor := domain.Actor{UserID: fixture.coach.ID, Role: domain.RoleCoach, RequestID: "check-in-request"}
	checkedIn, err := fixture.service.Redeem(context.Background(), actor, fixture.booking.ID, fixture.booking.Version)
	if err != nil {
		t.Fatalf("Redeem() error: %v", err)
	}
	if checkedIn.Status != domain.BookingCheckedIn || checkedIn.Version != fixture.booking.Version+1 {
		t.Fatalf("checked-in booking = %+v", checkedIn)
	}
	events, err := fixture.store.ListAuditEvents(context.Background(), "booking", repository.AuditObjectID(checkedIn.ID), 10, 0)
	if err != nil || len(events) != 1 || events[0].Action != "booking.check_in" {
		t.Fatalf("audit events = %+v, error = %v", events, err)
	}
	if _, err := fixture.service.Redeem(context.Background(), actor, checkedIn.ID, checkedIn.Version); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("duplicate check-in error = %v", err)
	}
}

func TestRedeemRequiresCoachOrOperatorAndValidWindow(t *testing.T) {
	fixture, closeDB := newCheckInFixture(t)
	defer closeDB()
	studentActor := domain.Actor{UserID: fixture.student.ID, Role: domain.RoleStudent, RequestID: "student-check-in"}
	if _, err := fixture.service.Redeem(context.Background(), studentActor, fixture.booking.ID, fixture.booking.Version); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("student check-in error = %v", err)
	}
	fixture.service.now = func() time.Time { return fixture.slot.StartsAt.Add(-31 * time.Minute) }
	coachActor := domain.Actor{UserID: fixture.coach.ID, Role: domain.RoleCoach, RequestID: "early-check-in"}
	if _, err := fixture.service.Redeem(context.Background(), coachActor, fixture.booking.ID, fixture.booking.Version); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("early check-in error = %v", err)
	}
}

type checkInFixture struct {
	store   *repository.Store
	service *Service
	student domain.User
	coach   domain.User
	slot    domain.Slot
	booking domain.Booking
}

func newCheckInFixture(t *testing.T) (checkInFixture, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "checkin.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.New(db)
	start := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	birth := time.Date(2012, time.January, 1, 0, 0, 0, 0, time.UTC)
	student, _ := store.CreateUser(ctx, domain.User{Email: "student@example.test", PasswordHash: "hash", Name: "Student",
		Role: domain.RoleStudent, BirthDate: &birth, AbilityLevel: 3, Active: true, CreatedAt: start.Add(-time.Hour)})
	guardian, _ := store.CreateUser(ctx, domain.User{Email: "guardian@example.test", PasswordHash: "hash", Name: "Guardian",
		Role: domain.RoleGuardian, Active: true, CreatedAt: start.Add(-time.Hour)})
	coach, _ := store.CreateUser(ctx, domain.User{Email: "coach@example.test", PasswordHash: "hash", Name: "Coach",
		Role: domain.RoleCoach, Active: true, CreatedAt: start.Add(-time.Hour)})
	venue, _ := store.CreateVenue(ctx, domain.Venue{Name: "Check-in Venue", District: "North", Timezone: "UTC", Active: true})
	slot, _ := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "tennis", StartsAt: start, EndsAt: start.Add(time.Hour),
		Capacity: 4, MinimumAge: 8, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
	reserved, _ := store.ReserveSeat(ctx, slot.ID, slot.Version)
	booking, _ := store.CreateBooking(ctx, domain.Booking{StudentID: student.ID, GuardianID: guardian.ID, SlotID: slot.ID,
		Status: domain.BookingConfirmed, CreatedAt: start.Add(-time.Hour), UpdatedAt: start.Add(-time.Hour)})
	_ = reserved
	service := New(store, func() time.Time { return start })
	return checkInFixture{store: store, service: service, student: student, coach: coach, slot: slot, booking: booking}, func() { _ = db.Close() }
}
