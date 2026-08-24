package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
)

func TestScheduleCapacityAndCoachCoverage(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC)
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "River Sports Center", District: "River", Timezone: "Asia/Shanghai", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	coachUser := createUser(t, store, domain.User{Email: "coach@example.test", PasswordHash: "hash",
		Name: "Coach", Role: domain.RoleCoach, Active: true, CreatedAt: now})
	coachID, err := store.CreateCoach(ctx, coachUser.ID, "youth-swimming-level-2")
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, validSlot(venue.ID, now.Add(24*time.Hour), 2))
	if err != nil {
		t.Fatal(err)
	}
	covered, err := store.HasCoachCoverage(ctx, slot.ID)
	if err != nil || covered {
		t.Fatalf("coverage before assignment = %v, error = %v", covered, err)
	}
	if err := store.AssignCoach(ctx, coachID, slot.ID, now); err != nil {
		t.Fatalf("AssignCoach() error: %v", err)
	}
	covered, err = store.HasCoachCoverage(ctx, slot.ID)
	if err != nil || !covered {
		t.Fatalf("coverage after assignment = %v, error = %v", covered, err)
	}

	first, err := store.ReserveSeat(ctx, slot.ID, slot.Version)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if first.Reserved != 1 || first.Version != slot.Version+1 {
		t.Fatalf("first reservation = %+v", first)
	}
	if _, err := store.ReserveSeat(ctx, slot.ID, slot.Version); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
	second, err := store.ReserveSeat(ctx, slot.ID, first.Version)
	if err != nil {
		t.Fatalf("second reserve: %v", err)
	}
	if second.Reserved != second.Capacity {
		t.Fatalf("reserved = %d, capacity = %d", second.Reserved, second.Capacity)
	}
	if _, err := store.ReserveSeat(ctx, slot.ID, second.Version); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("full slot error = %v, want ErrCapacity", err)
	}
	if _, err := store.AdjustCapacity(ctx, slot.ID, second.Version, 1); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("capacity below reserved error = %v, want ErrConflict", err)
	}
	released, err := store.ReleaseSeat(ctx, slot.ID, second.Version)
	if err != nil {
		t.Fatalf("release seat: %v", err)
	}
	adjusted, err := store.AdjustCapacity(ctx, slot.ID, released.Version, 4)
	if err != nil {
		t.Fatalf("adjust capacity: %v", err)
	}
	if adjusted.Capacity != 4 || adjusted.Reserved != 1 {
		t.Fatalf("adjusted slot = %+v", adjusted)
	}
}

func TestCoachCannotOverlapSlots(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Now().UTC()
	venue, _ := store.CreateVenue(ctx, domain.Venue{Name: "Overlap Center", District: "South", Timezone: "UTC", Active: true})
	coach := createUser(t, store, domain.User{Email: "overlap-coach@example.test", PasswordHash: "hash",
		Name: "Coach", Role: domain.RoleCoach, Active: true, CreatedAt: now})
	coachID, _ := store.CreateCoach(ctx, coach.ID, "basketball")
	first, _ := store.CreateSlot(ctx, validSlot(venue.ID, now.Add(time.Hour), 8))
	second := validSlot(venue.ID, now.Add(90*time.Minute), 8)
	second.Sport = "volleyball"
	secondSlot, _ := store.CreateSlot(ctx, second)
	if err := store.AssignCoach(ctx, coachID, first.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignCoach(ctx, coachID, secondSlot.ID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("overlap error = %v, want ErrConflict", err)
	}
}

func TestSlotPaginationAndTimeFilter(t *testing.T) {
	ctx := context.Background()
	store, db := newTestStore(t)
	defer db.Close()
	now := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	venue, _ := store.CreateVenue(ctx, domain.Venue{Name: "Pagination Center", District: "Central", Timezone: "UTC", Active: true})
	for index := 0; index < 5; index++ {
		slot := validSlot(venue.ID, now.Add(time.Duration(index+1)*time.Hour), 6)
		slot.Sport = []string{"swimming", "basketball", "tennis", "football", "volleyball"}[index]
		if _, err := store.CreateSlot(ctx, slot); err != nil {
			t.Fatal(err)
		}
	}
	firstPage, err := store.ListSlots(ctx, venue.ID, now, now.Add(24*time.Hour), 2, 0)
	if err != nil || len(firstPage) != 2 {
		t.Fatalf("first page length = %d, error = %v", len(firstPage), err)
	}
	secondPage, err := store.ListSlots(ctx, venue.ID, now, now.Add(24*time.Hour), 2, 2)
	if err != nil || len(secondPage) != 2 {
		t.Fatalf("second page length = %d, error = %v", len(secondPage), err)
	}
	if firstPage[1].ID == secondPage[0].ID {
		t.Fatal("pagination returned overlapping records")
	}
}

func validSlot(venueID int64, startsAt time.Time, capacity int) domain.Slot {
	return domain.Slot{VenueID: venueID, Sport: "swimming", StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
		Capacity: capacity, Reserved: 0, MinimumAge: 8, MaximumAge: 16, MinimumAbility: 0,
		MaximumAbility: 10, Status: domain.SlotOpen}
}
