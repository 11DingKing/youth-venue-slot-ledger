package closure

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestFailedClosureReopenKeepsClosureAndSlotsApplied(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "reopen-atomicity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "reopen-operator@example.test", PasswordHash: "hash",
		Name: "Reopen Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Reopen Center", District: "Central", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, closureSlot(venue.ID, now.Add(24*time.Hour), "volleyball"))
	if err != nil {
		t.Fatal(err)
	}
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "reopen-atomicity"}
	service := New(store, func() time.Time { return now }, 4)
	applied, err := service.CreateAndApply(ctx, actor, CreateRequest{VenueID: venue.ID,
		StartsAt: slot.StartsAt.Add(-time.Minute), EndsAt: slot.EndsAt.Add(time.Minute), Reason: "electrical inspection"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_slot_reopen BEFORE UPDATE OF status ON slots
		WHEN OLD.id = `+repository.AuditObjectID(slot.ID)+` AND NEW.status = 'open'
		BEGIN SELECT RAISE(ABORT, 'slot reopen rejected'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Reopen(ctx, actor, applied.ID, applied.Version); err == nil {
		t.Fatal("Reopen() succeeded while slot persistence rejected the update")
	}
	closureAfterFailure, err := store.ClosureByID(ctx, applied.ID)
	if err != nil {
		t.Fatal(err)
	}
	slotAfterFailure, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closureAfterFailure.Status != domain.ClosureApplied || closureAfterFailure.Version != applied.Version || slotAfterFailure.Status != domain.SlotClosed {
		t.Fatalf("failed reopen left closure %s version %d and slot %s", closureAfterFailure.Status, closureAfterFailure.Version, slotAfterFailure.Status)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_slot_reopen`); err != nil {
		t.Fatal(err)
	}
	reopened, err := service.Reopen(ctx, actor, applied.ID, applied.Version)
	if err != nil {
		t.Fatalf("Reopen() after persistence recovery: %v", err)
	}
	reopenedSlot, err := store.SlotByID(ctx, slot.ID)
	if err != nil || reopened.Status != domain.ClosureReopened || reopenedSlot.Status != domain.SlotOpen {
		t.Fatalf("recovered reopen = closure %s, slot %s, error %v", reopened.Status, reopenedSlot.Status, err)
	}
}
