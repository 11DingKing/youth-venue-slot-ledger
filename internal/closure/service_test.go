package closure

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

func TestCreateAndApplyClosureClosesOnlyOverlappingSlotsAndQueuesCompensation(t *testing.T) {
	fixture, closeDB := newClosureFixture(t)
	defer closeDB()
	request := CreateRequest{VenueID: fixture.venue.ID, StartsAt: fixture.slot.StartsAt.Add(-time.Minute),
		EndsAt: fixture.slot.EndsAt.Add(time.Minute), Reason: "unsafe air quality"}
	applied, err := fixture.service.CreateAndApply(context.Background(), fixture.actor, request)
	if err != nil {
		t.Fatalf("CreateAndApply() error: %v", err)
	}
	if applied.Status != domain.ClosureApplied || applied.Version != 2 {
		t.Fatalf("applied closure = %+v", applied)
	}
	closedSlot, err := fixture.store.SlotByID(context.Background(), fixture.slot.ID)
	if err != nil || closedSlot.Status != domain.SlotClosed {
		t.Fatalf("closed slot = %+v, error = %v", closedSlot, err)
	}
	openSlot, err := fixture.store.SlotByID(context.Background(), fixture.otherSlot.ID)
	if err != nil || openSlot.Status != domain.SlotOpen {
		t.Fatalf("non-overlapping slot = %+v, error = %v", openSlot, err)
	}
	job, found, err := fixture.store.ClaimJob(context.Background(), fixture.now, time.Minute)
	if err != nil || !found || job.Kind != "closure_compensation" {
		t.Fatalf("compensation job = %+v, found = %v, error = %v", job, found, err)
	}
	events, err := fixture.store.ListAuditEvents(context.Background(), "closure", repository.AuditObjectID(applied.ID), 10, 0)
	if err != nil || len(events) != 1 || events[0].Metadata["reason"] != request.Reason {
		t.Fatalf("closure audit = %+v, error = %v", events, err)
	}
}

func TestReopenRestoresSlotsAndRejectsStaleVersion(t *testing.T) {
	fixture, closeDB := newClosureFixture(t)
	defer closeDB()
	request := CreateRequest{VenueID: fixture.venue.ID, StartsAt: fixture.slot.StartsAt.Add(-time.Minute),
		EndsAt: fixture.slot.EndsAt.Add(time.Minute), Reason: "storm warning"}
	applied, err := fixture.service.CreateAndApply(context.Background(), fixture.actor, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Reopen(context.Background(), fixture.actor, applied.ID, applied.Version-1); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("stale reopen error = %v", err)
	}
	reopened, err := fixture.service.Reopen(context.Background(), fixture.actor, applied.ID, applied.Version)
	if err != nil {
		t.Fatalf("Reopen() error: %v", err)
	}
	if reopened.Status != domain.ClosureReopened {
		t.Fatalf("reopened status = %s", reopened.Status)
	}
	slot, _ := fixture.store.SlotByID(context.Background(), fixture.slot.ID)
	if slot.Status != domain.SlotOpen {
		t.Fatalf("slot status after reopen = %s", slot.Status)
	}
}

func TestReopenLeavesClosureAppliedWhenSlotReopenFailsAndRetriesWithOriginalVersion(t *testing.T) {
	fixture, closeDB := newClosureFixture(t)
	defer closeDB()
	ctx := context.Background()
	request := CreateRequest{VenueID: fixture.venue.ID, StartsAt: fixture.slot.StartsAt.Add(-time.Minute),
		EndsAt: fixture.slot.EndsAt.Add(time.Minute), Reason: "power outage"}
	applied, err := fixture.service.CreateAndApply(ctx, fixture.actor, request)
	if err != nil {
		t.Fatalf("CreateAndApply() error: %v", err)
	}
	// Simulate the database refusing to reopen the affected slots: run the same
	// transaction the service now uses, transition the closure record, then fail
	// the slot-reopen step. Before the fix the transition committed separately,
	// leaving the closure reopened while the slots stayed closed and making every
	// retry with the original version fail with ErrVersionConflict.
	errSlotReopenFailed := errors.New("reopen slots: disk full")
	txErr := fixture.store.WithTx(ctx, func(tx *repository.Store) error {
		if _, err := tx.ReopenClosureRecord(ctx, applied.ID, applied.Version, fixture.now); err != nil {
			return err
		}
		return errSlotReopenFailed
	})
	if !errors.Is(txErr, errSlotReopenFailed) {
		t.Fatalf("failed reopen transaction error = %v", txErr)
	}
	current, err := fixture.store.ClosureByID(ctx, applied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.ClosureApplied || current.Version != applied.Version {
		t.Fatalf("closure after failed reopen = status %s version %d, want %s/%d",
			current.Status, current.Version, domain.ClosureApplied, applied.Version)
	}
	slot, _ := fixture.store.SlotByID(ctx, fixture.slot.ID)
	if slot.Status != domain.SlotClosed {
		t.Fatalf("slot status after failed reopen = %s, want %s", slot.Status, domain.SlotClosed)
	}
	// After the fault clears, the same original version must still complete the recovery.
	recovered, err := fixture.service.Reopen(ctx, fixture.actor, applied.ID, applied.Version)
	if err != nil {
		t.Fatalf("Reopen() retry error: %v", err)
	}
	if recovered.Status != domain.ClosureReopened {
		t.Fatalf("recovered status = %s, want %s", recovered.Status, domain.ClosureReopened)
	}
	slot, _ = fixture.store.SlotByID(ctx, fixture.slot.ID)
	if slot.Status != domain.SlotOpen {
		t.Fatalf("slot status after retry = %s, want %s", slot.Status, domain.SlotOpen)
	}
}

func TestClosureRequiresOperatorAndValidWindow(t *testing.T) {
	fixture, closeDB := newClosureFixture(t)
	defer closeDB()
	request := CreateRequest{VenueID: fixture.venue.ID, StartsAt: fixture.slot.StartsAt,
		EndsAt: fixture.slot.EndsAt, Reason: "maintenance"}
	guardian := domain.Actor{UserID: fixture.actor.UserID + 1, Role: domain.RoleGuardian, RequestID: "forbidden"}
	if _, err := fixture.service.CreateAndApply(context.Background(), guardian, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("guardian closure error = %v", err)
	}
	request.EndsAt = request.StartsAt
	if _, err := fixture.service.CreateAndApply(context.Background(), fixture.actor, request); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid window error = %v", err)
	}
}

type closureFixture struct {
	store     *repository.Store
	service   *Service
	now       time.Time
	actor     domain.Actor
	venue     domain.Venue
	slot      domain.Slot
	otherSlot domain.Slot
}

func newClosureFixture(t *testing.T) (closureFixture, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "closure.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, _ := store.CreateUser(ctx, domain.User{Email: "operator@example.test", PasswordHash: "hash", Name: "Operator",
		Role: domain.RoleOperator, Active: true, CreatedAt: now})
	venue, _ := store.CreateVenue(ctx, domain.Venue{Name: "Closure Venue", District: "South", Timezone: "UTC", Active: true})
	slot, _ := store.CreateSlot(ctx, closureSlot(venue.ID, now.Add(24*time.Hour), "swimming"))
	otherSlot, _ := store.CreateSlot(ctx, closureSlot(venue.ID, now.Add(48*time.Hour), "basketball"))
	return closureFixture{store: store, service: New(store, func() time.Time { return now }, 4), now: now,
		actor: domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "closure-request"},
		venue: venue, slot: slot, otherSlot: otherSlot}, func() { _ = db.Close() }
}

func closureSlot(venueID int64, starts time.Time, sport string) domain.Slot {
	return domain.Slot{VenueID: venueID, Sport: sport, StartsAt: starts, EndsAt: starts.Add(time.Hour),
		Capacity: 10, MinimumAge: 8, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen}
}
