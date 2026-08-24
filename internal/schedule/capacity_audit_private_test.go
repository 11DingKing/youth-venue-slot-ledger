package schedule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/database"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

func TestRejectedCapacityAuditRollsBackSlotAndAllowsRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "capacity-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "capacity-operator@example.test", PasswordHash: "hash", Name: "Capacity Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Capacity Center", District: "West", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "basketball", StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(25 * time.Hour), Capacity: 4, MinimumAge: 10, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now })
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "capacity-audit-retry"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_capacity_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'slot.capacity_adjust' BEGIN SELECT RAISE(ABORT, 'capacity audit rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdjustCapacity(ctx, actor, slot.ID, slot.Version, 6, "summer demand"); err == nil {
		t.Fatal("AdjustCapacity() succeeded while audit persistence rejected the event")
	}
	afterFailure, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	balance, err := store.LedgerBalance(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Capacity != slot.Capacity || afterFailure.Version != slot.Version || balance != 0 {
		t.Fatalf("failed adjustment left capacity %d version %d balance %d", afterFailure.Capacity, afterFailure.Version, balance)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_capacity_audit`); err != nil {
		t.Fatal(err)
	}
	adjusted, err := service.AdjustCapacity(ctx, actor, slot.ID, slot.Version, 6, "summer demand")
	if err != nil {
		t.Fatalf("AdjustCapacity() after audit recovery: %v", err)
	}
	events, err := store.ListAuditEvents(ctx, "slot", repository.AuditObjectID(slot.ID), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if adjusted.Capacity != 6 || adjusted.Version != slot.Version+1 || len(events) != 1 {
		t.Fatalf("recovered adjustment = %+v, events = %d", adjusted, len(events))
	}
}
