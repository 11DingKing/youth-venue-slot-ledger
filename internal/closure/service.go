package closure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store       *repository.Store
	now         func() time.Time
	maxAttempts int
}

func New(store *repository.Store, now func() time.Time, maxAttempts int) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now, maxAttempts: maxAttempts}
}

type CreateRequest struct {
	VenueID  int64     `json:"venue_id"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Reason   string    `json:"reason"`
}

func (s *Service) CreateAndApply(ctx context.Context, actor domain.Actor, request CreateRequest) (repository.Closure, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return repository.Closure{}, err
	}
	now := s.now().UTC()
	var applied repository.Closure
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		created, err := tx.CreateClosure(ctx, repository.Closure{VenueID: request.VenueID,
			StartsAt: request.StartsAt.UTC(), EndsAt: request.EndsAt.UTC(), Reason: request.Reason,
			Status: domain.ClosurePlanned, CreatedBy: actor.UserID, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		if _, err := tx.CloseSlotsForWindow(ctx, request.VenueID, request.StartsAt, request.EndsAt); err != nil {
			return err
		}
		applied, err = tx.TransitionClosure(ctx, created.ID, created.Version, domain.ClosurePlanned, domain.ClosureApplied, now)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]int64{"closure_id": applied.ID})
		if err != nil {
			return err
		}
		if _, err := tx.EnqueueJob(ctx, "closure_compensation", string(payload),
			fmt.Sprintf("closure:%d:compensate", applied.ID), now, now, s.maxAttempts); err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "closure.apply", ObjectType: "closure", ObjectID: repository.AuditObjectID(applied.ID),
			Result: "success", RequestID: actor.RequestID, Metadata: map[string]string{"reason": request.Reason}, CreatedAt: now})
		return err
	})
	return applied, err
}

func (s *Service) Reopen(ctx context.Context, actor domain.Actor, id, version int64) (repository.Closure, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return repository.Closure{}, err
	}
	now := s.now().UTC()
	var reopened repository.Closure
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		// Transition the closure record, reopen the affected slots and append the
		// audit event inside a single transaction. If reopening the slots fails
		// (the database rejects the slot update), the rollback must also undo the
		// closure status transition so the record stays applied at its original
		// version and the recovery can be retried with the same version once the
		// fault clears. Keeping these steps in separate transactions left a
		// half-completed reopen: closure reopened but slots still closed.
		var err error
		reopened, err = tx.ReopenClosureRecord(ctx, id, version, now)
		if err != nil {
			return err
		}
		if _, err := tx.ReopenSlotsForWindow(ctx, reopened.VenueID, reopened.StartsAt, reopened.EndsAt); err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "closure.reopen", ObjectType: "closure", ObjectID: repository.AuditObjectID(id),
			Result: "success", RequestID: actor.RequestID, CreatedAt: now})
		return err
	})
	return reopened, err
}
