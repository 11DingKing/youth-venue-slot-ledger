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

func TestRejectedCoachAssignmentLeavesNoSlotAndCanRetry(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "slot-coach.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := repository.New(db)
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	operator, err := store.CreateUser(ctx, domain.User{Email: "slot-operator@example.test", PasswordHash: "hash", Name: "Slot Operator", Role: domain.RoleOperator, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	coachUser, err := store.CreateUser(ctx, domain.User{Email: "slot-coach@example.test", PasswordHash: "hash", Name: "Slot Coach", Role: domain.RoleCoach, Active: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	coachID, err := store.CreateCoach(ctx, coachUser.ID, "youth-tennis")
	if err != nil {
		t.Fatal(err)
	}
	venue, err := store.CreateVenue(ctx, domain.Venue{Name: "Coach Coverage Center", District: "North", Timezone: "UTC", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.Slot{VenueID: venue.ID, Sport: "tennis", StartsAt: now.Add(24 * time.Hour), EndsAt: now.Add(25 * time.Hour), Capacity: 8, MinimumAge: 9, MaximumAge: 17, MinimumAbility: 0, MaximumAbility: 10}
	service := New(store, func() time.Time { return now })
	actor := domain.Actor{UserID: operator.ID, Role: domain.RoleOperator, RequestID: "slot-coach-retry"}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_coach_assignment BEFORE INSERT ON coach_assignments BEGIN SELECT RAISE(ABORT, 'assignment rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSlot(ctx, actor, request, coachID); err == nil {
		t.Fatal("CreateSlot() succeeded while coach assignment was rejected")
	}
	var slots int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM slots WHERE venue_id = ? AND sport = ? AND starts_at = ?`, venue.ID, request.Sport, request.StartsAt.UTC().Format(time.RFC3339Nano)).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if slots != 0 {
		t.Fatalf("failed slot creation left %d schedule rows", slots)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_coach_assignment`); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSlot(ctx, actor, request, coachID)
	if err != nil {
		t.Fatalf("CreateSlot() after recovery: %v", err)
	}
	covered, err := store.HasCoachCoverage(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !covered {
		t.Fatal("recovered slot has no coach coverage")
	}
}
