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

func TestCapacityLedgerFailurePreservesVersionForRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "capacity-ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "capacity-operator@example.test", PasswordHash: "hash", Name: "Capacity Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Capacity Center", District: "North", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, domain.Slot{VenueID: venue.ID, Sport: "basketball", StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(25 * time.Hour), Capacity: 8, MinimumAge: 10, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10, Status: domain.SlotOpen})
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now })
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "capacity-retry"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_capacity_ledger
		BEFORE INSERT ON ledger_entries WHEN NEW.kind = 'adjustment'
		BEGIN SELECT RAISE(FAIL, 'ledger storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdjustCapacity(ctx, actor, slot.ID, slot.Version, 10, "add supervised places"); err == nil {
		t.Fatal("AdjustCapacity() error = nil, want ledger failure")
	}
	failed, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ledgers, audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE slot_id = ? AND kind = 'adjustment'`, slot.ID).Scan(&ledgers); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'slot.capacity_adjust' AND object_id = ?`, repository.AuditObjectID(slot.ID)).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if failed.Capacity != 8 || failed.Version != slot.Version || ledgers != 0 || audits != 0 {
		t.Fatalf("failed adjustment capacity=%d version=%d ledgers=%d audits=%d, want 8/%d/0/0", failed.Capacity, failed.Version, ledgers, audits, slot.Version)
	}

	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_capacity_ledger`); err != nil {
		t.Fatal(err)
	}
	adjusted, err := service.AdjustCapacity(ctx, actor, slot.ID, slot.Version, 10, "add supervised places")
	if err != nil {
		t.Fatalf("retry AdjustCapacity() error: %v", err)
	}
	if adjusted.Capacity != 10 || adjusted.Version != slot.Version+1 {
		t.Fatalf("retry capacity=%d version=%d, want 10/%d", adjusted.Capacity, adjusted.Version, slot.Version+1)
	}
}
