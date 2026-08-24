package schedule

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store *repository.Store
	now   func() time.Time
}

func New(store *repository.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

func (s *Service) CreateVenue(ctx context.Context, actor domain.Actor, venue domain.Venue) (domain.Venue, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return domain.Venue{}, err
	}
	if venue.Name == "" || venue.District == "" || venue.Timezone == "" {
		return domain.Venue{}, fmt.Errorf("%w: venue name, district and timezone required", domain.ErrValidation)
	}
	if _, err := time.LoadLocation(venue.Timezone); err != nil {
		return domain.Venue{}, fmt.Errorf("%w: invalid venue timezone", domain.ErrValidation)
	}
	venue.Active = true
	var created domain.Venue
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		var err error
		created, err = tx.CreateVenue(ctx, venue)
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "venue.create", ObjectType: "venue", ObjectID: repository.AuditObjectID(created.ID),
			Result: "success", RequestID: actor.RequestID, CreatedAt: s.now().UTC()})
		return err
	})
	return created, err
}

func (s *Service) CreateSlot(ctx context.Context, actor domain.Actor, slot domain.Slot, coachID int64) (domain.Slot, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return domain.Slot{}, err
	}
	if coachID <= 0 {
		return domain.Slot{}, fmt.Errorf("%w: coach is required", domain.ErrValidation)
	}
	slot.Status = domain.SlotOpen
	var created domain.Slot
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		var err error
		created, err = tx.CreateSlot(ctx, slot)
		if err != nil {
			return err
		}
		if err := tx.AssignCoach(ctx, coachID, created.ID, s.now().UTC()); err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "slot.create", ObjectType: "slot", ObjectID: repository.AuditObjectID(created.ID),
			Result: "success", RequestID: actor.RequestID, CreatedAt: s.now().UTC(),
			Metadata: map[string]string{"coach_id": strconv.FormatInt(coachID, 10)}})
		return err
	})
	return created, err
}

func (s *Service) AdjustCapacity(ctx context.Context, actor domain.Actor, slotID, version int64, capacity int, reason string) (domain.Slot, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return domain.Slot{}, err
	}
	if reason == "" || capacity < 1 {
		return domain.Slot{}, fmt.Errorf("%w: capacity and audit reason required", domain.ErrValidation)
	}
	before, adjusted, err := s.store.AdjustCapacityStandalone(ctx, slotID, version, capacity)
	if err != nil {
		return domain.Slot{}, err
	}
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		delta := capacity - before.Capacity
		_, err = tx.AppendLedger(ctx, domain.LedgerEntry{SlotID: slotID, Kind: domain.LedgerAdjustment,
			Delta: 0, BalanceAfter: adjusted.Reserved, Reason: reason,
			CorrelationID: actor.RequestID + ":capacity", CreatedAt: s.now().UTC()})
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "slot.capacity_adjust", ObjectType: "slot", ObjectID: repository.AuditObjectID(slotID),
			Result: "success", RequestID: actor.RequestID, CreatedAt: s.now().UTC(), Metadata: map[string]string{
				"reason": reason, "capacity_delta": strconv.Itoa(delta),
			}})
		return err
	})
	return adjusted, err
}

func (s *Service) ListVenues(ctx context.Context, limit, offset int) ([]domain.Venue, error) {
	return s.store.ListVenues(ctx, limit, offset)
}

func (s *Service) ListSlots(ctx context.Context, venueID int64, from, until time.Time, limit, offset int) ([]domain.Slot, error) {
	return s.store.ListSlots(ctx, venueID, from, until, limit, offset)
}
