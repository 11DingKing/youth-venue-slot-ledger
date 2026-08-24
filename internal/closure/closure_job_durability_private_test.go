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

func TestClosureJobFailureRollsBackAppliedStateAndCanRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "closure-job.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "durability-operator@example.test", PasswordHash: "hash",
		Name: "Durability Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Durability Venue", District: "East", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	slot, err := store.CreateSlot(ctx, closureSlot(venue.ID, now.Add(24*time.Hour), "badminton"))
	if err != nil {
		t.Fatal(err)
	}
	service := New(store, func() time.Time { return now }, 4)
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "closure-job-durability"}
	request := CreateRequest{VenueID: venue.ID, StartsAt: slot.StartsAt.Add(-time.Minute),
		EndsAt: slot.EndsAt.Add(time.Minute), Reason: "heat warning"}

	_, err = db.ExecContext(ctx, `CREATE TRIGGER reject_closure_compensation
		BEFORE INSERT ON worker_jobs WHEN NEW.kind = 'closure_compensation'
		BEGIN SELECT RAISE(FAIL, 'worker job storage unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAndApply(ctx, actor, request); err == nil {
		t.Fatal("CreateAndApply() error = nil, want durable job failure")
	}

	var closures, jobs, audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM closures`).Scan(&closures); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'closure.apply'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	failedSlot, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closures != 0 || jobs != 0 || audits != 0 || failedSlot.Status != domain.SlotOpen {
		t.Fatalf("failed closure persisted closures=%d jobs=%d audits=%d slot=%s, want 0/0/0/open", closures, jobs, audits, failedSlot.Status)
	}

	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_closure_compensation`); err != nil {
		t.Fatal(err)
	}
	applied, err := service.CreateAndApply(ctx, actor, request)
	if err != nil {
		t.Fatalf("retry CreateAndApply() error: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_jobs WHERE status = 'pending'`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	succeededSlot, err := store.SlotByID(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != domain.ClosureApplied || succeededSlot.Status != domain.SlotClosed || jobs != 1 {
		t.Fatalf("successful retry closure=%s slot=%s pending_jobs=%d, want applied/closed/1", applied.Status, succeededSlot.Status, jobs)
	}
}
